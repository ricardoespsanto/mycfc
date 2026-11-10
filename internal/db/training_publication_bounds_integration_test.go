//go:build integration

package db

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
)

func publicationQuerySQL(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("queries/training_publication.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.SplitN(string(body), "-- name: "+name+" :", 2)
	if len(sql) != 2 {
		t.Fatal("missing named source query")
	}
	text := strings.SplitN(sql[1], "\n", 2)[1]
	text = strings.SplitN(text, "-- name: ", 2)[0]
	return strings.TrimSuffix(strings.TrimSpace(strings.NewReplacer("sqlc.arg(actor_id)", "$1", "sqlc.arg(plan_id)", "$2").Replace(text)), ";")
}
func publicationAssertOverflowGate(t *testing.T, web *pgx.Conn, h recipientHistory, artifact string) {
	t.Helper()
	q := dbgen.New(web)
	source, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil {
		t.Fatal(err)
	}
	if !source.Overflow || len(source.Envelope) != 0 {
		t.Fatal("overflow materialized/truncated source")
	}
	var planJSON []byte
	if err = web.QueryRow(t.Context(), "EXPLAIN (ANALYZE,VERBOSE,FORMAT JSON,TIMING OFF) "+publicationQuerySQL(t, "PublicationRawSource"), h.actor, h.plan).Scan(&planJSON); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(artifact, planJSON, 0600); err != nil {
		t.Fatal(err)
	}
	var explain any
	if err = json.Unmarshal(planJSON, &explain); err != nil {
		t.Fatal(err)
	}
	gated := 0
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if kind, ok := node["Node Type"].(string); ok && strings.Contains(kind, "Aggregate") {
				output, _ := json.Marshal(node["Output"])
				if bytesContainsStringAgg(output) {
					gated++
					if loops, _ := node["Actual Loops"].(float64); loops != 0 {
						t.Fatalf("overflow executed whole-array aggregate %v", loops)
					}
				}
			}
			for _, child := range node {
				visit(child)
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(explain)
	if gated != 8 {
		t.Fatalf("expected 8 independently unexecuted array aggregates, got %d", gated)
	}
}
func bytesContainsStringAgg(value []byte) bool { return strings.Contains(string(value), "string_agg(") }

func TestPublicationExactSourceBytesAndExecutorGate(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	web := f.web(t)
	q := dbgen.New(web)
	c := publicationContentFixture(t, f, h, web)
	const limit int64 = 33554432
	fixedVersion := "2026-11-10T12:00:00Z"
	fixed := func() {
		recipientSchemaExec(t, web, `UPDATE training_plans SET updated_at=$2::timestamptz WHERE id=$1`, h.plan, fixedVersion)
	}
	recipientSchemaExec(t, web, `UPDATE training_plans SET description=repeat('P',4000) WHERE id=$1`, h.plan)
	recipientSchemaExec(t, web, `UPDATE training_segment_blocks SET instructions=repeat('B',1000) WHERE id=$1`, c.gymBlock)
	recipientSchemaExec(t, web, `UPDATE gym_exercises SET notes='' WHERE id=$1`, c.gymExercise)
	fixed()
	getBudget := func() dbgen.PublicationSourceBudgetRow {
		t.Helper()
		b, err := q.PublicationSourceBudget(t.Context(), dbgen.PublicationSourceBudgetParams{ActorID: h.actor, PlanID: h.plan})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := getBudget()
	recipientSchemaExec(t, web, `INSERT INTO gym_exercises(block_id,position,name,repetitions) VALUES($1,2,'Pad exercise',10)`, c.gymBlock)
	fixed()
	second := getBudget()
	step := second.SourceBytes - first.SourceBytes
	if step <= 0 {
		t.Fatal("source row byte increment invalid")
	}
	rows := 2 + int((limit-second.SourceBytes)/step) - 10
	if rows < 2 {
		t.Fatal("unexpected source row width")
	}
	recipientSchemaExec(t, web, `INSERT INTO gym_exercises(block_id,position,name,repetitions) SELECT $1,position,'Pad exercise',10 FROM generate_series(3,$2::integer) position`, c.gymBlock, rows)
	fixed()
	b := getBudget()
	for b.Overflow {
		rows -= 10
		recipientSchemaExec(t, web, `DELETE FROM gym_exercises WHERE block_id=$1 AND position>$2`, c.gymBlock, rows)
		fixed()
		b = getBudget()
	}
	padding := limit - b.SourceBytes
	if padding < 0 || padding > int64(rows)*1000 {
		t.Fatalf("source padding out of range %d", padding)
	}
	recipientSchemaExec(t, web, `UPDATE gym_exercises SET notes=repeat('N',LEAST(1000::bigint,GREATEST(0::bigint,$2::bigint-(position-1)::bigint*1000))::integer) WHERE block_id=$1 AND position<=($2::bigint+999)/1000`, c.gymBlock, padding)
	fixed()
	b = getBudget()
	if b.Overflow || b.SourceBytes != limit {
		t.Fatalf("exact boundary preflight wrong %+v padding=%d", b, padding)
	}
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	var peak atomic.Uint64
	peak.Store(baseline.HeapAlloc)
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var sample runtime.MemStats
				runtime.ReadMemStats(&sample)
				for old := peak.Load(); sample.HeapAlloc > old && !peak.CompareAndSwap(old, sample.HeapAlloc); old = peak.Load() {
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	started := time.Now()
	source, err := q.PublicationRawSource(ctx, dbgen.PublicationRawSourceParams{ActorID: h.actor, PlanID: h.plan})
	elapsed := time.Since(started)
	cancel()
	close(done)
	<-sampled
	if err != nil {
		t.Fatal(err)
	}
	if source.Overflow || source.SourceBytes != limit || int64(len(source.Envelope)) != limit || !json.Valid(source.Envelope) {
		t.Fatal("exact32MiB source not fully admitted")
	}
	t.Logf("source32MiB records=%d elapsed=%s sampledGoHeapGrowth=%d bytes (query/driver only; full Go materializer separate)", rows, elapsed, peak.Load()-baseline.HeapAlloc)
	if elapsed >= 10*time.Second {
		t.Fatal("source boundary exceeds proposed transaction budget")
	}
	// One ASCII byte in one whitelisted raw row is exactly one transport byte.
	recipientSchemaExec(t, web, `UPDATE gym_exercises SET notes=notes||'X' WHERE block_id=$1 AND position=$2`, c.gymBlock, rows)
	fixed()
	overflow := getBudget()
	if !overflow.Overflow || overflow.SourceBytes != limit+1 {
		t.Fatalf("one-byte-over boundary not detected %+v", overflow)
	}
	publicationAssertOverflowGate(t, web, h, "/tmp/mycfc-391-publication-byte-overflow-explain.json")
	// Empty eligible audience does not hide a large raw definition/overview graph.
	recipientSchemaExec(t, web, `UPDATE training_segment_blocks SET instructions=repeat('B',2000) WHERE id=$1`, c.gymBlock)
	recipientSchemaExec(t, web, `UPDATE users SET is_active=false WHERE id=$1`, h.subject)
	fixed()
	if b = getBudget(); !b.Overflow {
		t.Fatal("empty audience escaped raw source budget")
	}
}

func TestPublicationTupleCeilingAndExecutorGate(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	web := f.web(t)
	q := dbgen.New(web)
	recipientSchemaExec(t, web, `WITH added AS (INSERT INTO users(name,email,password_hash,date_of_birth) SELECT 'Synthetic tuple subject '||position,gen_random_uuid()::text||'@example.test','hash','1980-01-01'::date FROM generate_series(1,31) position RETURNING id),memberships AS (INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) SELECT added.id,anchor.season_id,anchor.programme_id,CURRENT_DATE,plan.week_start FROM added CROSS JOIN user_memberships anchor JOIN training_plans plan ON plan.id=$2 WHERE anchor.id=$1 RETURNING id) INSERT INTO training_group_members(group_id,membership_id,added_by_id) SELECT $3,id,$4 FROM memberships`, h.membership, h.plan, h.group, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_sessions(plan_id,title,starts_at,ends_at,created_by_id) SELECT plan.id,'Synthetic tuple session '||position,(plan.week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours'+position*interval '1 minute',(plan.week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours'+position*interval '1 minute',$2 FROM training_plans plan CROSS JOIN generate_series(1,127) position WHERE plan.id=$1`, h.plan, h.actor)
	budget, err := q.PublicationSourceBudget(t.Context(), dbgen.PublicationSourceBudgetParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || budget.Overflow || budget.RawTupleCount != 4096 {
		t.Fatalf("exact4096 refused %+v %v", budget, err)
	}
	started := time.Now()
	source, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: h.actor, PlanID: h.plan})
	elapsed := time.Since(started)
	if err != nil || source.Overflow || int64(len(source.Envelope)) != source.SourceBytes {
		t.Fatalf("exact4096 materialization failed %+v %v", source, err)
	}
	t.Logf("4096tuples sourceBytes=%d elapsed=%s", source.SourceBytes, elapsed)
	recipientSchemaExec(t, web, `INSERT INTO training_sessions(plan_id,title,starts_at,ends_at,created_by_id) SELECT id,'Synthetic tuple overflow',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '1 day 10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '1 day 11 hours',$2 FROM training_plans WHERE id=$1`, h.plan, h.actor)
	budget, err = q.PublicationSourceBudget(t.Context(), dbgen.PublicationSourceBudgetParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || !budget.Overflow || budget.RawTupleCount != 4097 {
		t.Fatalf("4097sentinel missing %+v %v", budget, err)
	}
	publicationAssertOverflowGate(t, web, h, "/tmp/mycfc-391-publication-tuple-overflow-explain.json")
	members, err := q.LockPublicationCandidateMemberships(t.Context(), dbgen.LockPublicationCandidateMembershipsParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(members) != 0 {
		t.Fatal("overflow materialized membership witnesses")
	}
	users, err := q.LockPublicationCandidateUsers(t.Context(), dbgen.LockPublicationCandidateUsersParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(users) != 0 {
		t.Fatal("overflow materialized user witnesses")
	}
}
