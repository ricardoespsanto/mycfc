package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type plannerBoundaryGroups struct {
	*taskGroupsFake
	weekErr error
}

func (s plannerBoundaryGroups) AllowsWeek(ctx context.Context, actor, group, week uuid.UUID) (bool, error) {
	if s.weekErr != nil {
		return false, s.weekErr
	}
	return s.taskGroupsFake.AllowsWeek(ctx, actor, group, week)
}

func TestStructuredPlannerRechecksSessionReturnScope(t *testing.T) {
	actor := CurrentUser{ID: uuid.New(), IsAdmin: true}
	group, plan, session, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	title, start := "Private planning week", time.Date(2032, 1, 6, 12, 0, 0, 0, time.UTC)
	row := dbgen.ListStructuredTrainingOverviewForManagerRow{GroupID: group, GroupName: "Private planning group", PlanID: &plan, PlanTitle: &title, WeekStart: pgtype.Date{Time: start, Valid: true}}
	failure := errors.New("private database failure")
	for _, tc := range []struct {
		name                    string
		weekErr, groupErr, read error
		omitReturn, omitPlan    bool
		status, writes          int
	}{
		{name: "verified fallback", status: 303, writes: 1},
		{name: "no caller destination", omitReturn: true, status: 303, writes: 1},
		{name: "group-week proof read fails", weekErr: failure, status: 500},
		{name: "scope overview read fails", read: failure, status: 500},
		{name: "plan dropped from authoritative scope", omitPlan: true, status: 404},
		{name: "group authority revoked", groupErr: pgx.ErrNoRows, status: 404},
		{name: "group read fails", groupErr: failure, status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{weekOK: true, createdSessionID: session, overviewErr: tc.read, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{row}}
			if tc.omitPlan {
				store.overviewRows = nil
			}
			groups := plannerBoundaryGroups{taskGroupsFake: &taskGroupsFake{group: TrainingGroupRecord{ID: group}, groupErr: tc.groupErr}, weekErr: tc.weekErr}
			h := StructuredTraining{Store: store, Groups: groups, Location: time.UTC}
			form := url.Values{"plan_id": {plan.String()}, "title": {"Synthetic review"}, "starts_at": {"2032-01-06T12:00"}, "ends_at": {"2032-01-06T13:00"}, "entry_kind": {"TRAINING"}}
			if !tc.omitReturn {
				form.Set("return_to", structuredPlannerURL(foreign.String(), plan.String(), ""))
			}
			res := performStructuredTrainingRequest(t, actor, "POST", structuredPlannerPath, form, "", "", h.CreateSession)
			wantLocation := ""
			if tc.writes == 1 {
				wantLocation = structuredPlannerURL(group.String(), plan.String(), session.String(), "2032-01-06")
				if tc.omitReturn {
					wantLocation = structuredPlannerPath + "#training-plan"
				}
			}
			if res.Code != tc.status || len(store.sessions) != tc.writes || res.Header().Get("Location") != wantLocation {
				t.Fatalf("status=%d writes=%d location=%q", res.Code, len(store.sessions), res.Header().Get("Location"))
			}
			if tc.writes == 0 && (strings.Contains(res.Body.String(), title) || strings.Contains(res.Body.String(), failure.Error())) {
				t.Fatal("denied or failed request exposed private planning state")
			}
		})
	}

	h := StructuredTraining{Store: &structuredTrainingStoreStub{overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{row}}, Location: time.UTC}
	for _, tc := range []struct{ task, marker string }{
		{"session", "name=\"starts_at\""},
		{"variation", "name=\"change_summary\""},
		{"week", "name=\"week_start\""},
	} {
		query := url.Values{"group_id": {group.String()}, "week_id": {plan.String()}, "task": {tc.task}}
		res := performStructuredTrainingRequest(t, actor, "GET", structuredPlannerPath+"?"+query.Encode(), nil, "", "", h.Index)
		if res.Code != 200 || !strings.Contains(res.Body.String(), tc.marker) {
			t.Fatalf("task %s was not opened: HTTP %d", tc.task, res.Code)
		}
	}
}

func TestStructuredPlannerRejectsRepeatedAndStaleVariationInputs(t *testing.T) {
	actor := CurrentUser{ID: uuid.New(), IsAdmin: true}
	group, plan, session, block, member := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	weekTitle, sessionTitle := "Authorized week", "Authorized workout"
	start := time.Date(2032, 1, 6, 12, 0, 0, 0, time.UTC)
	row := dbgen.ListStructuredTrainingOverviewForManagerRow{GroupID: group, GroupName: "Authorized group", PlanID: &plan, PlanTitle: &weekTitle, WeekStart: pgtype.Date{Time: start, Valid: true}, SessionID: &session, SessionTitle: &sessionTitle, StartsAt: pgtype.Timestamptz{Time: start, Valid: true}, EndsAt: pgtype.Timestamptz{Time: start.Add(time.Hour), Valid: true}}
	for _, tc := range []struct {
		name, key, value, message string
		err                       error
		code, writes              int
	}{
		{"repeated target", "duplicate", "", "campos repetidos", nil, 422, 0},
		{"invalid subject", "subject", "invalid", "Selecione o alvo", nil, 422, 0},
		{"wrong reference for omit", "intensity_profile_id", uuid.NewString(), "Escolha uma referência válida", nil, 422, 0},
		{"unknown target type", "target", "UNKNOWN:" + member.String(), "Selecione um atleta", nil, 422, 0},
		{"concurrent change", "", "", "O plano ou os atletas mudaram", errStructuredTrainingPublicationConflict, 409, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{weekOK: true, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{row}, createVariationErr: tc.err}
			h := StructuredTraining{Store: store, Location: time.UTC}
			form := url.Values{"plan_id": {plan.String()}, "target": {"ATHLETE:" + member.String()}, "subject": {"BLOCK:" + block.String()}, "operation": {"OMIT"}, "change_summary": {"Keep submitted review"}, "gorilla.csrf.Token": {"private-form-token"}}
			if tc.key == "duplicate" {
				form.Add("target", form.Get("target"))
			} else if tc.key != "" {
				form.Set(tc.key, tc.value)
			}
			res := performStructuredTrainingRequest(t, actor, "POST", structuredPlannerPath, form, "", "", h.CreateVariation)
			body := res.Body.String()
			if res.Code != tc.code || len(store.variationInputs) != tc.writes || res.Header().Get("Location") != "" ||
				!strings.Contains(body, tc.message) || !strings.Contains(body, "Keep submitted review") || strings.Contains(body, "private-form-token") {
				t.Fatalf("status=%d writes=%d, expected safe feedback %q", res.Code, len(store.variationInputs), tc.message)
			}
		})
	}

	store := &structuredTrainingStoreStub{manageable: true, createVariationGroupErr: db.ErrCommitOutcomeUnknown}
	h := StructuredTraining{Store: store, Location: time.UTC}
	form := url.Values{"training_group_id": {group.String()}, "name": {"Reviewed subgroup"}, "kind": {"SUBGROUP"}, "membership_id": {member.String()}, "effective_from": {"2032-01-06"}}
	for _, tc := range []struct {
		err  error
		code int
		msg  string
	}{{db.ErrCommitOutcomeUnknown, 503, "não será repetido automaticamente"}, {errStructuredTrainingPublicationConflict, 409, "os atletas ou as colocações mudaram"}} {
		store.createVariationGroupErr = tc.err
		res := performStructuredTrainingRequest(t, actor, "POST", structuredPlannerPath, form, "", "", h.CreateVariationGroup)
		if res.Code != tc.code || !strings.Contains(strings.ToLower(res.Body.String()), strings.ToLower(tc.msg)) || res.Header().Get("Location") != "" {
			t.Fatalf("status=%d for %v", res.Code, tc.err)
		}
	}
}

type plannerBoundaryViewer struct {
	*structuredTrainingStoreStub
	rows []dbgen.ListTrainingPrescriptionsForViewerRow
}

func (s plannerBoundaryViewer) ListTrainingPrescriptionsForViewer(context.Context, dbgen.ListTrainingPrescriptionsForViewerParams) ([]dbgen.ListTrainingPrescriptionsForViewerRow, error) {
	return s.rows, nil
}

func TestStructuredPlannerWithholdsUnreadablePrescriptionContents(t *testing.T) {
	actor := CurrentUser{ID: uuid.New()}
	id := uuid.New()
	unreadable := []byte(`{"schema_version":99,"session":{"title":"private unknown-schema instructions"}}`)
	store := plannerBoundaryViewer{
		structuredTrainingStoreStub: &structuredTrainingStoreStub{prescriptionRow: dbgen.GetTrainingPrescriptionForViewerRow{ID: id, AthleteUserID: actor.ID, Snapshot: unreadable}},
		rows:                        []dbgen.ListTrainingPrescriptionsForViewerRow{{ID: id, AthleteUserID: actor.ID, Snapshot: unreadable}},
	}
	h := StructuredTraining{Store: store, Location: time.UTC}
	for _, handler := range []http.HandlerFunc{h.Index, h.PrescriptionDetail} {
		res := performStructuredTrainingRequest(t, actor, "GET", "/treinos/estruturados", nil, "id", id.String(), handler)
		if res.Code != 503 || res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Referrer-Policy") != "no-referrer" ||
			strings.Contains(res.Body.String(), "private unknown-schema instructions") {
			t.Fatalf("unreadable prescription status=%d headers=%v", res.Code, res.Header())
		}
	}
}
