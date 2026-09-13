//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestEventResultsLinksAuthorizationLifecycleAndIntegrity(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	admin, member, eventID := uuid.New(), uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Results administrator',$2,'hash','1980-01-01'),($3,'Results member',$4,'hash','1990-01-01')`, admin, admin.String()+"@example.test", member, member.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO events(id,title,event_type,starts_at,ends_at,created_by_id) VALUES($1,'Completed competition','COMPETITION',now()-interval '2 days',now()-interval '1 day',$2)`, eventID, admin); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(tx)
	first := "https://fpcanoagem.pt/resultados/primeira.pdf"
	second := "https://www.fpcanoagem.pt/resultados/final.pdf"
	save := func(actor uuid.UUID, version int64, link *string, want int64) {
		t.Helper()
		n, e := q.UpdateEventResultsLink(ctx, dbgen.UpdateEventResultsLinkParams{ID: eventID, ActorUserID: &actor, ExpectedVersion: version, OfficialResultsUrl: link})
		if e != nil || n != want {
			t.Fatalf("save version %d: rows=%d error=%v", version, n, e)
		}
	}
	save(member, 0, &first, 0)
	if _, err = tx.Exec(ctx, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) SELECT $1,'COACH',id,$2 FROM programmes LIMIT 1`, member, admin); err != nil {
		t.Fatal(err)
	}
	save(member, 0, &first, 0)
	save(admin, 0, &first, 1)
	save(admin, 1, &second, 1)
	save(admin, 1, &first, 0)
	save(admin, 1, nil, 0)
	detail, err := q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{EventID: eventID, UserID: member})
	if err != nil || detail.OfficialResultsUrl == nil || *detail.OfficialResultsUrl != second {
		t.Fatalf("authorized detail=%+v err=%v", detail, err)
	}
	var actor uuid.UUID
	var version int64
	var timed bool
	if err = tx.QueryRow(ctx, `SELECT results_updated_by_id,results_version,results_updated_at IS NOT NULL FROM events WHERE id=$1`, eventID).Scan(&actor, &version, &timed); err != nil || actor != admin || version != 2 || !timed {
		t.Fatalf("provenance %v/%d/%t err=%v", actor, version, timed, err)
	}
	// Direct SQL must uphold the invariant too, independently of the handler.
	sp, _ := tx.Begin(ctx)
	_, err = sp.Exec(ctx, `UPDATE events SET event_type='GENERAL' WHERE id=$1`, eventID)
	if err == nil {
		t.Fatal("general transition accepted with link")
	}
	_ = sp.Rollback(ctx)
	for _, bad := range []string{"http://fpcanoagem.pt/a", "https://fpcanoagem.pt.evil.test/a", "https://user@fpcanoagem.pt/a", "https://fpcanoagem.pt:443/a", "https://fpcanoagem.pt/a?token=x", "https://fpcanoagem.pt/a#x", "https://fpcanoagem.pt/%0A", "https://fpcanoagem.pt/%5c", "https://fpcanoagem.pt/%252f", "https://fpcanoagem.pt/%GG", "https://fpcanoagem.pt/%"} {
		sp, _ := tx.Begin(ctx)
		_, err = sp.Exec(ctx, `UPDATE events SET official_results_url=$2 WHERE id=$1`, eventID, bad)
		if err == nil {
			t.Errorf("DB accepted %q", bad)
		}
		_ = sp.Rollback(ctx)
	}
	save(admin, 2, nil, 1)
	if _, err = tx.Exec(ctx, `UPDATE events SET event_type='GENERAL' WHERE id=$1`, eventID); err != nil {
		t.Fatal(err)
	}
	save(admin, 3, &first, 0)
	if _, err = tx.Exec(ctx, `UPDATE events SET event_type='COMPETITION',status='CANCELLED',cancelled_at=now(),cancelled_by_id=$2,cancellation_reason='Preserved competition' WHERE id=$1`, eventID, admin); err != nil {
		t.Fatal(err)
	}
	save(admin, 3, &first, 1)
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, member); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, admin); err != nil {
		t.Fatal(err)
	}
	save(admin, 4, nil, 0)
	programme := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO programmes(id,code,name_pt) VALUES($1,$2,'Restricted results')`, programme, "RESULTS_"+programme.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO event_audiences(event_id,programme_id) VALUES($1,$2)`, eventID, programme); err != nil {
		t.Fatal(err)
	}
	_, err = q.GetEventDetailForMember(ctx, dbgen.GetEventDetailForMemberParams{EventID: eventID, UserID: member})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("outsider detail err=%v", err)
	}
}

func TestEventResultsTypeChangeSerializesBothLockOrders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	other, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	actor := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Results race actor',$2,'hash','1980-01-01')`, actor, actor.String()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, actor); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Fixture teardown only: a pristine test DB may have no other administrator.
		cleanup, e := conn.Begin(context.Background())
		if e != nil {
			return
		}
		defer cleanup.Rollback(context.Background())
		_, _ = cleanup.Exec(context.Background(), `SET LOCAL session_replication_role=replica`)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM user_platform_roles WHERE user_id=$1`, actor)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, actor)
		_ = cleanup.Commit(context.Background())
	}()
	for _, resultsFirst := range []bool{true, false} {
		eventID := uuid.New()
		if _, err = conn.Exec(ctx, `INSERT INTO events(id,title,event_type,starts_at,ends_at,created_by_id) VALUES($1,'Concurrent competition','COMPETITION',now()+interval '2 days',now()+interval '3 days',$2)`, eventID, actor); err != nil {
			t.Fatal(err)
		}
		defer conn.Exec(context.Background(), `DELETE FROM events WHERE id=$1`, eventID)
		current, e := dbgen.New(conn).GetEventForEdit(ctx, eventID)
		if e != nil {
			t.Fatal(e)
		}
		first, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		second, err := other.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		resultsChange := func(tx pgx.Tx) (int64, error) {
			link := "https://fpcanoagem.pt/results.pdf"
			return dbgen.New(tx).UpdateEventResultsLink(ctx, dbgen.UpdateEventResultsLinkParams{ID: eventID, ActorUserID: &actor, OfficialResultsUrl: &link, ExpectedVersion: 0})
		}
		typeChange := func(tx pgx.Tx) (int64, error) {
			_, e := dbgen.New(tx).UpdateEvent(ctx, dbgen.UpdateEventParams{ID: eventID, Title: current.Title, Description: current.Description, EventType: "GENERAL", StartsAt: current.StartsAt, EndsAt: current.EndsAt, AsOf: pgtype.Timestamptz{Time: time.Now(), Valid: true}, ExpectedUpdatedAt: current.UpdatedAt})
			if errors.Is(e, pgx.ErrNoRows) {
				return 0, nil
			}
			if e != nil {
				return 0, e
			}
			return 1, nil
		}
		firstChange, secondChange := resultsChange, typeChange
		if !resultsFirst {
			firstChange, secondChange = typeChange, resultsChange
		}
		if count, e := firstChange(first); e != nil || count != 1 {
			t.Fatalf("first change rows=%d err=%v", count, e)
		}
		type outcome struct {
			rows int64
			err  error
		}
		done := make(chan outcome, 1)
		go func() { count, e := secondChange(second); done <- outcome{count, e} }()
		// Observe the second transaction waiting on the first row lock before release.
		for {
			var blocked bool
			if e := first.QueryRow(ctx, `SELECT wait_event_type='Lock' FROM pg_stat_activity WHERE pid=$1`, other.PgConn().PID()).Scan(&blocked); e == nil && blocked {
				break
			}
			select {
			case result := <-done:
				t.Fatalf("second update did not block: %+v", result)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
		if err = first.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		result := <-done
		if result.err != nil || result.rows != 0 {
			t.Fatalf("resultsFirst=%t second=%+v", resultsFirst, result)
		}
		if err = second.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEventResultsMigrationFromPredecessor(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// The predecessor is reconstructed within a rollback-only test transaction.
	if _, err = tx.Exec(ctx, `ALTER TABLE events DROP COLUMN official_results_url,DROP COLUMN results_updated_by_id,DROP COLUMN results_updated_at,DROP COLUMN results_version;
      ALTER TABLE privacy_activation_authenticated_artifacts RENAME CONSTRAINT privacy_activation_authenticated_artifacts_v16_check TO privacy_activation_authenticated_artifacts_v15_check;
      DO $$DECLARE d text; BEGIN
      SELECT pg_get_constraintdef(oid) INTO d FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v15_check';
      d:=replace(d, ', ''202609130001_event_results_links''::text','');
      ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v15_check;
      EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v15_check '||d||' NOT VALID';
      EXECUTE replace(pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),'202609130001_event_results_links','202609120007_guardian_release_status');
      EXECUTE replace(pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure),'202609130001_event_results_links','202609120007_guardian_release_status');END$$;`); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/202609130001_event_results_links.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var off bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM privacy_request_activation WHERE enabled OR fulfilment_ready) AND k.engaged AND NOT g.enabled FROM privacy_worker_kill_switch k CROSS JOIN guardian_application_intake_release g`).Scan(&off); err != nil || !off {
		t.Fatalf("migration activation fence=%t err=%v", off, err)
	}
}
