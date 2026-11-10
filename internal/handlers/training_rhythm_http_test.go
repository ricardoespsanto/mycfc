package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRhythmProfilePatchUsesCanonicalUUIDOnlyForWaterBlockOverride(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		kind  dbgen.TrainingVariationSubjectKind
		value string
		valid bool
	}{{dbgen.TrainingVariationSubjectKindBLOCK, id, true}, {dbgen.TrainingVariationSubjectKindBLOCK, strings.ToUpper("abcdefab-cdef-abcd-efab-cdefabcdefab"), false}, {dbgen.TrainingVariationSubjectKindWATERSTEP, id, false}, {dbgen.TrainingVariationSubjectKindGYMEXERCISE, id, false}, {dbgen.TrainingVariationSubjectKindBLOCK, "not-a-profile", false}} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"intensity_profile_id": {tc.value}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_ = r.ParseForm()
		patch, e := parseTrainingVariationPatch(r, tc.kind)
		if (e == nil) != tc.valid {
			t.Fatalf("kind=%s value=%s err=%v", tc.kind, tc.value, e)
		}
		if tc.valid && patch["intensity_profile_id"] != id {
			t.Fatal("typed patch changed explicit choice")
		}
	}
}

type rhythmHandlerStore struct {
	*structuredTrainingStoreStub
	profiles         []dbgen.ListActiveWaterIntensityProfilesRow
	revokeAfterWrite bool
}

func (s *rhythmHandlerStore) ListActiveWaterIntensityProfiles(context.Context) ([]dbgen.ListActiveWaterIntensityProfilesRow, error) {
	return s.profiles, nil
}
func (s *rhythmHandlerStore) CreateTrainingVariation(ctx context.Context, p dbgen.CreateTrainingVariationParams) (dbgen.TrainingVariation, error) {
	result, e := s.structuredTrainingStoreStub.CreateTrainingVariation(ctx, p)
	if s.revokeAfterWrite {
		s.weekOK = false
	}
	return result, e
}
func TestRhythmNativeVariationRetainsOwnFieldsAndRechecksScope(t *testing.T) {
	source := rhythmFixture()
	profile := source.Rhythm.Profiles[1]
	for _, revoked := range []bool{false, true} {
		store := &rhythmHandlerStore{structuredTrainingStoreStub: &structuredTrainingStoreStub{weekOK: true, overviewRows: source.Overview, createVariationErr: pgx.ErrNoRows}, profiles: []dbgen.ListActiveWaterIntensityProfilesRow{{ID: profile.ID, Name: "Scoped canoe reference", Craft: profile.Craft, Revision: profile.Revision}}, revokeAfterWrite: revoked}
		values := url.Values{"plan_id": {source.Header.PlanID.String()}, "target": {"ATHLETE:" + source.EligibleRecipients[1].MembershipID.String()}, "subject": {"BLOCK:" + source.Overview[0].BlockID.String()}, "operation": {"OVERRIDE"}, "change_summary": {"Own original profile choice"}, "intensity_profile_id": {profile.ID.String()}}
		r := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New(), IsAdmin: true}, http.MethodPost, "/admin/treinos/estruturados/variacoes", values, "", "", (StructuredTraining{Store: store, Location: time.UTC}).CreateVariation)
		if revoked {
			if r.Code != 403 || strings.Contains(r.Body.String(), "Scoped canoe reference") || strings.Contains(r.Body.String(), "Own original profile choice") {
				t.Fatal("revoked scope redisclosed retained task", r.Code)
			}
			continue
		}
		if r.Code != 422 || !strings.Contains(r.Body.String(), "Own original profile choice") || !strings.Contains(r.Body.String(), `value="`+profile.ID.String()+`" selected`) || !strings.Contains(r.Body.String(), `value="`+values.Get("target")+`"`) {
			t.Fatalf("status=%d did not preserve explicit own values", r.Code)
		}
		if len(store.variationInputs) != 1 || !strings.Contains(string(store.variationInputs[0].Patch), profile.ID.String()) {
			t.Fatal("profile selection did not reach typed write")
		}
	}
}
func TestRhythmRetainedInstructionInspectionBindsCompleteReferenceFacts(t *testing.T) {
	source := rhythmFixture()
	now := source.Header.SourceUpdatedAt.Time
	preview, e := makePublicationPreview(source, uuid.New(), "Complete rhythm review", trainingPublicationReturnContext{}, time.UTC, now)
	if e != nil {
		t.Fatal(e)
	}
	posted := preview.Metadata
	posted.referenceSHA256 = [32]byte{}
	claims, e := trainingPublicationReviews.verify(preview.Token, posted, now)
	if e != nil {
		t.Fatal(e)
	}
	current, e := inspectPublication(source, claims, preview.Token, time.UTC, now)
	if e != nil || current.Token != preview.Token {
		t.Fatal("inspection re-signed or dropped complete reference facts", e)
	}
	source.Rhythm.Profiles[0].Notes = "Changed complete reference facts"
	if _, e = inspectPublication(source, claims, preview.Token, time.UTC, now); e == nil {
		t.Fatal("inspection silently refreshed a stale reference review")
	}
}
func TestRhythmPermissionRowsSupportPoloAndC4AndProtectMissingModality(t *testing.T) {
	polo, c4 := uuid.New(), uuid.New()
	facts := publicationRhythmFacts{Crafts: []publicationRhythmCraft{{SubjectID: polo, Kayak: true}, {SubjectID: c4, Canoe: true}}}
	rows := []dbgen.LockPublicationRecipientCraftsRow{{UserID: polo, PermissionKind: "MODALITY", PermissionCode: "KAYAK_POLO"}, {UserID: c4, PermissionKind: "MODALITY", PermissionCode: "CANOEING"}, {UserID: c4, PermissionKind: "CRAFT", PermissionCode: "C4"}}
	if !publicationRhythmPermissionsMatch(facts, rows) {
		t.Fatal("actual Polo/C4 permission was conflated with narrower temporary crew rules")
	}
	if publicationRhythmPermissionsMatch(facts, append(append([]dbgen.LockPublicationRecipientCraftsRow(nil), rows[:1]...), rows[2])) {
		t.Fatal("craft row without current Canoeing modality granted permission")
	}
	if publicationRhythmPermissionsMatch(facts, append(rows, rows[0])) {
		t.Fatal("duplicate locked permission tuple accepted")
	}
}
