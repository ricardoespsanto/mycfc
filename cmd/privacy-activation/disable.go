package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const privacyActivationDisableRole = "mycfc_privacy_activation_disable"

type activationDisableDatabase interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Close()
}

var effectiveUserID = os.Geteuid

var openActivationDisableDatabase = func(ctx context.Context, databaseURL string) (activationDisableDatabase, error) {
	return pgxpool.New(ctx, databaseURL)
}

func runDisable(ctx context.Context, getenv func(string) string, output io.Writer) error {
	databaseURL, actor, err := loadDisableInputs(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	database, err := openActivationDisableDatabase(ctx, databaseURL)
	if err != nil {
		return errors.New("open privacy activation disable database")
	}
	defer database.Close()

	var switchVersion int64
	if err = database.QueryRow(ctx, `SELECT privacy_activation_disable($1)`, actor).Scan(&switchVersion); err != nil || switchVersion < 1 {
		return errors.New("privacy activation disable rejected")
	}
	_, err = fmt.Fprintln(output, "privacy_activation_disabled kill_switch=engaged readiness=blocked")
	return err
}

func loadDisableInputs(getenv func(string) string) (string, uuid.UUID, error) {
	if effectiveUserID() != 0 {
		return "", uuid.Nil, errors.New("privacy activation disable requires root")
	}
	rawActor := getenv("PRIVACY_ACTIVATION_DISABLE_ACTOR_REF")
	actor, err := uuid.Parse(rawActor)
	if err != nil || actor == uuid.Nil || rawActor != actor.String() {
		return "", uuid.Nil, errors.New("privacy activation disable actor rejected")
	}
	databaseURL := getenv("PRIVACY_ACTIVATION_DISABLE_DATABASE_URL")
	parsed, err := url.Parse(databaseURL)
	password, passwordPresent := "", false
	if parsed != nil && parsed.User != nil {
		password, passwordPresent = parsed.User.Password()
	}
	if err != nil || parsed == nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" ||
		parsed.User == nil || parsed.User.Username() != privacyActivationDisableRole || !passwordPresent || password == "" ||
		parsed.Path == "" || parsed.Path == "/" || parsed.Fragment != "" || databaseURL != parsed.String() {
		return "", uuid.Nil, errors.New("privacy activation disable database credential rejected")
	}
	return databaseURL, actor, nil
}
