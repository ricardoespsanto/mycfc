//go:build integration

package main

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestActivationDisableIncidentDrillReengagesSwitchAndBlocksReadiness(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if admin.Config().Database != "mycfc_test" {
		t.Fatalf("privacy activation disable drill requires mycfc_test, got %q", admin.Config().Database)
	}

	var roleExists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, privacyActivationDisableRole).Scan(&roleExists); err != nil {
		t.Fatal(err)
	}
	if roleExists {
		t.Skip("disposable privacy activation disable role already exists")
	}
	roleIdentifier := pgx.Identifier{privacyActivationDisableRole}.Sanitize()
	databaseIdentifier := pgx.Identifier{admin.Config().Database}.Sanitize()
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE ROLE `+roleIdentifier+` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '`+password+`'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, cleanupErr := admin.Exec(cleanupCtx, `DROP OWNED BY `+roleIdentifier); cleanupErr != nil {
			t.Errorf("drop disable role privileges: %v", cleanupErr)
			return
		}
		if _, cleanupErr := admin.Exec(cleanupCtx, `DROP ROLE `+roleIdentifier); cleanupErr != nil {
			t.Errorf("drop disable role: %v", cleanupErr)
		}
	})
	for _, statement := range []string{
		`GRANT CONNECT ON DATABASE ` + databaseIdentifier + ` TO ` + roleIdentifier,
		`REVOKE ALL ON SCHEMA public FROM ` + roleIdentifier,
		`GRANT USAGE ON SCHEMA privacy_disable TO ` + roleIdentifier,
		`REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM ` + roleIdentifier,
		`GRANT EXECUTE ON FUNCTION privacy_disable.privacy_activation_disable(uuid,text) TO ` + roleIdentifier,
	} {
		if _, err = admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var canDisable, canUsePublicSchema, canReadBroker, canActivateBroker, canReadActivationTable bool
	if err = admin.QueryRow(ctx, `SELECT
		has_function_privilege($1,'privacy_disable.privacy_activation_disable(uuid,text)','EXECUTE'),
		has_schema_privilege($1,'public','USAGE'),
		has_function_privilege($1,'privacy_activation_broker_material(text)','EXECUTE'),
		has_function_privilege($1,'privacy_activation_broker_activate(uuid,text,uuid[],bytea,bytea,uuid,uuid,text,text,bytea,bytea,bytea,bytea,jsonb,jsonb,timestamptz,timestamptz,timestamptz,timestamptz)','EXECUTE'),
		has_table_privilege($1,'privacy_request_activation','SELECT')`, privacyActivationDisableRole).
		Scan(&canDisable, &canUsePublicSchema, &canReadBroker, &canActivateBroker, &canReadActivationTable); err != nil {
		t.Fatal(err)
	}
	if !canDisable || canUsePublicSchema || canReadBroker || canActivateBroker || canReadActivationTable {
		t.Fatalf("disable privilege boundary disable=%t public_schema=%t material=%t activate=%t activation_table=%t", canDisable, canUsePublicSchema, canReadBroker, canActivateBroker, canReadActivationTable)
	}

	var beforeVersion int64
	if err = admin.QueryRow(ctx, `UPDATE privacy_worker_kill_switch
		SET engaged=false,activation_approval_id=NULL,version=version+1,changed_at=clock_timestamp()
		WHERE singleton RETURNING version`).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `INSERT INTO privacy_worker_kill_switch_events(version,engaged,occurred_at) VALUES($1,false,clock_timestamp())`, beforeVersion); err != nil {
		t.Fatal(err)
	}

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(privacyActivationDisableRole, password)
	actor := uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth)
		VALUES($1,'Activation disable operator',$2,'not-a-login-hash','1990-01-01')`, actor, "activation-disable-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"PRIVACY_ACTIVATION_DISABLE_DATABASE_URL":      parsed.String(),
		"PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE": admin.Config().Database,
		"PRIVACY_ACTIVATION_DISABLE_ACTOR_REF":         actor.String(),
	}
	restricted, err := pgx.Connect(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	var unrelatedReady bool
	if unrelatedErr := restricted.QueryRow(ctx, `SELECT public.privacy_activation_ready($1)`, "irrelevant-policy").Scan(&unrelatedReady); unrelatedErr == nil {
		_ = restricted.Close(ctx)
		t.Fatal("disable role executed an unrelated public-schema function")
	}
	if err = restricted.Close(ctx); err != nil {
		t.Fatal(err)
	}
	withDisableRuntime(t, 0, nil)
	var output bytes.Buffer
	if err = runDisable(ctx, func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "privacy_activation_disabled kill_switch=engaged readiness=blocked\n" {
		t.Fatalf("disable outcome=%q", output.String())
	}
	var engaged, ready bool
	var afterVersion int64
	if err = admin.QueryRow(ctx, `SELECT switch_row.engaged,switch_row.version,privacy_worker_activation_ready()
		FROM privacy_worker_kill_switch switch_row WHERE switch_row.singleton`).Scan(&engaged, &afterVersion, &ready); err != nil {
		t.Fatal(err)
	}
	if !engaged || ready || afterVersion != beforeVersion+1 {
		t.Fatalf("post-disable engaged=%t ready=%t version=%d want_version=%d", engaged, ready, afterVersion, beforeVersion+1)
	}

	output.Reset()
	if err = runDisable(ctx, func(name string) string { return env[name] }, &output); err != nil {
		t.Fatal(err)
	}
	var repeatedVersion int64
	if err = admin.QueryRow(ctx, `SELECT version FROM privacy_worker_kill_switch WHERE singleton`).Scan(&repeatedVersion); err != nil {
		t.Fatal(err)
	}
	if repeatedVersion != afterVersion+1 {
		t.Fatalf("repeated disable did not advance the incident fence from %d to %d", afterVersion, repeatedVersion)
	}
	var engagedEvents int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM privacy_worker_kill_switch_events WHERE version IN($1,$2) AND engaged`, afterVersion, repeatedVersion).Scan(&engagedEvents); err != nil {
		t.Fatal(err)
	}
	if engagedEvents != 2 {
		t.Fatalf("two disable incidents recorded %d engaged events", engagedEvents)
	}
}
