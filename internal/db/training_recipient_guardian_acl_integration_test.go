//go:build integration

package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func recipientControlConnection(t *testing.T, f recipientSchemaFixture, role, password string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(f.url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.User, cfg.Password = role, password
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}
func recipientGuardianState(t *testing.T, f recipientSchemaFixture) string {
	t.Helper()
	return recipientGuardianStateAt(t, f.low)
}
func recipientGuardianStateAt(t *testing.T, q TrainingScheduleQueryReader) string {
	t.Helper()
	var state string
	err := q.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY version) FROM guardian_authority_policies x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY policy_version) FROM guardian_authority_policy_approvals x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM guardian_authority_policy_events x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM guardian_application_intake_release_events x),
 (SELECT jsonb_agg(to_jsonb(x)) FROM guardian_application_intake_release x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM guardian_authority_relationships x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM guardian_authority_events x),
(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM users x),
 (SELECT jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text) FROM sessions x))::text`).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func TestTrainingRecipientGuardianInternalACL(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "forward"
		if fresh {
			name = "fresh"
		}
		t.Run(name, func(t *testing.T) {
			f := newRecipientSchemaFixture(t, fresh)
			if fresh {
				recipientStampedFixture(t)
				if err := FreshInitializeDatabase(t.Context(), f.root, "mycfc", f.roles); err != nil {
					t.Fatal(err)
				}
				f.low = f.connectLow(t)
			} else {
				f.install79(t)
				if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
					t.Fatal(err)
				}
			}
			h := recipientHistory{actor: uuid.New()}
			recipientSchemaExec(t, f.low, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic administrator',$2,'hash','1980-01-01')`, h.actor, h.actor.String()+"@example.test")
			recipientSchemaExec(t, f.low, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, h.actor)
			if err := ProvisionGuardianReleaseBindRole(t.Context(), f.root, "mycfc", guardianReleaseBindRole, "synthetic-binder"); err != nil {
				t.Fatal(err)
			}
			if err := ProvisionGuardianActivationRole(t.Context(), f.root, "mycfc", guardianActivationRole, "synthetic-operator"); err != nil {
				t.Fatal(err)
			}
			recipientSchemaExec(t, f.root, `CREATE ROLE recipient_public_only LOGIN PASSWORD 'synthetic-public' NOINHERIT;GRANT CONNECT ON DATABASE mycfc TO recipient_public_only`)
			operator := recipientControlConnection(t, f, guardianActivationRole, "synthetic-operator")
			binder := recipientControlConnection(t, f, guardianReleaseBindRole, "synthetic-binder")
			public := recipientControlConnection(t, f, "recipient_public_only", "synthetic-public")
			web := f.web(t)
			signatures := []string{"guardian_ops.preflight(uuid,text,bytea,bytea,text,text,text)", "guardian_ops.canonical_policy()", "guardian_ops.canonical_approval(jsonb)", "guardian_ops.schema_ready(text)", "guardian_ops.enable(uuid,text,bytea,bytea,text,text,text)", "guardian_ops.disable(uuid,text)", "guardian_ops.status(text,text,text)", "guardian_ops.release_disable_and_bind(text,text,text)", "guardian_ops.release_intake_enabled(text,text,text)"}
			assertACL := func() {
				t.Helper()
				for i, sig := range signatures {
					var owner, volatility string
					var definer bool
					var config []string
					if err := f.low.QueryRow(t.Context(), `SELECT pg_get_userbyid(proowner),prosecdef,provolatile::text,proconfig FROM pg_proc WHERE oid=$1::regprocedure`, sig).Scan(&owner, &definer, &volatility, &config); err != nil {
						t.Fatal(err)
					}
					if owner != f.roles.MigrationUsername || !definer || len(config) != 1 || !strings.HasPrefix(config[0], "search_path=pg_catalog") {
						t.Fatalf("unproved function ownership/config %s %s %v %v", sig, owner, definer, config)
					}
					if i == 0 && volatility != "s" || (i == 1 || i == 2) && volatility != "i" {
						t.Fatalf("read helper volatility changed %s", sig)
					}
					for _, r := range []string{f.roles.MigrationUsername, f.roles.AppUsername, guardianActivationRole, guardianReleaseBindRole, "recipient_public_only"} {
						want := r == f.roles.MigrationUsername && i < 4 || r == guardianActivationRole && (i == 0 || i == 4 || i == 5 || i == 6) || r == guardianReleaseBindRole && i >= 7
						var allowed bool
						if err := f.low.QueryRow(t.Context(), `SELECT has_function_privilege($1,$2,'EXECUTE')`, r, sig).Scan(&allowed); err != nil {
							t.Fatal(err)
						}
						if allowed != want {
							t.Fatalf("ACL %s %s allowed=%v want=%v", r, sig, allowed, want)
						}
					}
				}
				for _, r := range []string{f.roles.AppUsername, "recipient_public_only"} {
					var usage bool
					if err := f.low.QueryRow(t.Context(), `SELECT has_schema_privilege($1,'guardian_ops','USAGE')`, r).Scan(&usage); err != nil || usage {
						t.Fatalf("private schema %s %v %v", r, usage, err)
					}
				}
				for _, r := range []string{f.roles.AppUsername, guardianActivationRole, guardianReleaseBindRole, "recipient_public_only"} {
					var tableAccess bool
					if err := f.low.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE ((n.nspname='guardian_ops' AND c.relkind='r') OR c.relname='guardian_authority_policy_approvals') AND has_table_privilege($1,c.oid,'SELECT,INSERT,UPDATE,DELETE'))`, r).Scan(&tableAccess); err != nil || tableAccess {
						t.Fatalf("private table %s %v %v", r, tableAccess, err)
					}
				}
			}
			assertACL()
			image := "sha256:" + strings.Repeat("a", 64)
			recipientSchemaExec(t, binder, `SELECT * FROM guardian_ops.release_disable_and_bind($1,$2,'mycfc')`, image, EmbeddedMigrationDigest())
			approval, policy := guardianActivationFixture(t, h.actor, "mycfc")
			before := recipientGuardianState(t, f)
			var canonical []byte
			if err := f.low.QueryRow(t.Context(), `SELECT guardian_ops.canonical_policy()`).Scan(&canonical); err != nil || !bytes.Equal(canonical, policy) {
				t.Fatal("canonical policy mismatch", err)
			}
			if err := f.low.QueryRow(t.Context(), `SELECT guardian_ops.canonical_approval($1::jsonb)`, string(approval)).Scan(&canonical); err != nil || !bytes.Equal(canonical, approval) {
				t.Fatal("canonical approval mismatch", err)
			}
			args := []any{h.actor, string(approval), approval, policy, image, EmbeddedMigrationDigest(), "mycfc"}
			const preflight = `SELECT ready FROM guardian_ops.preflight($1,$2,$3,$4,$5,$6,$7)`
			var ready bool
			for _, c := range []*pgx.Conn{f.low, operator} {
				if err := c.QueryRow(t.Context(), preflight, args...).Scan(&ready); err != nil || !ready {
					t.Fatalf("legitimate read-only preflight failed %v %v", ready, err)
				}
			}
			if recipientGuardianState(t, f) != before {
				t.Fatal("preflight/canonical helpers changed durable activation/user/session state")
			}
			denied := func(c *pgx.Conn, sql string, args ...any) {
				t.Helper()
				_, err := c.Exec(t.Context(), sql, args...)
				var pe *pgconn.PgError
				if !errors.As(err, &pe) || pe.Code != "42501" {
					t.Fatalf("expected privilege denial got %v", err)
				}
			}
			for _, c := range []*pgx.Conn{web, public, binder} {
				denied(c, preflight, args...)
			}
			denied(f.low, `SELECT * FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, args...)
			denied(f.low, `SELECT * FROM guardian_ops.disable($1,'mycfc')`, h.actor)
			denied(f.low, `SELECT * FROM guardian_ops.status('mycfc',$1,$2)`, image, EmbeddedMigrationDigest())
			for _, c := range []*pgx.Conn{operator, binder, public, web} {
				denied(c, `SELECT guardian_ops.canonical_policy()`)
				denied(c, `SELECT * FROM guardian_ops.runtime_release_binding`)
				denied(c, `SELECT * FROM mycfc_meta.schema_migrations`)
			}
			// Each failure exercises the unchanged authority/canonical/binding rules and leaves every recorded state byte unchanged.
			tests := [][]any{}
			for index, value := range map[int]any{0: uuid.New(), 1: string(approval) + " ", 2: append(append([]byte(nil), approval...), byte(' ')), 3: append(append([]byte(nil), policy...), byte(' ')), 4: "sha256:" + strings.Repeat("b", 64), 5: strings.Repeat("b", 64), 6: "wrong_database"} {
				x := append([]any(nil), args...)
				x[index] = value
				tests = append(tests, x)
			}
			var expired guardianActivationApproval
			if err := json.Unmarshal(approval, &expired); err != nil {
				t.Fatal(err)
			}
			expired.ReviewDueOn = time.Now().AddDate(0, 0, -1).Format(time.DateOnly)
			bad, _ := json.Marshal(expired)
			x := append([]any(nil), args...)
			x[1], x[2] = string(bad), bad
			tests = append(tests, x)
			for _, badArgs := range tests {
				ready = true
				err := operator.QueryRow(t.Context(), preflight, badArgs...).Scan(&ready)
				if err == nil && ready {
					t.Fatal("invalid preflight accepted")
				}
				if recipientGuardianState(t, f) != before {
					t.Fatal("invalid preflight mutated state")
				}
				if _, err = operator.Exec(t.Context(), `SELECT * FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, badArgs...); err == nil {
					t.Fatal("invalid enable accepted")
				}
				if recipientGuardianState(t, f) != before {
					t.Fatal("invalid enable mutated state")
				}

			}
			// A genuinely non-administrator with matching canonical actor bytes is denied.
			nonadmin := uuid.New()
			recipientSchemaExec(t, web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic nonadministrator',$2,'hash','1980-01-01')`, nonadmin, nonadmin.String()+"@example.test")
			before = recipientGuardianState(t, f)
			nonadminApproval, _ := guardianActivationFixture(t, nonadmin, "mycfc")
			_, err := operator.Exec(t.Context(), preflight, nonadmin, string(nonadminApproval), nonadminApproval, policy, image, EmbeddedMigrationDigest(), "mycfc")
			var deniedActor *pgconn.PgError
			if !errors.As(err, &deniedActor) || deniedActor.Code != "42501" || recipientGuardianState(t, f) != before {
				t.Fatalf("nonadministrator preflight altered state or escaped denial %v", err)
			}
			var changed bool
			if err := operator.QueryRow(t.Context(), `SELECT changed FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, args...).Scan(&changed); err != nil || !changed {
				t.Fatalf("enable %v %v", changed, err)
			}
			if err := operator.QueryRow(t.Context(), `SELECT changed FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, args...).Scan(&changed); err != nil || changed {
				t.Fatalf("repeat enable %v %v", changed, err)
			}
			var state string
			if err := operator.QueryRow(t.Context(), `SELECT state FROM guardian_ops.status('mycfc',$1,$2)`, image, EmbeddedMigrationDigest()).Scan(&state); err != nil || state != "ENABLED" {
				t.Fatalf("enabled status %s %v", state, err)
			}
			otherApproval, _ := guardianActivationFixture(t, h.actor, "mycfc")
			before = recipientGuardianState(t, f)
			if err := operator.QueryRow(t.Context(), preflight, h.actor, string(otherApproval), otherApproval, policy, image, EmbeddedMigrationDigest(), "mycfc").Scan(&ready); err != nil || ready {
				t.Fatalf("conflicting policy accepted %v %v", ready, err)
			}
			if recipientGuardianState(t, f) != before {
				t.Fatal("conflicting policy preflight changed state")
			}
			if err := HardenPrivacyExecutionRoles(t.Context(), f.root, "mycfc", f.roles); err != nil {
				t.Fatal(err)
			}
			assertACL()
			if err := operator.QueryRow(t.Context(), `SELECT changed FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,$7)`, args...).Scan(&changed); err != nil || changed {
				t.Fatalf("post hardening enable %v %v", changed, err)
			}
			recipientSchemaExec(t, operator, `SELECT * FROM guardian_ops.disable($1,'mycfc')`, h.actor)
			if err := operator.QueryRow(t.Context(), `SELECT state FROM guardian_ops.status('mycfc',$1,$2)`, image, EmbeddedMigrationDigest()).Scan(&state); err != nil || state != "DISABLED" {
				t.Fatalf("disabled status %s %v", state, err)
			}
		})
	}
}
