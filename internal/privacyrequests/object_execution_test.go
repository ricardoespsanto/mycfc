package privacyrequests

import (
	"bytes"
	"context"
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/google/uuid"
)

func TestObjectExecutionWorkerRejectsIncompleteWorkerConfiguration(t *testing.T) {
	if _, err := (ObjectExecutionWorker{}).CompleteCheckpoint(context.Background(), ExecutionLease{}); !errors.Is(err, ErrObjectExecutionUnavailable) {
		t.Fatalf("CompleteCheckpoint() error=%v", err)
	}
}

func TestObjectEvidenceTranscriptBindsFenceTargetAndCounts(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	binding := objectTargetTestBinding()
	lease := ExecutionLease{Job: ExecutionJob{PrivacyErasureCategoryJob: dbgen.PrivacyErasureCategoryJob{LeaseEpoch: 3}, ActiveAttemptID: uuid.New()}}
	evidence := storage.VersionDeletionEvidence{DeletedVersions: 2, DeletedMarkers: 1, ListCalls: 4, StableChecks: 2}
	first := objectEvidenceTranscriptDigest(key, binding, lease, evidence)
	if repeated := objectEvidenceTranscriptDigest(key, binding, lease, evidence); !bytes.Equal(first, repeated) {
		t.Fatal("equal deletion evidence produced a different transcript digest")
	}
	binding.TargetID = uuid.New()
	if changed := objectEvidenceTranscriptDigest(key, binding, lease, evidence); bytes.Equal(first, changed) {
		t.Fatal("target identity was not bound into the transcript digest")
	}
	binding = objectTargetTestBinding()
	evidence.StableChecks++
	if changed := objectEvidenceTranscriptDigest(key, binding, lease, evidence); bytes.Equal(first, changed) {
		t.Fatal("stable absence evidence was not bound into the transcript digest")
	}
}
