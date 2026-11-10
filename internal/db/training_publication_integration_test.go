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

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPublicationScopedSourceAndEligibility(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	web := f.web(t)
	var groupedCheck string
	if err := web.QueryRow(t.Context(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='training_plans'::regclass AND conname='training_plans_week_valid'`).Scan(&groupedCheck); err != nil {
		t.Fatal(err)
	}
	t.Log("grouped plan check:", groupedCheck)
	q := dbgen.New(web)
	header, err := q.PublicationPlanForReview(t.Context(), dbgen.PublicationPlanForReviewParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || header.PlanID != h.plan || header.GroupID != h.group || header.LatestPublicationID == nil || *header.LatestPublicationID != h.publication {
		t.Fatalf("scoped header missing %+v %v", header, err)
	}
	budget, err := q.PublicationSourceBudget(t.Context(), dbgen.PublicationSourceBudgetParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || budget.Overflow || budget.RawTupleCount != 1 || budget.LatestTupleCount != 1 {
		t.Fatalf("budget failed %+v %v", budget, err)
	}
	source, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil {
		t.Fatal(err)
	}
	if source.Overflow || int64(len(source.Envelope)) != source.SourceBytes || source.SourceBytes != budget.SourceBytes {
		t.Fatalf("exact source byte framing mismatch actual=%d budget=%d raw=%d", len(source.Envelope), budget.SourceBytes, source.SourceBytes)
	}
	var envelope struct {
		Contract   string                                              `json:"contract"`
		Overview   []dbgen.ListStructuredTrainingOverviewForManagerRow `json:"overview"`
		Recipients []dbgen.ListStructuredTrainingPublicationMembersRow `json:"eligible_recipients"`
		Candidates []json.RawMessage                                   `json:"candidates"`
	}
	if err = json.Unmarshal(source.Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Contract != "mycfc/training-publication-source/v1" || len(envelope.Recipients) != 1 || len(envelope.Candidates) != 1 {
		t.Fatal("source envelope contract/set incorrect")
	}
	legacy, err := q.ListStructuredTrainingOverviewForManager(t.Context(), dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: true, UserID: h.actor})
	if err != nil {
		t.Fatal(err)
	}
	for i := range legacy {
		legacy[i].MemberCount = 0
	}
	oldJSON, _ := json.Marshal(legacy)
	newJSON, _ := json.Marshal(envelope.Overview)
	if !bytes.Equal(oldJSON, newJSON) {
		t.Fatal("typed source changes existing canonical overview input")
	}
	eligible, err := q.PublicationEligibleRecipients(t.Context(), dbgen.PublicationEligibleRecipientsParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(eligible) != 1 || eligible[0].AthleteUserID != h.subject {
		t.Fatalf("eligible set mismatch %+v %v", eligible, err)
	}
	candidates, err := q.PublicationStructuralCandidates(t.Context(), dbgen.PublicationStructuralCandidatesParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("structural candidate failed %v", err)
	}
	members, err := q.LockPublicationCandidateMemberships(t.Context(), dbgen.LockPublicationCandidateMembershipsParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(members) != 1 || members[0] != h.membership {
		t.Fatalf("membership witnesses wrong %v", err)
	}
	users, err := q.LockPublicationCandidateUsers(t.Context(), dbgen.LockPublicationCandidateUsersParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(users) != 1 || users[0] != h.subject {
		t.Fatalf("user witnesses wrong %v", err)
	}
	events, err := q.LockPublicationReferencedEvents(t.Context(), dbgen.LockPublicationReferencedEventsParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(events) != 0 {
		t.Fatalf("event witnesses wrong %v", err)
	}
	matches, err := q.PublicationVariationMatches(t.Context(), dbgen.PublicationVariationMatchesParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(matches) != 0 {
		t.Fatalf("variation matches wrong %v", err)
	}
	hashes, err := q.PublicationLatestHashes(t.Context(), dbgen.PublicationLatestHashesParams{ActorID: h.actor, PlanID: h.plan, PublicationID: h.publication})
	if err != nil || len(hashes) != 1 || hashes[0].AthleteUserID == nil {
		t.Fatalf("captured hashes wrong %v", err)
	}
	final, err := q.PublicationFinalFacts(t.Context(), dbgen.PublicationFinalFactsParams{ActorID: h.actor, PlanID: h.plan, PublicationID: h.publication})
	if err != nil || final.Overflow || final.LatestPublicationID != h.publication || !final.DecisionAt.Valid {
		t.Fatalf("baseline final facts wrong %+v %v", final, err)
	}
	if _, err = q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: uuid.New(), PlanID: h.plan}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("unrelated actor can read source")
	}
	recipientSchemaExec(t, web, `UPDATE users SET is_dependent=true,date_of_birth='2014-01-01',email=NULL,password_hash=NULL WHERE id=$1`, h.subject)
	eligible, err = q.PublicationEligibleRecipients(t.Context(), dbgen.PublicationEligibleRecipientsParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(eligible) != 0 {
		t.Fatal("unverified dependent listed as eligible")
	}
	oldEligible, err := q.ListStructuredTrainingPublicationMembers(t.Context(), h.plan)
	if err != nil || len(oldEligible) != 0 {
		t.Fatal("legacy publication recipient predicate differs")
	}
	candidates, err = q.PublicationStructuralCandidates(t.Context(), dbgen.PublicationStructuralCandidatesParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(candidates) != 1 {
		t.Fatal("unfiltered closure omitted ineligible subject")
	}
	users, err = q.LockPublicationCandidateUsers(t.Context(), dbgen.LockPublicationCandidateUsersParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(users) != 1 || users[0] != h.subject {
		t.Fatal("ineligible candidate user witness missing")
	}
	_, err = q.InsertPrescription(t.Context(), dbgen.InsertPrescriptionParams{ActorID: h.actor, PublicationID: h.publication, SessionID: h.session, MembershipID: h.membership, AthleteUserID: h.subject, Snapshot: []byte(`{"schema_version":1}`), SnapshotSha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ineligible INSERT did not refuse atomically %v", err)
	}
}

func TestPublicationInsertWitnessAndFinalSets(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	a, b := f.web(t), f.web(t)
	txA, err := a.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback(context.Background())
	qA := dbgen.New(txA)
	txB, err := b.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer txB.Rollback(context.Background())
	qB := dbgen.New(txB)
	if _, err = qB.PublicationPlanForReview(t.Context(), dbgen.PublicationPlanForReviewParams{ActorID: h.actor, PlanID: h.plan}); err != nil {
		t.Fatal(err)
	}
	header, err := qA.LockPublicationPlan(t.Context(), dbgen.LockPublicationPlanParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() {
		_, err := qB.LockPublicationPlan(t.Context(), dbgen.LockPublicationPlanParams{ActorID: h.actor, PlanID: h.plan})
		blocked <- err
	}()
	recipientWait(t, func() bool {
		var waiting bool
		if err := f.low.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, b.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		return waiting
	})
	publication, err := qA.InsertPublication(t.Context(), dbgen.InsertPublicationParams{ActorID: h.actor, PlanID: h.plan, SourceUpdatedAt: header.SourceUpdatedAt, ExpectedLatestRevision: header.LatestRevision, ExpectedLatestPublicationID: header.LatestPublicationID, ChangeSummary: "Real same-source reviewed successor"})
	if err != nil {
		t.Fatal(err)
	}
	if publication.Revision != 2 || publication.SupersedesID == nil || *publication.SupersedesID != h.publication || !publication.SourceUpdatedAt.Time.Equal(h.source) {
		t.Fatal("same-source INSERT lineage wrong")
	}
	var raw []byte
	var hash string
	if err = txA.QueryRow(t.Context(), `SELECT snapshot,snapshot_sha256 FROM training_prescriptions WHERE id=$1`, h.prescription).Scan(&raw, &hash); err != nil {
		t.Fatal(err)
	}
	if _, err = qA.InsertPrescription(t.Context(), dbgen.InsertPrescriptionParams{ActorID: h.actor, PublicationID: publication.ID, SessionID: h.session, MembershipID: h.membership, AthleteUserID: h.subject, Snapshot: raw, SnapshotSha256: hash}); err != nil {
		t.Fatal(err)
	}
	final, err := qA.PublicationFinalFacts(t.Context(), dbgen.PublicationFinalFactsParams{ActorID: h.actor, PlanID: h.plan, PublicationID: publication.ID})
	if err != nil || final.LatestPublicationID != publication.ID || final.LatestRevision != 2 || final.LatestSupersedesID == nil || *final.LatestSupersedesID != h.publication || final.PublicationTupleCount != 1 {
		t.Fatalf("own successor final facts wrong %+v %v", final, err)
	}
	if err = txA.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-blocked:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
			t.Fatalf("waited stale NOOP not rejected %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale plan waiter hung")
	}
	_ = txB.Rollback(t.Context())
	if recipientHistoryBytes(t, f.low, h) != h.before {
		t.Fatal("physical witness changed logical plan/history/outcome values")
	}
	var beforeXmin, afterXmin string
	if err = a.QueryRow(t.Context(), `SELECT xmin::text FROM training_plans WHERE id=$1`, h.plan).Scan(&beforeXmin); err != nil {
		t.Fatal(err)
	}
	// NOOP doesn't call INSERT: locking and final read alone leave xmin unchanged.
	noop, err := a.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	noopQ := dbgen.New(noop)
	if _, err = noopQ.LockPublicationPlan(t.Context(), dbgen.LockPublicationPlanParams{ActorID: h.actor, PlanID: h.plan}); err != nil {
		t.Fatal(err)
	}
	if _, err = noopQ.PublicationFinalFacts(t.Context(), dbgen.PublicationFinalFactsParams{ActorID: h.actor, PlanID: h.plan, PublicationID: publication.ID}); err != nil {
		t.Fatal(err)
	}
	if err = noop.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = a.QueryRow(t.Context(), `SELECT xmin::text FROM training_plans WHERE id=$1`, h.plan).Scan(&afterXmin); err != nil || beforeXmin != afterXmin {
		t.Fatal("read-only NOOP physically updated plan")
	}
	stale := dbgen.New(a)
	_, err = stale.InsertPublication(t.Context(), dbgen.InsertPublicationParams{ActorID: h.actor, PlanID: h.plan, SourceUpdatedAt: header.SourceUpdatedAt, ExpectedLatestRevision: 1, ExpectedLatestPublicationID: &h.publication, ChangeSummary: "Stale review"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale baseline INSERT accepted %v", err)
	}
	if err = a.QueryRow(t.Context(), `SELECT xmin::text FROM training_plans WHERE id=$1`, h.plan).Scan(&afterXmin); err != nil || beforeXmin != afterXmin {
		t.Fatal("zero-row INSERT witness changed plan")
	}
	// Empty successor uses the same physical witness, with an explicitly empty
	// eligible set; root/domain signed review and atomic task checks are separate.
	recipientSchemaExec(t, a, `UPDATE users SET is_active=false WHERE id=$1`, h.subject)
	header, err = stale.LockPublicationPlan(t.Context(), dbgen.LockPublicationPlanParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := stale.InsertPublication(t.Context(), dbgen.InsertPublicationParams{ActorID: h.actor, PlanID: h.plan, SourceUpdatedAt: header.SourceUpdatedAt, ExpectedLatestRevision: header.LatestRevision, ExpectedLatestPublicationID: header.LatestPublicationID, ChangeSummary: "Reviewed empty successor"})
	if err != nil {
		t.Fatal(err)
	}
	emptyFinal, err := stale.PublicationFinalFacts(t.Context(), dbgen.PublicationFinalFactsParams{ActorID: h.actor, PlanID: h.plan, PublicationID: empty.ID})
	if err != nil || emptyFinal.PublicationTupleCount != 0 || string(emptyFinal.RecipientTuples) != "[]" || string(emptyFinal.PublicationTuples) != "[]" {
		t.Fatalf("empty successor final facts wrong %+v %v", emptyFinal, err)
	}
}

type publicationContent struct{ gymSegment, gymBlock, gymExercise, waterSegment, waterBlock, waterFirst, waterSecond, variation, event, crew uuid.UUID }

func publicationContentFixture(t *testing.T, f recipientSchemaFixture, h recipientHistory, web *pgx.Conn) publicationContent {
	t.Helper()
	c := publicationContent{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	recipientSchemaExec(t, web, `INSERT INTO training_session_segments(id,session_id,position,modality) VALUES($1,$3,1,'GYM'),($2,$3,2,'WATER')`, c.gymSegment, c.waterSegment, h.session)
	recipientSchemaExec(t, web, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,instructions) VALUES($1,$2,1,'MAIN','Synthetic gym instructions'),($3,$4,1,'MAIN','Synthetic water instructions')`, c.gymBlock, c.gymSegment, c.waterBlock, c.waterSegment)
	recipientSchemaExec(t, web, `INSERT INTO gym_block_prescriptions(block_id,structure,objective) VALUES($1,'STRAIGHT_SETS','CORE')`, c.gymBlock)
	recipientSchemaExec(t, web, `INSERT INTO gym_exercises(id,block_id,position,name,repetitions,notes) VALUES($1,$2,1,'Synthetic exercise',10,'Precise notes')`, c.gymExercise, c.gymBlock)
	recipientSchemaExec(t, web, `INSERT INTO water_block_prescriptions(block_id,method) VALUES($1,'CONTINUOUS')`, c.waterBlock)
	recipientSchemaExec(t, web, `INSERT INTO water_work_steps(id,block_id,position,kind,name,duration_seconds,duration_certainty,instructions,created_at) VALUES($1,$3,1,'EFFORT','First by position',60,'EXACT','Exact effort','2020-01-02'),($2,$3,2,'EFFORT','First by creation',120,'EXACT','Other effort','2020-01-01')`, c.waterFirst, c.waterSecond, c.waterBlock)
	recipientSchemaExec(t, web, `INSERT INTO training_variations(id,plan_id,target_membership_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id) VALUES($1,$2,$3,'BLOCK',$4,'OVERRIDE','Synthetic individual instruction','{"instructions":"Personalized notes"}',$5)`, c.variation, h.plan, h.membership, c.gymBlock, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO events(id,title,event_type,starts_at,ends_at,created_by_id) SELECT $1,'Synthetic linked competition','COMPETITION',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 days',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 days',$3 FROM training_plans WHERE id=$2`, c.event, h.plan, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_variation_groups(id,training_group_id,name,kind,craft_code,effective_from,competition_event_id,created_by_id) SELECT $1,$2,'Synthetic crew','CREW','K2',week_start,$3,$4 FROM training_plans WHERE id=$5`, c.crew, h.group, c.event, h.actor, h.plan)
	recipientSchemaExec(t, web, `INSERT INTO training_variation_group_members(variation_group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, c.crew, h.membership, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_variations(plan_id,target_group_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id) VALUES($1,$2,'BLOCK',$3,'OVERRIDE','Synthetic crew instruction','{"instructions":"Crew notes"}',$4)`, h.plan, c.crew, c.gymBlock, h.actor)
	return c
}
func TestPublicationVariationTransportEventsAndScope(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	web := f.web(t)
	q := dbgen.New(web)
	c := publicationContentFixture(t, f, h, web)
	source, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Overview    []dbgen.ListStructuredTrainingOverviewForManagerRow `json:"overview"`
		Definitions []json.RawMessage                                   `json:"variation_definitions"`
		Targets     []json.RawMessage                                   `json:"variation_targets"`
		Matches     []json.RawMessage                                   `json:"variation_matches"`
		Events      []json.RawMessage                                   `json:"referenced_events"`
	}
	if err = json.Unmarshal(source.Envelope, &envelope); err != nil {
		t.Fatal(err)
	}
	legacy, err := q.ListStructuredTrainingOverviewForManager(t.Context(), dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: true, UserID: h.actor})
	if err != nil {
		t.Fatal(err)
	}
	for i := range legacy {
		legacy[i].MemberCount = 0
	}
	a, _ := json.Marshal(legacy)
	b, _ := json.Marshal(envelope.Overview)
	if !bytes.Equal(a, b) {
		t.Fatal("gym/water transport changes literal overview fields/order")
	}
	if len(envelope.Definitions) != 2 || len(envelope.Targets) != 2 || len(envelope.Matches) != 2 || len(envelope.Events) != 1 {
		t.Fatal("complete raw variation/reference graph missing")
	}
	if source.SourceBytes != int64(len(source.Envelope)) {
		t.Fatal("nested variation exact byte proof incorrect")
	}
	native, err := q.PublicationVariationMatches(t.Context(), dbgen.PublicationVariationMatchesParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(native) != 2 {
		t.Fatalf("eligible variation matches failed %v", err)
	}
	for _, raw := range envelope.Matches {
		var patch struct {
			Patch json.RawMessage `json:"patch"`
		}
		if err = json.Unmarshal(raw, &patch); err != nil || len(patch.Patch) == 0 || patch.Patch[0] != '{' {
			t.Fatal("transport patch isn't object JSON")
		}
	}
	// Event changes do not touch plan; the derived NOWAIT row witness closes this
	// missing reference dependency without exposing event title/audience.
	var before, after time.Time
	if err = web.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, h.plan).Scan(&before); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, web, `UPDATE events SET starts_at=starts_at+interval '1 day',ends_at=ends_at+interval '1 day' WHERE id=$1`, c.event)
	if err = web.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, h.plan).Scan(&after); err != nil || !before.Equal(after) {
		t.Fatal("event fixture unexpectedly provides plan-touch witness")
	}
	changed, err := q.PublicationVariationMatches(t.Context(), dbgen.PublicationVariationMatchesParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil {
		t.Fatal(err)
	}
	var oldUntil, newUntil time.Time
	for _, row := range native {
		if row.Priority == 1 {
			oldUntil = row.EffectiveUntil.Time
		}
	}
	for _, row := range changed {
		if row.Priority == 1 {
			newUntil = row.EffectiveUntil.Time
		}
	}
	if !newUntil.Equal(oldUntil.AddDate(0, 0, 1)) {
		t.Fatal("linked event effective date wasn't read from current reference")
	}
	tx, err := web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	txQ := dbgen.New(tx)
	if _, err = txQ.PublicationPlanForReview(t.Context(), dbgen.PublicationPlanForReviewParams{ActorID: h.actor, PlanID: h.plan}); err != nil {
		t.Fatal(err)
	}
	editor := f.web(t)
	recipientSchemaExec(t, editor, `UPDATE events SET starts_at=starts_at+interval '1 day',ends_at=ends_at+interval '1 day' WHERE id=$1`, c.event)
	_, err = txQ.LockPublicationReferencedEvents(t.Context(), dbgen.LockPublicationReferencedEventsParams{ActorID: h.actor, PlanID: h.plan})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
		t.Fatalf("post-snapshot event update not rejected %v", err)
	}
	_ = tx.Rollback(t.Context())
	tx, err = web.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	recipientSchemaExec(t, tx, `UPDATE events SET starts_at=starts_at+interval '1 day',ends_at=ends_at+interval '1 day' WHERE id=$1`, c.event)
	_, err = dbgen.New(editor).LockPublicationReferencedEvents(t.Context(), dbgen.LockPublicationReferencedEventsParams{ActorID: h.actor, PlanID: h.plan})
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("event NOWAIT did not refuse conflicting update %v", err)
	}
	_ = tx.Rollback(t.Context())
	// Exact programme grant cannot read a team group, even where the plan also
	// carries that same programme. An exact team grant authorizes its own scope.
	coach, team, teamGroup, teamPlan := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	recipientSchemaExec(t, web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic coach',$2,'hash','1980-01-01')`, coach, coach.String()+"@example.test")
	recipientSchemaExec(t, web, `INSERT INTO teams(id,code,name,programme_id,season_id) SELECT $1,'SYN_TEAM','Synthetic scoped team',programme_id,season_id FROM user_memberships WHERE id=$2`, team, h.membership)
	recipientSchemaExec(t, web, `INSERT INTO training_groups(id,name,team_id,created_by_id) VALUES($1,'Synthetic team group',$2,$3)`, teamGroup, team, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_plans(id,title,programme_id,team_id,training_group_id,season_id,week_start,created_by_id) SELECT $1,'Synthetic team plan',programme_id,$2,$3,season_id,week_start,$4 FROM training_plans WHERE id=$5`, teamPlan, team, teamGroup, h.actor, h.plan)
	recipientSchemaExec(t, web, `INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) SELECT $1,'COACH',programme_id,$2 FROM user_memberships WHERE id=$3`, coach, h.actor, h.membership)
	if _, err = q.PublicationPlanForReview(t.Context(), dbgen.PublicationPlanForReviewParams{ActorID: coach, PlanID: teamPlan}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("programme coach gained team-group authority")
	}
	recipientSchemaExec(t, web, `INSERT INTO staff_grants(user_id,capability,team_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, team, h.actor)
	if _, err = q.PublicationPlanForReview(t.Context(), dbgen.PublicationPlanForReviewParams{ActorID: coach, PlanID: teamPlan}); err != nil {
		t.Fatal("exact team coach denied")
	}
	recipientSchemaExec(t, web, `UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$2,revoke_reason='Synthetic scope revoked' WHERE user_id=$1 AND team_id=$3`, coach, h.actor, team)
	if _, err = q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: coach, PlanID: teamPlan}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked team grant can materialize source")
	}
	unrelated := uuid.New()
	states, err := q.ListManagedTrainingPublicationStates(t.Context(), dbgen.ListManagedTrainingPublicationStatesParams{IsAdmin: true, UserID: unrelated})
	if err != nil || len(states) != 0 {
		t.Fatal("legacy status trusted cached IsAdmin")
	}
	hidden, err := q.ListTrainingVariationMatchesForManager(t.Context(), dbgen.ListTrainingVariationMatchesForManagerParams{IsAdmin: true, UserID: unrelated, TimeZone: "Europe/Lisbon"})
	if err != nil || len(hidden) != 0 {
		t.Fatal("legacy variation trusted cached IsAdmin")
	}
	states, err = q.ListManagedTrainingPublicationStates(t.Context(), dbgen.ListManagedTrainingPublicationStatesParams{IsAdmin: true, UserID: coach})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.ID == teamPlan {
			t.Fatal("legacy status broadened programme grant to revoked team")
		}
	}

}

func TestPublicationGuardianAuthorityFreshnessAndClock(t *testing.T) {
	f := newRecipientSchemaFixture(t)
	f.install79(t)
	h := recipientHistoryFixture(t, f)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	web := f.web(t)
	q := dbgen.New(web)
	content := publicationContentFixture(t, f, h, web)
	if err := ProvisionGuardianReleaseBindRole(t.Context(), f.root, "mycfc", guardianReleaseBindRole, "synthetic-binder"); err != nil {
		t.Fatal(err)
	}
	if err := ProvisionGuardianActivationRole(t.Context(), f.root, "mycfc", guardianActivationRole, "synthetic-operator"); err != nil {
		t.Fatal(err)
	}
	connectControl := func(role, password string) *pgx.Conn {
		t.Helper()
		cfg, err := pgx.ParseConfig(f.url)
		if err != nil {
			t.Fatal(err)
		}
		cfg.User, cfg.Password = role, password
		c, err := pgx.ConnectConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		return c
	}
	binder := connectControl(guardianReleaseBindRole, "synthetic-binder")
	operator := connectControl(guardianActivationRole, "synthetic-operator")
	image := "sha256:" + strings.Repeat("a", 64)
	recipientSchemaExec(t, binder, `SELECT * FROM guardian_ops.release_disable_and_bind($1,$2,'mycfc')`, image, EmbeddedMigrationDigest())
	approval, policy := guardianActivationFixture(t, h.actor, "mycfc")
	var changed bool
	var policyVersion string
	if err := operator.QueryRow(t.Context(), `SELECT changed,policy_version FROM guardian_ops.enable($1,$2,$3,$4,$5,$6,'mycfc')`, h.actor, string(approval), approval, policy, image, EmbeddedMigrationDigest()).Scan(&changed, &policyVersion); err != nil || !changed {
		t.Fatalf("synthetic authority policy setup failed %v", err)
	}
	guardian := uuid.New()
	recipientSchemaExec(t, web, `INSERT INTO users(id,name,email,password_hash,email_verified_at,date_of_birth) VALUES($1,'Synthetic guardian',$2,'hash',clock_timestamp(),'1980-01-01')`, guardian, guardian.String()+"@example.test")
	recipientSchemaExec(t, web, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,season_id,programme_id,CURRENT_DATE,ends_on FROM user_memberships WHERE id=$2`, guardian, h.membership)
	const childName = "Synthetic verified publication child"
	var child uuid.UUID
	if err := web.QueryRow(t.Context(), `SELECT id FROM public.guardian_authority_create_dependent($1,(CURRENT_DATE-interval '12 years')::date,$2,NULL::bytea)`, childName, guardian).Scan(&child); err != nil {
		t.Fatal(err)
	}
	var relationship uuid.UUID
	var version int64
	relationshipState := func() {
		t.Helper()
		if err := web.QueryRow(t.Context(), `SELECT public_ref,version FROM guardian_authority_relationships WHERE subject_user_id=$1`, child).Scan(&relationship, &version); err != nil {
			t.Fatal(err)
		}
	}
	transition := func(conn *pgx.Conn, action, reason string) {
		t.Helper()
		relationshipState()
		recipientSchemaExec(t, conn, `SELECT * FROM public.guardian_authority_admin_transition($1,$2,$3,$4,'CLUB_REGISTRATION_RECORD',$5)`, h.actor, relationship, version, action, reason)
	}
	transition(web, "APPROVE", "RELATIONSHIP_CONFIRMED")
	childMembership := uuid.New()
	recipientSchemaExec(t, web, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,$2,season_id,programme_id,CURRENT_DATE,ends_on FROM user_memberships WHERE id=$3`, childMembership, child, h.membership)
	recipientSchemaExec(t, web, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, h.group, childMembership, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_variations(plan_id,target_membership_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id) VALUES($1,$2,'BLOCK',$3,'OVERRIDE','Synthetic child instruction','{"instructions":"Child-specific notes"}',$4)`, h.plan, childMembership, content.gymBlock, h.actor)
	eligible, err := q.PublicationEligibleRecipients(t.Context(), dbgen.PublicationEligibleRecipientsParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(eligible) != 2 {
		t.Fatalf("verified child not eligible %v", err)
	}
	historyBeforeAuthority := recipientHistoryBytes(t, f.low, h)
	// Ordinary account writers do not take P/A/H. All unfiltered subjects and
	// their guardian rows must therefore be witnessed, even before name filters.
	editor := f.web(t)
	edit, err := editor.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, edit, `UPDATE users SET is_active=false WHERE id=$1`, guardian)
	_, err = q.LockPublicationCandidateUsers(t.Context(), dbgen.LockPublicationCandidateUsersParams{ActorID: h.actor, PlanID: h.plan})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("guardian NOWAIT witness failed %v", err)
	}
	_ = edit.Rollback(t.Context())
	stale, err := web.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	staleQ := dbgen.New(stale)
	if _, err = staleQ.PublicationPlanForReview(t.Context(), dbgen.PublicationPlanForReviewParams{ActorID: h.actor, PlanID: h.plan}); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, editor, `UPDATE users SET is_active=false WHERE id=$1`, guardian)
	_, err = staleQ.LockPublicationCandidateUsers(t.Context(), dbgen.LockPublicationCandidateUsersParams{ActorID: h.actor, PlanID: h.plan})
	if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
		t.Fatalf("post-snapshot guardian state not rejected %v", err)
	}
	_ = stale.Rollback(t.Context())
	recipientSchemaExec(t, editor, `UPDATE users SET is_active=true WHERE id=$1`, guardian)
	cfg, err := pgxpool.ParseConfig(f.url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User = f.roles.AppUsername
	cfg.ConnConfig.Password = f.roles.AppPassword
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pid := recipientPID(t, pool)
	recipientSchemaExec(t, f.low, `SELECT pg_advisory_lock(hashtextextended('mycfc/guardian-authority',0))`)
	done := make(chan recipientRun, 1)
	seen := 0
	go func() {
		result, err := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
			rows, err := dbgen.New(commands).PublicationEligibleRecipients(ctx, dbgen.PublicationEligibleRecipientsParams{ActorID: h.actor, PlanID: h.plan})
			seen = len(rows)
			return err
		})
		done <- recipientRun{result, err}
	}()
	recipientWaitLock(t, f.root, pid)
	var sourceBefore, sourceAfter time.Time
	if err = f.low.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, h.plan).Scan(&sourceBefore); err != nil {
		t.Fatal(err)
	}
	transition(f.low, "SUSPEND", "AUTHORITY_CHANGED")
	if err = f.low.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, h.plan).Scan(&sourceAfter); err != nil || !sourceBefore.Equal(sourceAfter) {
		t.Fatal("authority-only change unexpectedly touched plan source version")
	}
	recipientSchemaExec(t, f.low, `SELECT pg_advisory_unlock(hashtextextended('mycfc/guardian-authority',0))`)
	run := recipientAwait(t, done)
	if run.err != nil || run.result.Outcome != SessionTxCommitted || seen != 1 {
		t.Fatalf("pre-BEGIN admission saw stale authority %+v count=%d", run, seen)
	}
	source, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(source.Envelope, []byte(childName)) {
		t.Fatal("revoked child name leaked through source/variation projection")
	}
	oldMatches, err := q.ListTrainingVariationMatchesForManager(t.Context(), dbgen.ListTrainingVariationMatchesForManagerParams{IsAdmin: true, UserID: h.actor, TimeZone: "Europe/Lisbon"})
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range oldMatches {
		if match.AthleteName == childName || match.TargetName == childName {
			t.Fatal("legacy variation name filter differs")
		}
	}
	users, err := q.LockPublicationCandidateUsers(t.Context(), dbgen.LockPublicationCandidateUsersParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(users) != 3 {
		t.Fatal("revoked child/guardian disappeared from internal lock closure")
	}
	deniedPrescription := func() {
		t.Helper()
		_, err := q.InsertPrescription(t.Context(), dbgen.InsertPrescriptionParams{PublicationID: h.publication, SessionID: h.session, MembershipID: childMembership, AthleteUserID: child, ActorID: h.actor, Snapshot: []byte(`{"schema_version":1,"session":{"EntryKind":"WORKOUT"}}`), SnapshotSha256: strings.Repeat("a", 64)})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("independent INSERT retained revoked/expired child %v", err)
		}
	}
	deniedPrescription()
	facts, err := q.PublicationFinalFacts(t.Context(), dbgen.PublicationFinalFactsParams{ActorID: h.actor, PlanID: h.plan, PublicationID: h.publication})
	if err != nil || bytes.Contains(facts.RecipientTuples, []byte(child.String())) {
		t.Fatalf("final exact set retained revoked child %v", err)
	}
	transition(web, "APPROVE", "RELATIONSHIP_CONFIRMED")
	// Seed a synthetic historical verification close to its real cutoff. No
	// production clock override, grace rule, or audit/history rewrite is added.
	recipientSchemaExec(t, f.low, `WITH instant AS MATERIALIZED(SELECT clock_timestamp() AS now_at) UPDATE guardian_authority_relationships SET verified_at=instant.now_at-interval '365 days',review_due_at=instant.now_at+interval '800 milliseconds',verified_until=instant.now_at+interval '800 milliseconds' FROM instant WHERE subject_user_id=$1`, child)
	first, last := 0, 0
	run.result, run.err = WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
		queries := dbgen.New(commands)
		rows, err := queries.PublicationEligibleRecipients(ctx, dbgen.PublicationEligibleRecipientsParams{ActorID: h.actor, PlanID: h.plan})
		first = len(rows)
		if err != nil {
			return err
		}
		if _, err = commands.Exec(ctx, `SELECT pg_sleep(1.0)`); err != nil {
			return err
		}
		rows, err = queries.PublicationEligibleRecipients(ctx, dbgen.PublicationEligibleRecipientsParams{ActorID: h.actor, PlanID: h.plan})
		last = len(rows)
		return err
	})
	if run.err != nil || first != 2 || last != 1 {
		t.Fatalf("locks pretended to stop expiry first=%d last=%d err=%v", first, last, run.err)
	}
	deniedPrescription()
	recipientSchemaExec(t, operator, `SELECT * FROM guardian_ops.disable($1,'mycfc')`, h.actor)
	eligible, err = q.PublicationEligibleRecipients(t.Context(), dbgen.PublicationEligibleRecipientsParams{ActorID: h.actor, PlanID: h.plan})
	if err != nil || len(eligible) != 1 {
		t.Fatal("closed guardian gate retained dependent recipient")
	}
	deniedPrescription()
	if recipientHistoryBytes(t, f.low, h) != historyBeforeAuthority {
		t.Fatal("authority/clock/gate changes rewrote stored publication/outcome history or plan")
	}
	// Isolated migration-owner historical fixtures exercise preflight refusal
	// of stale authority/credentials/sessions; no test grants or root routines.
	cases := []struct{ name, sql string }{
		{"stale verified relationship", `UPDATE guardian_authority_relationships SET state='VERIFIED',verified_at=clock_timestamp(),verified_until=clock_timestamp()+interval '365 days',review_due_at=clock_timestamp()+interval '300 days' WHERE subject_user_id=$1`},
		{"dangling credential", `UPDATE users SET minor_login_id='synthetic-dangling',password_hash='synthetic-hash' WHERE id=$1`},
		{"indexed dependent session", `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES('synthetic-dangling-session','synthetic',clock_timestamp()+interval '1 hour',$1,true)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.low.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			recipientSchemaExec(t, tx, tc.sql, child)
			before := recipientGuardianStateAt(t, tx)
			var ready bool
			err = tx.QueryRow(t.Context(), `SELECT ready FROM guardian_ops.preflight($1,$2,$3,$4,$5,$6,'mycfc')`, h.actor, string(approval), approval, policy, image, EmbeddedMigrationDigest()).Scan(&ready)
			if err != nil || ready {
				t.Fatalf("unsafe stale state preflight accepted %v %v", ready, err)
			}
			if recipientGuardianStateAt(t, tx) != before {
				t.Fatal("refused preflight changed durable state")
			}
		})
	}
}
