package handlers

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func nativeReuseSourcePlan(read reuseRead) *dbgen.GetReusePlanRow {
	c := read.Source.Items[0].Graph.Context
	return &dbgen.GetReusePlanRow{ID: c.PlanID, Title: "Semana de origem sintética", TrainingGroupID: &c.GroupID, SeasonID: &c.SeasonID, GroupName: "Grupo de origem sintético", SeasonName: "Época de origem sintética", WeekStart: pgtype.Date{Time: c.WeekStart, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: c.PlanUpdatedAt, Valid: true}}
}

func TestNativeReuseWeekAndDayRoutesKeepExplicitDates(t *testing.T) {
	p, store := nativeReuseFixture(t)
	plan := nativeReuseSourcePlan(store.read)
	store.read.Source.Plan = plan
	store.read.Source.Selector = reuseSourceSelector{Kind: "WEEK", PlanID: plan.ID}
	week, err := time.Parse("2006-01-02", "2027-10-25")
	if err != nil {
		t.Fatal(err)
	}
	store.read.Target.Selector.WeekStart = week.Format("2006-01-02")
	store.read.Target.Configuration.WeekStart = pgtype.Date{Time: week, Valid: true}
	fields := reuseSelectorValues(store.read.Source.Selector, &store.read.Target.Selector)
	routes, _ := nativeReuseRoutes(p.ActorID, store, nil)
	result := nativeReuseGet(routes, fields)
	item := store.read.Source.Items[0]
	sourceDate, err := time.Parse("2006-01-02", item.Graph.Schedule.Date)
	if err != nil {
		t.Fatal(err)
	}
	dayOffset := int(sourceDate.Sub(plan.WeekStart.Time).Hours() / 24)
	wantDate := week.AddDate(0, 0, dayOffset).Format("2006-01-02")
	if result.Code != 200 || !strings.Contains(result.Body.String(), "Copiar semana") || !strings.Contains(result.Body.String(), `value="`+wantDate+`"`) || !strings.Contains(result.Body.String(), "Semana de origem sintética") {
		t.Fatalf("week route did not render source weekday and destination date: %d", result.Code)
	}

	for name, value := range initialReuseMapping(item, "2027-10-31") {
		fields[name] = value
	}
	fields.Set(reuseMappingPrefix(item.Key)+".start_time", "01:30")
	fields.Set("command", "refresh")
	result = publicationPost(t, routes, structuredReusePath+"/rascunho", fields)
	body := html.UnescapeString(result.Body.String())
	if result.Code != 200 || !strings.Contains(body, "Ocorrência da hora de início") || !strings.Contains(body, "WEST +01:00") || !strings.Contains(body, "WET +00:00") || store.saves != 0 {
		t.Fatalf("Lisbon fold choices were not distinguishable: %d writes=%d", result.Code, store.saves)
	}

	store.read.Source.Selector = reuseSourceSelector{Kind: "DAY", PlanID: plan.ID, Day: item.Graph.Schedule.Date}
	store.read.Source.Items = nil
	result = nativeReuseGet(routes, reuseSelectorValues(store.read.Source.Selector, nil))
	returnURL := structuredPlannerURL(plan.TrainingGroupID.String(), plan.ID.String(), "", item.Graph.Schedule.Date)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "Não existe conteúdo para copiar") || !strings.Contains(result.Body.String(), html.EscapeString(returnURL)) || strings.Contains(result.Body.String(), `value="preview"`) {
		t.Fatalf("empty source day offered a copy or lost its return context: %d", result.Code)
	}
}

func TestNativeReuseRoutineLibraryPaginatesWithoutChangingTarget(t *testing.T) {
	p, store := nativeReuseFixture(t)
	target := p.Target.Selector
	groups := &taskGroupsFake{group: TrainingGroupRecord{ID: target.GroupID, Name: "Grupo de destino sintético"}}
	for i := 1; i <= 21; i++ {
		store.routines = append(store.routines, dbgen.ListReuseRoutinesRow{ID: uuid.New(), Name: fmt.Sprintf("Rotina sintética %02d", i), Kind: "SESSION", Description: "Exercício de biblioteca"})
	}
	fields := reuseSelectorValues(reuseSourceSelector{Kind: "ROUTINE"}, &target)
	fields.Set("routine_page", "2")
	fields.Set("routine_query", "Sintética")
	fields.Set("routine_modality", "WATER")
	fields.Set("routine_objective", "Técnica")
	fields.Set("routine_tag", "Ritmo")
	routes, _ := nativeReuseRoutes(p.ActorID, store, groups)
	result := nativeReuseGet(routes, fields)
	body := result.Body.String()
	if result.Code != 200 || store.routineQuery.ActorID != p.ActorID || store.routineQuery.RowOffset != 40 || store.routineQuery.Query != "Sintética" || store.routineQuery.Modality != "WATER" || store.routineQuery.Objective != "Técnica" || store.routineQuery.Tag != "Ritmo" {
		t.Fatalf("routine filters or paging changed: status=%d query=%+v", result.Code, store.routineQuery)
	}
	selected := reuseTaskURL(reuseSourceSelector{Kind: "ROUTINE", RoutineID: store.routines[0].ID}, &target)
	if !strings.Contains(body, "Grupo de destino sintético") || !strings.Contains(body, "Rotina sintética 20") || strings.Contains(body, "Rotina sintética 21") || !strings.Contains(body, html.EscapeString(selected)) {
		t.Fatal("library leaked its extra result or changed the selected destination")
	}
	for _, page := range []string{"1", "3"} {
		pagination := cloneFocusedValues(fields)
		pagination.Set("routine_page", page)
		if !strings.Contains(body, html.EscapeString(structuredReusePath+"?"+pagination.Encode())) {
			t.Fatalf("paging link lost the filters or target: page %s", page)
		}
	}
	fields.Set("routine_query", strings.Repeat("a", 101))
	store.routineQuery = dbgen.ListReuseRoutinesParams{}
	result = nativeReuseGet(routes, fields)
	if result.Code != 200 || store.routineQuery.ActorID != uuid.Nil || !strings.Contains(result.Body.String(), "Reveja este filtro") || strings.Contains(result.Body.String(), "Rotina sintética 01") {
		t.Fatalf("invalid library filter queried or exposed routines: %d", result.Code)
	}
	fields.Set("routine_query", "Sintética")
	store.routineErr = pgx.ErrNoRows
	result = nativeReuseGet(routes, fields)
	if result.Code != 404 || strings.Contains(result.Body.String(), "Grupo de destino sintético") {
		t.Fatalf("denied routine lookup retained library context: %d", result.Code)
	}
}

func nativeReuseRoutineSource(store *nativeReuseRouteStore, kind dbgen.TrainingRoutineKind) {
	id := uuid.New()
	store.read.Source.Selector = reuseSourceSelector{Kind: "ROUTINE", RoutineID: id}
	store.read.Source.Routine = &dbgen.GetReuseRoutineRow{ID: id, Kind: kind, Name: "Rotina fixa sintética"}
	item := &store.read.Source.Items[0]
	item.Raw = nil
	item.Graph.Schedule = TrainingSessionSchedule{}
	if kind == "SESSION" {
		item.DurationSeconds = focusedPointer(int64(720))
	} else {
		item.FragmentKind = kind
	}
}

func TestNativeReuseBlockRoutineScopesAndBoundsDestinationChoices(t *testing.T) {
	for _, mode := range []string{"scoped choices", "session cap", "activity cap", "session access revoked", "activity access revoked", "group lookup revoked", "season cap"} {
		t.Run(mode, func(t *testing.T) {
			p, store := nativeReuseFixture(t)
			nativeReuseRoutineSource(store, "BLOCK")
			target := &store.read.Target.Selector
			old := focusedEditFixture(t)
			planID, sessionID := old.Context.PlanID, old.Raw.Session.ID
			segmentID := old.Raw.Segments[0].ID
			target.SessionID, target.SegmentID = sessionID, segmentID
			store.read.Target.Plan = &dbgen.GetReusePlanRow{ID: planID, Title: "Semana privada de destino"}
			store.read.Target.Configuration.ExistingPlanID = &planID
			store.read.Target.Existing, store.read.TargetGraph = &old.Raw, &old.Graph
			store.read.DecisionAt = old.DecisionAt
			store.sessions = []dbgen.ListReuseSourceSessionsRow{{ID: sessionID, PlanID: planID, Title: "Treino elegível sintético", StartsAt: old.Raw.Session.StartsAt, SchedulePrecision: "START_ONLY", ScheduledOn: old.Raw.Session.ScheduledOn, SessionOn: old.Raw.Session.ScheduledOn, EntryKind: "TRAINING", Status: "ACTIVE"}, {ID: uuid.New(), Title: "Treino cancelado excluído", EntryKind: "TRAINING", Status: "CANCELLED"}}
			store.segments = []dbgen.TrainingSessionSegment{{ID: segmentID, Title: "Atividade elegível sintética", Modality: "WATER"}}
			groups := &taskGroupsFake{group: TrainingGroupRecord{ID: target.GroupID, Name: "Grupo fixado fora desta página"}}
			for i := 0; i < 20; i++ {
				groups.groups = append(groups.groups, TrainingGroupRecord{ID: uuid.New(), Name: fmt.Sprintf("Grupo sintético %02d", i)})
			}
			status, message := 200, ""
			switch mode {
			case "session cap":
				store.sessions = make([]dbgen.ListReuseSourceSessionsRow, 65)
				message = "A semana excede o limite técnico"
			case "activity cap":
				store.segments = make([]dbgen.TrainingSessionSegment, 9)
				message = "A sessão excede o limite técnico"
			case "season cap":
				store.seasons = make([]dbgen.ListReuseTargetSeasonsRow, 101)
				message = "Há demasiadas épocas para esta lista"
			case "session access revoked":
				store.sessionErr, status = pgx.ErrNoRows, 404
			case "activity access revoked":
				store.segmentErr, status = pgx.ErrNoRows, 404
			case "group lookup revoked":
				groups.groupErr, status = pgx.ErrNoRows, 404
			}
			fields := reuseSelectorValues(store.read.Source.Selector, target)
			fields.Set("group_page", "1")
			routes, _ := nativeReuseRoutes(p.ActorID, store, groups)
			result := nativeReuseGet(routes, fields)
			body := result.Body.String()
			if result.Code != status || !strings.Contains(body, message) || strings.Contains(body, `value="preview"`) != (mode == "scoped choices") || store.saves != 0 {
				t.Fatalf("destination choice status=%d message=%t writes=%d", result.Code, strings.Contains(body, message), store.saves)
			}
			if status == 404 && (strings.Contains(body, "Rotina fixa sintética") || strings.Contains(body, "Semana privada de destino")) {
				t.Fatal("revoked destination rendered private context")
			}
			if mode == "scoped choices" {
				for _, text := range []string{"Rotina fixa sintética", "Grupo fixado fora desta página", "Grupos anteriores", "Mais grupos", "Treino elegível sintético", "Atividade elegível sintética"} {
					if !strings.Contains(body, text) {
						t.Fatalf("missing authorized choice %q", text)
					}
				}
				if strings.Contains(body, "Treino cancelado excluído") || groups.listOffset != 20 || groups.gets != 1 || store.segmentLookup != (FocusedWorkoutEditRequest{p.ActorID, planID, sessionID}) {
					t.Fatal("destination options were not scoped to the selected plan/session or included cancelled work")
				}
			}
		})
	}
}

func TestNativeReuseSessionRoutineShowsDurationWithoutAnInventedAppointment(t *testing.T) {
	p, store := nativeReuseFixture(t)
	nativeReuseRoutineSource(store, "SESSION")
	store.read.Target = nil
	store.read.Source.Items[0].Graph.Title = "Instrução integral de rotina"
	routes, _ := nativeReuseRoutes(p.ActorID, store, nil)
	fields := reuseSelectorValues(store.read.Source.Selector, nil)
	result := nativeReuseGet(routes, fields)
	body := result.Body.String()
	if result.Code != 200 || !strings.Contains(body, "Rotina fixa sintética") || !strings.Contains(body, "Instrução integral de rotina") || !strings.Contains(body, "720 s") || strings.Contains(body, `name="mappings[m_1].end_time"`) || strings.Contains(body, `value="0001-01-01"`) {
		t.Fatalf("SESSION routine lost represented duration or invented its appointment: %d", result.Code)
	}
	store.read.State.PartialV2Enabled = false
	result = nativeReuseGet(routes, fields)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "A criação de cópias está temporariamente indisponível") || strings.Contains(result.Body.String(), `value="preview"`) {
		t.Fatalf("closed writer offered to review a routine: %d", result.Code)
	}
}

func TestNativeReuseReadLimitRetainsAuthorizedSourceWithoutOfferingCopy(t *testing.T) {
	p, store := nativeReuseFixture(t)
	store.read.Source.Plan = nativeReuseSourcePlan(store.read)
	store.readErr = errors.Join(errFocusedWorkoutLimit, errors.New("synthetic internal diagnostic"))
	routes, _ := nativeReuseRoutes(p.ActorID, store, nil)
	result := nativeReuseGet(routes, p.Values)
	body := result.Body.String()
	if result.Code != 422 || !strings.Contains(body, "Semana de origem sintética") || !strings.Contains(body, "Nada foi copiado parcialmente") || strings.Contains(body, `value="preview"`) || strings.Contains(body, "synthetic internal diagnostic") || store.saves != 0 {
		t.Fatalf("source limit exposed internal details or offered partial copy: %d", result.Code)
	}
}
