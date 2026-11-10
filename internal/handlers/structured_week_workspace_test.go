package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
)

func workspaceFixture() (pages.StructuredTrainingAudience, string, string) {
	group, week := uuid.NewString(), uuid.NewString()
	return pages.StructuredTrainingAudience{GroupID: group, GroupName: "Group week", Scope: "Iniciação", MemberCount: 4, Weeks: []pages.StructuredTrainingWeek{{ID: week, Title: "Week actual", Season: "Configured season", StartsOn: "2026-11-09", EndsOn: "2026-11-15", DateRange: "DO NOT PARSE LOCALIZED LABEL", Sessions: []pages.StructuredTrainingSession{
		{ID: uuid.NewString(), Title: "Second afternoon", StartsOn: "2026-11-09", EndsOn: "2026-11-09", StartsAt: "18:00", EndsAt: "19:15", When: "DO NOT PARSE", EntryKind: "TRAINING", Status: "ACTIVE", Modalities: []string{"GYM", "RUNNING"}},
		{ID: uuid.NewString(), Title: "First afternoon", StartsOn: "2026-11-09", EndsOn: "2026-11-09", StartsAt: "16:00", EndsAt: "17:30", EntryKind: "TRAINING", Status: "CANCELLED"},
		{ID: uuid.NewString(), Title: "Saturday water", StartsOn: "2026-11-14", EndsOn: "2026-11-14", StartsAt: "10:30", EndsAt: "12:15", EntryKind: "TRAINING", Status: "ACTIVE"},
		{ID: uuid.NewString(), Title: "Explicit rest", StartsOn: "2026-11-10", EndsOn: "2026-11-10", StartsAt: "07:00", EndsAt: "08:00", EntryKind: "REST", Status: "ACTIVE"},
	}, Summary: pages.StructuredTrainingWeekSummary{PlannedWater: []pages.StructuredTrainingSummaryItem{{Label: "Duration", Value: "Por definir", Certainty: "UNKNOWN"}}}}}}, group, week
}
func TestWeekWorkspaceUsesWholeWeekRawDatesAndActualIntervals(t *testing.T) {
	audience, group, week := workspaceFixture()
	selected := audience.Weeks[0].Sessions[0].ID
	current := TrainingGroupRecord{ID: uuid.MustParse(group), Name: "Fresh group name", ScopeName: "Fresh exact scope", MemberCount: 2}
	now, _ := time.Parse("2006-01-02", "2026-11-13")
	out := buildStructuredWeekWorkspace([]pages.StructuredTrainingAudience{audience}, []pages.StructuredPublicationState{{PlanID: week}}, false, &current, CurrentUser{Name: "Coach"}, group, week, selected, "2026-11-13", now)
	if !out.Enabled || !out.HasWeek || len(out.Days) != 7 || out.GroupName != current.Name || out.MemberCount != 2 || out.Scope != current.ScopeName {
		t.Fatalf("workspace %#v", out)
	}
	if out.SelectedDay != "2026-11-09" || out.Days[0].Date != "2026-11-09" || out.Days[6].Date != "2026-11-15" {
		t.Fatalf("days/selection %#v", out.Days)
	}
	if len(out.Days[0].Afternoon) != 2 || out.Days[0].Afternoon[0].StartsAt != "16:00" || out.Days[0].Afternoon[0].Status != "CANCELLED" {
		t.Fatalf("full chronological afternoon %#v", out.Days[0])
	}
	if len(out.Days[5].Morning) != 1 || len(out.Days[5].Afternoon) != 0 || out.Days[5].Morning[0].EndsAt != "12:15" {
		t.Fatal("cross-noon appointment duplicated or fabricated")
	}
	if len(out.Days[1].Morning) != 1 || out.Days[1].Morning[0].EntryKind != "REST" || len(out.Days[2].Morning)+len(out.Days[2].Afternoon) != 0 {
		t.Fatal("unplanned/rest distinction lost")
	}
	if out.Summary.PlannedWater[0].Certainty != "UNKNOWN" || out.Summary.PlannedWater[0].Value != "Por definir" {
		t.Fatal("unknown summary invented")
	}
	for _, day := range out.Days {
		if !strings.Contains(day.URL, "group_id="+group) || !strings.Contains(day.URL, "week_id="+week) || !strings.Contains(day.AddSessionURL, "day="+day.Date) || !strings.Contains(day.AddSessionURL, "task=session") {
			t.Fatalf("lost native exact context %#v", day)
		}
	}
	// A detail-engine selection may be narrowed independently; the overview keeps all work.
	filtered := structuredPlannerContextAudience([]pages.StructuredTrainingAudience{audience}, group, week, selected)
	if len(filtered[0].Weeks[0].Sessions) != 1 || len(out.Days[0].Afternoon) != 2 {
		t.Fatal("session filtering removed calendar work")
	}
}
func TestWeekWorkspaceDayDefaultsAndEmptyGroupDraft(t *testing.T) {
	audience, group, week := workspaceFixture()
	today, _ := time.Parse("2006-01-02", "2026-11-13")
	for _, tc := range []struct {
		day  string
		now  time.Time
		want string
	}{{"", today, "2026-11-13"}, {"invalid", today, "2026-11-13"}, {"2026-11-30", today.AddDate(0, 1, 0), "2026-11-09"}, {"2026-11-14", today, "2026-11-14"}} {
		out := buildStructuredWeekWorkspace([]pages.StructuredTrainingAudience{audience}, nil, false, nil, CurrentUser{}, group, week, "", tc.day, tc.now)
		if out.SelectedDay != tc.want {
			t.Fatalf("day=%q got=%q want=%q", tc.day, out.SelectedDay, tc.want)
		}
	}
	audience.Weeks = nil
	out := buildStructuredWeekWorkspace([]pages.StructuredTrainingAudience{audience}, nil, false, nil, CurrentUser{}, group, "", "", "", today)
	if !out.Enabled || out.HasWeek || out.GroupID != group || !strings.Contains(out.CreateWeekURL, "group_id="+group) || !strings.Contains(out.CreateWeekURL, "task=week") || out.SelectedDay != "" || len(out.Days) != 0 {
		t.Fatalf("empty draft group %#v", out)
	}
}
func TestWeekWorkspacePublicationStatesRemainTruthful(t *testing.T) {
	audience, group, week := workspaceFixture()
	for _, tc := range []struct {
		name        string
		publication *pages.StructuredPublicationState
		unavailable bool
		want        string
	}{{"never", &pages.StructuredPublicationState{PlanID: week}, false, "never"}, {"current", &pages.StructuredPublicationState{PlanID: week, Revision: 3, Current: true, SourceUpdatedAt: "opaque-source"}, false, "current"}, {"changes", &pages.StructuredPublicationState{PlanID: week, Revision: 4, ConflictCount: 2}, false, "changes"}, {"query failure", &pages.StructuredPublicationState{PlanID: week}, true, "unavailable"}, {"recipient failure", &pages.StructuredPublicationState{PlanID: week, Unavailable: true}, false, "unavailable"}, {"missing authoritative row", nil, false, "unavailable"}} {
		t.Run(tc.name, func(t *testing.T) {
			var publications []pages.StructuredPublicationState
			if tc.publication != nil {
				publications = append(publications, *tc.publication)
			}
			out := buildStructuredWeekWorkspace([]pages.StructuredTrainingAudience{audience}, publications, tc.unavailable, nil, CurrentUser{}, group, week, "", "", time.Now())
			if out.Publication.State != tc.want {
				t.Fatalf("publication %#v", out.Publication)
			}
			if tc.want == "unavailable" && !out.Publication.Unavailable {
				t.Fatal("unreadable publication mislabeled draft")
			}
			if tc.want == "changes" && (out.Publication.ConflictCount != 2 || out.Publication.Revision != 4) {
				t.Fatal("real conflict/revision lost")
			}
		})
	}
}
func TestWeekWorkspaceRawMetadataCannotChangePrescriptionJSONOrHash(t *testing.T) {
	base := structuredPrescriptionSnapshot{SchemaVersion: 1, PlanID: uuid.NewString(), Session: pages.StructuredTrainingSession{ID: uuid.NewString(), Title: "Immutable existing session", When: "09/11/2026 16:00–17:30", EntryKind: "TRAINING"}}
	original, e := json.Marshal(base)
	if e != nil {
		t.Fatal(e)
	}
	base.Session.StartsOn = "2026-11-09"
	base.Session.EndsOn = "2026-11-09"
	base.Session.StartsAt = "16:00"
	base.Session.EndsAt = "17:30"
	base.Session.Status = "CANCELLED"
	enriched, e := json.Marshal(base)
	if e != nil {
		t.Fatal(e)
	}
	if string(original) != string(enriched) || sha256.Sum256(original) != sha256.Sum256(enriched) {
		t.Fatalf("view metadata changed prescription JSON/hash before=%s after=%s", original, enriched)
	}
	// Exercise the actual canonical prescription builder as well as raw JSON.
	planID, memberID, athleteID := uuid.MustParse(base.PlanID), uuid.New(), uuid.New()
	recipient := []dbgen.ListStructuredTrainingPublicationMembersRow{{MembershipID: memberID, AthleteUserID: &athleteID, SessionID: uuid.MustParse(base.Session.ID)}}
	var plain structuredPrescriptionSnapshot
	if err := json.Unmarshal(original, &plain); err != nil {
		t.Fatal(err)
	}
	build := func(session pages.StructuredTrainingSession) []StructuredPrescriptionInput {
		t.Helper()
		out, err := buildStructuredPrescriptionInputs(planID, pages.StructuredTrainingAudience{GroupName: "Same group"}, pages.StructuredTrainingWeek{ID: planID.String(), Title: "Same week", Sessions: []pages.StructuredTrainingSession{session}}, recipient, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before, after := build(plain.Session), build(base.Session)
	if len(before) != 1 || len(after) != 1 || string(before[0].Snapshot) != string(after[0].Snapshot) || before[0].SnapshotSHA256 != after[0].SnapshotSHA256 {
		t.Fatal("view metadata changed canonical engine prescription/hash")
	}

}
func TestPlannerReturnRetainsOnlyNativeAllowedDayContext(t *testing.T) {
	group, week, session := uuid.NewString(), uuid.NewString(), uuid.NewString()
	valid := structuredPlannerURL(group, week, session, "2026-11-14")
	if got := structuredPlannerReturn(valid); got != valid {
		t.Fatalf("valid return %q", got)
	}
	for _, raw := range []string{"https://outside.example" + valid, valid + "&unknown=yes", strings.Replace(valid, "2026-11-14", "2026-02-30", 1), strings.Replace(valid, "day=", "day=2026-11-14&day=", 1)} {
		if got := structuredPlannerReturn(raw); got != "" {
			t.Fatalf("unsafe return admitted %q => %q", raw, got)
		}
	}
	if got := structuredPlannerSessionReturn(valid, week, session); !strings.Contains(got, "day=2026-11-14") {
		t.Fatalf("session return lost day %q", got)
	}
	if got := structuredPlannerWeekReturn(valid, week); got != valid {
		t.Fatalf("week return lost context %q", got)
	}
}
func TestWorkspaceNavigationDoesNotCrossGroupOrAssumeSession(t *testing.T) {
	audience, group, week := workspaceFixture()
	if validStructuredWorkspaceSelection([]pages.StructuredTrainingAudience{audience}, uuid.NewString(), week) || validStructuredWorkspaceSelection([]pages.StructuredTrainingAudience{audience}, group, uuid.NewString()) {
		t.Fatal("forged group/week accepted")
	}
	g, w, s := structuredPlannerSelection([]pages.StructuredTrainingAudience{audience}, group, week, "")
	if g != group || w != week || s != "" {
		t.Fatalf("session implicitly selected %s %s %s", g, w, s)
	}
}

func TestWorkspaceRawLocalTimeMetadataUsesActualStoredTimestamps(t *testing.T) {
	group, week, session := uuid.New(), uuid.New(), uuid.New()
	start, _ := time.Parse(time.RFC3339, "2026-03-29T00:30:00Z")
	end, _ := time.Parse(time.RFC3339, "2026-03-29T01:30:00Z")
	monday, _ := time.Parse("2006-01-02", "2026-03-23")
	rows := []structuredTrainingRow{{groupID: &group, planID: &week, sessionID: &session, weekStart: monday, startsAt: start, endsAt: end, sessionStatus: "CANCELLED", entryKind: "TRAINING"}}
	actual := assembleStructuredTraining(rows, lisbonLocation())[0].Weeks[0].Sessions[0]
	if actual.StartsOn != "2026-03-29" || actual.StartsAt != "00:30" || actual.EndsAt != "02:30" || actual.Status != "CANCELLED" {
		t.Fatalf("club-local DST metadata %#v", actual)
	}
}
func TestWorkspaceIndexPublicationFailureIsRecoverableAndFreshRevocationHidesData(t *testing.T) {
	group, week := uuid.New(), uuid.New()
	title, name, season := "Week actual", "Current group", "Configured season"
	monday, _ := time.Parse("2006-01-02", "2026-11-09")
	row := dbgen.ListStructuredTrainingOverviewForManagerRow{GroupID: group, GroupName: name, PlanID: &week, PlanTitle: &title, SeasonName: &season, WeekStart: pgtype.Date{Time: monday, Valid: true}}
	for _, tc := range []struct {
		name     string
		groupErr error
		want     int
	}{{"publication unavailable", nil, 200}, {"fresh revoked group", pgx.ErrNoRows, 404}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{row}, publicationStatesErr: errors.New("private diagnostic")}
			groups := &taskGroupsFake{group: TrainingGroupRecord{ID: group, Name: name}, groupErr: tc.groupErr}
			r := httptest.NewRequest("GET", strings.Split(structuredPlannerURL(group.String(), week.String(), "", "2026-11-14"), "#")[0], nil)
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
			w := httptest.NewRecorder()
			(StructuredTraining{Store: store, Groups: groups, Location: lisbonLocation()}).Index(w, r)
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("personal workspace lacks privacy headers")
			}
			if w.Code != tc.want || strings.Contains(w.Body.String(), "private diagnostic") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.want == 200 && !strings.Contains(w.Body.String(), "Não foi possível confirmar o estado de publicação") {
				t.Fatal("publication failure mislabeled draft")
			}
			if tc.want == 404 && strings.Contains(w.Body.String(), name) {
				t.Fatal("revoked group data retained")
			}
		})
	}
}
func TestWorkspaceAdvancedSuccessRedirectRetainsAllowedDay(t *testing.T) {
	group, week, session := uuid.NewString(), uuid.NewString(), uuid.NewString()
	expected := structuredPlannerURL(group, week, session, "2026-11-14")
	for _, raw := range []string{expected, "https://outside.example/path"} {
		r := httptest.NewRequest("POST", structuredPlannerPath, strings.NewReader(url.Values{"return_to": {raw}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		(StructuredTraining{}).redirectPlanner(w, r, structuredPlannerPath)
		want := structuredPlannerPath
		if raw == expected {
			want = expected
		}
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != want {
			t.Fatalf("redirect=%s want=%s", w.Header().Get("Location"), want)
		}
	}
}

func TestSessionCreatedInAnotherAuthorizedWeekReturnsItsActualContext(t *testing.T) {
	oldGroup, oldWeek, oldSession, newGroup, newWeek, created := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := &structuredTrainingStoreStub{weekOK: true, createdSessionID: created, overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: newGroup, PlanID: &newWeek}}}
	values := url.Values{"plan_id": {newWeek.String()}, "title": {"New actual appointment"}, "entry_kind": {"TRAINING"}, "starts_at": {"2026-11-14T10:30"}, "ends_at": {"2026-11-14T11:45"}, "return_to": {structuredPlannerURL(oldGroup.String(), oldWeek.String(), oldSession.String(), "2026-11-09")}}
	response := performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New(), IsAdmin: true}, "POST", structuredPlannerPath+"/sessoes", values, "", "", (StructuredTraining{Store: store, Location: lisbonLocation()}).CreateSession)
	want := structuredPlannerURL(newGroup.String(), newWeek.String(), created.String(), "2026-11-14")
	if response.Code != 303 || response.Header().Get("Location") != want || len(store.sessions) != 1 {
		t.Fatalf("status=%d target=%s want=%s saves=%d", response.Code, response.Header().Get("Location"), want, len(store.sessions))
	}
	// Matching posted plan does not prove the posted group: verify it and use
	// the authoritative target-group lookup when the posted group does not match.
	values.Set("return_to", structuredPlannerURL(oldGroup.String(), newWeek.String(), oldSession.String(), "2026-11-09"))
	store.sessions = nil
	handler := StructuredTraining{Store: store, Groups: &taskGroupsFake{group: TrainingGroupRecord{ID: newGroup}, allowedWeeks: map[uuid.UUID]bool{}}, Location: lisbonLocation()}
	response = performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New(), IsAdmin: true}, "POST", structuredPlannerPath+"/sessoes", values, "", "", handler.CreateSession)
	if response.Code != 303 || response.Header().Get("Location") != want || len(store.sessions) != 1 {
		t.Fatalf("forged group status=%d destination=%s", response.Code, response.Header().Get("Location"))
	}
	values.Set("return_to", structuredPlannerURL(oldGroup.String(), oldWeek.String(), oldSession.String(), "2026-11-09"))
	// A forged/unresolvable target-context must fail before creating, rather than
	// sending the coach silently back to an unrelated old appointment.
	store.overviewRows = nil
	store.sessions = nil
	response = performStructuredTrainingRequest(t, CurrentUser{ID: uuid.New(), IsAdmin: true}, "POST", structuredPlannerPath+"/sessoes", values, "", "", (StructuredTraining{Store: store, Location: lisbonLocation()}).CreateSession)
	if response.Code != 404 || len(store.sessions) != 0 {
		t.Fatalf("unknown context status=%d saves=%d", response.Code, len(store.sessions))
	}
}
func TestPublicationComparisonFailuresAndRosterChangesCannotClaimCurrent(t *testing.T) {
	audience, _, plan := workspaceFixture()
	week := audience.Weeks[0]
	planID := uuid.MustParse(plan)
	sessionID := uuid.MustParse(week.Sessions[0].ID)
	member, athlete := uuid.New(), uuid.New()
	yes := true
	recipients := []dbgen.ListStructuredTrainingPublicationMembersRow{{MembershipID: member, AthleteUserID: &athlete, SessionID: sessionID}}
	candidate, err := buildStructuredPrescriptionInputs(planID, audience, week, recipients, nil)
	if err != nil {
		t.Fatal(err)
	}
	existing := []dbgen.ListLatestTrainingPrescriptionHashesForPlanRow{{MembershipID: member, SessionID: sessionID, SnapshotSha256: candidate[0].SnapshotSHA256}}
	for _, tc := range []struct {
		name                 string
		hashErr              error
		matches              []dbgen.ListTrainingVariationMatchesForManagerRow
		hashes               []dbgen.ListLatestTrainingPrescriptionHashesForPlanRow
		unavailable, current bool
		added                int
	}{{name: "hash failure", hashErr: errors.New("hash read unavailable"), unavailable: true}, {name: "candidate failure", matches: []dbgen.ListTrainingVariationMatchesForManagerRow{{PlanID: planID, SessionID: sessionID, MembershipID: &member, SubjectID: uuid.New(), SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, Priority: 1, Patch: []byte("invalid candidate JSON")}}, unavailable: true}, {name: "roster changed despite equal source timestamp", hashes: nil, added: 1}, {name: "known unchanged", hashes: existing, current: true}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &structuredTrainingStoreStub{publicationMembers: recipients, publicationHashes: tc.hashes, publicationHashesErr: tc.hashErr}
			states := (StructuredTraining{Store: store, Location: lisbonLocation()}).structuredPublicationStates(t.Context(), []pages.StructuredTrainingAudience{audience}, []dbgen.ListManagedTrainingPublicationStatesRow{{ID: planID, PublishedRevision: 2, PublicationCurrent: &yes}}, tc.matches)
			if len(states) != 1 || states[0].Unavailable != tc.unavailable || states[0].Current != tc.current || states[0].AddedCount != tc.added {
				t.Fatalf("state %#v", states)
			}
			view := buildStructuredWeekWorkspace([]pages.StructuredTrainingAudience{audience}, states, false, nil, CurrentUser{}, audience.GroupID, plan, "", "", time.Now())
			if tc.unavailable && view.Publication.State != "unavailable" {
				t.Fatal("failed comparison rendered as draft/current")
			}
			if tc.added > 0 && view.Publication.State != "changes" {
				t.Fatal("roster delta rendered current")
			}
		})
	}
}

func TestWorkspaceGetKeepsEmptySelectedDayAndResetsOutsideChangedWeek(t *testing.T) {
	group, first, second := uuid.New(), uuid.New(), uuid.New()
	firstDate, _ := time.Parse("2006-01-02", "2026-11-09")
	secondDate := firstDate.AddDate(0, 0, 7)
	store := &structuredTrainingStoreStub{overviewRows: []dbgen.ListStructuredTrainingOverviewForManagerRow{{GroupID: group, GroupName: "Exact group", PlanID: &first, WeekStart: pgtype.Date{Time: firstDate, Valid: true}}, {GroupID: group, GroupName: "Exact group", PlanID: &second, WeekStart: pgtype.Date{Time: secondDate, Valid: true}}}}
	h := StructuredTraining{Store: store, Groups: &taskGroupsFake{group: TrainingGroupRecord{ID: group, Name: "Exact group"}}, Location: lisbonLocation()}
	for _, tc := range []struct {
		week uuid.UUID
		want string
	}{{first, "2026-11-14"}, {second, "2026-11-16"}} {
		target := strings.Split(structuredPlannerURL(group.String(), tc.week.String(), "", "2026-11-14"), "#")[0] + "&routine_query=mobility"
		r := httptest.NewRequest("GET", target, nil)
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), IsAdmin: true}))
		w := httptest.NewRecorder()
		h.Index(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `name="day" value="`+tc.want+`"`) {
			t.Fatalf("GET status=%d expected native day=%s", w.Code, tc.want)
		}
	}
}
