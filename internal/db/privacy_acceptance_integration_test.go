//go:build integration

package db

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func TestSyntheticAcceptanceIsolation(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	database := "synthetic_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec(ctx, "CREATE DATABASE "+quoteIdentifier(database)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE "+quoteIdentifier(database)+" WITH (FORCE)")
		_, _ = conn.Exec(ctx, "DROP ROLE mycfc_privacy_acceptance")
	}()
	cfg := conn.Config().Copy()
	cfg.Database = database
	fixtureConn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureConn.Close(ctx)
	if _, err = fixtureConn.Exec(ctx, baselineSchema); err != nil {
		t.Fatal(err)
	}
	tx, err := fixtureConn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := tx.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	reject := func(sql string, args ...any) {
		t.Helper()
		sp, e := tx.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = sp.Exec(ctx, sql, args...)
		_ = sp.Rollback(ctx)
		if e == nil {
			t.Fatal("unsafe operation accepted")
		}
	}
	admin, executor, real := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, executor, real} {
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Existing fixture',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	activatePrivacyDatabaseIntegrationFixture(t, ctx, tx, admin, executor)
	exec(`CREATE ROLE mycfc_privacy_acceptance NOLOGIN NOINHERIT`)
	exec(`GRANT USAGE ON SCHEMA privacy_protected TO mycfc_privacy_acceptance`)
	exec(`GRANT EXECUTE ON FUNCTION privacy_protected.acceptance_create(text,text,text),privacy_protected.acceptance_finish(uuid,bytea),privacy_protected.acceptance_observe(uuid,bytea) TO mycfc_privacy_acceptance`)
	hash, _ := bcrypt.GenerateFromPassword([]byte("internally-generated-test-secret"), bcrypt.MinCost)
	image := "sha256:" + strings.Repeat("7", 64)
	schema := hex.EncodeToString(bytes.Repeat([]byte{7}, 32))
	// Even the database owner cannot accidentally invoke fixture creation as an ordinary command.
	reject(`SELECT * FROM privacy_protected.acceptance_create($1,$2,$3)`, string(hash), image, schema)
	exec(`SET SESSION AUTHORIZATION mycfc_privacy_acceptance`)
	reject(`SELECT * FROM privacy_protected.acceptance_create($1,$2,$3)`, string(hash), "sha256:"+strings.Repeat("a", 64), schema)
	reject(`SELECT * FROM privacy_protected.acceptance_create(NULL,$1,$2)`, image, schema)
	var fixture, subject, reviewer, syntheticExecutor, worker uuid.UUID
	var proof, marker []byte
	err = tx.QueryRow(ctx, `SELECT * FROM privacy_protected.acceptance_create($1,$2,$3)`, string(hash), image, schema).Scan(&fixture, &subject, &reviewer, &syntheticExecutor, &worker, &proof, &marker)
	if err != nil {
		t.Fatal(err)
	}
	reject(`SELECT * FROM privacy_protected.acceptance_fixtures`)
	exec(`RESET SESSION AUTHORIZATION`)
	mac := hmac.New(sha256.New, proof)
	fmt.Fprintf(mac, "mycfc/privacy-acceptance-fixture/v1:%s:%s:%s:%s:%s:%s:%s", fixture, subject, reviewer, syntheticExecutor, worker, image, schema)
	if len(proof) != 32 || !hmac.Equal(mac.Sum(nil), marker) || subject == real {
		t.Fatal("fixture marker invalid")
	}
	q := dbgen.New(tx)
	sp, _ := tx.Begin(ctx)
	_, err = dbgen.New(sp).CreatePrivacyRequest(ctx, privacyParams(subject))
	_ = sp.Rollback(ctx)
	if err == nil {
		t.Fatal("synthetic request accepted without capability")
	}
	exec(`SELECT set_config('mycfc.acceptance_proof',$1,true)`, hex.EncodeToString(proof))
	syntheticRequest, err := q.CreatePrivacyRequest(ctx, privacyParams(subject))
	if err != nil {
		t.Fatal(err)
	}
	realRequest, err := q.CreatePrivacyRequest(ctx, privacyParams(real))
	if err != nil {
		t.Fatal(err)
	}
	reject(`UPDATE data_erasure_requests SET subject_user_id=$1 WHERE id=$2`, real, syntheticRequest.ID)
	reject(`UPDATE data_erasure_requests SET subject_user_id=$1 WHERE id=$2`, subject, realRequest.ID)
	// Caller cannot transplant the marker to a request on an existing person.
	reject(`UPDATE privacy_protected.acceptance_requests SET request_id=$1 WHERE fixture_id=$2`, realRequest.ID, fixture)
	for _, id := range []uuid.UUID{subject, reviewer, syntheticExecutor} {
		reject(`INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES('synthetic-session',decode('00','hex'),clock_timestamp()+interval '1 hour',$1,true)`, id)
		reject(`INSERT INTO password_reset_tokens(user_id,email,token_digest,expires_at) SELECT id,email,gen_random_bytes(32),clock_timestamp()+interval '1 hour' FROM users WHERE id=$1`, id)
		reject(`INSERT INTO email_verification_tokens(user_id,email,expires_at) SELECT id,email,clock_timestamp()+interval '1 hour' FROM users WHERE id=$1`, id)
	}
	var notice uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO email_outbox(message_type,privacy_request_id,privacy_requester_id,privacy_event_key,sealed_payload) VALUES('PRIVACY_PROCESSING_STARTED',$1,$2,gen_random_uuid(),gen_random_bytes(48)) RETURNING id`, syntheticRequest.ID, subject).Scan(&notice)
	if err != nil {
		t.Fatal(err)
	}
	reject(`UPDATE email_outbox SET privacy_request_id=$1,status='PENDING' WHERE id=$2`, realRequest.ID, notice)
	exec(`UPDATE email_outbox SET status='PENDING' WHERE id=$1`, notice)
	var status string
	var simulated int
	if err = tx.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM privacy_protected.acceptance_notice_simulations WHERE outbox_id=$1) FROM email_outbox WHERE id=$1`, notice).Scan(&status, &simulated); err != nil || status != "CANCELLED" || simulated != 1 {
		t.Fatalf("notice escaped simulation: %s %d %v", status, simulated, err)
	}
	var policy string
	if err = tx.QueryRow(ctx, `SELECT policy_version FROM privacy_request_activation WHERE singleton`).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	job := func(request uuid.UUID) uuid.UUID {
		t.Helper()
		execution, j := uuid.New(), uuid.New()
		exec(`INSERT INTO privacy_request_execution_plans(request_id,policy_version,executor_version,schema_version,plan,plan_sha256,created_at) VALUES($1,$2,'privacy-erasure-executor/v2','privacy-erasure-plan/v2','{}',decode(repeat('01',32),'hex'),clock_timestamp())`, request, policy)
		exec(`INSERT INTO privacy_erasure_executions(id,request_id,plan_sha256,executor_version,schema_version,request_version_at_start,started_by_ref,accepted_at,updated_at) VALUES($1,$2,decode(repeat('01',32),'hex'),'privacy-erasure-executor/v2','privacy-erasure-plan/v2',2,$3,clock_timestamp(),clock_timestamp())`, execution, request, executor)
		exec(`INSERT INTO privacy_erasure_category_jobs(id,execution_id,plan_entry_position,entry_sha256,category_key,purpose_code,next_attempt_at,created_at,updated_at) VALUES($1,$2,1,decode(repeat('01',32),'hex'),'identity-core','ACCOUNT_IDENTITY',clock_timestamp(),clock_timestamp(),clock_timestamp())`, j, execution)
		return j
	}
	syntheticJob, realJob := job(syntheticRequest.ID), job(realRequest.ID)
	// Commit only within this disposable database, then use independent
	// connections to prove scoped contention preserves one lease owner.
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	connections := make([]*pgx.Conn, 2)
	for i := range connections {
		connections[i], err = pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer connections[i].Close(ctx)
	}
	type claimResult struct {
		job uuid.UUID
		err error
	}
	results := make(chan claimResult, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, connection := range connections {
		workers.Add(1)
		go func(c *pgx.Conn) {
			defer workers.Done()
			<-start
			var j, l, a uuid.UUID
			e := c.QueryRow(ctx, `SELECT * FROM privacy_acceptance_claim(3600000,$1,$2)`, worker, proof).Scan(&j, &l, &a)
			results <- claimResult{j, e}
		}(connection)
	}
	close(start)
	workers.Wait()
	close(results)
	successes, empty := 0, 0
	for result := range results {
		if result.err == nil && result.job == syntheticJob {
			successes++
		} else if result.err == pgx.ErrNoRows {
			empty++
		} else {
			t.Fatalf("concurrent scoped claim: %v", result.err)
		}
	}
	if successes != 1 || empty != 1 {
		t.Fatalf("concurrent lease owners: success=%d empty=%d", successes, empty)
	}
	tx, err = fixtureConn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	reject(`SELECT * FROM privacy_acceptance_claim(1000,$1,$2)`, worker, bytes.Repeat([]byte{0}, 32))
	reject(`SELECT * FROM privacy_acceptance_claim(1000,$1,$2)`, uuid.New(), proof)
	var pending, leased, retryable, terminal, aged int64
	if err = tx.QueryRow(ctx, `SELECT * FROM privacy_worker_status()`).Scan(&pending, &leased, &retryable, &terminal, &aged); err != nil || pending != 1 || leased != 0 {
		t.Fatalf("synthetic work leaked into normal alarms: pending=%d leased=%d err=%v", pending, leased, err)
	}
	var claimed, lease, attempt uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT * FROM privacy_worker_claim(1000,$1)`, uuid.New()).Scan(&claimed, &lease, &attempt); err != nil || claimed != realJob {
		t.Fatalf("ordinary worker claimed synthetic job: %v", err)
	}
	if err = tx.QueryRow(ctx, `SELECT * FROM privacy_acceptance_claim(1000,$1,$2)`, worker, proof).Scan(&claimed, &lease, &attempt); err != pgx.ErrNoRows {
		t.Fatalf("unexpired fixture lease reclaimed: %v", err)
	}
	reject(`SELECT * FROM privacy_protected.acceptance_observe($1,$2)`, worker, proof)
	exec(`SELECT privacy_protected.acceptance_finish($1,$2)`, worker, proof)
	exec(`SELECT privacy_protected.acceptance_finish($1,$2)`, worker, proof)
	reject(`SELECT * FROM privacy_acceptance_claim(1000,$1,$2)`, worker, proof)
	var active, realActive bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=ANY($1) AND is_active),(SELECT is_active FROM users WHERE id=$2)`, []uuid.UUID{subject, reviewer, syntheticExecutor}, real).Scan(&active, &realActive); err != nil || active || !realActive {
		t.Fatalf("fixture cleanup affected real user: %v", err)
	}
}

func TestSyntheticAcceptanceCredentialLifecycle(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	config, err := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var exists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, PrivacyAcceptanceRole).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("isolated test requires absent acceptance role")
	}
	defer func() {
		_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename='mycfc_privacy_acceptance'`)
		_, _ = admin.Exec(ctx, `DROP OWNED BY mycfc_privacy_acceptance`)
		_, _ = admin.Exec(ctx, `DROP ROLE mycfc_privacy_acceptance`)
	}()
	password := strings.Repeat("initial-", 6)
	if err = ConfigurePrivacyAcceptanceRole(ctx, admin, "wrong_database", password, false); err == nil {
		t.Fatal("wrong database accepted")
	}
	if err = ConfigurePrivacyAcceptanceRole(ctx, admin, config.Database, password, false); err != nil {
		t.Fatal(err)
	}
	operator := config.Copy()
	operator.User = PrivacyAcceptanceRole
	operator.Password = password
	connection, err := pgx.ConnectConfig(ctx, operator)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	var publicUsage, tableRead, createCall, finishCall, observeCall bool
	err = admin.QueryRow(ctx, `SELECT has_schema_privilege($1,'public','USAGE'),has_table_privilege($1,'public.users','SELECT'),has_function_privilege($1,'privacy_protected.acceptance_create(text,text,text)','EXECUTE'),has_function_privilege($1,'privacy_protected.acceptance_finish(uuid,bytea)','EXECUTE'),has_function_privilege($1,'privacy_protected.acceptance_observe(uuid,bytea)','EXECUTE')`, PrivacyAcceptanceRole).Scan(&publicUsage, &tableRead, &createCall, &finishCall, &observeCall)
	if err != nil || publicUsage || tableRead || !createCall || !finishCall || !observeCall {
		t.Fatalf("operator privilege boundary violated: %v", err)
	}
	if _, err = connection.Exec(ctx, `CREATE TEMP TABLE acceptance_forbidden(id integer)`); err == nil {
		t.Fatal("operator can create temporary objects")
	}
	var allowed int
	if err = admin.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname<>'information_schema' AND has_schema_privilege($1,n.oid,'USAGE') AND has_function_privilege($1,p.oid,'EXECUTE')`, PrivacyAcceptanceRole).Scan(&allowed); err != nil || allowed != 3 {
		t.Fatalf("operator can execute unexpected application APIs: count=%d err=%v", allowed, err)
	}
	if _, err = connection.Exec(ctx, `SELECT * FROM public.users`); err == nil {
		t.Fatal("operator read people")
	}
	password2 := strings.Repeat("rotated-", 6)
	if err = ConfigurePrivacyAcceptanceRole(ctx, admin, config.Database, password2, false); err != nil {
		t.Fatal(err)
	}
	if err = connection.Ping(ctx); err == nil {
		t.Fatal("rotation retained old session")
	}
	old, err := pgx.ConnectConfig(ctx, operator)
	if err == nil {
		old.Close(ctx)
		t.Fatal("rotation retained old password")
	}
	operator.Password = password2
	rotated, err := pgx.ConnectConfig(ctx, operator)
	if err != nil {
		t.Fatal(err)
	}
	defer rotated.Close(ctx)
	if err = ConfigurePrivacyAcceptanceRole(ctx, admin, config.Database, "", true); err != nil {
		t.Fatal(err)
	}
	if err = rotated.Ping(ctx); err == nil {
		t.Fatal("revocation retained session")
	}
	revoked, err := pgx.ConnectConfig(ctx, operator)
	if err == nil {
		revoked.Close(ctx)
		t.Fatal("revoked login authenticated")
	}
	var login bool
	if err = admin.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, PrivacyAcceptanceRole).Scan(&login); err != nil || login {
		t.Fatalf("revocation left login enabled: %v", err)
	}
}

// Older migration tests reconstruct their own historical activation contracts.
// This reconstructs the predecessor before historical forward-migration tests.
func rewindSyntheticAcceptanceBinding(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	_, err := tx.Exec(ctx, `DO $$DECLARE d text;BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v17_check') THEN
  ALTER TABLE privacy_activation_authenticated_artifacts DISABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  DELETE FROM privacy_activation_authenticated_artifacts;
  ALTER TABLE privacy_activation_authenticated_artifacts ENABLE TRIGGER privacy_activation_authenticated_artifacts_immutable;
  SELECT pg_get_constraintdef(oid) INTO d FROM pg_constraint WHERE conrelid='privacy_activation_authenticated_artifacts'::regclass AND conname='privacy_activation_authenticated_artifacts_v17_check';
  IF strpos(d,'202609170001_privacy_synthetic_acceptance')=0 THEN RAISE EXCEPTION 'synthetic rewind mismatch'; END IF;
  d:=replace(d,', ''202609170001_privacy_synthetic_acceptance''::text','');
  ALTER TABLE privacy_activation_authenticated_artifacts DROP CONSTRAINT privacy_activation_authenticated_artifacts_v17_check;
  EXECUTE 'ALTER TABLE privacy_activation_authenticated_artifacts ADD CONSTRAINT privacy_activation_authenticated_artifacts_v16_check '||d;
  EXECUTE replace(pg_get_functiondef('privacy_activation_record_authenticated_evidence(uuid,text,bytea,text,timestamptz,timestamptz,jsonb)'::regprocedure),'202609170001_privacy_synthetic_acceptance','202609130001_event_results_links');
  EXECUTE replace(pg_get_functiondef('privacy_activation_authenticated_set_digest(text,uuid[])'::regprocedure),'202609170001_privacy_synthetic_acceptance','202609130001_event_results_links');
  EXECUTE replace(pg_get_functiondef('privacy_worker_claim_inner_013(bigint,uuid)'::regprocedure),'NOT EXISTS(SELECT 1 FROM public.privacy_erasure_executions execution JOIN privacy_protected.acceptance_requests synthetic ON synthetic.request_id=execution.request_id WHERE execution.id=job.execution_id) AND ','');
  EXECUTE replace(pg_get_functiondef('privacy_completion_list_pending_inner_013(uuid,integer)'::regprocedure),'NOT EXISTS(SELECT 1 FROM privacy_protected.acceptance_requests fixture WHERE fixture.request_id=execution.request_id) AND ','');
  EXECUTE replace(pg_get_functiondef('privacy_worker_status()'::regprocedure),' WHERE NOT EXISTS(SELECT 1 FROM privacy_erasure_executions execution JOIN privacy_protected.acceptance_requests synthetic ON synthetic.request_id=execution.request_id WHERE execution.id=job.execution_id)','');
  DROP FUNCTION privacy_acceptance_bind_request() CASCADE;
  DROP FUNCTION privacy_acceptance_reject_auth() CASCADE;
  DROP FUNCTION privacy_acceptance_simulate_notice() CASCADE;
  DROP FUNCTION privacy_protected.acceptance_create(text,text,text),privacy_protected.acceptance_finish(uuid,bytea),privacy_protected.acceptance_observe(uuid,bytea);
  DROP FUNCTION privacy_acceptance_claim(bigint,uuid,bytea),privacy_acceptance_claim_inner(bigint,uuid,uuid),privacy_acceptance_authorized(uuid,bytea);
  DROP TABLE privacy_protected.acceptance_notice_simulations,privacy_protected.acceptance_requests,privacy_protected.acceptance_finished,privacy_protected.acceptance_fixtures;

 END IF;
 END$$`)
	if err != nil {
		t.Fatal(err)
	}
}
