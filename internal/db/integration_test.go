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
	"github.com/jackc/pgx/v5/pgxpool"
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
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

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
	if n, err := queries.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: sessionID, UserID: athleteA, DistanceMetres: int32PtrDB(2500), ExpectedVersion: 1}); err != nil || n != 1 {
		t.Fatalf("own correction rows = %d, err = %v", n, err)
	}
	if n, err := queries.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: sessionID, UserID: uuid.New(), DistanceMetres: int32PtrDB(2500), ExpectedVersion: 1}); err != nil || n != 0 {
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

func TestTrainingSessionEditingAndCancellationLifecycle(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })
	queries := dbgen.New(pool)
	programme, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}

	actorID, athleteID, seasonID := uuid.New(), uuid.New(), uuid.New()
	planA, planB, sessionID, sourceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_plans WHERE id = ANY($1)`, []uuid.UUID{planA, planB})
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE season_id = $1`, seasonID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM seasons WHERE id = $1`, seasonID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{actorID, athleteID})
	})
	for id, name := range map[uuid.UUID]string{actorID: "Treinador lifecycle", athleteID: "Atleta lifecycle"} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', '2000-01-01')`, id, name, "training-"+uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época lifecycle', $3, $4)`, seasonID, "IT_"+uuid.NewString()[:8], today.AddDate(0, -1, 0), today.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships (user_id, season_id, programme_id, starts_on) VALUES ($1, $2, $3, $4)`, athleteID, seasonID, programme.ID, today.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	for id, title := range map[uuid.UUID]string{planA: "Plano lifecycle A", planB: "Plano lifecycle B"} {
		if _, err := pool.Exec(ctx, `INSERT INTO training_plans (id, title, programme_id, created_by_id) VALUES ($1, $2, $3, $4)`, id, title, programme.ID, actorID); err != nil {
			t.Fatal(err)
		}
	}
	starts := now.Add(48 * time.Hour)
	for id, title := range map[uuid.UUID]string{sessionID: "Sessão lifecycle", sourceID: "Origem lifecycle"} {
		if _, err := pool.Exec(ctx, `INSERT INTO training_sessions (id, plan_id, title, starts_at, ends_at, created_by_id) VALUES ($1, $2, $3, $4, $5, $6)`, id, planA, title, starts, starts.Add(time.Hour), actorID); err != nil {
			t.Fatal(err)
		}
	}
	current, err := queries.GetTrainingSessionForEdit(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := queries.UpdateTrainingSession(ctx, dbgen.UpdateTrainingSessionParams{PlanID: planB, Title: "Sessão editada", Description: "Descrição editada", StartsAt: pgtype.Timestamptz{Time: starts.Add(time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: starts.Add(2 * time.Hour), Valid: true}, ID: sessionID, AsOf: pgtype.Timestamptz{Time: now, Valid: true}, ExpectedUpdatedAt: current.UpdatedAt})
	if err != nil || updated.PlanID != planB {
		t.Fatalf("update = %#v, err = %v", updated, err)
	}
	if _, err := queries.UpdateTrainingSession(ctx, dbgen.UpdateTrainingSessionParams{PlanID: planB, Title: "Stale", StartsAt: updated.StartsAt, EndsAt: updated.EndsAt, ID: sessionID, AsOf: pgtype.Timestamptz{Time: now, Valid: true}, ExpectedUpdatedAt: current.UpdatedAt}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale update err = %v", err)
	}
	if n, err := queries.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: sessionID, UserID: athleteID, Status: dbgen.TrainingOutcomeStatusCOMPLETED, DistanceMetres: int32PtrDB(5000)}); err != nil || n != 1 {
		t.Fatalf("save outcome rows = %d, err = %v", n, err)
	}
	if _, err := queries.UpdateTrainingSession(ctx, dbgen.UpdateTrainingSessionParams{PlanID: planA, Title: updated.Title, Description: updated.Description, StartsAt: updated.StartsAt, EndsAt: updated.EndsAt, ID: sessionID, AsOf: pgtype.Timestamptz{Time: now, Valid: true}, ExpectedUpdatedAt: updated.UpdatedAt}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("move after outcome err = %v", err)
	}
	reason := "Alteração definitiva do plano de treino"
	cancelled, err := queries.CancelTrainingSession(ctx, dbgen.CancelTrainingSessionParams{CancelledAt: pgtype.Timestamptz{Time: now, Valid: true}, CancelledByID: &actorID, CancellationReason: &reason, ID: sessionID, ExpectedUpdatedAt: updated.UpdatedAt})
	if err != nil || cancelled.Status != "CANCELLED" || cancelled.CancellationReason == nil || *cancelled.CancellationReason != reason {
		t.Fatalf("cancelled = %#v, err = %v", cancelled, err)
	}
	if n, err := queries.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: sessionID, UserID: athleteID, Status: dbgen.TrainingOutcomeStatusMISSED}); err != nil || n != 0 {
		t.Fatalf("cancelled outcome rows = %d, err = %v", n, err)
	}
	if n, err := queries.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: sessionID, UserID: athleteID, DistanceMetres: int32PtrDB(6000), ExpectedVersion: 1}); err != nil || n != 0 {
		t.Fatalf("cancelled distance rows = %d, err = %v", n, err)
	}
	if n, err := queries.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: sourceID, UserID: athleteID, Status: dbgen.TrainingOutcomeStatusREPLACED, ReplacementSessionID: &sessionID, ReplacementReason: stringPtrDB("Sessão cancelada")}); err != nil || n != 0 {
		t.Fatalf("cancelled replacement rows = %d, err = %v", n, err)
	}
	var outcomes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_session_outcomes WHERE session_id = $1`, sessionID).Scan(&outcomes); err != nil || outcomes != 1 {
		t.Fatalf("preserved outcomes = %d, err = %v", outcomes, err)
	}
	leaders, err := queries.ListDistanceLeaderboard(ctx, dbgen.ListDistanceLeaderboardParams{CurrentUserID: athleteID, ActiveOn: pgtype.Date{Time: today, Valid: true}, AsOf: pgtype.Timestamptz{Time: now.Add(7 * 24 * time.Hour), Valid: true}})
	if err != nil || len(leaders) != 0 {
		t.Fatalf("cancelled leaderboard rows = %#v, err = %v", leaders, err)
	}
	sessions, err := queries.ListTrainingSessionsForAthlete(ctx, dbgen.ListTrainingSessionsForAthleteParams{UserID: athleteID, RowLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, session := range sessions {
		if session.ID == sessionID {
			found = session.Status == "CANCELLED" && session.CancellationReason != nil
		}
	}
	if !found {
		t.Fatal("cancelled session was not retained in athlete visibility")
	}
}

func TestStructuredTrainingHybridPlanAndGuardianVisibility(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	queries := dbgen.New(conn)
	programme, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}

	actorID, guardianID, athleteID, unrelatedID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seasonID := uuid.New()
	for id, name := range map[uuid.UUID]string{actorID: "Treinadora estruturada", guardianID: "Responsável estruturada", unrelatedID: "Pessoa sem relação"} {
		if _, err := conn.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', '1980-01-01')`, id, name, "structured-"+uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO users (id, name, guardian_id, is_dependent, date_of_birth) VALUES ($1, 'Atleta menor estruturada', $2, true, CURRENT_DATE - INTERVAL '14 years')`, athleteID, guardianID); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if _, err := conn.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época estruturada', $3, $4)`, seasonID, "ST_"+uuid.NewString()[:8], today.AddDate(0, -1, 0), today.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	var membershipID uuid.UUID
	if err := conn.QueryRow(ctx, `INSERT INTO user_memberships (user_id, season_id, programme_id, starts_on) VALUES ($1, $2, $3, $4) RETURNING id`, athleteID, seasonID, programme.ID, today.AddDate(0, 0, -1)).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DELETE FROM training_plans WHERE training_group_id IN (SELECT id FROM training_groups WHERE created_by_id = $1)`, actorID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM training_groups WHERE created_by_id = $1`, actorID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM water_intensity_profiles WHERE created_by_id = $1`, actorID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM user_memberships WHERE season_id = $1`, seasonID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM seasons WHERE id = $1`, seasonID)
		_, _ = conn.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{athleteID, actorID, guardianID, unrelatedID})
	})

	group, err := queries.CreateStructuredTrainingGroup(ctx, dbgen.CreateStructuredTrainingGroupParams{Name: "Cadetes estruturados", ProgrammeID: &programme.ID, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := queries.AddStructuredTrainingGroupMember(ctx, dbgen.AddStructuredTrainingGroupMemberParams{AddedByID: actorID, MembershipID: membershipID, GroupID: group.ID}); err != nil || rows != 1 {
		t.Fatalf("add group member rows = %d, err = %v", rows, err)
	}
	weekStart := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	week, err := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: "M33", Description: "Semana híbrida", WeekStart: pgtype.Date{Time: weekStart, Valid: true}, CreatedByID: actorID, GroupID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	if week.SeasonID == nil || *week.SeasonID != seasonID {
		t.Fatalf("structured week season = %v, want %s", week.SeasonID, seasonID)
	}
	startsAt := weekStart.Add(17 * time.Hour)
	session, err := queries.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{Title: "Ginásio + água", StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(2 * time.Hour), Valid: true}, EntryKind: dbgen.TrainingEntryKindTRAINING, CreatedByID: actorID, PlanID: week.ID})
	if err != nil {
		t.Fatal(err)
	}
	gymID, err := queries.CreateTrainingSessionSegment(ctx, dbgen.CreateTrainingSessionSegmentParams{SessionID: session.ID, Modality: dbgen.TrainingSegmentModalityGYM, Title: "Mobilidade", PlannedDurationMinutes: int32PtrDB(30), PlannedStartOffsetMinutes: int32PtrDB(0), EquipmentNotes: "Elásticos e halteres"})
	if err != nil {
		t.Fatal(err)
	}
	waterID, err := queries.CreateTrainingSessionSegment(ctx, dbgen.CreateTrainingSessionSegmentParams{SessionID: session.ID, Modality: dbgen.TrainingSegmentModalityWATER, Title: "Série principal", PlannedStartOffsetMinutes: int32PtrDB(35), TransitionDurationMinutes: int32PtrDB(5), EquipmentNotes: "Barco e pagaia"})
	if err != nil {
		t.Fatal(err)
	}
	gymBlockID, err := queries.CreateTrainingSegmentBlock(ctx, dbgen.CreateTrainingSegmentBlockParams{SegmentID: gymID, Purpose: dbgen.TrainingBlockPurposeWARMUP, Instructions: "Mobilidade articular"})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := queries.CreateGymBlockPrescription(ctx, dbgen.CreateGymBlockPrescriptionParams{BlockID: gymBlockID, Structure: dbgen.GymBlockStructureSUPERSET, Objective: dbgen.TrainingObjectiveACTIVATION, Rounds: 3, RoundRecoverySeconds: int32PtrDB(120)}); err != nil || rows != 1 {
		t.Fatalf("create gym prescription rows = %d, err = %v", rows, err)
	}
	percentKind, explosive := dbgen.GymResistanceKindPERCENT1RM, dbgen.GymExecutionIntentEXPLOSIVE
	percent, tempo := 75.0, "2-0-X-1"
	firstExerciseID, err := queries.CreateGymExercise(ctx, dbgen.CreateGymExerciseParams{BlockID: gymBlockID, Name: "Supino", Sets: int32PtrDB(3), Repetitions: int32PtrDB(5), RecoverySeconds: int32PtrDB(60), ResistanceKind: &percentKind, ResistanceValue: &percent, ExecutionIntent: &explosive, Tempo: &tempo})
	if err != nil {
		t.Fatal(err)
	}
	secondExerciseID, err := queries.CreateGymExercise(ctx, dbgen.CreateGymExerciseParams{BlockID: gymBlockID, Name: "Elevação frontal", Sets: int32PtrDB(3), Repetitions: int32PtrDB(5)})
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := queries.MoveGymExercise(ctx, dbgen.MoveGymExerciseParams{ExerciseID: secondExerciseID, Direction: -1}); err != nil || !moved {
		t.Fatalf("move gym exercise = %t, err = %v", moved, err)
	}
	profile, err := queries.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: "Perfil integração", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Limites do clube", CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{ProfileID: profile.ID, Code: "R7", Label: "Ritmo de prova", Meaning: "Ritmo sustentável para a duração prescrita"}); err != nil {
		t.Fatal(err)
	}
	waterBlockID, err := queries.CreateTrainingSegmentBlock(ctx, dbgen.CreateTrainingSegmentBlockParams{SegmentID: waterID, Purpose: dbgen.TrainingBlockPurposeMAIN, Instructions: "3x2' R7 / 1'"})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := queries.CreateWaterBlockPrescription(ctx, dbgen.CreateWaterBlockPrescriptionParams{BlockID: waterBlockID, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &profile.ID}); err != nil || rows != 1 {
		t.Fatalf("create water prescription rows = %d, err = %v", rows, err)
	}
	groupStepID, err := queries.CreateWaterWorkStep(ctx, dbgen.CreateWaterWorkStepParams{BlockID: waterBlockID, Kind: dbgen.WaterStepKindREPEATGROUP, Name: "Série", Repeats: int32PtrDB(3), RecoverySeconds: int32PtrDB(60), Instructions: ""})
	if err != nil {
		t.Fatal(err)
	}
	durationCertainty := dbgen.TrainingMeasureCertaintyEXACT
	intensity := "R7"
	if _, err := queries.CreateWaterWorkStep(ctx, dbgen.CreateWaterWorkStepParams{BlockID: waterBlockID, ParentStepID: &groupStepID, Kind: dbgen.WaterStepKindEFFORT, Name: "Ritmo de prova", DurationSeconds: int32PtrDB(120), DurationCertainty: &durationCertainty, IntensityCode: &intensity, Instructions: "Como uma prova de dois minutos"}); err != nil {
		t.Fatal(err)
	}
	revisedProfile, err := queries.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: "Perfil integração", Craft: dbgen.PaddlingCraftKAYAK, Notes: "Revisão sem alterar o histórico", CreatedByID: actorID})
	if err != nil || revisedProfile.Revision != 2 || revisedProfile.SupersedesID == nil || *revisedProfile.SupersedesID != profile.ID {
		t.Fatalf("revised profile=%#v err=%v", revisedProfile, err)
	}
	var copiedZones int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM water_intensity_zones WHERE profile_id = $1`, revisedProfile.ID).Scan(&copiedZones); err != nil || copiedZones != 1 {
		t.Fatalf("copied profile zones=%d err=%v", copiedZones, err)
	}
	if _, err := queries.CreateWaterIntensityZone(ctx, dbgen.CreateWaterIntensityZoneParams{ProfileID: revisedProfile.ID, Code: "R7", Label: "Ritmo de prova revisto", Meaning: "Ritmo sustentável revisto"}); err != nil {
		t.Fatal(err)
	}
	var revisedMeaning, historicalMeaning string
	if err := conn.QueryRow(ctx, `SELECT meaning FROM water_intensity_zones WHERE profile_id = $1 AND code = 'R7'`, revisedProfile.ID).Scan(&revisedMeaning); err != nil || revisedMeaning != "Ritmo sustentável revisto" {
		t.Fatalf("revised zone meaning=%q err=%v", revisedMeaning, err)
	}
	if err := conn.QueryRow(ctx, `SELECT meaning FROM water_intensity_zones WHERE profile_id = $1 AND code = 'R7'`, profile.ID).Scan(&historicalMeaning); err != nil || historicalMeaning != "Ritmo sustentável para a duração prescrita" {
		t.Fatalf("historical zone meaning=%q err=%v", historicalMeaning, err)
	}
	if moved, err := queries.MoveTrainingSessionSegment(ctx, dbgen.MoveTrainingSessionSegmentParams{SegmentID: waterID, Direction: -1}); err != nil || !moved {
		t.Fatalf("move water segment = %t, err = %v", moved, err)
	}

	for name, userID := range map[string]uuid.UUID{"athlete": athleteID, "guardian": guardianID} {
		rows, err := queries.ListStructuredTrainingOverviewForSubject(ctx, userID)
		if err != nil || len(rows) != 4 {
			t.Fatalf("%s rows = %d, err = %v", name, len(rows), err)
		}
		if rows[0].SegmentModality == nil || *rows[0].SegmentModality != dbgen.TrainingSegmentModalityWATER {
			t.Fatalf("%s first segment = %#v", name, rows[0].SegmentModality)
		}
		if rows[0].WaterProfileName == nil || *rows[0].WaterProfileName != "Perfil integração" || rows[1].WaterZoneMeaning == nil || *rows[1].WaterZoneMeaning != "Ritmo sustentável para a duração prescrita" {
			t.Fatalf("%s water profile metadata = %#v, %#v", name, rows[0].WaterProfileName, rows[1].WaterZoneMeaning)
		}
		if rows[2].ExerciseID == nil || *rows[2].ExerciseID != secondExerciseID || rows[3].ExerciseID == nil || *rows[3].ExerciseID != firstExerciseID {
			t.Fatalf("%s exercise order = %#v, %#v", name, rows[2].ExerciseID, rows[3].ExerciseID)
		}
		if rows[2].PlannedStartOffsetMinutes == nil || *rows[2].PlannedStartOffsetMinutes != 0 || rows[2].EquipmentNotes == nil || *rows[2].EquipmentNotes != "Elásticos e halteres" {
			t.Fatalf("%s gym metadata = offset %#v, equipment %#v", name, rows[2].PlannedStartOffsetMinutes, rows[2].EquipmentNotes)
		}
	}
	rows, err := queries.ListStructuredTrainingOverviewForSubject(ctx, unrelatedID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("unrelated rows = %d, err = %v", len(rows), err)
	}
	if _, err := conn.Exec(ctx, `UPDATE users SET date_of_birth = CURRENT_DATE - INTERVAL '19 years' WHERE id = $1`, athleteID); err != nil {
		t.Fatal(err)
	}
	rows, err = queries.ListStructuredTrainingOverviewForSubject(ctx, guardianID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("guardian rows after athlete adulthood = %d, err = %v", len(rows), err)
	}
	if _, err := conn.Exec(ctx, `UPDATE users SET date_of_birth = CURRENT_DATE - INTERVAL '14 years' WHERE id = $1`, athleteID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE user_memberships SET ends_on = CURRENT_DATE - 1 WHERE id = $1`, membershipID); err != nil {
		t.Fatal(err)
	}
	rows, err = queries.ListStructuredTrainingOverviewForSubject(ctx, guardianID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("guardian rows after membership expiry = %d, err = %v", len(rows), err)
	}

	source, err := queries.GetSessionRoutineSource(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	routine, err := queries.CreateTrainingRoutine(ctx, dbgen.CreateTrainingRoutineParams{Name: "Sessão híbrida reutilizável", Kind: dbgen.TrainingRoutineKindSESSION, Visibility: dbgen.TrainingRoutineVisibilityPRIVATE, OwnerUserID: actorID, Method: "Ativação + água", Tags: []string{"híbrido", "ginásio"}, SourceID: session.ID, SourceUpdatedAt: source.SourceUpdatedAt, Snapshot: source.Snapshot})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := queries.ListVisibleTrainingRoutines(ctx, dbgen.ListVisibleTrainingRoutinesParams{UserID: actorID, Query: "híbrida"})
	if err != nil || len(visible) != 1 || visible[0].ID != routine.ID {
		t.Fatalf("owner routine visibility = %#v, err = %v", visible, err)
	}
	visible, err = queries.ListVisibleTrainingRoutines(ctx, dbgen.ListVisibleTrainingRoutinesParams{UserID: actorID, Tag: "GINÁSIO"})
	if err != nil || len(visible) != 1 || visible[0].ID != routine.ID {
		t.Fatalf("case-insensitive routine tag filter = %#v, err = %v", visible, err)
	}
	visible, err = queries.ListVisibleTrainingRoutines(ctx, dbgen.ListVisibleTrainingRoutinesParams{UserID: unrelatedID})
	if err != nil || len(visible) != 0 {
		t.Fatalf("private routine leaked = %#v, err = %v", visible, err)
	}
	copiedSessionID, err := queries.RestoreTrainingSession(ctx, dbgen.RestoreTrainingSessionParams{Snapshot: routine.Snapshot, PlanID: week.ID, StartsAt: pgtype.Timestamptz{Time: startsAt.AddDate(0, 0, 1), Valid: true}, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.CreateTrainingCopyEvent(ctx, dbgen.CreateTrainingCopyEventParams{SourceKind: "ROUTINE", SourceID: routine.ID, SourceUpdatedAt: routine.UpdatedAt, DestinationKind: "SESSION", DestinationID: copiedSessionID, CopiedByID: actorID}); err != nil {
		t.Fatal(err)
	}
	var copiedExerciseID uuid.UUID
	if err := conn.QueryRow(ctx, `SELECT exercise.id FROM gym_exercises exercise JOIN training_segment_blocks block ON block.id = exercise.block_id JOIN training_session_segments segment ON segment.id = block.segment_id WHERE segment.session_id = $1 ORDER BY exercise.position LIMIT 1`, copiedSessionID).Scan(&copiedExerciseID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE gym_exercises SET name = 'Cópia alterada' WHERE id = $1`, copiedExerciseID); err != nil {
		t.Fatal(err)
	}
	var originalName string
	if err := conn.QueryRow(ctx, `SELECT name FROM gym_exercises WHERE id = $1`, secondExerciseID).Scan(&originalName); err != nil || originalName != "Elevação frontal" {
		t.Fatalf("source exercise changed through copy: name=%q err=%v", originalName, err)
	}
	var copyEvents int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM training_copy_events WHERE destination_id = $1 AND source_id = $2`, copiedSessionID, routine.ID).Scan(&copyEvents); err != nil || copyEvents != 1 {
		t.Fatalf("copy provenance count=%d err=%v", copyEvents, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE training_copy_events SET copied_at = now() WHERE destination_id = $1`, copiedSessionID); err == nil {
		t.Fatal("expected copy provenance to be append-only")
	}

}

func stringPtrDB(value string) *string { return &value }

func TestMemberProfileOptimisticUpdateAndImmutableAudit(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	userID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Perfil integração', $2, 'hash', '1990-01-01')`, userID, "profile-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	queries := dbgen.New(pool)
	if err := queries.EnsureMemberProfile(ctx, userID); err != nil {
		t.Fatal(err)
	}
	profile, err := queries.GetMemberProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	params := dbgen.UpdateMemberProfileParams{
		Phone: "+351 910 000 000", CountryCode: "PT", NationalityCode: "PT",
		EmergencyContactName: "Contacto", EmergencyContactRelationship: "Família", EmergencyContactPhone: "+351 920 000 000",
		MedicalDeclaration: "NONE_KNOWN", UserID: userID, ExpectedUpdatedAt: profile.UpdatedAt,
	}
	updated, err := queries.UpdateMemberProfile(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Phone != params.Phone || updated.MedicalDeclaration != "NONE_KNOWN" {
		t.Fatalf("updated profile = %#v", updated)
	}
	if _, err := pool.Exec(ctx, `UPDATE member_profiles SET phone = '+++' WHERE user_id = $1`, userID); err == nil {
		t.Fatal("invalid profile phone unexpectedly satisfied database constraint")
	}
	if _, err := queries.UpdateMemberProfile(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale profile update error = %v", err)
	}
	auditID, err := queries.CreateMemberProfileAudit(ctx, dbgen.CreateMemberProfileAuditParams{ActorUserID: userID, SubjectUserID: userID, Action: "PROFILE_UPDATED", ChangedFields: []string{"phone", "medical_declaration"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE member_profile_audit_events SET changed_fields = '{}' WHERE id = $1`, auditID); err == nil {
		t.Fatal("member profile audit update unexpectedly succeeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM member_profile_audit_events WHERE id = $1`, auditID); err == nil {
		t.Fatal("member profile audit delete unexpectedly succeeded")
	}
}

func TestEventEditAndCancellationPreserveResponsesAndRejectStaleWrites(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	staffID, memberID, eventID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES
		($1, 'Gestora de eventos', $2, 'hash', '1980-01-01'),
		($3, 'Membro de eventos', $4, 'hash', '1990-01-01')`, staffID, "event-staff-"+uuid.NewString()+"@example.test", memberID, "event-member-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id = $1`, eventID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{staffID, memberID})
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `INSERT INTO events (id, title, description, starts_at, ends_at, capacity, created_by_id) VALUES ($1, 'Evento original', '', $2, $3, 2, $4)`, eventID, now.Add(24*time.Hour), now.Add(26*time.Hour), staffID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_responses (event_id, user_id, status, responded_by_id) VALUES ($1, $2, 'Going', $2)`, eventID, memberID); err != nil {
		t.Fatal(err)
	}

	queries := dbgen.New(pool)
	current, err := queries.GetEventForEdit(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := queries.UpdateEvent(ctx, dbgen.UpdateEventParams{
		Title: "Evento atualizado", Description: "Novo detalhe", EventType: "GENERAL",
		StartsAt: pgtype.Timestamptz{Time: now.Add(25 * time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: now.Add(27 * time.Hour), Valid: true},
		Capacity: int32PtrDB(1), ID: eventID, AsOf: pgtype.Timestamptz{Time: now, Valid: true}, ExpectedUpdatedAt: current.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Evento atualizado" || updated.Capacity == nil || *updated.Capacity != 1 {
		t.Fatalf("updated event = %#v", updated)
	}
	if _, err := queries.UpdateEvent(ctx, dbgen.UpdateEventParams{Title: "Stale", Description: "", EventType: "GENERAL", StartsAt: updated.StartsAt, EndsAt: updated.EndsAt, Capacity: int32PtrDB(1), ID: eventID, AsOf: pgtype.Timestamptz{Time: now, Valid: true}, ExpectedUpdatedAt: current.UpdatedAt}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale event update error = %v", err)
	}
	if _, err := queries.UpdateEvent(ctx, dbgen.UpdateEventParams{Title: "Lotação inválida", Description: "", EventType: "GENERAL", StartsAt: updated.StartsAt, EndsAt: updated.EndsAt, Capacity: int32PtrDB(0), ID: eventID, AsOf: pgtype.Timestamptz{Time: now, Valid: true}, ExpectedUpdatedAt: updated.UpdatedAt}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("below-attendance capacity error = %v", err)
	}

	reason := "Condições meteorológicas adversas"
	cancelled, err := queries.CancelEvent(ctx, dbgen.CancelEventParams{CancelledAt: pgtype.Timestamptz{Time: now, Valid: true}, CancelledByID: &staffID, CancellationReason: &reason, ID: eventID, ExpectedUpdatedAt: updated.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "CANCELLED" || cancelled.CancellationReason == nil || *cancelled.CancellationReason != reason {
		t.Fatalf("cancelled event = %#v", cancelled)
	}
	if _, err := queries.GetEventForResponse(ctx, eventID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cancelled event response lock error = %v", err)
	}
	response, err := queries.GetEventResponse(ctx, dbgen.GetEventResponseParams{EventID: eventID, UserID: memberID})
	if err != nil || response.Status != "Going" {
		t.Fatalf("preserved response = %#v, err = %v", response, err)
	}
	visible, err := queries.ListEventsForMember(ctx, dbgen.ListEventsForMemberParams{UserID: memberID, RowLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range visible {
		if event.ID == eventID {
			found = event.Status == "CANCELLED" && event.CancellationReason != nil && *event.CancellationReason == reason
		}
	}
	if !found {
		t.Fatalf("cancelled event missing from member list: %#v", visible)
	}
}

func TestFeatureFlagsUseDefaultsConcurrencyAndImmutableAudit(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Administradora de funcionalidades', $2, 'hash', '1980-01-01')`, actorID, "feature-admin-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	suggestionID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO suggestions (id, requester_id, category, subject, description) VALUES ($1, $2, 'OTHER', 'Sugestão preservada', 'Este registo não pode ser removido ao desligar a funcionalidade.')`, suggestionID, actorID); err != nil {
		t.Fatal(err)
	}

	queries := dbgen.New(pool)
	flags, err := queries.ListFeatureFlags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]dbgen.ListFeatureFlagsRow, len(flags))
	for _, flag := range flags {
		byKey[flag.FeatureKey] = flag
	}
	if byKey["suggestions"].Mode != "ENABLED" || byKey["photo_submissions"].Mode != "DISABLED" || byKey["structured_training_planning"].Mode != "ADMIN_ONLY" {
		t.Fatalf("seeded feature flags = %#v", byKey)
	}

	current := byKey["suggestions"]
	changed, err := queries.UpdateFeatureFlag(ctx, dbgen.UpdateFeatureFlagParams{
		FeatureKey: "suggestions", Mode: dbgen.FeatureAvailabilityModeADMINONLY,
		ActorUserID: &actorID, ExpectedUpdatedAt: current.UpdatedAt,
	})
	if err != nil || changed != 1 {
		t.Fatalf("feature update rows = %d, err = %v", changed, err)
	}
	staleRows, err := queries.UpdateFeatureFlag(ctx, dbgen.UpdateFeatureFlagParams{
		FeatureKey: "suggestions", Mode: dbgen.FeatureAvailabilityModeDISABLED,
		ActorUserID: &actorID, ExpectedUpdatedAt: current.UpdatedAt,
	})
	if err != nil || staleRows != 0 {
		t.Fatalf("stale feature update rows = %d, err = %v", staleRows, err)
	}

	events, err := queries.ListFeatureFlagEvents(ctx, 10)
	if err != nil || len(events) == 0 {
		t.Fatalf("feature events = %#v, err = %v", events, err)
	}
	event := events[0]
	if event.FeatureKey != "suggestions" || event.PreviousMode != "ENABLED" || event.NewMode != "ADMIN_ONLY" || event.ActorUserID != actorID {
		t.Fatalf("feature event = %#v", event)
	}
	if _, err := pool.Exec(ctx, `UPDATE feature_flag_events SET new_mode = 'DISABLED' WHERE id = $1`, event.ID); err == nil {
		t.Fatal("feature flag audit update unexpectedly succeeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM feature_flag_events WHERE id = $1`, event.ID); err == nil {
		t.Fatal("feature flag audit delete unexpectedly succeeded")
	}
	var suggestionCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM suggestions WHERE id = $1`, suggestionID).Scan(&suggestionCount); err != nil || suggestionCount != 1 {
		t.Fatalf("preserved suggestion count = %d, err = %v", suggestionCount, err)
	}

	flags, err = queries.ListFeatureFlags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range flags {
		if flag.FeatureKey == "suggestions" {
			changed, err = queries.UpdateFeatureFlag(ctx, dbgen.UpdateFeatureFlagParams{FeatureKey: "suggestions", Mode: dbgen.FeatureAvailabilityModeENABLED, ActorUserID: &actorID, ExpectedUpdatedAt: flag.UpdatedAt})
			if err != nil || changed != 1 {
				t.Fatalf("restore suggestions rows = %d, err = %v", changed, err)
			}
		}
	}

	if _, err := pool.Exec(ctx, `DELETE FROM feature_flags WHERE feature_key = 'photo_submissions'`); err != nil {
		t.Fatal(err)
	}
	changed, err = queries.UpdateFeatureFlag(ctx, dbgen.UpdateFeatureFlagParams{FeatureKey: "photo_submissions", Mode: dbgen.FeatureAvailabilityModeDISABLED, ActorUserID: &actorID})
	if err != nil || changed != 1 {
		t.Fatalf("repair missing registered flag rows = %d, err = %v", changed, err)
	}
}
