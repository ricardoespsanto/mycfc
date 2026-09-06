package handlers

import (
	"context"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresRegistrationStore struct {
	Pool db.Beginner
}

func (s PostgresRegistrationStore) RegisterAdult(ctx context.Context, input RegistrationInput) (RegistrationResult, error) {
	var result RegistrationResult
	err := db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		user, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{
			Name: input.Name, Email: &input.Email, PasswordHash: &input.PasswordHash,
			DateOfBirth: pgtype.Date{Time: input.DateOfBirth, Valid: true},
		})
		if err != nil {
			return err
		}
		if _, err := queries.CreateConsentForm(ctx, dbgen.CreateConsentFormParams{
			UserID: user.ID, GrantedByUserID: &user.ID, ConsentType: "Termos_Gerais",
			DocumentVersion: input.TermsVersion, DocumentSha256: input.TermsSHA256,
			IpAddress: input.IP, UserAgent: input.UserAgent,
		}); err != nil {
			return err
		}
		result = RegistrationResult{UserID: user.ID, CredentialVersion: user.CredentialVersion}
		return nil
	})
	return result, err
}
