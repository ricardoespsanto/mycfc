//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSportAssignmentCorrectionScopedAndAudited(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	admin, coach, otherCoach, outsider, member := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, otherCoach, outsider, member} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Teste',$2,'hash','1980-01-01')`, id, "correction-"+id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	season := uuid.New()
	today := time.Now().In(mustLisbon(t))
	start := today.AddDate(0, 0, -2)
	end := today.AddDate(0, 0, 2)
	if _, err := pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Teste',$3,$4)`, season, "IT_"+uuid.NewString()[:8], start, end); err != nil {
		t.Fatal(err)
	}
	var leisure, polo uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&leisure); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Kayak_Polo'`).Scan(&polo); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4)`, member, season, leisure, start); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, leisure, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, otherCoach, polo, admin); err != nil {
		t.Fatal(err)
	}
	svc := SportAssignmentService{Pool: pool}
	change := SportAssignmentCorrection{ActorID: admin, MemberID: member, Kind: SportingModalityAssignment, Codes: []string{"CANOEING", "SUP"}, Reason: "Correção da ficha"}
	if err := svc.Replace(ctx, change); err != nil {
		t.Fatal(err)
	}
	change.ActorID = coach
	change.Codes = []string{"KAYAK_POLO", "CANOEING"}
	if err := svc.Replace(ctx, change); err != nil {
		t.Fatal(err)
	}
	change.Codes = []string{"K2"}
	if err := svc.Replace(ctx, change); !errors.Is(err, ErrSportAssignmentInvalid) {
		t.Fatalf("invalid vocabulary: %v", err)
	}
	change.Codes = []string{"CANOEING", "CANOEING"}
	if err := svc.Replace(ctx, change); !errors.Is(err, ErrSportAssignmentInvalid) {
		t.Fatalf("duplicate selection: %v", err)
	}
	change.Codes = []string{"KAYAK_POLO", "CANOEING"}
	if err := svc.Replace(ctx, change); err != nil {
		t.Fatalf("idempotent selection: %v", err)
	}
	change.ActorID = otherCoach
	change.Codes = nil
	if err := svc.Replace(ctx, change); !errors.Is(err, ErrSportAssignmentForbidden) {
		t.Fatalf("unrelated grant: %v", err)
	}
	change.ActorID = outsider
	if err := svc.Replace(ctx, change); !errors.Is(err, ErrSportAssignmentForbidden) {
		t.Fatalf("outsider: %v", err)
	}
	change.ActorID = coach
	change.Kind = CanoeCraftAssignment
	change.Codes = []string{"K2", "C2"}
	if err := svc.Replace(ctx, change); err != nil {
		t.Fatal(err)
	}
	change.Codes = []string{"C2"}
	if err := svc.Replace(ctx, change); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1 WHERE user_id=$2 AND capability='COACH'`, admin, coach); err != nil {
		t.Fatal(err)
	}
	change.Codes = nil
	if err := svc.Replace(ctx, change); !errors.Is(err, ErrSportAssignmentForbidden) {
		t.Fatalf("revoked grant: %v", err)
	}
	change.ActorID = admin
	change.Kind = SportingModalityAssignment
	change.Codes = []string{"SUP"}
	if err := svc.Replace(ctx, change); err != nil {
		t.Fatal(err)
	}
	var sports, crafts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM person_sporting_modalities WHERE user_id=$1 AND modality_code='SUP'`, member).Scan(&sports); err != nil || sports != 1 {
		t.Fatalf("sports %d %v", sports, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM person_canoe_craft_classes WHERE user_id=$1`, member).Scan(&crafts); err != nil || crafts != 0 {
		t.Fatalf("cascade crafts %d %v", crafts, err)
	}
	change.Kind = CanoeCraftAssignment
	change.Codes = []string{"K1"}
	if err := svc.Replace(ctx, change); !errors.Is(err, ErrSportAssignmentInvalid) {
		t.Fatalf("craft without Canoagem parent: %v", err)
	}
	var events, attributed, removedCrafts, coachProvenance int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE actor_user_id=$2 AND reason='Correção da ficha'),count(*) FILTER(WHERE kind='CRAFT' AND action='REMOVED'),count(*) FILTER(WHERE actor_user_id=$3 AND coach_grant_id IS NOT NULL) FROM person_sport_assignment_events WHERE subject_user_id=$1`, member, admin, coach).Scan(&events, &attributed, &removedCrafts, &coachProvenance); err != nil || events != 11 || attributed == 0 || removedCrafts != 2 || coachProvenance != 5 {
		t.Fatalf("audit events=%d attributed=%d craft removals=%d coach provenance=%d err=%v", events, attributed, removedCrafts, coachProvenance, err)
	}
	for _, actor := range []uuid.UUID{uuid.New(), outsider} {
		if actor == outsider {
			if _, err := pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, actor); err != nil {
				t.Fatal(err)
			}
		}
		change.ActorID = actor
		if err := svc.Replace(ctx, change); !errors.Is(err, ErrSportAssignmentForbidden) {
			t.Fatalf("inactive/missing actor accepted: %v", err)
		}
	}
	change.ActorID = admin
	// Real database failures must roll back both assignments and their audit.
	for _, target := range []string{"person_sporting_modalities", "person_canoe_craft_classes", "person_sport_assignment_events"} {
		t.Run("rollback "+target, func(t *testing.T) {
			fn := "fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if _, err := pool.Exec(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic write failure'; END $$; CREATE TRIGGER `+fn+` BEFORE INSERT OR DELETE ON `+target+` FOR EACH ROW EXECUTE FUNCTION `+fn+`() `); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pool.Exec(ctx, `DROP TRIGGER `+fn+` ON `+target+`; DROP FUNCTION `+fn+`() `); err != nil {
					t.Error(err)
				}
			}()
			change.Kind = SportingModalityAssignment
			change.Codes = []string{"CANOEING"}
			if target == "person_canoe_craft_classes" {
				change.Kind = CanoeCraftAssignment
				change.Codes = []string{"K1"}
			}
			if err := svc.Replace(ctx, change); err == nil {
				t.Fatal("failed write accepted")
			}
			var remaining int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM person_sporting_modalities WHERE user_id=$1 AND modality_code='SUP'`, member).Scan(&remaining); err != nil || remaining != 1 {
				t.Fatalf("failed transaction changed selections: %v", err)
			}
		})
		// The craft trigger needs its parent before it can reach the write seam.
		if target == "person_sporting_modalities" {
			change.Kind = SportingModalityAssignment
			change.Codes = []string{"CANOEING", "SUP"}
			if err := svc.Replace(ctx, change); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		target string
		kind   SportAssignmentKind
		codes  []string
	}{
		{"person_sporting_modalities", SportingModalityAssignment, []string{"CANOEING", "SUP", "KAYAK_POLO"}},
		{"person_sport_assignment_events", SportingModalityAssignment, []string{"CANOEING", "SUP", "KAYAK_POLO"}},
		{"person_canoe_craft_classes", CanoeCraftAssignment, nil},
		{"person_sport_assignment_events", CanoeCraftAssignment, nil},
		{"person_canoe_craft_classes", SportingModalityAssignment, []string{"SUP"}},
	} {
		t.Run("additional rollback "+tc.target+string(tc.kind), func(t *testing.T) {
			if _, err := pool.Exec(ctx, `INSERT INTO person_canoe_craft_classes(user_id,craft_code) VALUES($1,'K1') ON CONFLICT DO NOTHING`, member); err != nil {
				t.Fatal(err)
			}
			fn := "fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if _, err := pool.Exec(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic write failure'; END $$; CREATE TRIGGER `+fn+` BEFORE INSERT OR DELETE ON `+tc.target+` FOR EACH ROW EXECUTE FUNCTION `+fn+`() `); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pool.Exec(ctx, `DROP TRIGGER `+fn+` ON `+tc.target+`; DROP FUNCTION `+fn+`() `); err != nil {
					t.Error(err)
				}
			}()
			change.Kind = tc.kind
			change.Codes = tc.codes
			if err := svc.Replace(ctx, change); err == nil {
				t.Fatal("failed correction accepted")
			}
			var remains bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM person_canoe_craft_classes WHERE user_id=$1 AND craft_code='K1')`, member).Scan(&remains); err != nil || !remains {
				t.Fatalf("craft rollback failed: %v", err)
			}
		})
	}
	if _, err := pool.Exec(ctx, `UPDATE person_sport_assignment_events SET reason='alterado' WHERE subject_user_id=$1`, member); !sqlState(err, "23514") {
		t.Fatalf("mutable audit: %v", err)
	}
}

func mustLisbon(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
