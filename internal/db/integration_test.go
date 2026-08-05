//go:build integration

package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestScheduleMaintenanceTaskUpdatesOnlyDueEquipment(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	queries := dbgen.New(pool)
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name          string
		scheduledFor  time.Time
		wantEquipment string
	}{
		{name: "due", scheduledFor: now.Add(-time.Minute), wantEquipment: "Maintenance"},
		{name: "future", scheduledFor: now.Add(time.Hour), wantEquipment: "Operational"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			equipmentID := uuid.New()
			assetTag := "IT-" + uuid.NewString()[:8]
			if _, err := pool.Exec(ctx, `INSERT INTO equipment (id, asset_tag, name, type) VALUES ($1, $2, 'Barco de teste', 'Boat')`, equipmentID, assetTag); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM maintenance_tasks WHERE equipment_id = $1`, equipmentID)
				_, _ = pool.Exec(context.Background(), `DELETE FROM equipment WHERE id = $1`, equipmentID)
			})

			task, err := queries.ScheduleMaintenanceTask(ctx, dbgen.ScheduleMaintenanceTaskParams{
				EquipmentID:  equipmentID,
				ScheduledFor: pgtype.Timestamptz{Time: tc.scheduledFor, Valid: true},
				Description:  "Manutenção de integração",
			})
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != "Scheduled" {
				t.Fatalf("task status = %q", task.Status)
			}
			equipment, err := queries.GetEquipmentByID(ctx, equipmentID)
			if err != nil {
				t.Fatal(err)
			}
			if equipment.Status != tc.wantEquipment {
				t.Fatalf("equipment status = %q, want %q", equipment.Status, tc.wantEquipment)
			}
		})
	}
}

func TestEquipmentManagementAuditsAndPreservesOperationalHistory(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Gestora de frota', $2, 'hash', '1980-01-01')`, actorID, "fleet-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	queries := dbgen.New(pool)
	created, err := queries.CreateEquipmentWithAudit(ctx, dbgen.CreateEquipmentWithAuditParams{AssetTag: "IT-" + uuid.NewString()[:8], Name: "K1 de integração", Type: "Boat", Status: "Operational", Notes: "Azul", ActorUserID: actorID})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	updated, err := queries.UpdateEquipmentWithAudit(ctx, dbgen.UpdateEquipmentWithAuditParams{EquipmentID: created.ID, ExpectedUpdatedAt: created.UpdatedAt, AssetTag: created.AssetTag, Name: "K1 atualizado", Type: "Boat", Status: "Maintenance", Notes: "Casco revisto", ActorUserID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.UpdateEquipmentWithAudit(ctx, dbgen.UpdateEquipmentWithAuditParams{EquipmentID: created.ID, ExpectedUpdatedAt: created.UpdatedAt, AssetTag: created.AssetTag, Name: "Alteração obsoleta", Type: "Boat", Status: "Operational", ActorUserID: actorID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale update error = %v", err)
	}

	taskIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	if _, err := pool.Exec(ctx, `
		INSERT INTO maintenance_tasks (id, equipment_id, scheduled_for, description, status, completed_at)
		VALUES ($1,$5,$6,'Agendada para cancelamento','Scheduled',NULL),
		       ($2,$5,$6,'Em curso para cancelamento','In_Progress',NULL),
		       ($3,$5,$6,'Manutenção já concluída','Completed',$6),
		       ($4,$5,$6,'Manutenção já cancelada','Cancelled',NULL)`, taskIDs[0], taskIDs[1], taskIDs[2], taskIDs[3], created.ID, now); err != nil {
		t.Fatal(err)
	}
	repairID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO repair_requests (id, idempotency_key, equipment_id, issue_description) VALUES ($1,$2,$3,'Pedido que deve permanecer após retirada')`, repairID, uuid.New(), created.ID); err != nil {
		t.Fatal(err)
	}

	retired, err := queries.RetireEquipmentWithAudit(ctx, dbgen.RetireEquipmentWithAuditParams{EquipmentID: created.ID, ActorUserID: actorID})
	if err != nil || retired.Status != "Retired" {
		t.Fatalf("retired = %#v, err = %v", retired, err)
	}
	var statuses []string
	rows, err := pool.Query(ctx, `SELECT status::text FROM maintenance_tasks WHERE equipment_id = $1 ORDER BY id`, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, status)
	}
	rows.Close()
	counts := map[string]int{}
	for _, status := range statuses {
		counts[status]++
	}
	if counts["Cancelled"] != 3 || counts["Completed"] != 1 {
		t.Fatalf("maintenance statuses = %#v", counts)
	}
	var repairCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM repair_requests WHERE id = $1`, repairID).Scan(&repairCount); err != nil || repairCount != 1 {
		t.Fatalf("repair count = %d, err = %v", repairCount, err)
	}

	operational, err := queries.ListOperationalEquipment(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range operational {
		if item.ID == created.ID {
			t.Fatal("retired equipment remained operational")
		}
	}
	reactivated, err := queries.ReactivateEquipmentWithAudit(ctx, dbgen.ReactivateEquipmentWithAuditParams{EquipmentID: created.ID, ActorUserID: actorID})
	if err != nil || reactivated.Status != "Operational" {
		t.Fatalf("reactivated = %#v, err = %v", reactivated, err)
	}

	events, err := queries.ListEquipmentAuditEvents(ctx, dbgen.ListEquipmentAuditEventsParams{EquipmentID: created.ID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Action != "REACTIVATED" || events[1].Action != "RETIRED" || len(events[1].AffectedMaintenanceIds) != 2 || events[2].Action != "UPDATED" || events[3].Action != "CREATED" {
		t.Fatalf("audit events = %#v", events)
	}
	if _, err := pool.Exec(ctx, `UPDATE equipment_audit_events SET action = 'UPDATED' WHERE id = $1`, events[0].ID); err == nil {
		t.Fatal("audit event update unexpectedly succeeded")
	}
	if updated.Name != "K1 atualizado" {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestCreateRepairRequestEnforcesIdempotencyKeyDuringConcurrentRetries(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	userID, equipmentID, key := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Utilizador de teste', $2, 'hash', '1990-01-01')`, userID, "repair-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO equipment (id, asset_tag, name, type) VALUES ($1, $2, 'Barco de teste', 'Boat')`, equipmentID, "IT-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repair_requests WHERE idempotency_key = $1`, key)
		_, _ = pool.Exec(context.Background(), `DELETE FROM equipment WHERE id = $1`, equipmentID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})

	queries := dbgen.New(pool)
	input := dbgen.CreateRepairRequestParams{
		IdempotencyKey:   key,
		EquipmentID:      equipmentID,
		ReportedByID:     &userID,
		IssueDescription: "Avaria criada por um teste de integração.",
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := queries.CreateRepairRequest(ctx, input)
			results <- err
		}()
	}
	group.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent inserts = %d, want 1", successes)
	}

	repair, err := queries.GetRepairByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if repair.EquipmentID != equipmentID || repair.ReportedByID == nil || *repair.ReportedByID != userID || repair.IssueDescription != input.IssueDescription {
		t.Fatalf("persisted repair = %#v", repair)
	}
	visible, err := queries.ListRepairRequestsForMembers(ctx, dbgen.ListRepairRequestsForMembersParams{UserID: userID, ResolvedSince: pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -30), Valid: true}, RowLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range visible {
		if item.ID == repair.ID {
			found = item.ReportedByUser && item.EquipmentID == equipmentID && item.Status == "Pendente"
		}
	}
	if !found {
		t.Fatalf("member repair was not visible as the user's own report: %#v", visible)
	}
}

func TestMembershipsResolveActiveSportStructureAndRejectMismatches(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	directorID, athleteID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Diretor de teste', $2, 'hash', '1980-01-01'), ($3, 'Atleta de teste', $4, 'hash', '2000-01-01')`, directorID, "director-"+uuid.NewString()+"@example.test", athleteID, "athlete-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles (user_id, role_id) SELECT $1, id FROM platform_roles WHERE code = 'ADMIN'`, directorID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE user_id IN ($1, $2)`, directorID, athleteID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM competition_categories WHERE approved_by_user_id = $1`, directorID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM teams WHERE code LIKE 'IT_%'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM seasons WHERE code LIKE 'IT_%'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1, $2)`, directorID, athleteID)
	})

	queries := dbgen.New(pool)
	competition, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	leisure, err := queries.GetProgrammeByCode(ctx, "Leisure")
	if err != nil {
		t.Fatal(err)
	}
	k1, err := queries.GetModalityByCode(ctx, "K1")
	if err != nil {
		t.Fatal(err)
	}
	k4, err := queries.GetModalityByCode(ctx, "K4")
	if err != nil {
		t.Fatal(err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	season, err := queries.CreateSeason(ctx, dbgen.CreateSeasonParams{
		Code: "IT_" + uuid.NewString()[:8], Name: "Época de integração",
		StartsOn: pgtype.Date{Time: today.AddDate(-1, 0, 0), Valid: true},
		EndsOn:   pgtype.Date{Time: today.AddDate(1, 0, 0), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	team, err := queries.CreateTeam(ctx, dbgen.CreateTeamParams{SeasonID: season.ID, ProgrammeID: competition.ID, Code: "IT_A", Name: "Equipa de integração"})
	if err != nil {
		t.Fatal(err)
	}
	category, err := queries.CreateCompetitionCategory(ctx, dbgen.CreateCompetitionCategoryParams{
		SeasonID: season.ID, ProgrammeID: competition.ID, Code: "IT_Junior", NamePt: "Júnior de integração",
		ApprovedByUserID: directorID, ApprovedAt: pgtype.Timestamptz{Time: today, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	membership, err := queries.CreateUserMembership(ctx, dbgen.CreateUserMembershipParams{
		UserID: athleteID, SeasonID: season.ID, ProgrammeID: competition.ID, TeamID: &team.ID,
		CompetitionCategoryID: &category.ID, StartsOn: pgtype.Date{Time: today, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, modalityID := range []uuid.UUID{k1.ID, k4.ID} {
		if err := queries.AddMembershipModality(ctx, dbgen.AddMembershipModalityParams{MembershipID: membership.ID, ModalityID: modalityID}); err != nil {
			t.Fatal(err)
		}
	}

	active, err := queries.ListActiveMembershipsForUser(ctx, athleteID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ProgrammeCode != "Competition" || active[0].TeamID == nil || *active[0].TeamID != team.ID || active[0].CompetitionCategoryID == nil || *active[0].CompetitionCategoryID != category.ID {
		t.Fatalf("active memberships = %#v", active)
	}
	modalities, err := queries.ListModalitiesForMembership(ctx, membership.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(modalities) != 2 || modalities[0].Code != "K1" || modalities[1].Code != "K4" {
		t.Fatalf("membership modalities = %#v", modalities)
	}

	if _, err := queries.CreateUserMembership(ctx, dbgen.CreateUserMembershipParams{
		UserID: athleteID, SeasonID: season.ID, ProgrammeID: leisure.ID, TeamID: &team.ID,
		StartsOn: pgtype.Date{Time: today, Valid: true},
	}); err == nil {
		t.Fatal("membership accepted a team from another programme")
	}
	otherSeason, err := queries.CreateSeason(ctx, dbgen.CreateSeasonParams{
		Code: "IT_" + uuid.NewString()[:8], Name: "Outra época de integração",
		StartsOn: pgtype.Date{Time: today.AddDate(1, 0, 1), Valid: true},
		EndsOn:   pgtype.Date{Time: today.AddDate(2, 0, 0), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateUserMembership(ctx, dbgen.CreateUserMembershipParams{
		UserID: athleteID, SeasonID: otherSeason.ID, ProgrammeID: competition.ID, TeamID: &team.ID,
		CompetitionCategoryID: &category.ID, StartsOn: pgtype.Date{Time: today.AddDate(1, 0, 1), Valid: true},
	}); err == nil {
		t.Fatal("membership accepted team and category from another season")
	}
}

func TestListEventsForTodayRespectsMembershipCoachGrantAndAdminVisibility(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	queries := dbgen.New(pool)
	authorID, memberID, outsiderID, coachID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES
        ($1, 'Autor de teste', $2, 'hash', '1980-01-01'),
        ($3, 'Membro de teste', $4, 'hash', '2000-01-01'),
        ($5, 'Visitante de teste', $6, 'hash', '2000-01-01'),
        ($7, 'Treinador de teste', $8, 'hash', '1980-01-01')`, authorID, "today-author-"+uuid.NewString()+"@example.test", memberID, "today-member-"+uuid.NewString()+"@example.test", outsiderID, "today-outsider-"+uuid.NewString()+"@example.test", coachID, "today-coach-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM user_memberships WHERE user_id = $1`, memberID)
		_, _ = pool.Exec(cleanup, `DELETE FROM events WHERE created_by_id = $1`, authorID)
		_, _ = pool.Exec(cleanup, `DELETE FROM seasons WHERE code LIKE 'IT_today_%'`)
	})

	competition, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	leisure, err := queries.GetProgrammeByCode(ctx, "Leisure")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	season, err := queries.CreateSeason(ctx, dbgen.CreateSeasonParams{Code: "IT_today_" + uuid.NewString()[:8], Name: "Época Today", StartsOn: pgtype.Date{Time: today.AddDate(0, 0, -1), Valid: true}, EndsOn: pgtype.Date{Time: today.AddDate(0, 0, 1), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateUserMembership(ctx, dbgen.CreateUserMembershipParams{UserID: memberID, SeasonID: season.ID, ProgrammeID: competition.ID, StartsOn: pgtype.Date{Time: today, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.GrantStaffCapability(ctx, dbgen.GrantStaffCapabilityParams{UserID: coachID, Capability: dbgen.StaffCapabilityCOACH, ProgrammeID: &competition.ID, GrantedByID: authorID}); err != nil {
		t.Fatal(err)
	}
	create := func(title string, hour time.Duration) dbgen.Event {
		event, err := queries.CreateEvent(ctx, dbgen.CreateEventParams{Title: title, EventType: "GENERAL", StartsAt: pgtype.Timestamptz{Time: today.Add(hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: today.Add(hour + time.Hour), Valid: true}, CreatedByID: authorID})
		if err != nil {
			t.Fatal(err)
		}
		return event
	}
	public := create("Público", 10*time.Hour)
	competitionEvent := create("Competição", 11*time.Hour)
	hidden := create("Lazer", 12*time.Hour)
	if err := queries.AddEventAudience(ctx, dbgen.AddEventAudienceParams{EventID: competitionEvent.ID, ProgrammeID: competition.ID}); err != nil {
		t.Fatal(err)
	}
	if err := queries.AddEventAudience(ctx, dbgen.AddEventAudienceParams{EventID: hidden.ID, ProgrammeID: leisure.ID}); err != nil {
		t.Fatal(err)
	}
	params := func(userID uuid.UUID, isAdmin bool) dbgen.ListEventsForTodayParams {
		return dbgen.ListEventsForTodayParams{UserID: userID, IsAdmin: isAdmin, DayStartsAt: pgtype.Timestamptz{Time: today, Valid: true}, DayEndsAt: pgtype.Timestamptz{Time: today.AddDate(0, 0, 1), Valid: true}}
	}
	assertTitles := func(userID uuid.UUID, isAdmin bool, want ...string) {
		items, err := queries.ListEventsForToday(ctx, params(userID, isAdmin))
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(items))
		for i, item := range items {
			got[i] = item.Title
		}
		if len(got) != len(want) {
			t.Fatalf("events for %s = %v, want %v", userID, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("events for %s = %v, want %v", userID, got, want)
			}
		}
	}
	assertTitles(memberID, false, public.Title, competitionEvent.Title)
	assertTitles(outsiderID, false, public.Title)
	assertTitles(coachID, false, public.Title, competitionEvent.Title)
	assertTitles(outsiderID, true, public.Title, competitionEvent.Title, hidden.Title)
}

func TestDistanceLeaderboardEnforcesRankingPrivacyAndOwnership(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })
	queries := dbgen.New(pool)
	competition, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	seasonID, planID := uuid.New(), uuid.New()
	athleteA, athleteB, currentAthlete := uuid.New(), uuid.New(), uuid.New()
	privateAthlete, expiredAthlete := uuid.New(), uuid.New()
	guardianID, dependentID := uuid.New(), uuid.New()
	userIDs := []uuid.UUID{athleteA, athleteB, currentAthlete, privateAthlete, expiredAthlete, guardianID, dependentID}
	sessionID, futureSessionID, oldSessionID := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_plans WHERE id = $1`, planID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE season_id = $1`, seasonID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM seasons WHERE id = $1`, seasonID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, userIDs)
	})

	for i, id := range userIDs[:6] {
		email := "leaderboard-" + uuid.NewString() + "@example.test"
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', '2000-01-01')`, id, fmt.Sprintf("Atleta %02d", i+1), email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, guardian_id, is_dependent, date_of_birth) VALUES ($1, 'Menor leaderboard', $2, true, '2014-01-01')`, dependentID, guardianID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET leaderboard_visible = false WHERE id = $1`, privateAthlete); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época leaderboard', $3, $4)`, seasonID, "IT_"+uuid.NewString()[:8], today.AddDate(-1, 0, 0), today.AddDate(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{athleteA, athleteB, currentAthlete, privateAthlete} {
		if _, err := pool.Exec(ctx, `INSERT INTO user_memberships (user_id, season_id, programme_id, starts_on) VALUES ($1, $2, $3, $4)`, id, seasonID, competition.ID, today.AddDate(0, 0, -1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships (user_id, season_id, programme_id, starts_on, ends_on) VALUES ($1, $2, $3, $4, $5)`, expiredAthlete, seasonID, competition.ID, today.AddDate(0, 0, -10), today.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO training_plans (id, title, description, programme_id, created_by_id) VALUES ($1, 'Plano leaderboard', '', $2, $3)`, planID, competition.ID, athleteA); err != nil {
		t.Fatal(err)
	}
	for _, session := range []struct {
		id    uuid.UUID
		start time.Time
	}{
		{sessionID, now.Add(-time.Hour)},
		{futureSessionID, now.Add(time.Hour)},
		{oldSessionID, now.AddDate(0, -2, 0)},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO training_sessions (id, plan_id, title, description, starts_at, ends_at, created_by_id) VALUES ($1, $2, 'Sessão leaderboard', '', $3, $4, $5)`, session.id, planID, session.start, session.start.Add(time.Hour), athleteA); err != nil {
			t.Fatal(err)
		}
	}
	for _, outcome := range []struct {
		user     uuid.UUID
		session  uuid.UUID
		distance int
	}{
		{athleteA, sessionID, 12000}, {athleteB, sessionID, 12000}, {currentAthlete, sessionID, 1000},
		{privateAthlete, sessionID, 99000}, {expiredAthlete, sessionID, 88000},
		{athleteA, futureSessionID, 200000}, {athleteA, oldSessionID, 50000},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO training_session_outcomes (session_id, user_id, status, distance_metres) VALUES ($1, $2, 'COMPLETED', $3)`, outcome.session, outcome.user, outcome.distance); err != nil {
			t.Fatal(err)
		}
	}

	params := dbgen.ListDistanceLeaderboardParams{
		CurrentUserID: currentAthlete,
		ActiveOn:      pgtype.Date{Time: today, Valid: true},
		AsOf:          pgtype.Timestamptz{Time: now, Valid: true},
		PeriodStart:   pgtype.Timestamptz{Time: today.AddDate(0, -1, 0), Valid: true},
		PeriodEnd:     pgtype.Timestamptz{Time: today.AddDate(0, 1, 0), Valid: true},
	}
	rows, err := queries.ListDistanceLeaderboard(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Position != 1 || rows[1].Position != 1 || rows[2].Position != 3 || rows[2].UserID != currentAthlete {
		t.Fatalf("leaderboard rows = %#v", rows)
	}
	if n, err := queries.UpdateOwnCompletedSessionDistance(ctx, dbgen.UpdateOwnCompletedSessionDistanceParams{SessionID: sessionID, UserID: athleteA, DistanceMetres: int32PtrDB(2500)}); err != nil || n != 1 {
		t.Fatalf("own correction rows = %d, err = %v", n, err)
	}
	if n, err := queries.UpdateOwnCompletedSessionDistance(ctx, dbgen.UpdateOwnCompletedSessionDistanceParams{SessionID: sessionID, UserID: uuid.New(), DistanceMetres: int32PtrDB(2500)}); err != nil || n != 0 {
		t.Fatalf("foreign correction rows = %d, err = %v", n, err)
	}
	if n, err := queries.UpdateOwnLeaderboardVisibility(ctx, dbgen.UpdateOwnLeaderboardVisibilityParams{UserID: currentAthlete, LeaderboardVisible: false}); err != nil || n != 1 {
		t.Fatalf("privacy rows = %d, err = %v", n, err)
	}
	rows, err = queries.ListDistanceLeaderboard(ctx, params)
	if err != nil || len(rows) != 2 {
		t.Fatalf("private leaderboard rows = %#v, err = %v", rows, err)
	}
	if n, err := queries.UpdateDependentLeaderboardVisibility(ctx, dbgen.UpdateDependentLeaderboardVisibilityParams{DependentUserID: dependentID, GuardianUserID: &guardianID, LeaderboardVisible: false}); err != nil || n != 1 {
		t.Fatalf("guardian privacy rows = %d, err = %v", n, err)
	}
	wrongGuardian := uuid.New()
	if n, err := queries.UpdateDependentLeaderboardVisibility(ctx, dbgen.UpdateDependentLeaderboardVisibilityParams{DependentUserID: dependentID, GuardianUserID: &wrongGuardian, LeaderboardVisible: true}); err != nil || n != 0 {
		t.Fatalf("foreign guardian rows = %d, err = %v", n, err)
	}
}

func int32PtrDB(value int32) *int32 { return &value }
