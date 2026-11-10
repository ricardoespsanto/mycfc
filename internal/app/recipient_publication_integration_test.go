//go:build integration

package app

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// This is the real fresh app.New/router/SCS/CSRF flow. Only unrelated config
// loading/AWS discovery are supplied; authorization, SQL and session store are real.
func TestFreshRecipientPublicationActualApplicationSessionCSRF(t *testing.T) {
	name := "mycfc-recipient-app-" + uuid.NewString()[:8]
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-P", "-e", "POSTGRES_DB=mycfc", "-e", "POSTGRES_USER=acceptance", "-e", "POSTGRES_PASSWORD="+password, "postgres:16.9").Run(); err != nil {
		t.Fatal("owned fresh app PostgreSQL startup failed")
	}
	t.Cleanup(func() {
		if err := exec.Command("docker", "stop", name).Run(); err != nil {
			t.Error("owned app PostgreSQL cleanup failed")
		}
	})
	out, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	port := line[strings.LastIndex(line, ":")+1:]
	raw := "postgres://acceptance:" + password + "@127.0.0.1:" + port + "/mycfc?sslmode=disable"
	var root *pgx.Conn
	for until := time.Now().Add(30 * time.Second); time.Now().Before(until); {
		root, err = pgx.Connect(t.Context(), raw)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("owned app PostgreSQL unavailable")
	}
	defer root.Close(context.Background())
	roles := db.RoleCredentials{AppUsername: "recipient_web", AppPassword: "synthetic-web", MigrationUsername: "recipient_migration", MigrationPassword: "synthetic-migration", MediaCleanupUsername: "recipient_media", MediaCleanupPassword: "synthetic-media", DataRetentionUsername: "recipient_retention", DataRetentionPassword: "synthetic-retention"}
	if err = db.BootstrapRoles(t.Context(), root, "mycfc", roles); err != nil {
		t.Fatal(err)
	}
	lowConfig := root.Config().Copy()
	lowConfig.User, lowConfig.Password = roles.MigrationUsername, roles.MigrationPassword
	low, err := pgx.ConnectConfig(t.Context(), lowConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer low.Close(context.Background())
	if err = db.ApplyBaselineAndHarden(t.Context(), low, "mycfc", roles); err != nil {
		t.Fatal(err)
	}
	if err = db.HardenPrivacyExecutionRoles(t.Context(), root, "mycfc", roles); err != nil {
		t.Fatal(err)
	}
	state, err := db.TrainingScheduleStatus(t.Context(), low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = low.Exec(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,5)`, state.DatabaseIncarnation, state.StateEpoch, uuid.New(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	u.User = url.UserPassword(roles.AppUsername, roles.AppPassword)
	actor, adult, hidden, season, group, plan, session := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	email := actor.String() + "@example.test"
	hash, err := bcrypt.GenerateFromPassword([]byte("Synthetic-test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	execute := func(sql string, args ...any) {
		t.Helper()
		if _, err := root.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	execute(`INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Authenticated coach',$2,now(),$3,'1980-01-01')`, actor, email, string(hash))
	execute(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Approved adult',$2,'hash','1980-01-01')`, adult, adult.String()+"@example.test")
	execute(`INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Private excluded dependent',true,'2012-01-01')`, hidden)
	execute(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'APP-FRESH','Fresh app season',CURRENT_DATE-365,CURRENT_DATE+400)`, season)
	var programme, otherProgramme uuid.UUID
	if err = root.QueryRow(t.Context(), `SELECT (SELECT id FROM programmes WHERE code='Leisure'),(SELECT id FROM programmes WHERE code='Competition')`).Scan(&programme, &otherProgramme); err != nil {
		t.Fatal(err)
	}
	execute(`INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$1),($1,'COACH',$3,$1)`, actor, programme, otherProgramme)
	execute(`UPDATE feature_flags SET mode='ENABLED',updated_by_id=$1 WHERE feature_key='structured_training_planning'`, actor)
	execute(`INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Actual application group',$2,$3)`, group, programme, actor)
	for _, subject := range []uuid.UUID{adult, hidden} {
		membership := uuid.New()
		execute(`INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+300)`, membership, subject, season, programme)
		execute(`INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, group, membership, actor)
		if subject == adult {
			joinFreshAppTrainingPlacement(t, u.String(), actor, subject, programme, season, group)
		}
	}
	execute(`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Actual native week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, plan, programme, group, season, actor)
	execute(`INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Actual native session',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, session, plan, actor)
	var application *Application
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { application.Server.Handler.ServeHTTP(w, r) }))
	defer server.Close()
	previousConfig, previousAWS := loadApplicationConfig, loadApplicationAWS
	t.Cleanup(func() { loadApplicationConfig, loadApplicationAWS = previousConfig, previousAWS })
	cfg := applicationStartupTestConfig()
	cfg.DatabaseURL = config.Secret(u.String())
	cfg.BaseURL = server.URL
	cfg.HTTPWriteTimeout = 30 * time.Second
	cfg.MaxRequestBytes = 1 << 20
	loadApplicationConfig = func(context.Context) (config.Config, error) { return cfg, nil }
	loadApplicationAWS = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1"}, nil
	}
	application, err = New(t.Context())
	if err != nil {
		t.Fatal("fresh actual app startup", err)
	}
	defer application.Close()
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	requestOrigin := server.URL
	request := func(method, path string, values url.Values) (int, string, http.Header) {
		t.Helper()
		var body io.Reader
		if values != nil {
			body = strings.NewReader(values.Encode())
		}
		r, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if values != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", requestOrigin)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(b), response.Header
	}
	field := func(body, name string) string {
		t.Helper()
		match := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `"\s+value="([^"]*)"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("missing native field %s", name)
		}
		return html.UnescapeString(match[1])
	}
	status, body, _ := request("GET", "/login", nil)
	if status != 200 {
		t.Fatal("actual login GET", status)
	}
	csrf := field(body, "gorilla.csrf.Token")
	status, _, _ = request("POST", "/login", url.Values{"identifier": {email}, "password": {"Synthetic-test-password"}, "gorilla.csrf.Token": {csrf}})
	if status != 303 {
		t.Fatal("actual session login", status)
	}
	planner := "/admin/treinos/estruturados?group_id=" + group.String() + "&week_id=" + plan.String()
	status, body, _ = request("GET", planner, nil)
	if status != 200 || !strings.Contains(body, "Rever destinatários") {
		t.Fatalf("actual focused week preview entry status%d", status)
	}
	csrf = field(body, "gorilla.csrf.Token")
	returnTo := field(body, "return_to")
	action := "/admin/treinos/estruturados/semanas/" + plan.String() + "/publicar"
	requestOrigin = "https://foreign.example.test"
	status, _, _ = request("POST", action, url.Values{"publication_intent": {"preview"}, "change_summary": {"Cross-origin proposal"}})
	requestOrigin = server.URL
	if status != 403 {
		t.Fatal("cross-origin CSRF request accepted", status)
	}
	status, body, headers := request("POST", action, url.Values{"publication_intent": {"preview"}, "change_summary": {"Primeira\r\nlinha\tcom consentimento"}, "gorilla.csrf.Token": {csrf}, "return_to": {returnTo}})
	if status != 200 || !strings.Contains(body, "Approved adult") || strings.Contains(body, "Private excluded dependent") || !strings.Contains(headers.Get("Cache-Control"), "no-store") || headers.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("private actual preview status%d", status)
	}
	confirmationForm := func(body string) string {
		t.Helper()
		for _, form := range regexp.MustCompile(`(?s)<form\b[^>]*>.*?</form>`).FindAllString(body, -1) {
			if strings.Contains(form, `name="publication_intent" value="confirm"`) {
				return form
			}
		}
		t.Fatal("native confirmation form missing")
		return ""
	}
	body = confirmationForm(body)
	confirm := url.Values{}
	for _, name := range []string{"publication_intent", "confirm", "change_summary", "source_updated_at", "review_token", "reviewed_latest_id", "reviewed_latest_revision", "return_group", "return_week", "return_session", "return_day", "gorilla.csrf.Token"} {
		confirm.Set(name, field(body, name))
	}
	confirm.Set("change_summary", strings.ReplaceAll(strings.ReplaceAll(confirm.Get("change_summary"), "\r\n", "\n"), "\n", "\r\n"))
	status, _, _ = request("POST", action, confirm)
	if status != 303 {
		t.Fatal("actual native confirm", status)
	}
	status, body, _ = request("POST", action, confirm)
	if status != 409 || strings.Contains(body, "Approved adult") || strings.Contains(body, `name="confirm"`) {
		t.Fatal("stale actual confirm retained audience", status)
	}
	status, body, _ = request("GET", planner, nil)
	if status != 200 || !strings.Contains(body, "Rever destinatários") || !strings.Contains(body, "sem alterações pendentes") {
		t.Fatal("current actual week lacks explicit no-op review", status)
	}
	csrf = field(body, "gorilla.csrf.Token")
	status, body, _ = request("POST", action, url.Values{"publication_intent": {"preview"}, "change_summary": {"Reviewed current exact set"}, "gorilla.csrf.Token": {csrf}})
	if status != 200 {
		t.Fatal("fresh current review", status)
	}
	body = confirmationForm(body)
	fresh := url.Values{}
	for _, name := range []string{"publication_intent", "confirm", "change_summary", "source_updated_at", "review_token", "reviewed_latest_id", "reviewed_latest_revision", "return_group", "return_week", "return_session", "return_day", "gorilla.csrf.Token"} {
		fresh.Set(name, field(body, name))
	}
	execute(`UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1 WHERE user_id=$1 AND programme_id=$2`, actor, programme)
	status, body, _ = request("POST", action, fresh)
	if status != 404 || strings.Contains(body, "Reviewed current exact set") || strings.Contains(body, "Approved adult") {
		t.Fatal("fresh scoped revocation exposed review", status)
	}
	var revisions, prescriptions int
	var summary string
	if err = root.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_plan_publications WHERE plan_id=$1),(SELECT count(*) FROM training_prescriptions p JOIN training_plan_publications r ON r.id=p.publication_id WHERE r.plan_id=$1),(SELECT change_summary FROM training_plan_publications WHERE plan_id=$1)`, plan).Scan(&revisions, &prescriptions, &summary); err != nil || revisions != 1 || prescriptions != 1 || summary != "Primeira\r\nlinha\tcom consentimento" {
		t.Fatalf("actual saved lineage count%d/%d summary%s err%v", revisions, prescriptions, summary, err)
	}
	var sessionRows int
	if err = root.QueryRow(t.Context(), `SELECT count(*) FROM sessions`).Scan(&sessionRows); err != nil || sessionRows == 0 {
		t.Fatal("actual PostgreSQL SCS session not persisted", err)
	}
}
