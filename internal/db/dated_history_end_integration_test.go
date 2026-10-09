//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatedParticipationRecordedDatesCannotBeExcluded(t *testing.T) {
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
	actor, season := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Staff test',$2,'hash','1980-01-01')`, actor, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Recorded dates','2026-01-01','2026-12-31')`, season, "HD_"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	var programme uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"prescription", "event", "general event", "training"} {
		t.Run(kind, func(t *testing.T) {
			user, membership := uuid.New(), uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Athlete test',$2,'hash','2010-02-28')`, user, uuid.NewString()+"@example.test"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,'2026-01-01','2026-12-31')`, membership, user, season, programme); err != nil {
				t.Fatal(err)
			}
			var associationID uuid.UUID
			var snapshot []byte
			var digest string
			if kind == "event" || kind == "general event" {
				if err := tx.QueryRow(ctx, `INSERT INTO events(title,starts_at,ends_at,created_by_id) VALUES('Historical event','2026-06-12 10:00+01','2026-06-12 11:00+01',$1) RETURNING id`, actor).Scan(&associationID); err != nil {
					t.Fatal(err)
				}
				if kind == "event" {
					if _, err := tx.Exec(ctx, `INSERT INTO event_audiences(event_id,programme_id) VALUES($1,$2)`, associationID, programme); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := tx.Exec(ctx, `INSERT INTO event_responses(event_id,user_id,status,responded_by_id,checked_in_at,checked_in_by_id) VALUES($1,$2,'Going',$2,'2026-06-12 10:00+01',$3)`, associationID, user, actor); err != nil {
					t.Fatal(err)
				}
			} else {
				group, plan, session := uuid.New(), uuid.New(), uuid.New()
				if _, err := tx.Exec(ctx, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Recorded group',$2,$3)`, group, programme, actor); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, membership, actor); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Recorded plan',$2,$3,$4,'2026-06-08',$5)`, plan, programme, group, season, actor); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES($1,$2,'Recorded session','2026-06-12 10:00+01','2026-06-12 11:00+01',$3)`, session, plan, actor); err != nil {
					t.Fatal(err)
				}
				associationID = session
				if kind == "prescription" {
					var publication uuid.UUID
					if err := tx.QueryRow(ctx, `INSERT INTO training_plan_publications(plan_id,revision,source_updated_at,change_summary,published_by_id) VALUES($1,1,'2026-06-11 10:00+01','First publication',$2) RETURNING id`, plan, actor).Scan(&publication); err != nil {
						t.Fatal(err)
					}
					snapshot = []byte(`{"schema_version":1,"session":{"title":"Recorded session"}}`)
					hash := sha256.Sum256(snapshot)
					digest = hex.EncodeToString(hash[:])
					if err := tx.QueryRow(ctx, `INSERT INTO training_prescriptions(publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, publication, session, membership, user, snapshot, digest).Scan(&associationID); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := tx.Exec(ctx, `INSERT INTO training_session_outcomes(session_id,user_id,status) VALUES($1,$2,'COMPLETED')`, session, user); err != nil {
						t.Fatal(err)
					}
				}
			}
			// A saved response/attendance, completed session or immutable prescription must
			// preserve its exact membership and recorded calendar date.
			child, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = child.Exec(ctx, `UPDATE user_memberships SET ends_on='2026-06-11' WHERE id=$1`, membership)
			var pgerr *pgconn.PgError
			if !sqlState(err, "23514") {
				t.Errorf("excluded %s date, err=%v", kind, err)
			} else if ok := errorAsPg(err, &pgerr); !ok || pgerr.ConstraintName != "user_memberships_recorded_history_end" {
				t.Errorf("wrong staff error for %s: %v", kind, err)
			}
			if err := child.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var end string
			if err := tx.QueryRow(ctx, `SELECT ends_on::text FROM user_memberships WHERE id=$1`, membership).Scan(&end); err != nil || end != "2026-12-31" {
				t.Fatalf("end changed: %q %v", end, err)
			}
			if kind == "prescription" {
				var gotMember uuid.UUID
				var snapshotMatches bool
				var gotDigest string
				if err := tx.QueryRow(ctx, `SELECT membership_id,snapshot=$2::jsonb,snapshot_sha256 FROM training_prescriptions WHERE id=$1`, associationID, string(snapshot)).Scan(&gotMember, &snapshotMatches, &gotDigest); err != nil || gotMember != membership || !snapshotMatches || gotDigest != digest {
					t.Fatalf("published identity/snapshot/hash mutated: %v", err)
				}
			} else if kind == "event" || kind == "general event" {
				var got uuid.UUID
				if err := tx.QueryRow(ctx, `SELECT event_id FROM event_responses WHERE event_id=$1 AND user_id=$2`, associationID, user).Scan(&got); err != nil || got != associationID {
					t.Fatalf("event association changed: %v", err)
				}
			} else {
				var got uuid.UUID
				if err := tx.QueryRow(ctx, `SELECT session_id FROM training_session_outcomes WHERE session_id=$1 AND user_id=$2`, associationID, user).Scan(&got); err != nil || got != associationID {
					t.Fatalf("training association changed: %v", err)
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE user_memberships SET ends_on='2026-06-12' WHERE id=$1`, membership); err != nil {
				t.Fatalf("inclusive association day rejected: %v", err)
			}
		})
	}
}

func errorAsPg(err error, target **pgconn.PgError) bool { return errors.As(err, target) }
