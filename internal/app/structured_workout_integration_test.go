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
	t.Run("full structured mixed native draft", func(t *testing.T) {
		status, body, _ = request("GET", base+"?day="+day, nil)
		if status != 200 {
			t.Fatal("generic GET", status)
		}
		graph := focusedNativeEditor(t, body)
		graph.Set("title", "Água, força e corrida numa sessão")
		graph.Set("schedule.start_time", "16:00")
		main := "activities[a_1].phases[p_2]"
		graph.Set(main+".instructions", "Manter técnica em cada repetição.")
		graph.Set(main+".detail_mode", "WATER")
		graph.Set(main+".water.method", "INTERVALS")
		draftCommand := func(command string) {
			t.Helper()
			graph.Set("command", command)
			status, body, _ = request("POST", base+"/rascunho", graph)
			if status != 200 {
				t.Fatal("native graph command", command, status)
			}
			graph = focusedNativeEditor(t, body)
		}
		draftCommand("refresh")
		draftCommand("water_group_add:a_1:p_2:_root")
		nodes := graph[main+".water.node_order"]
		if len(nodes) != 1 {
			t.Fatal("native repeat group missing")
		}
		groupKey := nodes[0]
		gp := main + ".water.nodes[" + groupKey + "]"
		graph.Set(gp+".name", "Dez repetições com recuperação entre séries")
		graph.Set(gp+".repeats", "10")
		graph.Set(gp+".recovery_seconds", "120")
		draftCommand("water_effort_add:a_1:p_2:" + groupKey)
		nodes = graph[main+".water.node_order"]
		if len(nodes) != 2 {
			t.Fatal("native effort missing")
		}
		np := main + ".water.nodes[" + nodes[1] + "]"
		graph.Set(np+".name", "Quatro minutos de remada")
		graph.Set(np+".duration_seconds", "240")
		graph.Set(np+".duration_certainty", "EXACT")
		graph.Set(np+".intensity_code", "R2")
		graph.Set(main+".water.target_distance_metres", "8000")
		graph.Set(main+".water.target_distance_certainty", "EXACT")
		draftCommand("activity_add")
		activities := graph["activity_order"]
		if len(activities) != 2 {
			t.Fatal("native gym activity missing")
		}
		gymKey := activities[1]
		ap := "activities[" + gymKey + "]"
		graph.Set(ap+".modality", "GYM")
		draftCommand("phase_add:" + gymKey)
		phaseKey := graph.Get(ap + ".phase_order")
		pp := ap + ".phases[" + phaseKey + "]"
		graph.Set(pp+".purpose", "MAIN")
		graph.Set(pp+".instructions", "Força controlada no ginásio.")
		graph.Set(pp+".detail_mode", "GYM")
		graph.Set(pp+".gym.structure", "CIRCUIT")
		graph.Set(pp+".gym.objective", "CORE")
		graph.Set(pp+".gym.rounds", "3")
		draftCommand("refresh")
		draftCommand("gym_add:" + gymKey + ":" + phaseKey)
		exerciseKey := graph.Get(pp + ".gym.exercise_order")
		ep := pp + ".gym.exercises[" + exerciseKey + "]"
		graph.Set(ep+".name", "Agachamento")
		graph.Set(ep+".sets", "2")
		graph.Set(ep+".repetitions", "12")
		graph.Set(ep+".resistance_kind", "KILOGRAMS")
		graph.Set(ep+".resistance_value", "40")
		graph.Set(ep+".execution_intent", "CONTROLLED")
		draftCommand("activity_add")
		activities = graph["activity_order"]
		runKey := activities[2]
		rp := "activities[" + runKey + "]"
		graph.Set(rp+".modality", "RUN")
		draftCommand("phase_add:" + runKey)
		runPhase := graph.Get(rp + ".phase_order")
		rpp := rp + ".phases[" + runPhase + "]"
		graph.Set(rpp+".purpose", "COOL_DOWN")
		graph.Set(rpp+".instructions", "Corrida lenta para terminar.")
		graph.Set("command", "preview")
		status, body, _ = request("POST", base+"/prever", graph)
		if status != 200 || !strings.Contains(body, "40 min") || !strings.Contains(body, "18 min") || !strings.Contains(body, "8 km") || !strings.Contains(body, "Agachamento") || !strings.Contains(body, "Corrida lenta para terminar.") {
			t.Fatal("canonical mixed native preview", status)
		}
		approval := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
		var graphProposal struct {
			Proposal struct {
				SessionID uuid.UUID `json:"session_id"`
			} `json:"proposal"`
		}
		if err = json.Unmarshal([]byte(approval.Get("proposal")), &graphProposal); err != nil {
			t.Fatal(err)
		}
		var sessionsBefore int
		if err = root.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE plan_id=$1`, plan).Scan(&sessionsBefore); err != nil {
			t.Fatal(err)
		}
		status, _, headers = request("POST", base+"/confirmar", approval)
		if status != 303 || !strings.Contains(headers.Get("Location"), graphProposal.Proposal.SessionID.String()) {
			t.Fatal("native mixed confirm", status)
		}
		var segments, blocks, steps, exercises int
		var noEnd bool
		if err = root.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM training_session_segments WHERE session_id=s.id),(SELECT count(*) FROM training_segment_blocks b JOIN training_session_segments a ON a.id=b.segment_id WHERE a.session_id=s.id),(SELECT count(*) FROM water_work_steps n JOIN training_segment_blocks b ON b.id=n.block_id JOIN training_session_segments a ON a.id=b.segment_id WHERE a.session_id=s.id),(SELECT count(*) FROM gym_exercises x JOIN training_segment_blocks b ON b.id=x.block_id JOIN training_session_segments a ON a.id=b.segment_id WHERE a.session_id=s.id),ends_at IS NULL FROM training_sessions s WHERE id=$1`, graphProposal.Proposal.SessionID).Scan(&segments, &blocks, &steps, &exercises, &noEnd); err != nil || segments != 3 || blocks != 3 || steps != 2 || exercises != 1 || !noEnd {
			t.Fatal("native mixed raw graph", segments, blocks, steps, exercises, noEnd, err)
		}
		status, _, _ = request("POST", base+"/confirmar", approval)
		if status != 303 {
			t.Fatal("native mixed replay", status)
		}
		var after int
		if err = root.QueryRow(t.Context(), `SELECT count(*) FROM training_sessions WHERE plan_id=$1`, plan).Scan(&after); err != nil || after != sessionsBefore+1 {
			t.Fatal("native mixed duplicate identity", after, err)
		}
	})

	t.Run("saved workout native edit and scoped cancellation", func(t *testing.T) {
		futurePlan := uuid.New()
		execute(`INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Actual editable week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, futurePlan, programme, group, season, actor)
		var sourceDay, proposedDay string
		if err = root.QueryRow(t.Context(), `SELECT week_start::text,(week_start+1)::text FROM training_plans WHERE id=$1`, futurePlan).Scan(&sourceDay, &proposedDay); err != nil {
			t.Fatal(err)
		}
		createBase := "/admin/treinos/estruturados/semanas/" + futurePlan.String() + "/treinos/criar"
		status, body, _ = request("GET", createBase+"?day="+sourceDay, nil)
		if status != 200 {
			t.Fatal("edit fixture create", status)
		}
		editor := focusedNativeEditor(t, body)
		editor.Set("title", "Treino original para alterar")
		editor.Set("description", "Instruções de água originais.")
		editor.Set("schedule.start_time", "15:00")
		editor.Set("command", "preview")
		status, body, _ = request("POST", createBase+"/prever", editor)
		if status != 200 {
			t.Fatal("edit fixture preview", status)
		}
		createApproval := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
		var created struct {
			Proposal struct {
				SessionID uuid.UUID `json:"session_id"`
			} `json:"proposal"`
		}
		if err = json.Unmarshal([]byte(createApproval.Get("proposal")), &created); err != nil {
			t.Fatal(err)
		}
		status, _, _ = request("POST", createBase+"/confirmar", createApproval)
		if status != 303 {
			t.Fatal("edit fixture save", status)
		}
		editBase := "/admin/treinos/estruturados/semanas/" + futurePlan.String() + "/treinos/" + created.Proposal.SessionID.String() + "/editar"
		status, body, headers = request("GET", editBase, nil)
		if status != 200 || !strings.Contains(headers.Get("Cache-Control"), "no-store") {
			t.Fatal("actual private edit", status)
		}
		change := focusedNativeEditor(t, body)
		oldVersion := change.Get("expected_session_updated_at")
		if change.Get("session_id") != created.Proposal.SessionID.String() {
			t.Fatal("edit lost session identity")
		}
		change.Set("title", "Título próprio alterado")
		change.Set("description", "Texto editado\r\n\tcom identidade estável.")
		change.Set("schedule.date", proposedDay)
		change.Set("command", "preview")
		status, body, _ = request("POST", editBase+"/prever", change)
		if status != 200 || !strings.Contains(body, "Guardar alterações") {
			t.Fatal("native edit preview", status, body[:min(len(body), 1200)])
		}
		approval := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
		// A real independent CREATE changes only the plan version. Confirmation is
		// strict; deliberate re-preview may refresh only this unchanged own source.
		status, body, _ = request("GET", createBase+"?day="+sourceDay, nil)
		other := focusedNativeEditor(t, body)
		other.Set("title", "Outro treino independente")
		other.Set("description", "Instruções do outro treino.")
		other.Set("schedule.start_time", "18:00")
		other.Set("command", "preview")
		status, body, _ = request("POST", createBase+"/prever", other)
		if status != 200 {
			t.Fatal("other preview", status)
		}
		otherApproval := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
		status, _, _ = request("POST", createBase+"/confirmar", otherApproval)
		if status != 303 {
			t.Fatal("other save", status)
		}
		status, body, _ = request("POST", editBase+"/confirmar", approval)
		if status != 409 || field(body, "prior_proposal") != approval.Get("proposal") {
			t.Fatal("plan-only stale edit", status)
		}
		retained := focusedNativeEditor(t, body)
		if retained.Get("title") != "Título próprio alterado" || retained.Get("expected_session_updated_at") != oldVersion {
			t.Fatal("stale edit rebased/lost own input")
		}
		retained.Set("command", "preview")
		status, body, _ = request("POST", editBase+"/prever", retained)
		if status != 200 {
			t.Fatal("deliberate own-unchanged edit repreview", status)
		}
		revised := url.Values{"intent": {"confirm"}, "proposal": {field(body, "proposal")}, "review_token": {field(body, "review_token")}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
		status, _, headers = request("POST", editBase+"/confirmar", revised)
		if status != 303 || !strings.Contains(headers.Get("Location"), "day="+proposedDay) || !strings.Contains(headers.Get("Location"), "session_id="+created.Proposal.SessionID.String()) {
			t.Fatal("edit actual destination", status, headers.Get("Location"))
		}
		status, body, _ = request("POST", editBase+"/confirmar", revised)
		if status != 409 || regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(body) || !strings.Contains(body, `<textarea id="workout-description"`) {
			t.Fatal("changed-own replay didn't give copyable terminal recovery", status)
		}
		var savedTitle string
		var sameIDs bool
		if err = root.QueryRow(t.Context(), `SELECT title,id=$1 FROM training_sessions WHERE id=$1`, created.Proposal.SessionID).Scan(&savedTitle, &sameIDs); err != nil || savedTitle != "Título próprio alterado" || !sameIDs {
			t.Fatal("edit identity", savedTitle, err)
		}
		cancelBase := strings.TrimSuffix(editBase, "editar") + "cancelar"
		status, body, _ = request("GET", cancelBase, nil)
		if status != 200 || !strings.Contains(body, `action="`+cancelBase+`"`) {
			t.Fatal("scoped cancel GET", status)
		}
		cancelApproval := url.Values{"expected_updated_at": {field(body, "expected_updated_at")}, "cancellation_reason": {"Motivo próprio mantido"}, "confirm_cancellation": {"yes"}, "gorilla.csrf.Token": {field(body, "gorilla.csrf.Token")}}
		status, _, _ = request("POST", cancelBase, cancelApproval)
		if status != 303 {
			t.Fatal("actual scoped cancel", status)
		}
		status, body, _ = request("POST", cancelBase, cancelApproval)
		if status != 409 || !strings.Contains(body, "Motivo próprio mantido") || strings.Contains(body, `data-task-submit`) {
			t.Fatal("cancel stale own reason/version", status)
		}
	})
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
