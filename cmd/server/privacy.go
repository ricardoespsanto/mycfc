package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"os"
	"strings"
)

func runPrivacyCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("privacy requires grant, revoke, import-policy, activate, deactivate, hold or expire")
	}
	flags := flag.NewFlagSet("privacy "+args[0], flag.ContinueOnError)
	actorText := flags.String("actor", "", "active adult administrator UUID")
	targetText := flags.String("user", "", "reviewer UUID")
	file := flags.String("file", "", "approved policy JSON file")
	version := flags.String("policy", "", "adopted policy version")
	referenceText := flags.String("reference", "", "privacy case public reference UUID")
	ownerText := flags.String("owner", "", "active adult accountable for the retention exception")
	reason := flags.String("reason", "", "retention exception reason: COMPLAINT or LEGAL_HOLD")
	category := flags.String("category", "", "category from the frozen decision plan")
	evidence := flags.String("evidence", "", "opaque evidence reference")
	if e := flags.Parse(args[1:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected privacy command arguments")
	}
	actor, e := uuid.Parse(*actorText)
	if e != nil {
		return errors.New("valid explicit --actor is required")
	}
	cfg, e := config.Load(ctx)
	if e != nil {
		return e
	}
	dsn, e := cfg.ResolvedDatabaseURL()
	if e != nil {
		return e
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		return errors.New("open privacy operator database")
	}
	defer pool.Close()
	s := privacyrequests.Service{Pool: pool}
	switch args[0] {
	case "grant", "revoke":
		target, e := uuid.Parse(*targetText)
		if e != nil {
			return errors.New("valid --user is required")
		}
		e = s.GrantReviewer(ctx, actor, target, args[0] == "revoke")
		if e != nil {
			return e
		}
	case "import-policy":
		f, e := os.Open(*file)
		if e != nil {
			return errors.New("read policy file")
		}
		defer f.Close()
		decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
		decoder.DisallowUnknownFields()
		var p privacyrequests.AdoptedPolicy
		if e = decoder.Decode(&p); e != nil {
			return errors.New("invalid adopted policy JSON")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return errors.New("unexpected policy JSON content")
		}
		if e = s.ImportPolicy(ctx, actor, p); e != nil {
			return e
		}
	case "activate", "deactivate":
		if strings.TrimSpace(*version) == "" {
			return errors.New("--policy is required")
		}
		if e = s.Activate(ctx, actor, *version, args[0] == "activate"); e != nil {
			return e
		}
	case "hold":
		hold, parseErr := parsePrivacyHold(*referenceText, *ownerText, *category, *reason, *evidence)
		if parseErr != nil {
			return parseErr
		}
		if e = s.AddRetentionException(ctx, actor, hold.reference, hold.owner, hold.category, hold.reason, hold.evidence); e != nil {
			return e
		}
	case "expire":
		result, e := s.Expire(ctx, actor)
		if e != nil {
			return e
		}
		fmt.Printf("Privacy records expired: working=%d evidence=%d\n", result.WorkingRecords, result.EvidenceRecords)
	default:
		return errors.New("unknown privacy command")
	}
	return nil
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
