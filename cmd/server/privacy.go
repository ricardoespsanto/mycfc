package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type privacyCommandService interface {
	GrantReviewer(context.Context, uuid.UUID, uuid.UUID, bool) error
	ImportPolicy(context.Context, uuid.UUID, privacyrequests.AdoptedPolicy) error
	Activate(context.Context, uuid.UUID, string, bool) error
	AddRetentionException(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string) error
	Expire(context.Context, uuid.UUID) (privacyrequests.ExpiryResult, error)
}

type privacyCommandDependencies struct {
	openService func(context.Context) (privacyCommandService, func(), error)
	openFile    func(string) (io.ReadCloser, error)
	stdout      io.Writer
	stderr      io.Writer
}

var loadPrivacyCommandConfig = config.Load

func defaultPrivacyCommandDependencies() privacyCommandDependencies {
	return privacyCommandDependencies{
		openService: openDefaultPrivacyCommandService,
		openFile:    func(path string) (io.ReadCloser, error) { return os.Open(path) },
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}
}

func openDefaultPrivacyCommandService(ctx context.Context) (privacyCommandService, func(), error) {
	cfg, err := loadPrivacyCommandConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	dsn, err := cfg.ResolvedDatabaseURL()
	if err != nil {
		return nil, nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, errors.New("open privacy operator database")
	}
	return privacyrequests.Service{Pool: pool}, pool.Close, nil
}

func runPrivacyCommand(ctx context.Context, args []string) error {
	return runPrivacyCommandWith(ctx, args, defaultPrivacyCommandDependencies())
}

func runPrivacyCommandWith(ctx context.Context, args []string, deps privacyCommandDependencies) error {
	if len(args) == 0 {
		return errors.New("privacy requires grant, revoke, import-policy, activate, deactivate, hold or expire")
	}
	if !privacySubcommand(args[0]) {
		return errors.New("unknown privacy command")
	}
	flags := flag.NewFlagSet("privacy "+args[0], flag.ContinueOnError)
	flags.SetOutput(deps.stderr)
	actorText := flags.String("actor", "", "active adult administrator UUID")
	targetText := flags.String("user", "", "reviewer UUID")
	file := flags.String("file", "", "approved policy JSON file")
	version := flags.String("policy", "", "adopted policy version")
	referenceText := flags.String("reference", "", "privacy case public reference UUID")
	ownerText := flags.String("owner", "", "active adult accountable for the retention exception")
	reason := flags.String("reason", "", "retention exception reason: COMPLAINT or LEGAL_HOLD")
	category := flags.String("category", "", "category from the frozen decision plan")
	evidence := flags.String("evidence", "", "opaque evidence reference")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected privacy command arguments")
	}
	actor, err := uuid.Parse(*actorText)
	if err != nil {
		return errors.New("valid explicit --actor is required")
	}

	var target uuid.UUID
	var hold privacyHold
	var policy privacyrequests.AdoptedPolicy
	switch args[0] {
	case "grant", "revoke":
		target, err = uuid.Parse(*targetText)
		if err != nil {
			return errors.New("valid --user is required")
		}
	case "import-policy":
		policy, err = readPrivacyPolicy(deps.openFile, *file)
		if err != nil {
			return err
		}
	case "activate", "deactivate":
		*version = strings.TrimSpace(*version)
		if *version == "" {
			return errors.New("--policy is required")
		}
	case "hold":
		hold, err = parsePrivacyHold(*referenceText, *ownerText, *category, *reason, *evidence)
		if err != nil {
			return err
		}
	}

	service, closeService, err := deps.openService(ctx)
	if err != nil {
		return err
	}
	defer closeService()

	switch args[0] {
	case "grant", "revoke":
		return service.GrantReviewer(ctx, actor, target, args[0] == "revoke")
	case "import-policy":
		return service.ImportPolicy(ctx, actor, policy)
	case "activate", "deactivate":
		return service.Activate(ctx, actor, *version, args[0] == "activate")
	case "hold":
		return service.AddRetentionException(ctx, actor, hold.reference, hold.owner, hold.category, hold.reason, hold.evidence)
	case "expire":
		result, expireErr := service.Expire(ctx, actor)
		if expireErr != nil {
			return expireErr
		}
		_, err = fmt.Fprintf(deps.stdout, "Privacy records expired: working=%d evidence=%d\n", result.WorkingRecords, result.EvidenceRecords)
		return err
	default:
		panic("validated privacy subcommand was not dispatched")
	}
}

func privacySubcommand(value string) bool {
	switch value {
	case "grant", "revoke", "import-policy", "activate", "deactivate", "hold", "expire":
		return true
	default:
		return false
	}
}

func readPrivacyPolicy(openFile func(string) (io.ReadCloser, error), path string) (privacyrequests.AdoptedPolicy, error) {
	var policy privacyrequests.AdoptedPolicy
	file, err := openFile(path)
	if err != nil {
		return policy, errors.New("read policy file")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&policy); err != nil {
		return privacyrequests.AdoptedPolicy{}, errors.New("invalid adopted policy JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return privacyrequests.AdoptedPolicy{}, errors.New("unexpected policy JSON content")
	}
	return policy, nil
}

type privacyHold struct {
	reference, owner           uuid.UUID
	category, reason, evidence string
}

func parsePrivacyHold(referenceText, ownerText, categoryText, reasonText, evidenceText string) (privacyHold, error) {
	reference, err := uuid.Parse(referenceText)
	if err != nil {
		return privacyHold{}, errors.New("valid --reference is required")
	}
	owner, err := uuid.Parse(ownerText)
	if err != nil {
		return privacyHold{}, errors.New("valid --owner is required")
	}
	hold := privacyHold{reference: reference, owner: owner, category: strings.TrimSpace(categoryText), reason: strings.ToUpper(strings.TrimSpace(reasonText)), evidence: strings.TrimSpace(evidenceText)}
	if hold.category == "" {
		return privacyHold{}, errors.New("--category is required")
	}
	if hold.evidence == "" {
		return privacyHold{}, errors.New("--evidence is required")
	}
	return hold, nil
}
