package handlers

import (
	"sort"
	"time"

	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

// Selecting instructions is a read of the original complete review. It must
// neither sign today's source again nor refresh an expired/stale confirmation.
func inspectPublication(source publicationSourceEnvelope, claims trainingPublicationReviewClaims, token string, location *time.Location, now time.Time) (publicationPreviewState, error) {
	if !claims.verified || !publicationReviewedHeaderMatches(claims, source.Header) || !publicationReturnMatchesSource(claims.metadata.Return, source) {
		return publicationPreviewState{}, errTrainingPublicationReviewChanged
	}
	material, err := materializePublicationSource(source, location)
	if err != nil {
		return publicationPreviewState{}, err
	}
	current := publicationCurrentReviewInput(publicationConfirmInput{ActorID: claims.metadata.ActorID, PlanID: claims.metadata.PlanID, Review: claims}, source.Header, material.Prescriptions)
	if err := claims.matches(current, now); err != nil {
		return publicationPreviewState{}, err
	}
	recipients, err := publicationPreviewRecipients(source)
	if err != nil {
		return publicationPreviewState{}, err
	}
	return publicationPreviewState{Metadata: claims.metadata, Token: token, Materialization: material, Recipients: recipients, CanConfirm: true}, nil
}

func publicationInstructionKey(athlete, session uuid.UUID) string {
	return athlete.String() + "/" + session.String()
}

func publicationInstructionReview(source publicationSourceEnvelope, material publicationMaterialization, selected string, location *time.Location) (*pages.TrainingPublicationInstructionReview, error) {
	if len(material.Prescriptions) == 0 {
		if selected != "" {
			return nil, errTrainingPublicationReviewInvalid
		}
		return nil, nil
	}
	if len(material.Prescriptions) > trainingPublicationReviewMaxTuples {
		return nil, errTrainingPublicationReviewLimit
	}
	names := map[uuid.UUID]string{}
	for _, recipient := range source.EligibleRecipients {
		if recipient.AthleteUserID == nil {
			return nil, errPublicationSourceUnavailable
		}
		if old, ok := names[*recipient.AthleteUserID]; ok && old != recipient.AthleteName {
			return nil, errPublicationSourceUnavailable
		}
		names[*recipient.AthleteUserID] = recipient.AthleteName
	}
	titles := map[uuid.UUID]string{}
	for _, row := range source.Overview {
		if row.SessionID != nil && row.SessionTitle != nil {
			if old, ok := titles[*row.SessionID]; ok && old != *row.SessionTitle {
				return nil, errPublicationSourceUnavailable
			}
			titles[*row.SessionID] = *row.SessionTitle
		}
	}
	view := &pages.TrainingPublicationInstructionReview{}
	byKey := map[string]int{}
	for index, prescription := range material.Prescriptions {
		key := publicationInstructionKey(prescription.AthleteUserID, prescription.SessionID)
		name, named := names[prescription.AthleteUserID]
		title, titled := titles[prescription.SessionID]
		if _, duplicate := byKey[key]; duplicate || !named || !titled {
			return nil, errPublicationSourceUnavailable
		}
		byKey[key] = index
		view.Choices = append(view.Choices, pages.TrainingPublicationInstructionChoice{Key: key, Label: name + " · " + title})
	}
	sort.Slice(view.Choices, func(i, j int) bool {
		if view.Choices[i].Label == view.Choices[j].Label {
			return view.Choices[i].Key < view.Choices[j].Key
		}
		return view.Choices[i].Label < view.Choices[j].Label
	})
	if selected == "" {
		selected = view.Choices[0].Key
	}
	index, exists := byKey[selected]
	if !exists {
		return nil, errTrainingPublicationReviewInvalid
	}
	chosen := material.Prescriptions[index]
	// A single preview is bounded independently of the complete review. This
	// accommodates escaped native artifacts without rendering an entire roster.
	if len(chosen.Snapshot) > 2<<20 {
		return nil, errTrainingPublicationReviewLimit
	}
	snapshot, err := decodeStructuredPrescription(chosen.Snapshot, location)
	if err != nil {
		return nil, err
	}
	if snapshot.PlanID != source.Header.PlanID.String() || snapshot.Session.ID != chosen.SessionID.String() {
		return nil, errPublicationSourceUnavailable
	}
	view.SelectedKey, view.Athlete, view.Session = selected, names[chosen.AthleteUserID], snapshot.Session
	return view, nil
}

func applyPublicationReview(page *pages.TrainingPublicationReviewPage, preview publicationPreviewState, source publicationSourceEnvelope, selected string, location *time.Location) error {
	instructions, err := publicationInstructionReview(source, preview.Materialization, selected, location)
	if err != nil {
		return err
	}
	metadata := preview.Metadata
	page.Mode = "preview"
	page.PlanTitle = preview.Materialization.PlanTitle
	page.GroupName = preview.Materialization.GroupName
	page.WeekLabel = preview.Materialization.WeekLabel
	page.Summary = metadata.ChangeSummary
	page.SourceUpdatedAt = metadata.SourceUpdatedAt.Format(time.RFC3339Nano)
	page.ReviewToken = preview.Token
	page.LatestPublicationID = ""
	if metadata.LatestPublicationID != uuid.Nil {
		page.LatestPublicationID = metadata.LatestPublicationID.String()
	}
	page.ExistingRevision = int(metadata.LatestRevision)
	page.ReturnGroup, page.ReturnWeek, page.ReturnSession, page.ReturnDay = publicationContextFields(metadata.Return)
	page.ReturnURL, page.ReviewURL = publicationReturnURL(metadata.Return), publicationReviewURL(metadata.Return)
	page.PrescriptionCount = len(preview.Materialization.Prescriptions)
	page.AddedCount = preview.Materialization.Added
	page.ChangedCount = preview.Materialization.Changed
	page.RemovedCount = preview.Materialization.Removed
	page.UnchangedCount = preview.Materialization.Unchanged
	page.NoChanges = preview.Materialization.NoChanges
	page.CanConfirm = preview.CanConfirm
	for _, recipient := range preview.Recipients {
		page.Recipients = append(page.Recipients, pages.TrainingPublicationRecipient{Name: recipient.Name, SessionCount: recipient.SessionCount})
	}

	page.Instructions = instructions
	return nil
}
