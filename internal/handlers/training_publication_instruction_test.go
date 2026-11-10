package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

func instructionReviewFixture() publicationSourceEnvelope {
	source := publicationSourceFixture()
	segment, variation, secondMember, secondAthlete, secondPlacement := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	position, modality, title := int32(1), dbgen.TrainingSegmentModalityGYM, "Common instructions"
	source.Overview[0].SegmentID, source.Overview[0].SegmentPosition = &segment, &position
	source.Overview[0].SegmentModality, source.Overview[0].SegmentTitle = &modality, &title
	other := source.EligibleRecipients[0]
	other.MembershipID, other.AthleteUserID, other.AthleteName, other.PlacementID = secondMember, &secondAthlete, "Bruno", secondPlacement
	source.EligibleRecipients = append(source.EligibleRecipients, other)
	candidate := source.Candidates[0]
	candidate.MembershipID, candidate.SubjectID, candidate.PlacementID = &secondMember, secondAthlete, secondPlacement
	source.Candidates = append(source.Candidates, candidate)
	patch := json.RawMessage(`{"title":"Ana only"}`)
	member := source.EligibleRecipients[0].MembershipID
	source.VariationDefinitions = []publicationDefinition{{VariationID: variation, PlanID: source.Header.PlanID, SubjectID: segment, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, Patch: patch}}
	source.VariationMatches = []publicationVariationTransport{{ListTrainingVariationMatchesForManagerRow: dbgen.ListTrainingVariationMatchesForManagerRow{VariationID: variation, PlanID: source.Header.PlanID, SessionID: *source.Overview[0].SessionID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segment, Operation: dbgen.TrainingVariationOperationOVERRIDE, MembershipID: &member, Priority: 2, ChangeSummary: "Personal instruction"}, Patch: patch}}
	return source
}

func TestPublicationInstructionSelectionShowsCanonicalPersonalAndCommonWithoutRebinding(t *testing.T) {
	source := instructionReviewFixture()
	before, _ := json.Marshal(source)
	now := time.Now()
	preview, err := makePublicationPreview(source, uuid.New(), "Reviewed instructions", trainingPublicationReturnContext{GroupID: source.Header.GroupID, WeekID: source.Header.PlanID}, time.UTC, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := trainingPublicationReviews.verify(preview.Token, preview.Metadata, now)
	if err != nil {
		t.Fatal(err)
	}
	for index, title := range []string{"Ana only", "Common instructions"} {
		inspected, err := inspectPublication(source, claims, preview.Token, time.UTC, now)
		if err != nil || inspected.Token != preview.Token || inspected.Metadata != preview.Metadata {
			t.Fatalf("selection refreshed signed review: %v", err)
		}
		p := inspected.Materialization.Prescriptions[index]
		view, err := publicationInstructionReview(source, inspected.Materialization, publicationInstructionKey(p.AthleteUserID, p.SessionID), time.UTC)
		if err != nil || view == nil || len(view.Choices) != 2 || view.Session.Segments[0].Title != title {
			t.Fatalf("recipient %d received wrong resolved instructions: %+v %v", index, view, err)
		}
	}
	after, _ := json.Marshal(source)
	if !bytes.Equal(before, after) {
		t.Fatal("instruction inspection changed the shared source")
	}
	if _, err := publicationInstructionReview(source, preview.Materialization, publicationInstructionKey(uuid.New(), uuid.New()), time.UTC); !errors.Is(err, errTrainingPublicationReviewInvalid) {
		t.Fatal("foreign instruction selector accepted", err)
	}
}

func TestPublicationInstructionInspectionRefusesChangedOrExpiredCompleteReview(t *testing.T) {
	for _, change := range []string{"header", "personal body", "recipient", "expired"} {
		t.Run(change, func(t *testing.T) {
			source := instructionReviewFixture()
			now := time.Now()
			preview, err := makePublicationPreview(source, uuid.New(), "Original summary", trainingPublicationReturnContext{GroupID: source.Header.GroupID, WeekID: source.Header.PlanID}, time.UTC, now)
			if err != nil {
				t.Fatal(err)
			}
			claims, err := trainingPublicationReviews.verify(preview.Token, preview.Metadata, now)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "header":
				source.Header.SourceUpdatedAt.Time = source.Header.SourceUpdatedAt.Time.Add(time.Microsecond)
			case "personal body":
				source.VariationMatches[0].Patch = json.RawMessage(`{"title":"Changed after review"}`)
			case "recipient":
				source.EligibleRecipients = source.EligibleRecipients[:1]
			case "expired":
				now = now.Add(trainingPublicationReviewTTL + time.Second)
			}
			state, err := inspectPublication(source, claims, preview.Token, time.UTC, now)
			if err == nil || state.CanConfirm || state.Token != "" || len(state.Recipients) != 0 || len(state.Materialization.Prescriptions) != 0 {
				t.Fatal("inspection exposed or re-signed a changed/expired review", err)
			}
		})
	}
}

func TestPublicationHTTPInstructionInspectionRetainsOriginalProofAndRejectsStaleAudience(t *testing.T) {
	store, handler, _ := publicationHTTPFixture(t)
	store.source = instructionReviewFixture()
	path := structuredPlannerPath + "/semanas/" + store.source.Header.PlanID.String() + "/publicar"
	preview := publicationPost(t, handler, path, url.Values{"change_summary": {"Original reviewed summary"}})
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status %d: %s", preview.Code, preview.Body.String())
	}
	original := publicationConfirmForm(t, preview.Body.String())
	inspect, _ := url.ParseQuery(original.Encode())
	inspect.Set("publication_intent", "inspect")
	inspect.Del("confirm")
	person := store.source.EligibleRecipients[1]
	inspect.Set("instruction_key", publicationInstructionKey(*person.AthleteUserID, person.SessionID))
	response := publicationPost(t, handler, path, inspect)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Common instructions") || strings.Contains(response.Body.String(), "Ana only") {
		t.Fatalf("wrong recipient instruction preview %d: %s", response.Code, response.Body.String())
	}
	retained := publicationConfirmForm(t, response.Body.String())
	if retained.Encode() != original.Encode() || store.confirmCalls != 0 {
		t.Fatal("inspection refreshed review metadata or invoked publication")
	}
	store.source.Header.SourceUpdatedAt.Time = store.source.Header.SourceUpdatedAt.Time.Add(time.Microsecond)
	stale := publicationPost(t, handler, path, inspect)
	if stale.Code != http.StatusConflict || strings.Contains(stale.Body.String(), "Common instructions") || strings.Contains(stale.Body.String(), "Ana only") || strings.Contains(stale.Body.String(), `name="confirm"`) || store.confirmCalls != 0 {
		t.Fatalf("stale inspection exposed/reconfirmed instructions: %d", stale.Code)
	}
	if !strings.Contains(stale.Body.String(), "Original reviewed summary") || !strings.Contains(stale.Body.String(), original.Get("review_token")) {
		t.Fatal("stale inspection discarded original own summary/proof")
	}
}

func TestPublicationInstructionReviewRefusesAmbiguousPrivateRecords(t *testing.T) {
	for _, tc := range []string{"missing recipient identity", "conflicting recipient names", "conflicting session titles", "duplicate instructions", "broken saved instructions"} {
		t.Run(tc, func(t *testing.T) {
			source := instructionReviewFixture()
			material, err := materializePublicationSource(source, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			switch tc {
			case "missing recipient identity":
				source.EligibleRecipients[0].AthleteUserID = nil
			case "conflicting recipient names":
				row := source.EligibleRecipients[0]
				row.AthleteName = "Different person"
				source.EligibleRecipients = append(source.EligibleRecipients, row)
			case "conflicting session titles":
				row := source.Overview[0]
				title := "Other session"
				row.SessionTitle = &title
				source.Overview = append(source.Overview, row)
			case "duplicate instructions":
				material.Prescriptions = append(material.Prescriptions, material.Prescriptions[0])
			case "broken saved instructions":
				material.Prescriptions[0].Snapshot = []byte(`{"schema_version":999}`)
			}
			view, err := publicationInstructionReview(source, material, "", time.UTC)
			if err == nil || view != nil {
				t.Fatal("inconsistent private instructions entered the rendered review")
			}
		})
	}
}
