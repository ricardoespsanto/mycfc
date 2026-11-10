package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	trainingPublicationReviewTTL              = 15 * time.Minute
	trainingPublicationReviewMaxTokenBytes    = 512
	trainingPublicationReviewMaxTuples        = 4096
	trainingPublicationReviewMaxSnapshotBytes = 32 << 20
	trainingPublicationReviewPurpose          = "mycfc/training-publication-review/v1"
)

var (
	errTrainingPublicationReviewInvalid = errors.New("training publication review invalid")
	errTrainingPublicationReviewChanged = errors.New("training publication review changed")
	errTrainingPublicationReviewLimit   = errors.New("training publication review limit exceeded")
)

// This context carries no URL. Its relationships and current authorization must
// be resolved by the caller before signing and again before using a return link.
// The zero value means the planner collection; other contexts select this plan.
type trainingPublicationReturnContext struct {
	GroupID, WeekID, SessionID uuid.UUID
	Day                        string
}

type trainingPublicationReviewMetadata struct {
	ActorID, PlanID     uuid.UUID
	SourceUpdatedAt     time.Time
	LatestPublicationID uuid.UUID // Nil and revision zero explicitly mean none.
	LatestRevision      int32
	ChangeSummary       string
	Return              trainingPublicationReturnContext
}

type trainingPublicationReviewInput struct {
	trainingPublicationReviewMetadata
	Prescriptions []StructuredPrescriptionInput
}

// Verified claims do not contain recipients, snapshot content or signing keys.
// They prove only review integrity, never current permission to publish. A save
// must rebuild all current inputs transactionally and call matches immediately
// before its final authorization decision. No database or no-op decision lives here.
type trainingPublicationReviewClaims struct {
	metadata               trainingPublicationReviewMetadata
	issuedAt, expiresAt    time.Time
	metadataDigest, digest [sha256.Size]byte
	verified               bool
}

type trainingPublicationReviewSigner struct {
	key         [32]byte
	initialized bool
}

var trainingPublicationReviews = func() trainingPublicationReviewSigner {
	signer, err := newTrainingPublicationReviewSigner()
	if err != nil {
		panic("training publication review signing key unavailable")
	}
	return signer
}()

func newTrainingPublicationReviewSigner() (trainingPublicationReviewSigner, error) {
	var signer trainingPublicationReviewSigner
	if _, err := rand.Read(signer.key[:]); err != nil {
		return signer, err
	}
	signer.initialized = true
	return signer, nil
}

// sign is pure apart from the caller-supplied signing key/time. Hashes are
// calculated from the exact raw canonical snapshot bytes, not JSONB text or a
// caller-declared hash. Reordering the input set does not change its binding.
func (s trainingPublicationReviewSigner) sign(input trainingPublicationReviewInput, now time.Time) (string, error) {
	if !s.initialized || !publicationReviewTimeValid(now) || now.Unix() < 0 || !publicationReviewTimeValid(now.Add(trainingPublicationReviewTTL)) {
		return "", errTrainingPublicationReviewInvalid
	}
	_, metadataDigest, digest, err := trainingPublicationReviewBinding(input)
	if err != nil {
		return "", err
	}
	// Fixed payload: issued/expiry seconds, metadata digest, complete set digest.
	// Names, IDs, summaries and tuple counts are only inside opaque hashes.
	payload := make([]byte, 16+2*sha256.Size)
	binary.BigEndian.PutUint64(payload[:8], uint64(now.Unix()))
	binary.BigEndian.PutUint64(payload[8:16], uint64(now.Add(trainingPublicationReviewTTL).Unix()))
	copy(payload[16:48], metadataDigest[:])
	copy(payload[48:], digest[:])
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte(trainingPublicationReviewPurpose + "." + encoded))
	token := "tpr1." + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > trainingPublicationReviewMaxTokenBytes {
		return "", errTrainingPublicationReviewLimit
	}
	return token, nil
}

// verify binds the request's approved metadata without requiring it to send the
// tuple list back. The transaction will rebuild that list for claims.matches.
func (s trainingPublicationReviewSigner) verify(token string, expected trainingPublicationReviewMetadata, now time.Time) (trainingPublicationReviewClaims, error) {
	var claims trainingPublicationReviewClaims
	if !s.initialized || !publicationReviewTimeValid(now) || len(token) > trainingPublicationReviewMaxTokenBytes {
		return claims, errTrainingPublicationReviewInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "tpr1" {
		return claims, errTrainingPublicationReviewInvalid
	}
	// Strict decoding still ignores CR/LF. Require the exact canonical spelling
	// of both segments as well, including the independently encoded signature.
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(payload) != 16+2*sha256.Size || base64.RawURLEncoding.EncodeToString(payload) != parts[1] {
		return claims, errTrainingPublicationReviewInvalid
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size || base64.RawURLEncoding.EncodeToString(signature) != parts[2] {
		return claims, errTrainingPublicationReviewInvalid
	}
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte(trainingPublicationReviewPurpose + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return claims, errTrainingPublicationReviewInvalid
	}
	issued, expiry := binary.BigEndian.Uint64(payload[:8]), binary.BigEndian.Uint64(payload[8:16])
	current := now.Unix()
	if current < 0 || issued > uint64(current) || expiry <= uint64(current) || expiry <= issued || expiry-issued != uint64(trainingPublicationReviewTTL/time.Second) {
		return claims, errTrainingPublicationReviewInvalid
	}
	normalized, expectedDigest, err := trainingPublicationMetadataBinding(expected)
	if err != nil || subtle.ConstantTimeCompare(payload[16:48], expectedDigest[:]) != 1 {
		return claims, errTrainingPublicationReviewInvalid
	}
	claims.metadata, claims.expiresAt = normalized, time.Unix(int64(expiry), 0).UTC()
	claims.issuedAt = time.Unix(int64(issued), 0).UTC()
	copy(claims.metadataDigest[:], payload[16:48])
	copy(claims.digest[:], payload[48:])
	claims.verified = true
	return claims, nil
}

func (c trainingPublicationReviewClaims) matches(current trainingPublicationReviewInput, now time.Time) error {
	if !c.verified || !publicationReviewTimeValid(now) || !now.Before(c.expiresAt) || now.Before(c.issuedAt) {
		return errTrainingPublicationReviewInvalid
	}
	_, metadataDigest, digest, err := trainingPublicationReviewBinding(current)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(c.metadataDigest[:], metadataDigest[:]) != 1 || subtle.ConstantTimeCompare(c.digest[:], digest[:]) != 1 {
		return errTrainingPublicationReviewChanged
	}
	return nil
}

func trainingPublicationMetadataBinding(input trainingPublicationReviewMetadata) (trainingPublicationReviewMetadata, [sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	if input.ActorID == uuid.Nil || input.PlanID == uuid.Nil || !publicationReviewTimeValid(input.SourceUpdatedAt) || input.LatestRevision < 0 || (input.LatestPublicationID == uuid.Nil) != (input.LatestRevision == 0) {
		return input, zero, errTrainingPublicationReviewInvalid
	}
	if !utf8.ValidString(input.ChangeSummary) || strings.ContainsRune(input.ChangeSummary, 0) {
		return input, zero, errTrainingPublicationReviewInvalid
	}
	input.ChangeSummary = strings.TrimSpace(input.ChangeSummary)
	if !validTrainingText(input.ChangeSummary, 2, 500) {
		return input, zero, errTrainingPublicationReviewInvalid
	}
	// pgx binary timestamptz encodes Unix seconds plus nanoseconds/1000.
	input.SourceUpdatedAt = time.UnixMicro(input.SourceUpdatedAt.UnixMicro()).UTC()
	c := input.Return
	if c.GroupID == uuid.Nil {
		if c.WeekID != uuid.Nil || c.SessionID != uuid.Nil || c.Day != "" {
			return input, zero, errTrainingPublicationReviewInvalid
		}
	} else if c.WeekID != input.PlanID {
		return input, zero, errTrainingPublicationReviewInvalid
	}
	if c.Day != "" {
		date, err := time.Parse("2006-01-02", c.Day)
		if err != nil || date.Year() < 1 || date.Format("2006-01-02") != c.Day {
			return input, zero, errTrainingPublicationReviewInvalid
		}
	}
	h := sha256.New()
	publicationReviewWriteField(h, []byte(trainingPublicationReviewPurpose+"/metadata"))
	_, _ = h.Write(input.ActorID[:])
	_, _ = h.Write(input.PlanID[:])
	publicationReviewWriteUint64(h, uint64(input.SourceUpdatedAt.UnixMicro()))
	// Explicit latest absence is encoded alongside fixed ID/revision fields.
	if input.LatestPublicationID == uuid.Nil {
		_, _ = h.Write([]byte{0})
	} else {
		_, _ = h.Write([]byte{1})
	}
	_, _ = h.Write(input.LatestPublicationID[:])
	publicationReviewWriteUint64(h, uint64(input.LatestRevision))
	summary := sha256.Sum256([]byte(input.ChangeSummary))
	_, _ = h.Write(summary[:])
	_, _ = h.Write(c.GroupID[:])
	_, _ = h.Write(c.WeekID[:])
	_, _ = h.Write(c.SessionID[:])
	publicationReviewWriteField(h, []byte(c.Day))
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return input, digest, nil
}

func trainingPublicationReviewBinding(input trainingPublicationReviewInput) (trainingPublicationReviewMetadata, [sha256.Size]byte, [sha256.Size]byte, error) {
	metadata, metadataDigest, err := trainingPublicationMetadataBinding(input.trainingPublicationReviewMetadata)
	var zero [sha256.Size]byte
	if err != nil {
		return metadata, zero, zero, err
	}
	if len(input.Prescriptions) > trainingPublicationReviewMaxTuples {
		return metadata, zero, zero, errTrainingPublicationReviewLimit
	}
	type tuple struct {
		session, membership, subject, placement uuid.UUID
		hash                                    [sha256.Size]byte
	}
	tuples := make([]tuple, 0, len(input.Prescriptions))
	subjects := make(map[uuid.UUID]uuid.UUID, len(input.Prescriptions))
	total := 0
	for _, p := range input.Prescriptions {
		if p.SessionID == uuid.Nil || p.MembershipID == uuid.Nil || p.AthleteUserID == uuid.Nil || p.PlacementID == uuid.Nil {
			return metadata, zero, zero, errTrainingPublicationReviewInvalid
		}
		if previous, ok := subjects[p.MembershipID]; ok && previous != p.AthleteUserID {
			return metadata, zero, zero, errTrainingPublicationReviewInvalid
		}
		subjects[p.MembershipID] = p.AthleteUserID
		if len(p.Snapshot) > trainingPublicationReviewMaxSnapshotBytes-total {
			return metadata, zero, zero, errTrainingPublicationReviewLimit
		}
		total += len(p.Snapshot)
		raw := bytes.TrimSpace(p.Snapshot)
		if len(raw) == 0 || raw[0] != '{' || !utf8.Valid(p.Snapshot) || !json.Valid(p.Snapshot) {
			return metadata, zero, zero, errTrainingPublicationReviewInvalid
		}
		digest := sha256.Sum256(p.Snapshot)
		if p.SnapshotSHA256 != "" {
			declared, err := hex.DecodeString(p.SnapshotSHA256)
			if err != nil || len(declared) != sha256.Size || p.SnapshotSHA256 != strings.ToLower(p.SnapshotSHA256) || subtle.ConstantTimeCompare(declared, digest[:]) != 1 {
				return metadata, zero, zero, errTrainingPublicationReviewInvalid
			}
		}
		tuples = append(tuples, tuple{p.SessionID, p.MembershipID, p.AthleteUserID, p.PlacementID, digest})
	}
	sort.Slice(tuples, func(i, j int) bool {
		a, b := tuples[i], tuples[j]
		if n := bytes.Compare(a.session[:], b.session[:]); n != 0 {
			return n < 0
		}
		return bytes.Compare(a.membership[:], b.membership[:]) < 0
	})
	h := sha256.New()
	publicationReviewWriteField(h, []byte(trainingPublicationReviewPurpose+"/set"))
	_, _ = h.Write(metadataDigest[:])
	publicationReviewWriteUint64(h, uint64(len(tuples)))
	for i, t := range tuples {
		if i > 0 && tuples[i-1].session == t.session && tuples[i-1].membership == t.membership {
			return metadata, zero, zero, errTrainingPublicationReviewInvalid
		}
		_, _ = h.Write(t.session[:])
		_, _ = h.Write(t.membership[:])
		_, _ = h.Write(t.subject[:])
		_, _ = h.Write(t.placement[:])
		_, _ = h.Write(t.hash[:])
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return metadata, metadataDigest, digest, nil
}

func publicationReviewTimeValid(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999
}
func publicationReviewWriteUint64(h hash.Hash, value uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], value)
	_, _ = h.Write(b[:])
}
func publicationReviewWriteField(h hash.Hash, value []byte) {
	publicationReviewWriteUint64(h, uint64(len(value)))
	_, _ = h.Write(value)
}
