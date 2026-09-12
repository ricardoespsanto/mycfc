package handlers

import (
	"context"
	"errors"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrGuardianAgeHandoffConflict  = errors.New("guardian age handoff changed concurrently")
	ErrGuardianAgeHandoffForbidden = errors.New("guardian age handoff forbidden")
	ErrGuardianAgeHandoffInvalid   = errors.New("guardian age handoff invalid")
	ErrGuardianAgeHandoffCollision = errors.New("guardian age handoff email collision")
)

type GuardianAgeHandoff struct {
	Reference           uuid.UUID
	SubjectName         string
	Status              string
	Version             int64
	Birthday            time.Time
	ProposedEmail       string
	EmailVerifiedAt     *time.Time
	IdentityConfirmedAt *time.Time
	ReadyAt             *time.Time
	RecoveryRequiredAt  *time.Time
	CompletedAt         *time.Time
	PersonalInvolvement bool
}

type GuardianAgeHandoffNotice struct {
	Kind      string
	Birthday  time.Time
	CreatedAt time.Time
}

type GuardianAgeHandoffStore interface {
	GetForSubject(context.Context, uuid.UUID) (GuardianAgeHandoff, error)
	ListNoticesForSubject(context.Context, uuid.UUID) ([]GuardianAgeHandoffNotice, error)
	ProposeEmail(context.Context, uuid.UUID, int64, string, []byte, time.Time, []byte) (GuardianAgeHandoff, error)
	VerifyEmail(context.Context, []byte) error
	ListForAdmin(context.Context, uuid.UUID, int32, int32) ([]GuardianAgeHandoff, error)
	GetForAdmin(context.Context, uuid.UUID, uuid.UUID) (GuardianAgeHandoff, error)
	ConfirmIdentity(context.Context, uuid.UUID, uuid.UUID, int64) (GuardianAgeHandoff, error)
	RecoverEmail(context.Context, uuid.UUID, uuid.UUID, int64, string, []byte, time.Time, []byte) (GuardianAgeHandoff, error)
}

type PostgresGuardianAgeHandoffStore struct{ DB dbgen.DBTX }

func (s PostgresGuardianAgeHandoffStore) GetForSubject(ctx context.Context, actorID uuid.UUID) (GuardianAgeHandoff, error) {
	row, err := dbgen.New(s.DB).GetGuardianAgeHandoffForSubject(ctx, actorID)
	if err != nil {
		return GuardianAgeHandoff{}, guardianAgeHandoffStoreError(err)
	}
	return GuardianAgeHandoff{Reference: row.PublicRef, Status: row.Status, Version: row.Version, Birthday: row.Birthday.Time,
		ProposedEmail: guardianAgeHandoffString(row.ProposedEmail), EmailVerifiedAt: optionalTime(row.EmailVerifiedAt), IdentityConfirmedAt: optionalTime(row.IdentityConfirmedAt),
		ReadyAt: optionalTime(row.ReadyAt), RecoveryRequiredAt: optionalTime(row.RecoveryRequiredAt), CompletedAt: optionalTime(row.CompletedAt)}, nil
}

func (s PostgresGuardianAgeHandoffStore) ListNoticesForSubject(ctx context.Context, actorID uuid.UUID) ([]GuardianAgeHandoffNotice, error) {
	rows, err := dbgen.New(s.DB).ListGuardianAgeHandoffNoticesForSubject(ctx, actorID)
	if err != nil {
		return nil, guardianAgeHandoffStoreError(err)
	}
	items := make([]GuardianAgeHandoffNotice, len(rows))
	for i, row := range rows {
		items[i] = GuardianAgeHandoffNotice{Kind: row.NoticeKind, Birthday: row.Birthday.Time, CreatedAt: row.CreatedAt.Time}
	}
	return items, nil
}

func (s PostgresGuardianAgeHandoffStore) ProposeEmail(ctx context.Context, actorID uuid.UUID, version int64, email string, digest []byte, expiresAt time.Time, payload []byte) (GuardianAgeHandoff, error) {
	row, err := dbgen.New(s.DB).ProposeGuardianAgeHandoffEmail(ctx, dbgen.ProposeGuardianAgeHandoffEmailParams{ActorID: actorID, ExpectedVersion: version, Email: email, TokenDigest: digest, ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true}, SealedPayload: payload})
	if err != nil {
		return GuardianAgeHandoff{}, guardianAgeHandoffStoreError(err)
	}
	return guardianAgeHandoffFromModel(row), nil
}

func (s PostgresGuardianAgeHandoffStore) VerifyEmail(ctx context.Context, digest []byte) error {
	_, err := dbgen.New(s.DB).VerifyGuardianAgeHandoffEmail(ctx, digest)
	return guardianAgeHandoffStoreError(err)
}

func (s PostgresGuardianAgeHandoffStore) ListForAdmin(ctx context.Context, actorID uuid.UUID, limit, offset int32) ([]GuardianAgeHandoff, error) {
	rows, err := dbgen.New(s.DB).ListGuardianAgeHandoffsForAdmin(ctx, dbgen.ListGuardianAgeHandoffsForAdminParams{ActorID: actorID, RowLimit: limit, RowOffset: offset})
	if err != nil {
		return nil, guardianAgeHandoffStoreError(err)
	}
	items := make([]GuardianAgeHandoff, len(rows))
	for i, row := range rows {
		items[i] = GuardianAgeHandoff{Reference: row.PublicRef, SubjectName: row.SubjectName, Status: row.Status, Version: row.Version, Birthday: row.Birthday.Time}
	}
	return items, nil
}

func (s PostgresGuardianAgeHandoffStore) GetForAdmin(ctx context.Context, actorID, ref uuid.UUID) (GuardianAgeHandoff, error) {
	row, err := dbgen.New(s.DB).GetGuardianAgeHandoffForAdmin(ctx, dbgen.GetGuardianAgeHandoffForAdminParams{ActorID: actorID, PublicRef: ref})
	if err != nil {
		return GuardianAgeHandoff{}, guardianAgeHandoffStoreError(err)
	}
	return GuardianAgeHandoff{Reference: row.PublicRef, SubjectName: row.SubjectName, Status: row.Status, Version: row.Version, Birthday: row.Birthday.Time,
		ProposedEmail: guardianAgeHandoffString(row.ProposedEmail), EmailVerifiedAt: optionalTime(row.EmailVerifiedAt), IdentityConfirmedAt: optionalTime(row.IdentityConfirmedAt),
		ReadyAt: optionalTime(row.ReadyAt), RecoveryRequiredAt: optionalTime(row.RecoveryRequiredAt), CompletedAt: optionalTime(row.CompletedAt), PersonalInvolvement: row.PersonalInvolvement}, nil
}

func (s PostgresGuardianAgeHandoffStore) ConfirmIdentity(ctx context.Context, actorID, ref uuid.UUID, version int64) (GuardianAgeHandoff, error) {
	row, err := dbgen.New(s.DB).ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: actorID, PublicRef: ref, ExpectedVersion: version})
	if err != nil {
		return GuardianAgeHandoff{}, guardianAgeHandoffStoreError(err)
	}
	return guardianAgeHandoffFromModel(row), nil
}

func (s PostgresGuardianAgeHandoffStore) RecoverEmail(ctx context.Context, actorID, ref uuid.UUID, version int64, email string, digest []byte, expiresAt time.Time, payload []byte) (GuardianAgeHandoff, error) {
	row, err := dbgen.New(s.DB).RecoverGuardianAgeHandoffEmail(ctx, dbgen.RecoverGuardianAgeHandoffEmailParams{ActorID: actorID, PublicRef: ref, ExpectedVersion: version, Email: email, TokenDigest: digest, ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true}, SealedPayload: payload})
	if err != nil {
		return GuardianAgeHandoff{}, guardianAgeHandoffStoreError(err)
	}
	return guardianAgeHandoffFromModel(row), nil
}

func guardianAgeHandoffFromModel(row dbgen.GuardianAgeHandoff) GuardianAgeHandoff {
	return GuardianAgeHandoff{Reference: row.PublicRef, Status: row.Status, Version: row.Version, ProposedEmail: stringValue(row.ProposedEmail),
		EmailVerifiedAt: optionalTime(row.EmailVerifiedAt), IdentityConfirmedAt: optionalTime(row.IdentityConfirmedAt), ReadyAt: optionalTime(row.ReadyAt),
		RecoveryRequiredAt: optionalTime(row.RecoveryRequiredAt), CompletedAt: optionalTime(row.CompletedAt)}
}

func guardianAgeHandoffStoreError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		return err
	}
	switch databaseError.Message {
	case "guardian_age_handoff_stale":
		return ErrGuardianAgeHandoffConflict
	case "guardian_age_handoff_administrator_required", "guardian_age_handoff_separation_required", "guardian_age_handoff_recovery_rejected":
		return ErrGuardianAgeHandoffForbidden
	case "guardian_age_handoff_email_collision":
		return ErrGuardianAgeHandoffCollision
	case "guardian_age_handoff_not_available", "guardian_age_handoff_token_invalid", "guardian_age_handoff_transition_rejected":
		return ErrGuardianAgeHandoffInvalid
	default:
		return err
	}
}

func guardianAgeHandoffString(value any) string {
	if result, ok := value.(string); ok {
		return result
	}
	if result, ok := value.([]byte); ok {
		return string(result)
	}
	return ""
}
