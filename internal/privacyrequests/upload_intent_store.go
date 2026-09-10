package privacyrequests

import (
	"context"
	"encoding/json"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type uploadIntentQueries interface {
	BeginPrivacyUploadIntent(context.Context, dbgen.BeginPrivacyUploadIntentParams) ([]byte, error)
	FinalizePrivacyUploadIntent(context.Context, dbgen.FinalizePrivacyUploadIntentParams) error
	ConfirmPrivacyUploadPut(context.Context, dbgen.ConfirmPrivacyUploadPutParams) error
	MarkPrivacyUploadCleanup(context.Context, dbgen.MarkPrivacyUploadCleanupParams) error
}

type PostgresUploadIntentStore struct{ Queries uploadIntentQueries }

func (s PostgresUploadIntentStore) Begin(ctx context.Context, input UploadIntentBegin) (UploadIntentClock, error) {
	var clock UploadIntentClock
	if s.Queries == nil {
		return clock, ErrUploadProvenanceUnavailable
	}
	payload, err := s.Queries.BeginPrivacyUploadIntent(ctx, dbgen.BeginPrivacyUploadIntentParams{
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
	return s.Queries.FinalizePrivacyUploadIntent(ctx, dbgen.FinalizePrivacyUploadIntentParams{
		IntentID: record.IntentID, HoldToken: record.HoldToken, EnvelopeVersion: record.Envelope.Version, Algorithm: record.Envelope.Algorithm,
		EncryptionKeyID: record.Envelope.KeyID, Encapsulation: record.Envelope.Encapsulation, Nonce: record.Envelope.Nonce, Ciphertext: record.Envelope.Ciphertext,
		DigestKeyID: record.Digest.KeyID, LocatorDigest: record.Digest.Digest, LocatorCommitment: record.LocatorCommitment,
	})
}

func (s PostgresUploadIntentStore) ConfirmPut(ctx context.Context, intentID uuid.UUID, holdToken []byte) error {
	if s.Queries == nil {
		return ErrUploadProvenanceUnavailable
	}
	return s.Queries.ConfirmPrivacyUploadPut(ctx, dbgen.ConfirmPrivacyUploadPutParams{IntentID: intentID, HoldToken: holdToken})
}

func (s PostgresUploadIntentStore) MarkCleanup(ctx context.Context, intentID uuid.UUID, holdToken []byte, reason string) error {
	if s.Queries == nil {
		return ErrUploadProvenanceUnavailable
	}
	return s.Queries.MarkPrivacyUploadCleanup(ctx, dbgen.MarkPrivacyUploadCleanupParams{IntentID: intentID, HoldToken: holdToken, ReasonCode: reason})
}
