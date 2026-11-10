//go:build integration

package app

import (
	"context"
	"encoding/json"
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
func TestFreshWaterCreateActualApplicationSessionCSRF(t *testing.T) {
	name := "mycfc-water-app-" + uuid.NewString()[:8]
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
	if _, err = low.Exec(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,3)`, state.DatabaseIncarnation, state.StateEpoch, uuid.New(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	state, err = db.TrainingScheduleStatus(t.Context(), low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, state.DatabaseIncarnation, state.StateEpoch); err != nil {
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
	}
	execute(`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Actual native week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date-7,$5)`, plan, programme, group, season, actor)
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

	var day string
	if err = root.QueryRow(t.Context(), `SELECT (week_start+2)::text FROM training_plans WHERE id=$1`, plan).Scan(&day); err != nil {
		t.Fatal(err)
	}
	planner := "/admin/treinos/estruturados?group_id=" + group.String() + "&week_id=" + plan.String() + "&day=" + day
	status, body, _ = request("GET", planner, nil)
	base := "/admin/treinos/estruturados/semanas/" + plan.String() + "/treinos/criar"
	if status != 200 || !strings.Contains(body, base+"?day="+day) {
		t.Fatal("historical selected-day water entry unavailable", status)
	}
	status, body, headers := request("GET", base+"?day="+day, nil)
	if status != 200 || !strings.Contains(headers.Get("Cache-Control"), "no-store") || headers.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("private actual create GET", status)
	}
	draft := focusedNativeEditor(t, body)
	if draft.Get("schedule.start_time") != "" || draft.Get("schedule.end_time") != "" || draft.Get("schedule.date") != day {
		t.Fatal("create guessed clock/date")
	}
	draft.Set("title", "Treino histórico de água")
	draft.Set("schedule.start_time", "16:00")
	draft.Set("activities[a_1].phases[p_2].instructions", "Remar com atenção à técnica.\r\n\tRegressar em conjunto.")
	draft.Set("command", "phase_remove:a_1:p_1")
	status, body, _ = request("POST", base+"/rascunho", draft)
	if status != 200 {
		t.Fatal("native remove", status)
	}
	draft = focusedNativeEditor(t, body)
	draft.Set("command", "restore:"+draft.Get("removed_order"))
	status, body, _ = request("POST", base+"/rascunho", draft)
	if status != 200 {
		t.Fatal("native restore", status)
	}
	if regexp.MustCompile(`<select\b[^>]*name="schedule\.(start|end)_occurrence"`).MatchString(body) {
		t.Fatal("ordinary draft refresh requires DST occurrence")
	}
	draft = focusedNativeEditor(t, body)
	if draft.Get("activities[a_1].phases[p_2].instructions") != "Remar com atenção à técnica.\r\n\tRegressar em conjunto." {
		t.Fatal("native command lost own text")
	}
	bad := url.Values{}
	for k, v := range draft {
		bad[k] = append([]string(nil), v...)
	}
	bad.Set("title", "x")
	bad.Set("command", "preview")
	status, body, _ = request("POST", base+"/prever", bad)
	if status != 422 || !strings.Contains(body, `href="#workout-title"`) || focusedNativeEditor(t, body).Get("title") != "x" {
		t.Fatal("title validation is not linked/retained", status)
	}
	draft.Set("command", "preview")
	requestOrigin = "https://foreign.example.test"
	status, _, _ = request("POST", base+"/prever", draft)
	requestOrigin = server.URL
	if status != 403 {
		t.Fatal("focused CSRF accepted foreign origin", status)
	}
	status, body, _ = request("POST", base+"/prever", draft)
	if status != 200 || !strings.Contains(body, "Remar com atenção à técnica.\r\n\tRegressar em conjunto.") {
		t.Fatalf("native canonical preview %d: %.1000s", status, body)
	}
	confirm := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
	var proposal struct {
		Proposal struct {
			SessionID uuid.UUID `json:"session_id"`
		} `json:"proposal"`
	}
	if err = json.Unmarshal([]byte(confirm.Get("proposal")), &proposal); err != nil || proposal.Proposal.SessionID == uuid.Nil {
		t.Fatal("allocated signed ID", err)
	}
	var count int
	if err = root.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE plan_id=$1`, plan).Scan(&count); err != nil || count != 1 {
		t.Fatal("preview mutated sessions", count, err)
	}
	// A second genuine task changes the week while the first review is open.
	status, body, _ = request("GET", base+"?day="+day, nil)
	second := focusedNativeEditor(t, body)
	second.Set("title", "Outro treino da mesma semana")
	second.Set("schedule.start_time", "18:00")
	second.Set("schedule.end_date", day)
	second.Set("schedule.end_time", "18:45")
	second.Set("description", "Instruções de outro treino.")
	second.Set("command", "preview")
	status, body, _ = request("POST", base+"/prever", second)
	if status != 200 {
		t.Fatal("second preview", status)
	}
	secondConfirm := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
	status, _, _ = request("POST", base+"/confirmar", secondConfirm)
	if status != 303 {
		t.Fatal("second explicit create", status)
	}
	status, body, _ = request("POST", base+"/confirmar", confirm)
	if status != 409 || !strings.Contains(body, "Treino histórico de água") || !strings.Contains(body, "Remar com atenção à técnica.\r\n\tRegressar em conjunto.") || field(body, "prior_proposal") != confirm.Get("proposal") {
		t.Fatal("stale confirm lost raw/original proof", status)
	}
	if regexp.MustCompile(`<select\b[^>]*name="schedule\.(start|end)_occurrence"`).MatchString(body) {
		t.Fatal("ordinary stale editback requires DST occurrence")
	}
	// Re-preview is deliberate and refreshes the version, not the create identity.
	repreview := focusedNativeEditor(t, body)
	repreview.Set("command", "preview")
	status, body, _ = request("POST", base+"/prever", repreview)
	if status != 200 {
		t.Fatal("deliberate re-preview", status)
	}
	refreshed := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
	var freshProposal = proposal
	if err = json.Unmarshal([]byte(refreshed.Get("proposal")), &freshProposal); err != nil || freshProposal.Proposal.SessionID != proposal.Proposal.SessionID {
		t.Fatal("re-preview changed allocated identity", err)
	}
	status, _, headers = request("POST", base+"/confirmar", refreshed)
	if status != 303 || !strings.Contains(headers.Get("Location"), "session_id="+proposal.Proposal.SessionID.String()) || !strings.Contains(headers.Get("Location"), "day="+day) {
		t.Fatal("create exact destination", status, headers.Get("Location"))
	}
	status, _, headers = request("POST", base+"/confirmar", refreshed)
	if status != 303 || !strings.Contains(headers.Get("Location"), proposal.Proposal.SessionID.String()) {
		t.Fatal("idempotent native replay", status)
	}
	// Edit-back of a saved proof cannot allocate a duplicate or overwrite it.
	repreview.Set("prior_proposal", refreshed.Get("proposal"))
	repreview.Set("prior_review_token", refreshed.Get("review_token"))
	repreview.Set("title", "Título alterado depois de guardar")
	status, body, _ = request("POST", base+"/prever", repreview)
	if status != 200 {
		t.Fatal("saved proof re-preview", status)
	}
	changed := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
	status, _, _ = request("POST", base+"/confirmar", changed)
	if status != 409 {
		t.Fatal("saved identity overwritten", status)
	}
	var nativeInstructions string
	if err = root.QueryRow(t.Context(), `SELECT b.instructions FROM training_segment_blocks b JOIN training_session_segments s ON s.id=b.segment_id WHERE s.session_id=$1`, proposal.Proposal.SessionID).Scan(&nativeInstructions); err != nil || nativeInstructions != "Remar com atenção à técnica.\r\n\tRegressar em conjunto." {
		t.Fatal("native multiline/tab changed", err)
	}
	var actualEnd bool
	if err = root.QueryRow(t.Context(), `SELECT ends_at=(scheduled_on::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '18 hours 45 minutes' FROM training_sessions WHERE title='Outro treino da mesma semana' AND plan_id=$1`, plan).Scan(&actualEnd); err != nil || !actualEnd {
		t.Fatal("explicit optional end changed", err)
	}
	var title, precision string
	var noEnd bool
	if err = root.QueryRow(t.Context(), `SELECT title,schedule_precision,ends_at IS NULL FROM training_sessions WHERE id=$1`, proposal.Proposal.SessionID).Scan(&title, &precision, &noEnd); err != nil || title != "Treino histórico de água" || precision != "START_ONLY" || !noEnd {
		t.Fatal("actual schedule/content changed", title, precision, noEnd, err)
	}
	if err = root.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE plan_id=$1`, plan).Scan(&count); err != nil || count != 3 {
		t.Fatal("duplicate native create", count, err)
	}
	execute(`UPDATE staff_grants SET revoked_at=now(),revoked_by_id=$1 WHERE user_id=$1 AND programme_id=$2`, actor, programme)
	status, body, _ = request("POST", base+"/confirmar", refreshed)
	if status != 404 || strings.Contains(body, "Treino histórico de água") || strings.Contains(body, "Remar com atenção") {
		t.Fatal("fresh revoke exposed private draft", status)
	}
}

// Reads only successful controls in this server-rendered task's first editor.
// The real SCS/CSRF middleware and actual generated HTML remain in the path.
func focusedNativeEditor(t *testing.T, body string) url.Values {
	t.Helper()
	forms := regexp.MustCompile(`(?s)<form\b[^>]*>(.*?)</form>`).FindAllStringSubmatch(body, -1)
	var form string
	for _, f := range forms {
		if strings.Contains(f[1], `name="activity_order"`) {
			form = f[1]
			break
		}
	}
	if form == "" {
		t.Fatal("native editor missing")
	}
	attr := func(tag, name string) string {
		m := regexp.MustCompile(`\b` + name + `="([^"]*)"`).FindStringSubmatch(tag)
		if len(m) == 2 {
			return html.UnescapeString(m[1])
		}
		return ""
	}
	out := url.Values{}
	for _, tag := range regexp.MustCompile(`<input\b[^>]*>`).FindAllString(form, -1) {
		if n := attr(tag, "name"); n != "" && !strings.Contains(tag, " disabled") {
			out.Add(n, attr(tag, "value"))
		}
	}
	for _, m := range regexp.MustCompile(`(?s)<textarea\b([^>]*)>(.*?)</textarea>`).FindAllStringSubmatch(form, -1) {
		out.Add(attr(m[1], "name"), html.UnescapeString(m[2]))
	}
	for _, m := range regexp.MustCompile(`(?s)<select\b([^>]*)>(.*?)</select>`).FindAllStringSubmatch(form, -1) {
		options := regexp.MustCompile(`<option\b([^>]*)>`).FindAllStringSubmatch(m[2], -1)
		if len(options) == 0 {
			continue
		}
		chosen := options[0][1]
		for _, o := range options {
			if strings.Contains(o[1], " selected") {
				chosen = o[1]
				break
			}
		}
		out.Add(attr(m[1], "name"), attr(chosen, "value"))
	}
	return out
}
