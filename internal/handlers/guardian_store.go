package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresGuardianDependentStore struct {
	Pool db.Beginner
}

func (s PostgresGuardianDependentStore) CreateDependent(ctx context.Context, input GuardianDependentInput) error {
	err := db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		if _, err := queries.LockActiveAdult(ctx, input.GuardianID); err != nil {
			return err
		}
		count, err := queries.CountDependentsByGuardian(ctx, &input.GuardianID)
		if err != nil {
			return err
		}
		if count >= 10 {
			return ErrMaximumDependents
		}
		dependent, err := queries.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{
			Name: input.Name, GuardianID: input.GuardianID,
			DateOfBirth: pgtype.Date{Time: input.DateOfBirth, Valid: true},
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
		result[i] = GuardianAuthorityRelationship{Reference: row.RelationshipRef, GuardianID: row.GuardianUserID, GuardianName: row.GuardianName, SubjectID: row.SubjectUserID, SubmittedLabel: row.SubjectName, SubjectName: row.SubjectName, State: row.State, Version: row.Version, CreatedAt: row.CreatedAt.Time, DateOfBirth: row.DateOfBirth.Time, VerifiedUntil: optionalTime(row.VerifiedUntil), ReviewDueAt: optionalTime(row.ReviewDueAt), Conflict: row.Conflict, ConflictActorID: row.ConflictActorRef}
	}
	return result, nil
}

func (s PostgresGuardianAuthorityStore) GetForVerifier(ctx context.Context, reference, actorID uuid.UUID) (GuardianAuthorityRelationship, error) {
	row, err := dbgen.New(s.DB).GetGuardianAuthorityRequestForVerifier(ctx, dbgen.GetGuardianAuthorityRequestForVerifierParams{RelationshipRef: reference, ActorID: actorID})
	if err != nil {
		return GuardianAuthorityRelationship{}, guardianAuthorityStoreError(err)
	}
	return GuardianAuthorityRelationship{Reference: row.RelationshipRef, GuardianID: row.GuardianUserID, GuardianName: row.GuardianName, SubjectID: row.SubjectUserID, SubmittedLabel: row.SubmittedLabel, SubjectName: row.SubjectName, State: row.State, StoredState: row.StoredState, Version: row.Version, CreatedAt: row.CreatedAt.Time, DateOfBirth: row.DateOfBirth.Time, VerifiedUntil: optionalTime(row.VerifiedUntil), ReviewDueAt: optionalTime(row.ReviewDueAt), Conflict: row.Conflict, ConflictActorID: row.ConflictActorRef}, nil
}

func (s PostgresGuardianAuthorityStore) Transition(ctx context.Context, input GuardianAuthorityTransitionInput) error {
	targets := map[string]string{"VERIFY": "VERIFIED", "REJECT": "REJECTED", "SUSPEND": "SUSPENDED", "EXPIRE": "EXPIRED"}
	var evidenceType, evidenceReference, reasonCode *string
	if input.EvidenceType != "" {
		evidenceType = &input.EvidenceType
	}
	if input.EvidenceReference != "" {
		evidenceReference = &input.EvidenceReference
	}
	if input.ReasonCode != "" {
		reasonCode = &input.ReasonCode
	}
	_, err := dbgen.New(s.DB).TransitionGuardianAuthority(ctx, dbgen.TransitionGuardianAuthorityParams{
		ActorID: input.ActorID, RelationshipRef: input.Reference, ExpectedVersion: input.ExpectedVersion,
		TargetState: targets[input.Action], EvidenceType: evidenceType, EvidenceReference: evidenceReference,
		EvidenceSha256: input.EvidenceDigest, ReasonCode: reasonCode,
	})
	return guardianAuthorityStoreError(err)
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
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
	case "guardian_authority_policy_unavailable":
		return ErrGuardianAuthorityPolicyUnavailable
	case "guardian_authority_stale":
		return ErrGuardianAuthorityConflict
	case "guardian_authority_verifier_required", "guardian_authority_separation_required":
		return ErrGuardianAuthorityForbidden
	case "guardian_authority_transition_rejected", "guardian_authority_relationship_ineligible", "guardian_authority_not_due", "guardian_authority_evidence_rejected", "guardian_authority_reason_rejected", "guardian_authority_subject_rejected":
		return ErrGuardianAuthorityInvalid
	default:
		return err
	}
}
