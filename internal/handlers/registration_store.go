package handlers

import (
	"context"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRegistrationStore struct {
	Pool *pgxpool.Pool
}

func (s PostgresRegistrationStore) RegisterAdult(ctx context.Context, input RegistrationInput) (RegistrationResult, error) {
	var result RegistrationResult
	err := db.WithinTx(ctx, s.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		user, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{
			Name: input.Name, Email: &input.Email, PasswordHash: &input.PasswordHash,
			Role: input.Role, SquadCategory: input.Squad,
			DateOfBirth: pgtype.Date{Time: input.DateOfBirth, Valid: true},
		})
		if err != nil {
			return err
		}
		for _, consent := range []struct{ consentType, version, hash string }{
			{"Termos_Gerais", input.TermsVersion, input.TermsSHA256},
			{"Uso_Imagem", input.ImageVersion, input.ImageSHA256},
		} {
			if _, err := queries.CreateConsentForm(ctx, dbgen.CreateConsentFormParams{
				UserID: user.ID, GrantedByUserID: &user.ID, ConsentType: consent.consentType,
				DocumentVersion: consent.version, DocumentSha256: consent.hash,
				IpAddress: input.IP, UserAgent: input.UserAgent,
			}); err != nil {
				return err
			}
		}
		result = RegistrationResult{UserID: user.ID, Role: input.Role}
		return nil
	})
	return result, err
}
