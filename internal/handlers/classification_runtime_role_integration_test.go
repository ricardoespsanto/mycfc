//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the real freshly hardened web login, not a privileged test connection.
func TestClassificationGETWithHardenedWebRole(t *testing.T) {
	ctx := context.Background()
	control, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close(context.Background()) })
	database := "classification_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	web := "web_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	migration := "migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := "fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = control.Exec(ctx, `CREATE DATABASE `+database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = control.Exec(context.Background(), `DROP DATABASE `+database+` WITH (FORCE)`)
		_, _ = control.Exec(context.Background(), `DROP ROLE IF EXISTS `+web)
		_, _ = control.Exec(context.Background(), `DROP ROLE IF EXISTS `+migration)
	})
	config := control.Config().Copy()
	config.Database = database
	admin, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	credentials := db.RoleCredentials{AppUsername: web, AppPassword: password, MigrationUsername: migration, MigrationPassword: password}
	if err = db.BootstrapRoles(ctx, admin, database, credentials); err != nil {
		t.Fatal(err)
	}
	// The migration owner creates SECURITY DEFINER functions; bootstrap then
	// grants only the explicitly allowed callable routines to the web login.
	migrationConfig := config.Copy()
	migrationConfig.User, migrationConfig.Password = migration, password
	migrator, err := pgx.ConnectConfig(ctx, migrationConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer migrator.Close(ctx)
	if err = db.ApplyBaselineAndHarden(ctx, migrator, database, credentials); err != nil {
		t.Fatal(err)
	}
	var freshGrant bool
	if err = admin.QueryRow(ctx, `SELECT has_function_privilege($1,'public.guardian_authority_is_administrator(uuid)','EXECUTE')`, web).Scan(&freshGrant); err != nil || !freshGrant {
		t.Fatalf("fresh baseline/hardening web grant=%t err=%v", freshGrant, err)
	}
	// Simulate the already deployed hardened ACL before a forward harden-db
	// invocation. The prior explicit revoke wins over any default grants.
	if _, err = admin.Exec(ctx, `REVOKE EXECUTE ON FUNCTION guardian_authority_is_administrator(uuid) FROM `+web); err != nil {
		t.Fatal(err)
	}
	webConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	webConfig.ConnConfig.Database = database
	webConfig.ConnConfig.User, webConfig.ConnConfig.Password = web, password
	pool, err := pgxpool.NewWithConfig(ctx, webConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var denied bool
	if err = admin.QueryRow(ctx, `SELECT has_function_privilege($1,'public.guardian_authority_is_administrator(uuid)','EXECUTE')`, web).Scan(&denied); err != nil || denied {
		t.Fatalf("prior hardened web ACL executable=%t err=%v", denied, err)
	}
	var ignored bool
	var pgErr *pgconn.PgError
	if err = pool.QueryRow(ctx, `SELECT guardian_authority_is_administrator($1)`, uuid.New()).Scan(&ignored); !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("prior hardened web call should be denied, got %v", err)
	}
	for range 2 {
		if err = db.HardenPrivacyExecutionRoles(ctx, admin, database, credentials); err != nil {
			t.Fatal(err)
		}
	}
	var executable, privateRead, privateOtherRead, mutationExecutable bool
	var functionOwner, searchPath string
	var securityDefiner bool
	if err = admin.QueryRow(ctx, `SELECT has_function_privilege($1,'public.guardian_authority_is_administrator(uuid)','EXECUTE'),
		has_table_privilege($1,'public.guardian_authority_renewal_requests','SELECT'),
		has_table_privilege($1,'public.guardian_authority_renewal_reminders','SELECT'),
		has_function_privilege($1,'public.guardian_authority_adopt_policy(uuid,text,text[],text[],integer,integer)','EXECUTE'),
		pg_get_userbyid(proowner), prosecdef, proconfig::text
		FROM pg_proc WHERE oid='public.guardian_authority_is_administrator(uuid)'::regprocedure`, web).Scan(&executable, &privateRead, &privateOtherRead, &mutationExecutable, &functionOwner, &securityDefiner, &searchPath); err != nil {
		t.Fatal(err)
	}
	if !executable || privateRead || privateOtherRead || mutationExecutable || !securityDefiner || functionOwner != migration || !strings.Contains(searchPath, "search_path=pg_catalog, public") {
		t.Fatalf("web boundary: admin check executable=%t, restricted guardian reads=%t/%t, mutation=%t, definer=%t, expected owner=%t, fixed path=%t", executable, privateRead, privateOtherRead, mutationExecutable, securityDefiner, functionOwner == migration, strings.Contains(searchPath, "search_path=pg_catalog, public"))
	}

	adminID, coachID, outsiderID, subjectID, season := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{adminID, coachID, outsiderID, subjectID} {
		if _, err = migrator.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic subject',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = migrator.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	var programme uuid.UUID
	if err = migrator.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	var today time.Time
	if err = migrator.QueryRow(ctx, `SELECT (now() AT TIME ZONE 'Europe/Lisbon')::date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current) VALUES($1,$2,'Synthetic season',$3,$4,true)`, season, "RT_"+uuid.NewString()[:8], today.AddDate(0, 0, -10), today.AddDate(0, 0, 60)); err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4)`, subjectID, season, programme, today.AddDate(0, 0, -5)); err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Exec(ctx, `INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id) VALUES($1,$2,'COACH',$3,$4)`, uuid.New(), coachID, programme, adminID); err != nil {
		t.Fatal(err)
	}
	var currentRole string
	if err = pool.QueryRow(ctx, `SELECT current_user`).Scan(&currentRole); err != nil || currentRole != web {
		t.Fatalf("web login mismatch: role=%q err=%v", currentRole, err)
	}
	var isAdmin, isCoachAdmin bool
	if err = pool.QueryRow(ctx, `SELECT guardian_authority_is_administrator($1),guardian_authority_is_administrator($2)`, adminID, coachID).Scan(&isAdmin, &isCoachAdmin); err != nil || !isAdmin || isCoachAdmin {
		t.Fatalf("administrator check admin=%t coach=%t err=%v", isAdmin, isCoachAdmin, err)
	}
	// The SQL function is a bounded boolean oracle; private guardian relations
	// must remain unreadable even when callers use the freshly hardened role.
	for _, relation := range []string{"guardian_authority_renewal_requests", "guardian_authority_renewal_reminders"} {
		var count int
		queryErr := pool.QueryRow(ctx, `SELECT count(*) FROM public.`+relation).Scan(&count)
		var pgErr *pgconn.PgError
		if !errors.As(queryErr, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("guardian relation %s SELECT denied: %v", relation, queryErr)
		}
	}
	store := PostgresClassificationStore{Pool: pool}
	for _, tc := range []struct {
		name  string
		actor uuid.UUID
		want  int
	}{
		{"admin", adminID, http.StatusOK},
		{"scoped coach", coachID, http.StatusOK},
		{"unscoped adult", outsiderID, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/equipa/classificacao/"+subjectID.String(), nil)
			r.SetPathValue("id", subjectID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: tc.actor}))
			w := httptest.NewRecorder()
			(Classification{Store: store}).Get(w, r)
			if w.Code != tc.want {
				t.Fatalf("classification GET status=%d want=%d", w.Code, tc.want)
			}
			if tc.want == http.StatusOK && !strings.Contains(w.Body.String(), "Synthetic subject") {
				t.Fatal("authorized classification GET did not show synthetic subject")
			}
			if tc.want == http.StatusNotFound && strings.Contains(w.Body.String(), "Synthetic subject") {
				t.Fatal("unscoped GET disclosed subject")
			}
		})
	}
	firstMember := uuid.New()
	if _, err = migrator.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic first member',$2,'hash','1980-01-01')`, firstMember, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var baselineDefinition string
	if err = migrator.QueryRow(ctx, `SELECT pg_get_functiondef('public.require_active_privacy_attachment_subject()'::regprocedure)`).Scan(&baselineDefinition); err != nil {
		t.Fatal(err)
	}
	// Reproduce the deployed v2.0.6 trigger's invoker privilege boundary and
	// apply only the pending forward migration on the disposable database.
	if _, err = migrator.Exec(ctx, `ALTER FUNCTION require_active_privacy_attachment_subject() SECURITY INVOKER`); err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Exec(ctx, `DELETE FROM mycfc_meta.schema_migrations WHERE version='202610010003_privacy_attachment_trigger_runtime'`); err != nil {
		t.Fatal(err)
	}
	priorErr := store.Write(ctx, ClassificationWrite{ActorID: adminID, MemberID: firstMember, SeasonID: season, ProgrammeID: programme, StartsOn: today})
	var priorState *pgconn.PgError
	if !errors.As(priorErr, &priorState) || priorState.Code != "42501" || !strings.Contains(priorState.Message, "privacy_erasure_category_jobs") {
		t.Fatalf("prior membership trigger failure type=%T SQLSTATE=%s", priorErr, func() string {
			if priorState == nil {
				return ""
			}
			return priorState.Code
		}())
	}
	if err = db.ApplyBaselineAndHarden(ctx, migrator, database, credentials); err != nil {
		t.Fatalf("forward trigger migration: %v", err)
	}
	var migrated bool
	if err = migrator.QueryRow(ctx, `SELECT prosecdef FROM pg_proc WHERE oid='public.require_active_privacy_attachment_subject()'::regprocedure`).Scan(&migrated); err != nil || !migrated {
		t.Fatalf("forward trigger definer=%t err=%v", migrated, err)
	}
	var forwardDefinition string
	if err = migrator.QueryRow(ctx, `SELECT pg_get_functiondef('public.require_active_privacy_attachment_subject()'::regprocedure)`).Scan(&forwardDefinition); err != nil || forwardDefinition != baselineDefinition {
		t.Fatalf("fresh baseline and forward trigger definitions differ: query error=%v", err)
	}
	form := url.Values{"scope": {season.String() + ":" + programme.String()}, "starts_on": {today.Format("2006-01-02")}}
	request := func(actor, member uuid.UUID, values url.Values) *http.Request {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/equipa/classificacao/"+member.String(), strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", member.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor}))
	}
	preview := httptest.NewRecorder()
	(Classification{Store: store}).Post(preview, request(adminID, firstMember, form))
	if preview.Code != http.StatusOK {
		t.Fatalf("first assignment preview status=%d", preview.Code)
	}
	form.Set("preview_token", extractPreviewToken(t, preview.Body.String()))
	form.Set("confirm", "yes")
	confirmed := httptest.NewRecorder()
	(Classification{Store: store}).Post(confirmed, request(adminID, firstMember, form))
	if confirmed.Code != http.StatusOK {
		// Test-only diagnosis: report SQLSTATE rather than a private SQL message.
		writeErr := store.Write(ctx, ClassificationWrite{ActorID: adminID, MemberID: firstMember, SeasonID: season, ProgrammeID: programme, StartsOn: today})
		var postgresErr *pgconn.PgError
		if errors.As(writeErr, &postgresErr) {
			t.Fatalf("first assignment confirm status=%d, writer SQLSTATE=%s, message=%s", confirmed.Code, postgresErr.Code, postgresErr.Message)
		}
		t.Fatalf("first assignment confirm status=%d, writer error type=%T", confirmed.Code, writeErr)
	}
	var count int
	if err = migrator.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, firstMember).Scan(&count); err != nil || count != 1 {
		t.Fatalf("first assignment rows=%d err=%v", count, err)
	}
	var originalStart time.Time
	if err = migrator.QueryRow(ctx, `SELECT starts_on FROM user_memberships WHERE user_id=$1`, firstMember).Scan(&originalStart); err != nil {
		t.Fatal(err)
	}
	// Duplicate/overlapping ordinary writes cannot replace the historical row.
	overlapErr := store.Write(ctx, ClassificationWrite{ActorID: adminID, MemberID: firstMember, SeasonID: season, ProgrammeID: programme, StartsOn: today})
	if overlapErr == nil {
		t.Fatal("overlapping assignment succeeded")
	}
	var overlapState *pgconn.PgError
	if !errors.As(overlapErr, &overlapState) || (overlapState.Code != "23P01" && overlapState.Code != "23505") {
		t.Fatalf("overlap error type=%T SQLSTATE=%s", overlapErr, func() string {
			if overlapState == nil {
				return ""
			}
			return overlapState.Code
		}())
	}
	var historyCount int
	var historicalStart time.Time
	if err = migrator.QueryRow(ctx, `SELECT count(*),min(starts_on) FROM user_memberships WHERE user_id=$1`, firstMember).Scan(&historyCount, &historicalStart); err != nil || historyCount != 1 || !historicalStart.Equal(originalStart) {
		t.Fatalf("overlap changed membership history count=%d start_unchanged=%t err=%v", historyCount, historicalStart.Equal(originalStart), err)
	}
	newMember := func() uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, insertErr := migrator.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic first member',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); insertErr != nil {
			t.Fatal(insertErr)
		}
		return id
	}
	post := func(actor, member uuid.UUID, values url.Values) (int, string) {
		t.Helper()
		response := httptest.NewRecorder()
		(Classification{Store: store}).Post(response, request(actor, member, values))
		return response.Code, response.Body.String()
	}
	coachMember := newMember()
	coachForm := url.Values{"scope": {season.String() + ":" + programme.String()}, "starts_on": {today.Format("2006-01-02")}}
	status, body := post(coachID, coachMember, coachForm)
	if status != http.StatusOK {
		t.Fatalf("coach first assignment preview status=%d", status)
	}
	coachForm.Set("preview_token", extractPreviewToken(t, body))
	coachForm.Set("confirm", "yes")
	status, _ = post(coachID, coachMember, coachForm)
	if status != http.StatusOK {
		t.Fatalf("coach first assignment confirm status=%d", status)
	}
	unauthorizedMember := newMember()
	unauthorizedForm := url.Values{"scope": {season.String() + ":" + programme.String()}, "starts_on": {today.Format("2006-01-02")}}
	status, _ = post(outsiderID, unauthorizedMember, unauthorizedForm)
	if status != http.StatusNotFound {
		t.Fatalf("unscoped actor assignment status=%d", status)
	}
	staleMember := newMember()
	staleForm := url.Values{"scope": {season.String() + ":" + programme.String()}, "starts_on": {today.Format("2006-01-02")}}
	status, body = post(adminID, staleMember, staleForm)
	if status != http.StatusOK {
		t.Fatalf("stale preview setup status=%d", status)
	}
	staleForm.Set("preview_token", extractPreviewToken(t, body))
	staleForm.Set("confirm", "yes")
	if err = store.Write(ctx, ClassificationWrite{ActorID: coachID, MemberID: staleMember, SeasonID: season, ProgrammeID: programme, StartsOn: today}); err != nil {
		t.Fatalf("concurrent coach assignment: %v", err)
	}
	status, _ = post(adminID, staleMember, staleForm)
	if status != http.StatusConflict {
		t.Fatalf("stale preview confirm status=%d", status)
	}
	for _, tc := range []struct {
		member uuid.UUID
		want   int
	}{{coachMember, 1}, {unauthorizedMember, 0}, {staleMember, 1}} {
		if err = migrator.QueryRow(ctx, `SELECT count(*) FROM user_memberships WHERE user_id=$1`, tc.member).Scan(&count); err != nil || count != tc.want {
			t.Fatalf("membership rows=%d want=%d err=%v", count, tc.want, err)
		}
	}
	// An app login may set an arbitrary custom GUC but cannot bypass the
	// definer's active-subject check or read private erasure relations.
	inactive := newMember()
	if _, err = migrator.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, inactive); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL mycfc.privacy_erasure_operation='MEMBERSHIP_HISTORY_ANONYMIZE'`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,$4)`, inactive, season, programme, today)
	var spoofErr *pgconn.PgError
	if !errors.As(err, &spoofErr) || spoofErr.Code != "23514" {
		t.Fatalf("spoofed GUC bypass accepted or wrong error type=%T", err)
	}
}
