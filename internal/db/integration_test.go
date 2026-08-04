//go:build integration

package db

import (
	"context"
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
		event, err := queries.CreateEvent(ctx, dbgen.CreateEventParams{Title: title, StartsAt: pgtype.Timestamptz{Time: today.Add(hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: today.Add(hour + time.Hour), Valid: true}, CreatedByID: authorID})
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
