package handlers

import (
	"context"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresGuardianDependentStore struct {
	Pool *pgxpool.Pool
}

func (s PostgresGuardianDependentStore) CreateDependent(ctx context.Context, input GuardianDependentInput) error {
	return db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		if _, err := queries.LockActiveGuardian(ctx, input.GuardianID); err != nil {
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
			Name: input.Name, Role: input.Role, SquadCategory: input.Squad, GuardianID: &input.GuardianID,
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
}
