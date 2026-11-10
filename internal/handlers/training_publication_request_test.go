package handlers

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicationRequestKeepsOriginalReviewedBaseline(t *testing.T) {
	actor, plan, latest, group := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	values := url.Values{"source_updated_at": {"2026-11-10T11:00:00.123456999+01:00"}, "change_summary": {"  Resumo revisto  "}, "reviewed_latest_id": {latest.String()}, "reviewed_latest_revision": {"4"}, "return_group": {group.String()}, "return_week": {plan.String()}, "return_day": {"2026-11-10"}}
	metadata, err := publicationRequestMetadata(values, actor, plan)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.LatestPublicationID != latest || metadata.LatestRevision != 4 || metadata.ChangeSummary != "Resumo revisto" || !metadata.SourceUpdatedAt.Equal(time.Date(2026, 11, 10, 10, 0, 0, 123456000, time.UTC)) {
		t.Fatal("original review metadata rebased or not normalized")
	}
	for _, key := range []string{"source_updated_at", "change_summary", "reviewed_latest_id", "reviewed_latest_revision", "return_group", "return_day"} {
		copy := clonePublicationValues(values)
		copy.Add(key, "substitution")
		if _, err := publicationRequestMetadata(copy, actor, plan); err == nil {
			t.Fatal("duplicate field accepted", key)
		}
	}
	for _, bad := range []string{"-1", "04", "2147483648", ""} {
		copy := clonePublicationValues(values)
		copy.Set("reviewed_latest_revision", bad)
		if _, err := publicationRequestMetadata(copy, actor, plan); err == nil {
			t.Fatal("invalid reviewed revision accepted")
		}
	}
}

func TestPublicationPreviewReturnPreservesExactContextAndRejectsRedirect(t *testing.T) {
	source := publicationSourceFixture()
	header := source.Header
	session := *source.Overview[0].SessionID
	exact := structuredPlannerURL(header.GroupID.String(), header.PlanID.String(), session.String(), "2026-11-09")
	c, err := publicationPreviewReturn(url.Values{"return_to": {exact}}, header)
	if err != nil || c.GroupID != header.GroupID || c.WeekID != header.PlanID || c.SessionID != session || c.Day != "2026-11-09" || publicationReturnURL(c) != exact {
		t.Fatal("exact return lost", err)
	}
	if !strings.HasSuffix(publicationReviewURL(c), "#training-publication") {
		t.Fatal("review anchor lost")
	}
	for _, bad := range []string{"https://example.invalid/", "//example.invalid/", structuredPlannerURL(uuid.NewString(), header.PlanID.String(), ""), structuredPlannerURL(header.GroupID.String(), uuid.NewString(), "")} {
		if _, err := publicationPreviewReturn(url.Values{"return_to": {bad}}, header); err == nil {
			t.Fatal("foreign context accepted")
		}
	}
	emptyDay := url.Values{"return_group": {header.GroupID.String()}, "return_week": {header.PlanID.String()}, "return_day": {"2026-11-14"}}
	c, err = publicationPreviewReturn(emptyDay, header)
	if err != nil || !publicationReturnMatchesSource(c, source) || c.SessionID != uuid.Nil || c.Day != "2026-11-14" {
		t.Fatal("empty Saturday selection lost", err)
	}
}

func clonePublicationValues(values url.Values) url.Values {
	result := url.Values{}
	for k, v := range values {
		result[k] = append([]string(nil), v...)
	}
	return result
}
