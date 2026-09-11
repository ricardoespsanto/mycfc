package handlers

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGuardianAuthorityStoreHelpers(t *testing.T) {
	if optionalTime(pgtype.Timestamptz{}) != nil {
		t.Fatal("invalid timestamp should map to nil")
	}
	now := time.Now().UTC()
	got := optionalTime(pgtype.Timestamptz{Time: now, Valid: true})
	if got == nil || !got.Equal(now) {
		t.Fatalf("optional timestamp = %v", got)
	}
	for value, want := range map[string]string{
		"IN_PERSON_IDENTITY": "Identificação presencial",
		"COURT_ORDER":        "Decisão judicial", "BIRTH_CERTIFICATE": "Certidão de nascimento",
		"APPROVED": "Aprovado", "EVIDENCE_CONFIRMED": "Comprovativo confirmado",
		"INSUFFICIENT_EVIDENCE": "Comprovativo insuficiente", "EVIDENCE_INSUFFICIENT": "Comprovativo insuficiente",
		"AUTHORITY_CHANGED": "Alteração de responsabilidade", "CONFLICT": "Conflito comunicado",
		"REVIEW_DUE": "Revisão obrigatória", "VALIDITY_ENDED": "Validade terminada",
		"NO_AUTHORITY": "Autoridade não comprovada", "LOSS": "Perda de autoridade",
		"CHANGE": "Alteração confirmada", "CIVIL_REGISTRY": "Registo civil",
		"TEST_EVIDENCE": "Comprovativo de teste", "CUSTOM_CODE": "CUSTOM CODE",
	} {
		if label := guardianAuthorityCodeLabel(value); label != want {
			t.Errorf("label %q = %q, want %q", value, label, want)
		}
	}
}

func TestGuardianAuthorityStoreErrorMapping(t *testing.T) {
	plain := errors.New("plain")
	if guardianAuthorityStoreError(nil) != nil || guardianAuthorityStoreError(plain) != plain {
		t.Fatal("nil or non-database error mapping changed")
	}
	cases := map[string]error{
		"guardian_authority_limit_reached":           ErrMaximumDependents,
		"guardian_authority_policy_unavailable":      ErrGuardianAuthorityPolicyUnavailable,
		"guardian_authority_stale":                   ErrGuardianAuthorityConflict,
		"guardian_authority_verifier_required":       ErrGuardianAuthorityForbidden,
		"guardian_authority_separation_required":     ErrGuardianAuthorityForbidden,
		"guardian_authority_transition_rejected":     ErrGuardianAuthorityInvalid,
		"guardian_authority_relationship_ineligible": ErrGuardianAuthorityInvalid,
		"guardian_authority_not_due":                 ErrGuardianAuthorityInvalid,
		"guardian_authority_evidence_rejected":       ErrGuardianAuthorityInvalid,
		"guardian_authority_reason_rejected":         ErrGuardianAuthorityInvalid,
		"guardian_authority_subject_rejected":        ErrGuardianAuthorityInvalid,
	}
	for message, want := range cases {
		err := &pgconn.PgError{Message: message}
		if got := guardianAuthorityStoreError(err); !errors.Is(got, want) {
			t.Errorf("message %q mapped to %v, want %v", message, got, want)
		}
	}
	unknown := &pgconn.PgError{Message: "unknown"}
	if guardianAuthorityStoreError(unknown) != unknown {
		t.Fatal("unknown database error should be preserved")
	}
}
