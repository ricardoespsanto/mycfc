package handlers

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

type publicationPreviewRecipient struct {
	Name         string
	SessionCount int
}
type publicationPreviewState struct {
	Metadata        trainingPublicationReviewMetadata
	Token           string
	Materialization publicationMaterialization
	Recipients      []publicationPreviewRecipient
	CanConfirm      bool
}

// An explicit preview action may read today's source and sign the newly shown
// exact review. A failed confirm never calls this function automatically.
func makePublicationPreview(source publicationSourceEnvelope, actor uuid.UUID, summary string, returned trainingPublicationReturnContext, location *time.Location, now time.Time) (publicationPreviewState, error) {
	state := publicationPreviewState{}
	if !publicationReturnMatchesSource(returned, source) {
		return state, errTrainingPublicationReviewInvalid
	}
	metadata := trainingPublicationReviewMetadata{ActorID: actor, PlanID: source.Header.PlanID, SourceUpdatedAt: source.Header.SourceUpdatedAt.Time, LatestRevision: source.Header.LatestRevision, ChangeSummary: summary, Return: returned}
	if source.Header.LatestPublicationID != nil {
		metadata.LatestPublicationID = *source.Header.LatestPublicationID
	}
	normalized, _, err := trainingPublicationMetadataBinding(metadata)
	if err != nil {
		return state, err
	}
	state.Metadata = normalized
	state.Materialization, err = materializePublicationSource(source, location)
	if err != nil {
		return state, err
	}
	state.Token, err = trainingPublicationReviews.sign(trainingPublicationReviewInput{normalized, state.Materialization.Prescriptions}, now)
	if err != nil {
		return state, err
	}
	state.Recipients, err = publicationPreviewRecipients(source)
	if err != nil {
		return publicationPreviewState{}, err
	}
	state.CanConfirm = true
	return state, nil
}

func publicationPreviewRecipients(source publicationSourceEnvelope) ([]publicationPreviewRecipient, error) {
	byID := map[uuid.UUID]publicationPreviewRecipient{}
	sessions := map[uuid.UUID]map[uuid.UUID]bool{}
	for _, r := range source.EligibleRecipients {
		if r.AthleteUserID == nil || r.SessionID == uuid.Nil {
			return nil, errPublicationSourceUnavailable
		}
		id := *r.AthleteUserID
		recipient, exists := byID[id]
		if exists && recipient.Name != r.AthleteName {
			return nil, errPublicationSourceUnavailable
		}
		recipient.Name = r.AthleteName
		if sessions[id] == nil {
			sessions[id] = map[uuid.UUID]bool{}
		}
		if !sessions[id][r.SessionID] {
			recipient.SessionCount++
			sessions[id][r.SessionID] = true
		}
		byID[id] = recipient
	}
	result := make([]publicationPreviewRecipient, 0, len(byID))
	for _, r := range byID {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].SessionCount < result[j].SessionCount
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
