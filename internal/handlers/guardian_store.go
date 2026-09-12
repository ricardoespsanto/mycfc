package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

type PostgresGuardianDependentStore struct {
	Pool db.Beginner
	Key  []byte
}

func (s PostgresGuardianDependentStore) ReserveAttempt(ctx context.Context, actorID uuid.UUID, ip *netip.Addr, kind string) error {
	if len(s.Key) < 32 {
		return errors.New("guardian application key unavailable")
	}
	accountDigest := guardianApplicationDigest(s.Key, "account", actorID.String())
	network := guardianApplicationNetworkBucket(ip)
	networkDigest := guardianApplicationDigest(s.Key, "network", network)
	err := db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return dbgen.New(tx).ReserveGuardianApplicationRate(ctx, dbgen.ReserveGuardianApplicationRateParams{AccountDigest: accountDigest, NetworkDigest: networkDigest, Kind: kind})
	})
	return guardianAuthorityStoreError(err)
}

// Guardian application network buckets retain exact IPv4 separation while
// grouping IPv6 addresses by /64, avoiding address-level evidence for common
// privacy-address rotations.
func guardianApplicationNetworkBucket(ip *netip.Addr) string {
	if ip == nil || !ip.IsValid() {
		return "unavailable"
	}
	address := ip.WithZone("").Unmap()
	if address.Is4() {
		return address.String()
	}
	return netip.PrefixFrom(address, 64).Masked().String()
}

func (s PostgresGuardianDependentStore) Reauthenticate(ctx context.Context, actorID uuid.UUID, password string) error {
	return db.WithinTx(ctx, s.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		account, err := dbgen.New(tx).GetUserByID(ctx, actorID)
		if err != nil {
			return err
		}
		if !account.IsActive || account.IsDependent || account.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*account.PasswordHash), []byte(password)) != nil {
			return ErrGuardianAuthentication
		}
		return nil
	})
}

func guardianApplicationDigest(key []byte, domain, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("guardian-application-" + domain + "/v1:" + value))
	return mac.Sum(nil)
}

func guardianInvitationDigest(key []byte, token string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("guardian-invitation-token/v1:" + token))
	return mac.Sum(nil)
}

func (s PostgresGuardianDependentStore) CreateDependent(ctx context.Context, input GuardianDependentInput) error {
	err := db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		var invitationDigest []byte
		if input.InvitationToken != "" {
			if len(s.Key) < 32 {
				return errors.New("guardian invitation key unavailable")
			}
			invitationDigest = guardianInvitationDigest(s.Key, input.InvitationToken)
		}
		dependent, err := queries.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{
			Name: input.Name, GuardianID: input.GuardianID,
			DateOfBirth:      pgtype.Date{Time: input.DateOfBirth, Valid: true},
			InvitationDigest: invitationDigest,
		})
		if err != nil {
			return err
		}
		_, err = queries.CreateConsentForm(ctx, dbgen.CreateConsentFormParams{
			UserID: dependent.ID, GrantedByUserID: &input.GuardianID, ConsentType: "Responsabilidade_Menor",
			DocumentVersion: input.ResponsibilityVersion, DocumentSha256: input.ResponsibilitySHA256,
			IpAddress: input.IP, UserAgent: input.UserAgent,
		})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrGuardianApplicantIneligible
	}
	return guardianAuthorityStoreError(err)
}

type PostgresGuardianAuthorityStore struct {
	DB dbgen.DBTX
}

func (s PostgresGuardianAuthorityStore) PolicyAvailable(ctx context.Context) (bool, error) {
	types, err := dbgen.New(s.DB).ListActiveGuardianAuthorityEvidenceTypes(ctx)
	return len(types) > 0, err
}

func (s PostgresGuardianAuthorityStore) EvidenceTypes(ctx context.Context) ([]GuardianAuthorityEvidenceType, error) {
	values, err := dbgen.New(s.DB).ListActiveGuardianAuthorityEvidenceTypes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]GuardianAuthorityEvidenceType, len(values))
	for i, value := range values {
		result[i] = GuardianAuthorityEvidenceType{Code: value, Label: guardianAuthorityCodeLabel(value)}
	}
	return result, nil
}

func (s PostgresGuardianAuthorityStore) ReasonCodes(ctx context.Context) ([]GuardianAuthorityReasonCode, error) {
	values, err := dbgen.New(s.DB).ListActiveGuardianAuthorityReasonCodes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]GuardianAuthorityReasonCode, len(values))
	for i, value := range values {
		result[i] = GuardianAuthorityReasonCode{Code: value, Label: guardianAuthorityCodeLabel(value)}
	}
	return result, nil
}

func (s PostgresGuardianAuthorityStore) ListForGuardian(ctx context.Context, guardianID uuid.UUID, limit int32) ([]GuardianAuthorityRelationship, error) {
	rows, err := dbgen.New(s.DB).ListGuardianRelationshipsForGuardian(ctx, dbgen.ListGuardianRelationshipsForGuardianParams{GuardianID: guardianID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	result := make([]GuardianAuthorityRelationship, len(rows))
	for i, row := range rows {
		result[i] = GuardianAuthorityRelationship{
			Reference: row.RelationshipRef, GuardianID: guardianID, SubjectID: row.SubjectUserID,
			SubmittedLabel: row.SubmittedLabel, SubjectName: row.SubjectName, State: row.State, Version: row.Version,
			CreatedAt: row.CreatedAt.Time, DateOfBirth: row.DateOfBirth.Time, VerifiedUntil: optionalTime(row.VerifiedUntil),
			ReviewDueAt: optionalTime(row.ReviewDueAt), Conflict: row.Conflict, MinorLoginIssued: row.MinorLoginID != "",
			LeaderboardVisible: row.LeaderboardVisible, ProfileComplete: row.ProfileComplete,
			RenewalReference: row.RenewalRef, RenewalResponse: stringValue(row.RenewalResponse), RenewalStatus: stringValue(row.RenewalStatus), RenewalExpiry: optionalTime(row.RenewalExpiryAnchor),
			AgeHandoffStatus: guardianAgeHandoffString(row.AgeHandoffStatus), AgeHandoffBirthday: optionalDate(row.AgeHandoffBirthday),
		}
	}
	return result, nil
}

func (s PostgresGuardianAuthorityStore) ListPending(ctx context.Context, actorID uuid.UUID, limit, offset int32) ([]GuardianAuthorityRelationship, error) {
	rows, err := dbgen.New(s.DB).ListPendingGuardianAuthorityRequests(ctx, dbgen.ListPendingGuardianAuthorityRequestsParams{ActorID: actorID, RowLimit: limit, RowOffset: offset})
	if err != nil {
		return nil, guardianAuthorityStoreError(err)
	}
	result := make([]GuardianAuthorityRelationship, len(rows))
	for i, row := range rows {
		result[i] = GuardianAuthorityRelationship{Reference: row.RelationshipRef, GuardianID: row.GuardianUserID, GuardianName: row.GuardianName, SubjectID: row.SubjectUserID, SubmittedLabel: row.SubjectName, SubjectName: row.SubjectName, State: row.State, Version: row.Version, CreatedAt: row.CreatedAt.Time, DateOfBirth: row.DateOfBirth.Time, VerifiedUntil: optionalTime(row.VerifiedUntil), ReviewDueAt: optionalTime(row.ReviewDueAt), Conflict: row.Conflict, ConflictActorID: row.ConflictActorRef, PersonalInvolvement: row.PersonalInvolvement, RenewalReference: row.RenewalRef, RenewalResponse: stringValue(row.RenewalResponse), RenewalStatus: stringValue(row.RenewalStatus), RenewalExpiry: optionalTime(row.RenewalExpiryAnchor)}
	}
	return result, nil
}

func (s PostgresGuardianAuthorityStore) GetForVerifier(ctx context.Context, reference, actorID uuid.UUID) (GuardianAuthorityRelationship, error) {
	row, err := dbgen.New(s.DB).GetGuardianAuthorityRequestForVerifier(ctx, dbgen.GetGuardianAuthorityRequestForVerifierParams{RelationshipRef: reference, ActorID: actorID})
	if err != nil {
		return GuardianAuthorityRelationship{}, guardianAuthorityStoreError(err)
	}
	return GuardianAuthorityRelationship{Reference: row.RelationshipRef, GuardianID: row.GuardianUserID, GuardianName: row.GuardianName, SubjectID: row.SubjectUserID, SubmittedLabel: row.SubmittedLabel, SubjectName: row.SubjectName, State: row.State, StoredState: row.StoredState, Version: row.Version, CreatedAt: row.CreatedAt.Time, DateOfBirth: row.DateOfBirth.Time, VerifiedUntil: optionalTime(row.VerifiedUntil), ReviewDueAt: optionalTime(row.ReviewDueAt), Conflict: row.Conflict, ConflictActorID: row.ConflictActorRef, PersonalInvolvement: row.PersonalInvolvement, RenewalReference: row.RenewalRef, RenewalResponse: stringValue(row.RenewalResponse), RenewalStatus: stringValue(row.RenewalStatus), RenewalExpiry: optionalTime(row.RenewalExpiryAnchor)}, nil
}

func (s PostgresGuardianAuthorityStore) Transition(ctx context.Context, input GuardianAuthorityTransitionInput) error {
	_, err := dbgen.New(s.DB).AdminTransitionGuardianAuthority(ctx, dbgen.AdminTransitionGuardianAuthorityParams{
		ActorID: input.ActorID, RelationshipRef: input.Reference, ExpectedVersion: input.ExpectedVersion,
		Action: input.Action, EvidenceCategory: input.EvidenceCategory, ReasonCode: input.ReasonCode,
	})
	return guardianAuthorityStoreError(err)
}

func (s PostgresGuardianAuthorityStore) IssueInvitation(ctx context.Context, actorID uuid.UUID, email string, digest []byte) (GuardianAuthorityInvitation, error) {
	row, err := dbgen.New(s.DB).IssueGuardianAuthorityInvitation(ctx, dbgen.IssueGuardianAuthorityInvitationParams{ActorID: actorID, InvitedEmail: email, TokenDigest: digest})
	if err != nil {
		return GuardianAuthorityInvitation{}, guardianAuthorityStoreError(err)
	}
	return GuardianAuthorityInvitation{Reference: row.PublicRef, Email: stringValue(row.InvitedEmail), IssuedAt: row.IssuedAt.Time, ExpiresAt: row.ExpiresAt.Time}, nil
}

func (s PostgresGuardianAuthorityStore) ListInvitations(ctx context.Context, actorID uuid.UUID, limit int32) ([]GuardianAuthorityInvitation, error) {
	rows, err := dbgen.New(s.DB).ListGuardianAuthorityInvitations(ctx, dbgen.ListGuardianAuthorityInvitationsParams{ActorID: actorID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	result := make([]GuardianAuthorityInvitation, len(rows))
	for i, row := range rows {
		result[i] = GuardianAuthorityInvitation{Reference: row.PublicRef, Email: stringValue(row.InvitedEmail), IssuedAt: row.IssuedAt.Time, ExpiresAt: row.ExpiresAt.Time, RevokedAt: optionalTime(row.RevokedAt), ConsumedAt: optionalTime(row.ConsumedAt)}
	}
	return result, nil
}

func (s PostgresGuardianAuthorityStore) RevokeInvitation(ctx context.Context, actorID, reference uuid.UUID) error {
	changed, err := dbgen.New(s.DB).RevokeGuardianAuthorityInvitation(ctx, dbgen.RevokeGuardianAuthorityInvitationParams{ActorID: actorID, PublicRef: reference})
	if err != nil {
		return guardianAuthorityStoreError(err)
	}
	if changed != 1 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s PostgresGuardianAuthorityStore) SubmitRenewal(ctx context.Context, actorID, reference uuid.UUID, expectedVersion int64, response string) error {
	_, err := dbgen.New(s.DB).SubmitGuardianAuthorityRenewal(ctx, dbgen.SubmitGuardianAuthorityRenewalParams{ActorID: actorID, RelationshipRef: reference, ExpectedVersion: expectedVersion, ResponseCode: response})
	return guardianAuthorityStoreError(err)
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func optionalDate(value pgtype.Date) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func guardianAuthorityCodeLabel(value string) string {
	labels := map[string]string{
		"IN_PERSON_IDENTITY":    "Identificação presencial",
		"COURT_ORDER":           "Decisão judicial",
		"BIRTH_CERTIFICATE":     "Certidão de nascimento",
		"APPROVED":              "Aprovado",
		"EVIDENCE_CONFIRMED":    "Comprovativo confirmado",
		"INSUFFICIENT_EVIDENCE": "Comprovativo insuficiente",
		"EVIDENCE_INSUFFICIENT": "Comprovativo insuficiente",
		"AUTHORITY_CHANGED":     "Alteração de responsabilidade",
		"CONFLICT":              "Conflito comunicado",
		"REVIEW_DUE":            "Revisão obrigatória",
		"VALIDITY_ENDED":        "Validade terminada",
		"NO_AUTHORITY":          "Autoridade não comprovada",
		"LOSS":                  "Perda de autoridade",
		"CHANGE":                "Alteração confirmada",
		"CIVIL_REGISTRY":        "Registo civil",
		"TEST_EVIDENCE":         "Comprovativo de teste",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return strings.ReplaceAll(value, "_", " ")
}

func guardianAuthorityStoreError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return err
	}
	switch databaseError.Message {
	case "guardian_authority_limit_reached":
		return ErrMaximumDependents
	case "guardian_authority_applicant_ineligible":
		return ErrGuardianApplicantIneligible
	case "guardian_authority_invitation_invalid":
		return ErrGuardianInvitationInvalid
	case "guardian_application_rate_limited":
		return ErrGuardianApplicationLimited
	case "guardian_authority_policy_unavailable", "guardian_application_intake_unreleased":
		return ErrGuardianAuthorityPolicyUnavailable
	case "guardian_authority_stale":
		return ErrGuardianAuthorityConflict
	case "guardian_authority_verifier_required", "guardian_authority_separation_required":
		return ErrGuardianAuthorityForbidden
	case "guardian_authority_transition_rejected", "guardian_authority_relationship_ineligible", "guardian_authority_not_due", "guardian_authority_evidence_rejected", "guardian_authority_reason_rejected", "guardian_authority_subject_rejected", "guardian_authority_renewal_not_due":
		return ErrGuardianAuthorityInvalid
	case "guardian_authority_renewal_already_submitted":
		return ErrGuardianAuthorityConflict
	case "guardian_authority_guardian_required":
		return ErrGuardianAuthorityForbidden
	default:
		return err
	}
}
