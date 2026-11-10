package handlers

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Confirmation metadata is the original reviewed form, not today's latest
// publication reconstructed before token verification. SQL compares it later.
func publicationRequestMetadata(values url.Values, actor, plan uuid.UUID) (trainingPublicationReviewMetadata, error) {
	for _, key := range []string{"source_updated_at", "change_summary", "reviewed_latest_id", "reviewed_latest_revision", "return_group", "return_week", "return_session", "return_day", "review_token", "publication_intent", "confirm"} {
		if len(values[key]) > 1 {
			return trainingPublicationReviewMetadata{}, errTrainingPublicationReviewInvalid
		}
	}
	metadata := trainingPublicationReviewMetadata{ActorID: actor, PlanID: plan, ChangeSummary: values.Get("change_summary")}
	var err error
	metadata.SourceUpdatedAt, err = time.Parse(time.RFC3339Nano, values.Get("source_updated_at"))
	if err != nil {
		return metadata, errTrainingPublicationReviewInvalid
	}
	if raw := values.Get("reviewed_latest_id"); raw != "" {
		metadata.LatestPublicationID, err = publicationRequestID(raw)
		if err != nil {
			return metadata, err
		}
	}
	rawRevision := values.Get("reviewed_latest_revision")
	revision, err := strconv.ParseInt(rawRevision, 10, 32)
	if err != nil || revision < 0 || strconv.FormatInt(revision, 10) != rawRevision {
		return metadata, errTrainingPublicationReviewInvalid
	}
	metadata.LatestRevision = int32(revision)
	metadata.Return, err = publicationTypedReturn(values)
	if err != nil {
		return metadata, err
	}
	normalized, _, err := trainingPublicationMetadataBinding(metadata)
	return normalized, err
}
func publicationRequestID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || id.String() != raw {
		return uuid.Nil, errTrainingPublicationReviewInvalid
	}
	return id, nil
}
func publicationTypedReturn(values url.Values) (trainingPublicationReturnContext, error) {
	var result trainingPublicationReturnContext
	for key, target := range map[string]*uuid.UUID{"return_group": &result.GroupID, "return_week": &result.WeekID, "return_session": &result.SessionID} {
		if raw := values.Get(key); raw != "" {
			id, err := publicationRequestID(raw)
			if err != nil {
				return result, err
			}
			*target = id
		}
	}
	result.Day = values.Get("return_day")
	return result, nil
}
func publicationPreviewReturn(values url.Values, header publicationPlanHeader) (trainingPublicationReturnContext, error) {
	typed := false
	for _, field := range []string{"return_group", "return_week", "return_session", "return_day"} {
		if _, ok := values[field]; ok {
			typed = true
		}
	}
	if typed {
		return publicationTypedReturn(values)
	}
	result := trainingPublicationReturnContext{GroupID: header.GroupID, WeekID: header.PlanID}
	if raw := values.Get("return_to"); raw != "" {
		canonical := structuredPlannerReturn(raw)
		// A plain planner collection is the old general publication form default.
		if canonical == "" {
			if raw == structuredPlannerPath+"#training-plan" {
				return result, nil
			}
			return result, errTrainingPublicationReviewInvalid
		}
		parsed, _ := url.Parse(canonical)
		q := parsed.Query()
		if q.Get("group_id") != header.GroupID.String() || q.Get("week_id") != "" && q.Get("week_id") != header.PlanID.String() {
			return result, errTrainingPublicationReviewInvalid
		}
		if session := q.Get("session_id"); session != "" {
			id, err := publicationRequestID(session)
			if err != nil {
				return result, err
			}
			result.SessionID = id
		}
		result.Day = q.Get("day")
	}
	return result, nil
}
func publicationReturnURL(c trainingPublicationReturnContext) string {
	if c.GroupID == uuid.Nil {
		return structuredPlannerPath + "#training-publication"
	}
	week, session := "", ""
	if c.WeekID != uuid.Nil {
		week = c.WeekID.String()
	}
	if c.SessionID != uuid.Nil {
		session = c.SessionID.String()
	}
	return structuredPlannerURL(c.GroupID.String(), week, session, c.Day)
}
func publicationReviewURL(c trainingPublicationReturnContext) string {
	target := publicationReturnURL(c)
	if fragment := strings.IndexByte(target, '#'); fragment >= 0 {
		target = target[:fragment]
	}
	return target + "#training-publication"
}
