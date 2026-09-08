package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/config"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/google/uuid"
)

type fakePrivacyCommandService struct {
	call             string
	actor, target    uuid.UUID
	policy           privacyrequests.AdoptedPolicy
	policyVersion    string
	enabled, revoked bool
	hold             privacyHold
	expiry           privacyrequests.ExpiryResult
	err              error
}

func (s *fakePrivacyCommandService) GrantReviewer(_ context.Context, actor, target uuid.UUID, revoke bool) error {
	s.call, s.actor, s.target, s.revoked = "reviewer", actor, target, revoke
	return s.err
}

func (s *fakePrivacyCommandService) ImportPolicy(_ context.Context, actor uuid.UUID, policy privacyrequests.AdoptedPolicy) error {
	s.call, s.actor, s.policy = "import", actor, policy
	return s.err
}

func (s *fakePrivacyCommandService) Activate(_ context.Context, actor uuid.UUID, version string, enabled bool) error {
	s.call, s.actor, s.policyVersion, s.enabled = "activate", actor, version, enabled
	return s.err
}

func (s *fakePrivacyCommandService) AddRetentionException(_ context.Context, actor, reference, owner uuid.UUID, category, reason, evidence string) error {
	s.call, s.actor = "hold", actor
	s.hold = privacyHold{reference: reference, owner: owner, category: category, reason: reason, evidence: evidence}
	return s.err
}

func (s *fakePrivacyCommandService) Expire(_ context.Context, actor uuid.UUID) (privacyrequests.ExpiryResult, error) {
	s.call, s.actor = "expire", actor
	return s.expiry, s.err
}

type privacyCommandHarness struct {
	service        *fakePrivacyCommandService
	files          map[string]string
	stdout, stderr bytes.Buffer
	openErr        error
	opened, closed int
}

func (h *privacyCommandHarness) dependencies() privacyCommandDependencies {
	return privacyCommandDependencies{
		openService: func(context.Context) (privacyCommandService, func(), error) {
			h.opened++
			if h.openErr != nil {
				return nil, nil, h.openErr
			}
			return h.service, func() { h.closed++ }, nil
		},
		openFile: func(path string) (io.ReadCloser, error) {
			contents, ok := h.files[path]
			if !ok {
				return nil, fmt.Errorf("missing %s", path)
			}
			return io.NopCloser(strings.NewReader(contents)), nil
		},
		stdout: &h.stdout,
		stderr: &h.stderr,
	}
}

func TestPrivacyCommandDispatchesValidatedSubcommands(t *testing.T) {
	actor, target, reference, owner := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name, wantCall string
		args           []string
		check          func(*testing.T, *privacyCommandHarness)
	}{
		{"grant", "reviewer", []string{"grant", "--actor", actor.String(), "--user", target.String()}, func(t *testing.T, h *privacyCommandHarness) {
			if h.service.actor != actor || h.service.target != target || h.service.revoked {
				t.Fatalf("grant=%+v", h.service)
			}
		}},
		{"revoke", "reviewer", []string{"revoke", "--actor", actor.String(), "--user", target.String()}, func(t *testing.T, h *privacyCommandHarness) {
			if !h.service.revoked {
				t.Fatal("revoke was not dispatched")
			}
		}},
		{"import policy", "import", []string{"import-policy", "--actor", actor.String(), "--file", "approved.json"}, func(t *testing.T, h *privacyCommandHarness) {
			if h.service.policy.Version != "matrix-v1" {
				t.Fatalf("policy=%+v", h.service.policy)
			}
		}},
		{"activate", "activate", []string{"activate", "--actor", actor.String(), "--policy", " matrix-v1 "}, func(t *testing.T, h *privacyCommandHarness) {
			if h.service.policyVersion != "matrix-v1" || !h.service.enabled {
				t.Fatalf("activation=%+v", h.service)
			}
		}},
		{"deactivate", "activate", []string{"deactivate", "--actor", actor.String(), "--policy", "matrix-v1"}, func(t *testing.T, h *privacyCommandHarness) {
			if h.service.enabled {
				t.Fatal("deactivation enabled the policy")
			}
		}},
		{"hold", "hold", []string{"hold", "--actor", actor.String(), "--reference", reference.String(), "--owner", owner.String(), "--category", " identity-core ", "--reason", " legal_hold ", "--evidence", " case-file-1 "}, func(t *testing.T, h *privacyCommandHarness) {
			if h.service.hold.reference != reference || h.service.hold.owner != owner || h.service.hold.category != "identity-core" || h.service.hold.reason != "LEGAL_HOLD" || h.service.hold.evidence != "case-file-1" {
				t.Fatalf("hold=%+v", h.service.hold)
			}
		}},
		{"expire", "expire", []string{"expire", "--actor", actor.String()}, func(t *testing.T, h *privacyCommandHarness) {
			if got := h.stdout.String(); got != "Privacy records expired: working=2 evidence=3\n" {
				t.Fatalf("stdout=%q", got)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := &privacyCommandHarness{
				service: &fakePrivacyCommandService{expiry: privacyrequests.ExpiryResult{WorkingRecords: 2, EvidenceRecords: 3}},
				files:   map[string]string{"approved.json": `{"version":"matrix-v1"}`},
			}
			if err := runPrivacyCommandWith(t.Context(), test.args, h.dependencies()); err != nil {
				t.Fatal(err)
			}
			if h.service.call != test.wantCall || h.opened != 1 || h.closed != 1 {
				t.Fatalf("call=%q opened=%d closed=%d", h.service.call, h.opened, h.closed)
			}
			test.check(t, h)
		})
	}
}

func TestPrivacyCommandRejectsInvalidInputBeforeOpeningDatabase(t *testing.T) {
	actor, target, reference, owner := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name, want string
		args       []string
		files      map[string]string
	}{
		{"missing subcommand", "privacy requires", nil, nil},
		{"unknown subcommand", "unknown privacy command", []string{"destroy", "--actor", actor.String()}, nil},
		{"unknown flag", "flag provided but not defined", []string{"expire", "--actor", actor.String(), "--force"}, nil},
		{"positional argument", "unexpected privacy command arguments", []string{"expire", "--actor", actor.String(), "now"}, nil},
		{"invalid actor", "--actor", []string{"expire", "--actor", "not-a-uuid"}, nil},
		{"invalid reviewer", "--user", []string{"grant", "--actor", actor.String(), "--user", "not-a-uuid"}, nil},
		{"missing policy version", "--policy", []string{"activate", "--actor", actor.String(), "--policy", " "}, nil},
		{"invalid hold reference", "--reference", []string{"hold", "--actor", actor.String(), "--reference", "bad", "--owner", owner.String(), "--category", "identity-core", "--evidence", "record"}, nil},
		{"missing policy file", "read policy file", []string{"import-policy", "--actor", actor.String(), "--file", "missing.json"}, nil},
		{"invalid policy JSON", "invalid adopted policy JSON", []string{"import-policy", "--actor", actor.String(), "--file", "policy.json"}, map[string]string{"policy.json": `{`}},
		{"unknown policy field", "invalid adopted policy JSON", []string{"import-policy", "--actor", actor.String(), "--file", "policy.json"}, map[string]string{"policy.json": `{"version":"v1","invented":true}`}},
		{"trailing policy JSON", "unexpected policy JSON content", []string{"import-policy", "--actor", actor.String(), "--file", "policy.json"}, map[string]string{"policy.json": `{"version":"v1"}{}`}},
		{"valid identities sanity", "--policy", []string{"activate", "--actor", actor.String(), "--user", target.String()}, nil},
		{"valid hold identities sanity", "--category", []string{"hold", "--actor", actor.String(), "--reference", reference.String(), "--owner", owner.String(), "--evidence", "record"}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := &privacyCommandHarness{service: &fakePrivacyCommandService{}, files: test.files}
			err := runPrivacyCommandWith(t.Context(), test.args, h.dependencies())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
			if h.opened != 0 || h.closed != 0 || h.service.call != "" {
				t.Fatalf("invalid input reached service: opened=%d closed=%d call=%q", h.opened, h.closed, h.service.call)
			}
		})
	}
}

func TestPrivacyCommandPropagatesOpenAndServiceErrorsAndCloses(t *testing.T) {
	actor := uuid.New()
	openErr := errors.New("configuration unavailable")
	h := &privacyCommandHarness{service: &fakePrivacyCommandService{}, openErr: openErr}
	if err := runPrivacyCommandWith(t.Context(), []string{"expire", "--actor", actor.String()}, h.dependencies()); !errors.Is(err, openErr) || h.closed != 0 {
		t.Fatalf("open error=%v closed=%d", err, h.closed)
	}

	executorErr := privacyrequests.ErrExecutorUnavailable
	h = &privacyCommandHarness{service: &fakePrivacyCommandService{err: executorErr}}
	if err := runPrivacyCommandWith(t.Context(), []string{"activate", "--actor", actor.String(), "--policy", "matrix-v1"}, h.dependencies()); !errors.Is(err, executorErr) {
		t.Fatalf("activation error=%v", err)
	}
	if h.service.call != "activate" || !h.service.enabled || h.closed != 1 {
		t.Fatalf("fail-closed activation=%+v closed=%d", h.service, h.closed)
	}
}

func TestPrivacyCommandDefaultWiringFailsBeforeConfigurationForInvalidArguments(t *testing.T) {
	if err := runPrivacyCommand(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "privacy requires") {
		t.Fatalf("error=%v", err)
	}
}

func TestOpenDefaultPrivacyCommandService(t *testing.T) {
	original := loadPrivacyCommandConfig
	t.Cleanup(func() { loadPrivacyCommandConfig = original })

	configErr := errors.New("configuration unavailable")
	loadPrivacyCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{}, configErr
	}
	if _, _, err := openDefaultPrivacyCommandService(t.Context()); !errors.Is(err, configErr) {
		t.Fatalf("configuration error=%v", err)
	}

	loadPrivacyCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{DatabaseURL: config.Secret("://invalid")}, nil
	}
	if _, _, err := openDefaultPrivacyCommandService(t.Context()); err == nil || err.Error() != "open privacy operator database" {
		t.Fatalf("pool error=%v", err)
	}

	loadPrivacyCommandConfig = func(context.Context) (config.Config, error) {
		return config.Config{DatabaseURL: config.Secret("postgres://user:password@127.0.0.1:5432/mycfc")}, nil
	}
	service, closeService, err := openDefaultPrivacyCommandService(t.Context())
	if err != nil || service == nil || closeService == nil {
		t.Fatalf("service=%v close=%v error=%v", service, closeService != nil, err)
	}
	closeService()
}

func TestParsePrivacyHoldRequiresScopedPlanInputs(t *testing.T) {
	reference, owner := uuid.New(), uuid.New()
	hold, err := parsePrivacyHold(reference.String(), owner.String(), " identity-core ", " legal_hold ", " legal-file-001 ")
	if err != nil {
		t.Fatal(err)
	}
	if hold.reference != reference || hold.owner != owner || hold.category != "identity-core" || hold.reason != "LEGAL_HOLD" || hold.evidence != "legal-file-001" {
		t.Fatalf("hold=%+v", hold)
	}
	for _, test := range []struct {
		name, reference, owner, category, evidence, want string
	}{
		{"reference", "invalid", owner.String(), "identity-core", "evidence", "--reference"},
		{"owner", reference.String(), "invalid", "identity-core", "evidence", "--owner"},
		{"category", reference.String(), owner.String(), " ", "evidence", "--category"},
		{"evidence", reference.String(), owner.String(), "identity-core", " ", "--evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePrivacyHold(test.reference, test.owner, test.category, "LEGAL_HOLD", test.evidence)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}
