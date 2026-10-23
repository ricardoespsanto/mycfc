//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
}
