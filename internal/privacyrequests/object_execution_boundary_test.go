package privacyrequests

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type executionBoundaryRows struct {
	next   bool
	scan   func(...any) error
	err    error
	closed bool
}

func (r *executionBoundaryRows) Close()                                     { r.closed = true }
func (r *executionBoundaryRows) Err() error                                 { return r.err }
func (*executionBoundaryRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*executionBoundaryRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *executionBoundaryRows) Next() bool {
	if r.next {
		r.next = false
		return true
	}
	r.closed = true
	return false
}
func (r *executionBoundaryRows) Scan(dest ...any) error {
	if r.scan == nil {
		return nil
	}
	return r.scan(dest...)
}
func (*executionBoundaryRows) Values() ([]any, error) { return nil, errors.New("not implemented") }
func (*executionBoundaryRows) RawValues() [][]byte    { return nil }
func (*executionBoundaryRows) Conn() *pgx.Conn        { return nil }

type executionBoundaryDB struct {
	rows     pgx.Rows
	queryErr error
	rowScans []func(...any) error
	rowIndex int
}

func (executionBoundaryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected exec")
}
func (d executionBoundaryDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return d.rows, d.queryErr
}
func (d *executionBoundaryDB) QueryRow(context.Context, string, ...any) pgx.Row {
	index := d.rowIndex
	d.rowIndex++
	return executionBoundaryRow{scan: d.rowScans[index]}
}

type executionBoundaryRow struct{ scan func(...any) error }

func (r executionBoundaryRow) Scan(dest ...any) error { return r.scan(dest...) }

type objectDeletionFailure struct{ err error }

func (s objectDeletionFailure) DeleteAllVersions(context.Context, string) (storage.VersionDeletionEvidence, error) {
	return storage.VersionDeletionEvidence{}, s.err
}

func validObjectWorkerForBoundary(t *testing.T, pool dbgen.DBTX, objects storage.VersionedObjectStore) (ObjectExecutionWorker, ExecutionLease, ObjectTargetBinding, ObjectTargetEnvelope) {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := NewX25519ObjectTargetProtector("object-target-key", private.PublicKey().Bytes(), "object-digest-key", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	lease := ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{
		ID: uuid.New(), ExecutionID: uuid.New(), LeaseEpoch: 1, AttemptCount: 1,
	}, ActiveLeaseID: uuid.New(), ActiveAttemptID: uuid.New()}}
	binding := objectTargetTestBinding()
	binding.ExecutionID, binding.JobID = lease.Job.ExecutionID, lease.Job.ID
	envelope, err := protector.SealObjectKey(binding, "profiles/member/photo.png")
	if err != nil {
		t.Fatal(err)
	}
	return ObjectExecutionWorker{Pool: pool, Objects: objects, WorkerRef: uuid.New(), PrivateKey: private.Bytes(),
		TranscriptKeyID: "object-transcript-key", TranscriptKey: bytes.Repeat([]byte{6}, 32)}, lease, binding, envelope
}

func objectTargetBoundaryScan(binding ObjectTargetBinding, envelope ObjectTargetEnvelope) func(...any) error {
	return func(dest ...any) error {
		*dest[0].(*uuid.UUID), *dest[1].(*uuid.UUID), *dest[2].(*uuid.UUID), *dest[3].(*uuid.UUID) = binding.TargetID, binding.ExecutionID, binding.JobID, binding.CheckpointID
		*dest[4].(*[]byte), *dest[5].(*string), *dest[6].(*string), *dest[7].(*string) = bytes.Clone(binding.PlanEntrySHA256), binding.Category, binding.Service, binding.TargetKind
		*dest[8].(*string), *dest[9].(*uuid.UUID), *dest[10].(*string), *dest[11].(*string) = binding.SourceKind, binding.SourceRef, binding.OperationCode, binding.ActionVersion
		*dest[12].(*string), *dest[13].(*string), *dest[14].(*string), *dest[15].(*string) = binding.ProviderContractVersion, envelope.Version, envelope.Algorithm, envelope.KeyID
		*dest[16].(*[]byte), *dest[17].(*[]byte), *dest[18].(*[]byte) = bytes.Clone(envelope.Encapsulation), bytes.Clone(envelope.Nonce), bytes.Clone(envelope.Ciphertext)
		return nil
	}
}

func TestObjectExecutionWorkerDatabaseAndProtectedTargetFailures(t *testing.T) {
	queryFailure := errors.New("query unavailable")
	worker, lease, _, _ := validObjectWorkerForBoundary(t, &executionBoundaryDB{queryErr: queryFailure}, objectDeletionFailure{})
	if _, err := worker.CompleteCheckpoint(context.Background(), lease); !errors.Is(err, ErrObjectExecutionFailed) {
		t.Fatalf("query failure error=%v", err)
	}

	worker, lease, _, _ = validObjectWorkerForBoundary(t, &executionBoundaryDB{rows: &executionBoundaryRows{next: true, scan: func(...any) error { return errors.New("scan failed") }}}, objectDeletionFailure{})
	if _, err := worker.CompleteCheckpoint(context.Background(), lease); !errors.Is(err, ErrObjectExecutionFailed) {
		t.Fatalf("scan failure error=%v", err)
	}

	worker, lease, _, _ = validObjectWorkerForBoundary(t, &executionBoundaryDB{rows: &executionBoundaryRows{err: errors.New("stream failed")}}, objectDeletionFailure{})
	if _, err := worker.CompleteCheckpoint(context.Background(), lease); !errors.Is(err, ErrObjectExecutionFailed) {
		t.Fatalf("stream failure error=%v", err)
	}

	for name, rowErr := range map[string]error{"lease-lost": pgx.ErrNoRows, "completion-failed": errors.New("complete failed")} {
		t.Run(name, func(t *testing.T) {
			database := &executionBoundaryDB{rows: &executionBoundaryRows{}, rowScans: []func(...any) error{func(...any) error { return rowErr }}}
			worker, lease, _, _ := validObjectWorkerForBoundary(t, database, objectDeletionFailure{})
			_, err := worker.CompleteCheckpoint(context.Background(), lease)
			if name == "lease-lost" && !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("lease loss error=%v", err)
			}
			if name == "completion-failed" && !errors.Is(err, ErrObjectExecutionFailed) {
				t.Fatalf("completion failure error=%v", err)
			}
		})
	}

	database := &executionBoundaryDB{rows: &executionBoundaryRows{}, rowScans: []func(...any) error{
		func(dest ...any) error { *dest[0].(*uuid.UUID) = uuid.New(); return nil },
		func(...any) error { return errors.New("checkpoint fetch failed") },
	}}
	worker, lease, _, _ = validObjectWorkerForBoundary(t, database, objectDeletionFailure{})
	if _, err := worker.CompleteCheckpoint(context.Background(), lease); err == nil || err.Error() != "checkpoint fetch failed" {
		t.Fatalf("checkpoint fetch error=%v", err)
	}

	database = &executionBoundaryDB{}
	worker, lease, binding, envelope := validObjectWorkerForBoundary(t, database, objectDeletionFailure{})
	invalidEnvelope := envelope
	invalidEnvelope.Ciphertext = nil
	database.rows = &executionBoundaryRows{next: true, scan: objectTargetBoundaryScan(binding, invalidEnvelope)}
	if _, err := worker.CompleteCheckpoint(context.Background(), lease); !errors.Is(err, ErrObjectExecutionFailed) {
		t.Fatalf("invalid protected target error=%v", err)
	}

	database = &executionBoundaryDB{}
	deleteFailure := errors.New("object delete failed")
	worker, lease, binding, envelope = validObjectWorkerForBoundary(t, database, objectDeletionFailure{err: deleteFailure})
	database.rows = &executionBoundaryRows{next: true, scan: objectTargetBoundaryScan(binding, envelope)}
	if _, err := worker.CompleteCheckpoint(context.Background(), lease); !errors.Is(err, ErrObjectExecutionFailed) {
		t.Fatalf("object deletion error=%v", err)
	}

	database = &executionBoundaryDB{rowScans: []func(...any) error{func(...any) error { return pgx.ErrNoRows }}}
	worker, lease, binding, envelope = validObjectWorkerForBoundary(t, database, objectDeletionFailure{})
	database.rows = &executionBoundaryRows{next: true, scan: objectTargetBoundaryScan(binding, envelope)}
	if _, err := worker.CompleteCheckpoint(context.Background(), lease); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("evidence lease loss error=%v", err)
	}
}
