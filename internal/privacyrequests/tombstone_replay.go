package privacyrequests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrTombstoneReplayInvalid     = errors.New("privacy restore replay invalid")
	ErrTombstoneReplayUnavailable = errors.New("privacy restore replay unavailable")
)

// ListedTombstoneObject is the non-identifying material available to an
// offline recovery process after listing and reading the independent ledger.
// V2 AEAD binds the opaque locator, so no execution UUID is needed to open it.
type ListedTombstoneObject struct {
	Kind          string
	LocatorKeyID  string
	LocatorDigest []byte
	Payload       []byte
	Checksum      []byte
	ObjectVersion string
	WrittenAt     time.Time
	VerifiedAt    time.Time
	RetainUntil   time.Time
}

// AuthenticatedReplayTombstone has no public fields so callers cannot bypass
// AEAD verification and construct an importable prescription directly.
type AuthenticatedReplayTombstone struct {
	kind                   string
	locatorKeyID           string
	locatorDigest          []byte
	ciphertextSHA256       []byte
	objectVersion          string
	writtenAt              time.Time
	verifiedAt             time.Time
	retainUntil            time.Time
	effectiveAt            time.Time
	closureVersion         string
	envelopeVersion        string
	encryptionKeyID        string
	record                 RestoreTombstone
	prescriptionSHA256     []byte
	basePrescriptionSHA256 []byte
	recordSHA256           []byte
}

func (a AuthenticatedReplayTombstone) OpaqueReplayID() string {
	digest := sha256.Sum256(append([]byte("mycfc/privacy-restore-replay-id/v1\x00"), a.record.ExecutionID[:]...))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (a AuthenticatedReplayTombstone) IsClosure() bool { return a.kind == "closure" }
func (a AuthenticatedReplayTombstone) IsCurrentClosure() bool {
	return a.kind == "closure" && a.closureVersion == TombstoneClosureVersion
}
func (a AuthenticatedReplayTombstone) IsSynthetic() bool {
	return a.record.SyntheticFixture == SyntheticRestoreFixtureV1
}

// SameReplay reports whether two independently authenticated ledger objects
// prescribe the same source erasure. It deliberately exposes no identity.
func (a AuthenticatedReplayTombstone) SameReplay(other AuthenticatedReplayTombstone) bool {
	return a.record.ExecutionID == other.record.ExecutionID && a.record.RequestID == other.record.RequestID &&
		a.record.RequestRef == other.record.RequestRef && a.record.SubjectUserID == other.record.SubjectUserID &&
		a.record.ExecutionStart.Equal(other.record.ExecutionStart) && hmac.Equal(a.record.PlanSHA256, other.record.PlanSHA256) &&
		hmac.Equal(a.record.WorksetSHA256, other.record.WorksetSHA256) && hmac.Equal(a.basePrescriptionSHA256, other.basePrescriptionSHA256)
}

func AuthenticateReplayTombstone(privateKey []byte, listed ListedTombstoneObject) (AuthenticatedReplayTombstone, error) {
	var zero AuthenticatedReplayTombstone
	digest := sha256.Sum256(listed.Payload)
	if len(listed.Payload) == 0 || len(listed.Payload) > maxTombstonePayloadBytes ||
		len(listed.Checksum) != sha256.Size || !hmac.Equal(digest[:], listed.Checksum) || listed.ObjectVersion == "" ||
		listed.ObjectVersion != strings.TrimSpace(listed.ObjectVersion) || len(listed.ObjectVersion) > 1024 ||
		listed.WrittenAt.IsZero() || listed.VerifiedAt.IsZero() || listed.VerifiedAt.Before(listed.WrittenAt) ||
		(listed.Kind != "" && listed.Kind != "intent" && listed.Kind != "closure") {
		return zero, ErrTombstoneReplayInvalid
	}
	var envelope TombstoneEnvelope
	decoder := json.NewDecoder(bytes.NewReader(listed.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || ensureJSONEOF(decoder) != nil || envelope.Version != TombstoneEnvelopeVersion {
		return zero, ErrTombstoneReplayInvalid
	}
	kind, locatorKeyID, locatorDigest := envelope.Kind, envelope.LocatorKeyID, envelope.LocatorDigest
	if (listed.Kind != "" && listed.Kind != kind) || (listed.LocatorKeyID != "" && listed.LocatorKeyID != locatorKeyID) ||
		(len(listed.LocatorDigest) != 0 && !hmac.Equal(listed.LocatorDigest, locatorDigest)) ||
		(kind == "intent" && !listed.RetainUntil.IsZero()) ||
		(kind == "closure" && (listed.RetainUntil.IsZero() || listed.VerifiedAt.After(listed.RetainUntil))) {
		return zero, ErrTombstoneReplayInvalid
	}
	plaintext, err := openTombstoneEnvelopeV2(privateKey, kind, locatorKeyID, locatorDigest, envelope)
	if err != nil {
		return zero, ErrTombstoneReplayInvalid
	}
	var record RestoreTombstone
	var effectiveAt time.Time
	closureVersion := ""
	if kind == "intent" {
		if err = decodeStrictJSON(plaintext, &record); err != nil || !validReplayableRestoreTombstone(record) || record.Replay.MembershipHistoryPostcondition != nil {
			return zero, ErrTombstoneReplayInvalid
		}
		effectiveAt = record.ExecutionStart
	} else {
		var closure TombstoneClosure
		if err = decodeStrictJSON(plaintext, &closure); err != nil || (closure.Version != TombstoneClosureVersionV2 && closure.Version != TombstoneClosureVersionV3 && closure.Version != TombstoneClosureVersion) ||
			!validReplayableRestoreTombstone(closure.Tombstone) || closure.ClosedAt.IsZero() ||
			!closure.EvidenceExpiresAt.Equal(closure.ClosedAt.AddDate(0, 24, 0)) || !listed.RetainUntil.Equal(closure.EvidenceExpiresAt) ||
			(closure.Version == TombstoneClosureVersionV2 && (!closure.ErasureEffectiveAt.IsZero() || closure.Tombstone.Replay.MembershipHistoryPostcondition != nil)) ||
			(closure.Version == TombstoneClosureVersionV3 && (closure.ErasureEffectiveAt.IsZero() || closure.ErasureEffectiveAt.Before(closure.Tombstone.ExecutionStart) || closure.ErasureEffectiveAt.After(closure.ClosedAt) || closure.Tombstone.Replay.MembershipHistoryPostcondition != nil)) ||
			(closure.Version == TombstoneClosureVersion && (closure.ErasureEffectiveAt.IsZero() || closure.ErasureEffectiveAt.Before(closure.Tombstone.ExecutionStart) || closure.ErasureEffectiveAt.After(closure.ClosedAt) || !validMembershipHistoryPostcondition(closure.Tombstone.Replay.MembershipHistoryPostcondition))) {
			return zero, ErrTombstoneReplayInvalid
		}
		record = closure.Tombstone
		closureVersion = closure.Version
		if closure.Version != TombstoneClosureVersionV2 {
			effectiveAt = closure.ErasureEffectiveAt
		} else {
			effectiveAt = record.ExecutionStart
		}
	}
	prescription, err := json.Marshal(record.Replay)
	if err != nil {
		return zero, ErrTombstoneReplayInvalid
	}
	prescriptionDigest := sha256.Sum256(prescription)
	basePrescription, err := json.Marshal(RelationalReplayPrescription{
		Version: record.Replay.Version, ActionVersion: record.Replay.ActionVersion, Operations: record.Replay.Operations,
	})
	if err != nil {
		return zero, ErrTombstoneReplayInvalid
	}
	basePrescriptionDigest := sha256.Sum256(basePrescription)
	recordDigest := sha256.Sum256(plaintext)
	return AuthenticatedReplayTombstone{
		kind: kind, locatorKeyID: locatorKeyID, locatorDigest: bytes.Clone(locatorDigest),
		ciphertextSHA256: bytes.Clone(listed.Checksum), objectVersion: listed.ObjectVersion,
		writtenAt: listed.WrittenAt.UTC(), verifiedAt: listed.VerifiedAt.UTC(), retainUntil: listed.RetainUntil.UTC(), effectiveAt: effectiveAt.UTC(), closureVersion: closureVersion,
		envelopeVersion: envelope.Version, encryptionKeyID: envelope.KeyID, record: record,
		prescriptionSHA256: prescriptionDigest[:], basePrescriptionSHA256: basePrescriptionDigest[:], recordSHA256: recordDigest[:],
	}, nil
}

func decodeStrictJSON(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

// TombstoneReplayWorker is intentionally database-only. It never receives an
// S3 or provider client; ledger discovery and AEAD authentication must finish
// before this offline operator path is constructed.
type TombstoneReplayWorker struct {
	Store     dbgen.DBTX
	WorkerRef uuid.UUID
}

type TombstoneReplayResult struct {
	RunID                          uuid.UUID
	AlreadyApplied                 bool
	Synthetic                      bool
	ClosureVersion                 string
	MembershipHistoryPostcondition MembershipHistoryPostcondition
}

func (w TombstoneReplayWorker) Replay(ctx context.Context, authenticated AuthenticatedReplayTombstone) (TombstoneReplayResult, error) {
	if w.Store == nil || w.WorkerRef == uuid.Nil || !validAuthenticatedReplayTombstone(authenticated) {
		return TombstoneReplayResult{}, ErrTombstoneReplayInvalid
	}
	q := dbgen.New(w.Store)
	retainUntil := pgtype.Timestamptz{}
	if authenticated.kind == "closure" {
		retainUntil = stamp(authenticated.retainUntil)
	}
	var syntheticFixture *string
	if authenticated.record.SyntheticFixture != "" {
		syntheticFixture = &authenticated.record.SyntheticFixture
	}
	postcondition := authenticated.record.Replay.MembershipHistoryPostcondition
	if authenticated.kind != "closure" || authenticated.closureVersion != TombstoneClosureVersion || !validMembershipHistoryPostcondition(postcondition) {
		return TombstoneReplayResult{}, ErrTombstoneReplayInvalid
	}
	importID, err := q.ImportAuthenticatedPrivacyRestoreTombstoneV4Hardened(ctx, dbgen.ImportAuthenticatedPrivacyRestoreTombstoneV4HardenedParams{
		WorkerRef: w.WorkerRef, Kind: authenticated.kind, RecordVersion: authenticated.record.Version,
		EnvelopeVersion: authenticated.envelopeVersion, EncryptionKeyID: authenticated.encryptionKeyID,
		LocatorKeyID: authenticated.locatorKeyID, LocatorDigest: bytes.Clone(authenticated.locatorDigest),
		CiphertextSha256: bytes.Clone(authenticated.ciphertextSHA256), ObjectVersionID: authenticated.objectVersion,
		WrittenAt: stamp(authenticated.writtenAt), VerifiedAt: stamp(authenticated.verifiedAt), RetainUntil: retainUntil,
		SourceExecutionID: authenticated.record.ExecutionID, SourceRequestID: authenticated.record.RequestID,
		SourceRequestRef: authenticated.record.RequestRef, SubjectUserID: authenticated.record.SubjectUserID,
		PlanSha256: bytes.Clone(authenticated.record.PlanSHA256), WorksetSha256: bytes.Clone(authenticated.record.WorksetSHA256),
		ExecutionStartedAt: stamp(authenticated.record.ExecutionStart), ReplayVersion: authenticated.record.Replay.Version,
		ErasureEffectiveAt: stamp(authenticated.effectiveAt), SyntheticFixture: syntheticFixture,
		ClosureVersion: authenticated.closureVersion,
		ActionVersion:  authenticated.record.Replay.ActionVersion, Operations: append([]string(nil), authenticated.record.Replay.Operations...),
		PrescriptionSha256: bytes.Clone(authenticated.prescriptionSHA256), RecordSha256: bytes.Clone(authenticated.recordSHA256),
		MembershipPostconditionContract: postcondition.Contract, MembershipPostconditionSha256: bytes.Clone(postcondition.SHA256),
		MembershipCount: int64(postcondition.MembershipCount), VariationCount: int64(postcondition.VariationCount),
	})
	if err != nil {
		return TombstoneReplayResult{}, ErrTombstoneReplayUnavailable
	}
	run, err := q.BeginPrivacyRestoreReplayHardened(ctx, dbgen.BeginPrivacyRestoreReplayHardenedParams{ImportID: importID, WorkerRef: w.WorkerRef})
	if err != nil {
		return TombstoneReplayResult{}, ErrTombstoneReplayUnavailable
	}
	if run.OutcomeCode != "" {
		return w.verifiedResult(ctx, q, run.BegunRunID, run.OutcomeCode == "ALREADY_APPLIED_SOURCE", authenticated)
	}
	for index, operation := range authenticated.record.Replay.Operations {
		if _, err = q.ExecutePrivacyRestoreReplayCheckpoint(ctx, dbgen.ExecutePrivacyRestoreReplayCheckpointParams{
			RunID: run.BegunRunID, WorkerRef: w.WorkerRef, OperationPosition: int16(index + 1), OperationCode: operation,
			ActionVersion: authenticated.record.Replay.ActionVersion, PrescriptionSha256: bytes.Clone(authenticated.prescriptionSHA256),
		}); err != nil {
			return TombstoneReplayResult{}, ErrTombstoneReplayUnavailable
		}
	}
	return w.verifiedResult(ctx, q, run.BegunRunID, false, authenticated)
}

func (w TombstoneReplayWorker) verifiedResult(ctx context.Context, q *dbgen.Queries, runID uuid.UUID, alreadyApplied bool, authenticated AuthenticatedReplayTombstone) (TombstoneReplayResult, error) {
	row, err := q.GetPrivacyRestoreMembershipPostcondition(ctx, runID)
	expected := authenticated.record.Replay.MembershipHistoryPostcondition
	if err != nil || row.PostconditionMembershipPostconditionContract != expected.Contract || row.PostconditionMembershipCount < 0 || row.PostconditionVariationCount < 0 ||
		uint64(row.PostconditionMembershipCount) != expected.MembershipCount || uint64(row.PostconditionVariationCount) != expected.VariationCount ||
		subtle.ConstantTimeCompare(row.PostconditionMembershipPostconditionSha256, expected.SHA256) != 1 {
		return TombstoneReplayResult{}, ErrTombstoneReplayUnavailable
	}
	return TombstoneReplayResult{RunID: runID, AlreadyApplied: alreadyApplied, Synthetic: authenticated.IsSynthetic(),
		ClosureVersion: authenticated.closureVersion, MembershipHistoryPostcondition: *expected}, nil
}

func validAuthenticatedReplayTombstone(authenticated AuthenticatedReplayTombstone) bool {
	return (authenticated.kind == "intent" || authenticated.kind == "closure") &&
		authenticated.envelopeVersion == TombstoneEnvelopeVersion && policyKey.MatchString(authenticated.encryptionKeyID) &&
		policyKey.MatchString(authenticated.locatorKeyID) && len(authenticated.locatorDigest) == sha256.Size &&
		len(authenticated.ciphertextSHA256) == sha256.Size && len(authenticated.prescriptionSHA256) == sha256.Size &&
		len(authenticated.basePrescriptionSHA256) == sha256.Size && len(authenticated.recordSHA256) == sha256.Size && validReplayableRestoreTombstone(authenticated.record) &&
		authenticated.objectVersion != "" && !authenticated.writtenAt.IsZero() && !authenticated.effectiveAt.IsZero() && !authenticated.verifiedAt.Before(authenticated.writtenAt) &&
		((authenticated.kind == "intent" && authenticated.retainUntil.IsZero()) ||
			(authenticated.kind == "closure" && !authenticated.retainUntil.IsZero() && !authenticated.verifiedAt.After(authenticated.retainUntil)))
}
