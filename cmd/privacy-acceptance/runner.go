package main

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var acceptanceRegion = regexp.MustCompile(`^[a-z][a-z0-9-]{2,39}$`)
var acceptanceSigningID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$`)
var acceptanceRoleName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

var readAcceptanceProtectedFile = readProtectedFile
var connectAcceptanceDatabase = pgx.ConnectConfig
var closeAcceptanceDatabase = func(connection *pgx.Conn, ctx context.Context) {
	_ = connection.Close(ctx)
}
var configureAcceptanceRole = func(ctx context.Context, connection *pgx.Conn, expected, password string, revoke bool) error {
	return db.ConfigurePrivacyAcceptanceRole(ctx, connection, expected, password, revoke)
}
var newAcceptanceProtector = privacyrequests.NewTombstoneProtector
var loadAcceptanceAWSConfig = func(ctx context.Context, region string) (aws.Config, error) {
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
}
var newAcceptanceLedger = func(config aws.Config, functionName string) (privacyrequests.TombstoneLedger, error) {
	return privacyrequests.NewLambdaTombstoneLedger(lambda.NewFromConfig(config), functionName)
}
var newAcceptancePool = pgxpool.NewWithConfig
var closeAcceptancePool = func(pool *pgxpool.Pool) { pool.Close() }
var runSyntheticAcceptance = privacyrequests.RunSyntheticAcceptance
var signAcceptanceEvidence = privacyrequests.SignAcceptanceEvidence

func protectedKey(path string) ([]byte, error) {
	raw, err := readAcceptanceProtectedFile(path, 0)
	if err != nil {
		return nil, err
	}
	value, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, errors.New("acceptance key rejected")
	}
	return value, nil
}
func protectedDatabase(path string) (*pgx.ConnConfig, error) {
	raw, err := readAcceptanceProtectedFile(path, 0)
	if err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, errors.New("acceptance database rejected")
	}
	return cfg, nil
}

func runAcceptance(ctx context.Context, mode string, getenv func(string) string, out io.Writer) error {
	expected := getenv("PRIVACY_ACCEPTANCE_EXPECTED_DATABASE")
	appRole := getenv("PRIVACY_ACCEPTANCE_APP_ROLE")
	if !acceptanceRoleName.MatchString(expected) || !acceptanceRoleName.MatchString(appRole) || appRole == "postgres" || appRole == db.PrivacyAcceptanceRole || appRole == "mycfc_privacy_executor" {
		return privacyrequests.ErrAcceptance
	}
	schema := getenv("PRIVACY_ACCEPTANCE_SCHEMA_DIGEST")
	if schema != db.EmbeddedMigrationDigest() {
		return privacyrequests.ErrAcceptance
	}
	operator, err := protectedDatabase(getenv("PRIVACY_ACCEPTANCE_DATABASE_URL_FILE"))
	if err != nil {
		return err
	}
	app, err := protectedDatabase(getenv("PRIVACY_ACCEPTANCE_APP_DATABASE_URL_FILE"))
	if err != nil {
		return err
	}
	worker, err := protectedDatabase(getenv("PRIVACY_ACCEPTANCE_EXECUTOR_DATABASE_URL_FILE"))
	if err != nil {
		return err
	}
	if operator.User != db.PrivacyAcceptanceRole || len(operator.Password) < 32 || app.User != appRole || worker.User != "mycfc_privacy_executor" {
		return privacyrequests.ErrAcceptance
	}
	for _, cfg := range []*pgx.ConnConfig{operator, app, worker} {
		if cfg.Database != expected || cfg.Host != operator.Host || cfg.Port != operator.Port || cfg.Password == "" {
			return privacyrequests.ErrAcceptance
		}
		for _, fallback := range cfg.Fallbacks {
			if fallback.Host != operator.Host || fallback.Port != operator.Port {
				return privacyrequests.ErrAcceptance
			}
		}
	}
	signing, err := protectedKey(getenv("PRIVACY_ACCEPTANCE_SIGNING_KEY_FILE"))
	if err != nil {
		return err
	}
	if len(signing) == ed25519.SeedSize {
		signing = ed25519.NewKeyFromSeed(signing)
	}
	if len(signing) != ed25519.PrivateKeySize || !acceptanceSigningID.MatchString(getenv("PRIVACY_ACCEPTANCE_SIGNING_KEY_ID")) || !hmac.Equal(ed25519.NewKeyFromSeed(signing[:32]), signing) {
		return privacyrequests.ErrAcceptance
	}
	public, err := protectedKey(getenv("PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE"))
	if err != nil {
		return err
	}
	locator, err := protectedKey(getenv("PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE"))
	if err != nil {
		return err
	}
	protector, err := newAcceptanceProtector(getenv("PRIVACY_TOMBSTONE_ENCRYPTION_KEY_ID"), public, getenv("PRIVACY_TOMBSTONE_LOCATOR_KEY_ID"), locator)
	if err != nil {
		return err
	}
	if !acceptanceRegion.MatchString(getenv("AWS_REGION")) {
		return privacyrequests.ErrAcceptance
	}
	cfg, err := loadAcceptanceAWSConfig(ctx, getenv("AWS_REGION"))
	if err != nil {
		return privacyrequests.ErrAcceptance
	}
	ledger, err := newAcceptanceLedger(cfg, getenv("PRIVACY_TOMBSTONE_BROKER_FUNCTION_NAME"))
	if err != nil {
		return err
	}
	connection, err := connectAcceptanceDatabase(ctx, operator)
	if err != nil {
		return privacyrequests.ErrAcceptance
	}
	defer closeAcceptanceDatabase(connection, ctx)
	workerConfig, err := pgxpool.ParseConfig("")
	if err != nil {
		return privacyrequests.ErrAcceptance
	}
	workerConfig.ConnConfig = worker
	workerConfig.MaxConns = 4
	workerConfig.MinConns = 0
	workerPool, err := newAcceptancePool(ctx, workerConfig)
	if err != nil {
		return privacyrequests.ErrAcceptance
	}
	defer closeAcceptancePool(workerPool)
	appConfig, err := pgxpool.ParseConfig("")
	if err != nil {
		return privacyrequests.ErrAcceptance
	}
	appConfig.ConnConfig = app
	result, err := runSyntheticAcceptance(ctx, privacyrequests.AcceptanceOptions{Mode: mode, ExpectedDatabase: expected, AppRole: appRole, ImageDigest: getenv("PRIVACY_ACCEPTANCE_IMAGE_DIGEST"), SchemaDigest: schema, Operator: connection, AppConfig: appConfig, Worker: workerPool, Ledger: ledger, Protector: protector, Event: func(_ context.Context, event string) error {
		_, e := fmt.Fprintf(out, "event=%s count=1\n", event)
		return e
	}})
	if err != nil {
		return privacyrequests.ErrAcceptance
	}
	signed, err := signAcceptanceEvidence(result, getenv("PRIVACY_ACCEPTANCE_SIGNING_KEY_ID"), signing)
	if err != nil {
		return privacyrequests.ErrAcceptance
	}
	return json.NewEncoder(out).Encode(signed)
}
