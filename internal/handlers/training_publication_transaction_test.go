package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type publicationTransactionFake struct {
	source         publicationSourceEnvelope
	raw            []byte
	budget         publicationSourceBudget
	steps          []string
	failStep       string
	failErr        error
	factsChange    func(*publicationFinalFacts)
	publication    dbgen.TrainingPlanPublication
	inserted       []StructuredPrescriptionInput
	decision       time.Time
	partialEnabled bool
}

func (q *publicationTransactionFake) step(s string) error {
	q.steps = append(q.steps, s)
	if q.failStep == s {
		return q.failErr
	}
	return nil
}
func (q *publicationTransactionFake) AdmitScheduleWrites(context.Context) (bool, error) {
	return q.partialEnabled, q.step("admission")
}
func (q *publicationTransactionFake) CheckArtifactSizes(context.Context, []StructuredPrescriptionInput) error {
	return q.step("artifacts")
}
func (q *publicationTransactionFake) VerifyRuntime(context.Context) error { return q.step("runtime") }
func (q *publicationTransactionFake) LockActor(context.Context, uuid.UUID) error {
	return q.step("actor")
}
func (q *publicationTransactionFake) GuardPlanV1(context.Context, uuid.UUID, uuid.UUID) error {
	return q.step("guard")
}
func (q *publicationTransactionFake) LockPlan(context.Context, uuid.UUID, uuid.UUID) (publicationPlanHeader, error) {
	return q.source.Header, q.step("plan")
}
func (q *publicationTransactionFake) SourceBudget(context.Context, uuid.UUID, uuid.UUID) (publicationSourceBudget, error) {
	return q.budget, q.step("budget")
}
func (q *publicationTransactionFake) StructuralCandidates(context.Context, uuid.UUID, uuid.UUID) ([]publicationCandidate, error) {
	return q.source.Candidates, q.step("candidates")
}
func (q *publicationTransactionFake) LockMemberships(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return publicationMembershipIDs(q.source), q.step("memberships")
}
func (q *publicationTransactionFake) LockEvents(context.Context, uuid.UUID, uuid.UUID) ([]publicationEvent, error) {
	return q.source.ReferencedEvents, q.step("events")
}
func (q *publicationTransactionFake) LockUsers(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	users := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, c := range q.source.Candidates {
		if c.SubjectID != nil && !seen[*c.SubjectID] {
			users = append(users, *c.SubjectID)
			seen[*c.SubjectID] = true
		}
	}
	return users, q.step("users")
}
func (q *publicationTransactionFake) RawSource(context.Context, uuid.UUID, uuid.UUID) ([]byte, publicationSourceBudget, error) {
	return q.raw, q.budget, q.step("source")
}
func (q *publicationTransactionFake) InsertPublication(_ context.Context, _ uuid.UUID, h publicationPlanHeader, _ string) (dbgen.TrainingPlanPublication, error) {
	q.publication = dbgen.TrainingPlanPublication{ID: uuid.New(), PlanID: h.PlanID, Revision: h.LatestRevision + 1, SupersedesID: h.LatestPublicationID}
	return q.publication, q.step("insert publication")
}
func (q *publicationTransactionFake) InsertPrescription(_ context.Context, _ uuid.UUID, _ uuid.UUID, p StructuredPrescriptionInput) error {
	q.inserted = append(q.inserted, p)
	return q.step("insert prescription")
}
func (q *publicationTransactionFake) FinalFacts(_ context.Context, _, _ uuid.UUID, id uuid.UUID) (publicationFinalFacts, error) {
	h := q.source.Header
	f := publicationFinalFacts{PlanID: h.PlanID, GroupID: h.GroupID, SourceUpdatedAt: h.SourceUpdatedAt, LatestPublicationID: id, LatestRevision: h.LatestRevision, LatestSupersedesID: h.LatestSupersedesID, TransactionLisbonDate: h.WeekStart, ActualLisbonDate: h.WeekStart, DecisionAt: pgtype.Timestamptz{Time: q.decision, Valid: true}, RawTupleCount: int64(len(q.source.Candidates)), RecipientTuples: []publicationRecipientTuple{}, PublicationTuples: []publicationHashTuple{}}
	for _, r := range q.source.EligibleRecipients {
		f.RecipientTuples = append(f.RecipientTuples, publicationRecipientTuple{r.SessionID, r.MembershipID, *r.AthleteUserID})
	}
	if q.publication.ID != uuid.Nil {
		f.LatestRevision = q.publication.Revision
		f.LatestSupersedesID = q.publication.SupersedesID
		for _, p := range q.inserted {
			subject := p.AthleteUserID
			f.PublicationTuples = append(f.PublicationTuples, publicationHashTuple{p.SessionID, p.MembershipID, &subject, p.SnapshotSHA256})
		}
	} else {
		f.PublicationTuples = q.source.LatestPrescriptions
	}
	f.PublicationTupleCount = int64(len(f.PublicationTuples))
	if q.factsChange != nil {
		q.factsChange(&f)
	}
	return f, q.step("final")
}

func publicationTransactionFixture(t *testing.T) (*publicationTransactionFake, publicationConfirmInput, time.Time) {
	t.Helper()
	source := publicationSourceFixture()
	now := source.Header.SourceUpdatedAt.Time.Add(time.Second)
	material, err := materializePublicationSource(source, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	metadata := trainingPublicationReviewMetadata{ActorID: uuid.New(), PlanID: source.Header.PlanID, SourceUpdatedAt: source.Header.SourceUpdatedAt.Time, ChangeSummary: "Publicação revista", Return: trainingPublicationReturnContext{GroupID: source.Header.GroupID, WeekID: source.Header.PlanID, SessionID: *source.Overview[0].SessionID, Day: source.Overview[0].SessionOn.Time.Format("2006-01-02")}}
	signer, err := newTrainingPublicationReviewSigner()
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.sign(trainingPublicationReviewInput{metadata, material.Prescriptions}, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.verify(token, metadata, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	q := &publicationTransactionFake{source: source, raw: raw, budget: publicationSourceBudget{SourceBytes: int64(len(raw)), RawTupleCount: int64(len(source.Candidates))}, decision: now}
	return q, publicationConfirmInput{metadata.ActorID, metadata.PlanID, claims}, now
}

func TestPublicationTransactionOrdersPostAdmissionChecksAndOwnSuccessor(t *testing.T) {
	q, input, now := publicationTransactionFixture(t)
	result, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"admission", "runtime", "actor", "guard", "plan", "budget", "candidates", "memberships", "events", "users", "source", "artifacts", "insert publication", "insert prescription", "final"}
	if !reflect.DeepEqual(q.steps, expected) {
		t.Fatalf("wrong ordering: %v", q.steps)
	}
	if result.NoOp || result.Revision != 1 || result.PublicationID != q.publication.ID || len(q.inserted) != 1 {
		t.Fatal("wrong successor result")
	}
	// No helper Commit exists on this facade: caller owns transaction completion.
}

func TestPublicationTransactionStaleLatestAndRuntimeFailBeforeMutation(t *testing.T) {
	for _, step := range []string{"admission", "runtime", "actor", "guard", "plan", "budget", "candidates", "memberships", "events", "users", "source", "artifacts"} {
		t.Run(step, func(t *testing.T) {
			q, input, now := publicationTransactionFixture(t)
			sentinel := errors.New("refused")
			q.failStep = step
			q.failErr = sentinel
			if _, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now }); err == nil {
				t.Fatal("failure ignored")
			}
			if len(q.inserted) != 0 || q.publication.ID != uuid.Nil {
				t.Fatal("wrote before a failed prerequisite")
			}
		})
	}
	q, input, now := publicationTransactionFixture(t)
	latest := uuid.New()
	q.source.Header.LatestPublicationID = &latest
	q.source.Header.LatestRevision = 1
	if _, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now }); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatal("stale latest was rebased", err)
	}
	if !reflect.DeepEqual(q.steps, []string{"admission", "runtime", "actor", "guard", "plan"}) {
		t.Fatal("stale review evaluated no-op or wrote", q.steps)
	}
}

func TestPublicationFinalDecisionRejectsExpirySetSubstitutionAndRollover(t *testing.T) {
	cases := []struct {
		name   string
		change func(*publicationFinalFacts)
	}{
		{"expired token", func(f *publicationFinalFacts) {
			f.DecisionAt.Time = f.DecisionAt.Time.Add(trainingPublicationReviewTTL)
		}},
		{"Lisbon rollover", func(f *publicationFinalFacts) { f.ActualLisbonDate.Time = f.ActualLisbonDate.Time.AddDate(0, 0, 1) }},
		{"current subject substitution", func(f *publicationFinalFacts) { f.RecipientTuples[0].AthleteUserID = uuid.New() }},
		{"stored subject substitution", func(f *publicationFinalFacts) { f.PublicationTuples[0].AthleteUserID = testUUIDPtr(uuid.New()) }},
		{"stored hash change", func(f *publicationFinalFacts) { f.PublicationTuples[0].SnapshotSHA256 = stringsOfZeroHash() }},
		{"different latest", func(f *publicationFinalFacts) { f.LatestPublicationID = uuid.New() }},
		{"lost successor chain", func(f *publicationFinalFacts) { f.LatestSupersedesID = testUUIDPtr(uuid.New()) }},
		{"raw candidate changed", func(f *publicationFinalFacts) { f.RawTupleCount++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, input, now := publicationTransactionFixture(t)
			q.factsChange = tc.change
			if _, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now }); err == nil {
				t.Fatal("final decision accepted changed facts")
			}
			if len(q.inserted) != 1 {
				t.Fatal("fixture did not reach post-insert check")
			}
			// Unit port deliberately cannot claim rollback. Real helper/PG integration
			// must prove an error here rolls all already-inserted rows back atomically.
		})
	}
}

func TestPublicationTransactionFreshMaterialNoopKeepsOriginalRevision(t *testing.T) {
	q, input, now := publicationTransactionFixture(t)
	material, err := materializePublicationSource(q.source, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	latest, previous := uuid.New(), uuid.New()
	q.source.Header.LatestPublicationID = &latest
	q.source.Header.LatestRevision = 4
	q.source.Header.LatestSupersedesID = &previous
	q.source.Header.LatestSourceUpdatedAt = q.source.Header.SourceUpdatedAt
	q.source.Header.SourceUpdatedAt.Time = q.source.Header.SourceUpdatedAt.Time.Add(time.Minute)
	for _, p := range material.Prescriptions {
		subject := p.AthleteUserID
		q.source.LatestPrescriptions = append(q.source.LatestPrescriptions, publicationHashTuple{p.SessionID, p.MembershipID, &subject, p.SnapshotSHA256})
	}
	metadata := input.Review.metadata
	metadata.SourceUpdatedAt = q.source.Header.SourceUpdatedAt.Time
	metadata.LatestPublicationID = latest
	metadata.LatestRevision = 4
	metadata.ChangeSummary = "Apenas o resumo mudou"
	now = now.Add(2 * time.Minute)
	q.decision = now
	signer, err := newTrainingPublicationReviewSigner()
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.sign(trainingPublicationReviewInput{metadata, material.Prescriptions}, now)
	if err != nil {
		t.Fatal(err)
	}
	input.Review, err = signer.verify(token, metadata, now)
	if err != nil {
		t.Fatal(err)
	}
	q.raw, err = json.Marshal(q.source)
	if err != nil {
		t.Fatal(err)
	}
	q.budget.SourceBytes = int64(len(q.raw))
	q.budget.LatestTupleCount = 1
	for i := 0; i < 2; i++ {
		result, err := savePublicationInTransaction(t.Context(), q, input, time.UTC, func() time.Time { return now })
		if err != nil || !result.NoOp || result.PublicationID != latest || result.Revision != 4 {
			t.Fatal("identical material source manufactured revision", err)
		}
	}
	if len(q.inserted) != 0 || q.publication.ID != uuid.Nil {
		t.Fatal("no-op inserted rows")
	}
	for _, step := range q.steps {
		if step == "insert publication" || step == "insert prescription" {
			t.Fatal("no-op reached write")
		}
	}
}
