package mediauploads

import (
	"context"
	"encoding/json"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type uploadIntentQueries interface {
	BeginMediaUploadIntent(context.Context, dbgen.BeginMediaUploadIntentParams) ([]byte, error)
	FinalizeMediaUploadIntent(context.Context, dbgen.FinalizeMediaUploadIntentParams) error
	ConfirmMediaUploadPut(context.Context, dbgen.ConfirmMediaUploadPutParams) error
	MarkMediaUploadCleanup(context.Context, dbgen.MarkMediaUploadCleanupParams) error
}

type PostgresUploadIntentStore struct{ Queries uploadIntentQueries }

func (s PostgresUploadIntentStore) Begin(ctx context.Context, input UploadIntentBegin) (UploadIntentClock, error) {
	var clock UploadIntentClock
	if s.Queries == nil {
		return clock, ErrUploadProvenanceUnavailable
	}
	payload, err := s.Queries.BeginMediaUploadIntent(ctx, dbgen.BeginMediaUploadIntentParams{
		IntentID: input.IntentID, SubjectUserID: input.SubjectUserID, ActorUserID: input.ActorUserID, SourceKind: input.SourceKind,
		SourceRef: input.SourceRef, ServiceCode: input.ServiceCode, ContentType: input.ContentType, SizeBytes: input.SizeBytes, HoldToken: input.HoldToken,
	})
	if err != nil {
		return clock, err
	}
	var encoded struct {
		CreatedAt    time.Time `json:"created_at"`
		CleanupAfter time.Time `json:"cleanup_after"`
		HoldEpoch    int64     `json:"hold_epoch"`
	}
	if err = json.Unmarshal(payload, &encoded); err != nil || encoded.CreatedAt.IsZero() || !encoded.CleanupAfter.Equal(encoded.CreatedAt.Add(24*time.Hour)) || encoded.HoldEpoch < 1 {
		return clock, ErrUploadProvenanceFailed
	}
	return UploadIntentClock(encoded), nil
}

func (s PostgresUploadIntentStore) Finalize(ctx context.Context, record UploadIntentProtectedRecord) error {
	if s.Queries == nil {
		return ErrUploadProvenanceUnavailable
	}
	return s.Queries.FinalizeMediaUploadIntent(ctx, dbgen.FinalizeMediaUploadIntentParams{
		IntentID: record.IntentID, HoldToken: record.HoldToken, EnvelopeVersion: record.Envelope.Version, Algorithm: record.Envelope.Algorithm,
		EncryptionKeyID: record.Envelope.KeyID, Encapsulation: record.Envelope.Encapsulation, Nonce: record.Envelope.Nonce, Ciphertext: record.Envelope.Ciphertext,
		DigestKeyID: record.Digest.KeyID, LocatorDigest: record.Digest.Digest, LocatorCommitment: record.LocatorCommitment,
	})
}

func (s PostgresUploadIntentStore) ConfirmPut(ctx context.Context, intentID uuid.UUID, holdToken []byte) error {
	if s.Queries == nil {
		return ErrUploadProvenanceUnavailable
	}
	return s.Queries.ConfirmMediaUploadPut(ctx, dbgen.ConfirmMediaUploadPutParams{IntentID: intentID, HoldToken: holdToken})
}

func (s PostgresUploadIntentStore) MarkCleanup(ctx context.Context, intentID uuid.UUID, holdToken []byte, reason string) error {
	if s.Queries == nil {
		return ErrUploadProvenanceUnavailable
	}
	return s.Queries.MarkMediaUploadCleanup(ctx, dbgen.MarkMediaUploadCleanupParams{IntentID: intentID, HoldToken: holdToken, ReasonCode: reason})
}
