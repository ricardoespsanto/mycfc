package privacyrequests

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestProductionPolicyMatchesApprovedInternalScope(t *testing.T) {
	p, err := ProductionPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if !p.AccountClosureEnabled || p.WorkingRetentionDays != 90 || p.ResponseMonths != 1 || p.ExtensionMonths != 2 || len(p.Categories) != len(categoryContracts) {
		t.Fatal("approved scope changed")
	}
	for _, c := range p.Categories {
		if len(c.Grounds) != 0 || c.Rule.LegalGround != "APPROVED_POLICY" || c.Rule.Fallback != "BLOCK" {
			t.Fatal("unverified exception or fallback enabled")
		}
		wantDays := int32(0)
		if c.Key == "object-storage" || c.Key == "profile-photo" {
			wantDays = 30
		}
		if *c.Rule.CompleteWithinDays != wantDays {
			t.Fatal("execution deadline changed")
		}
		if spec, ok := categoryRetentionSpecs[c.Key]; ok {
			if c.Rule.ExpireAfter == nil || *c.Rule.ExpireAfter != spec.Max {
				t.Fatal("approved retention changed")
			}
			wantReview := spec.Max
			switch spec.Unit {
			case "CALENDAR_MONTH":
				if wantReview > 12 {
					wantReview = 12
				}
			case "CALENDAR_YEAR":
				wantReview = 1
			}
			if c.Rule.ReviewAfter == nil || *c.Rule.ReviewAfter != wantReview {
				t.Fatal("annual or earlier expiry review changed")
			}
		}
	}
	raw, digest, err := ProductionPolicyArtifact()
	if err != nil || len(digest) != 64 {
		t.Fatal("missing policy artifact")
	}
	canonical, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, append(canonical, '\n')) {
		t.Fatal("policy source is not canonical")
	}
	raw[0] = 'x'
	if _, err = ProductionPolicy(); err != nil {
		t.Fatal("caller mutated embedded policy")
	}
}

func TestProductionPolicyCompilesClosureAndRejectsUnverifiedRetention(t *testing.T) {
	p, err := ProductionPolicy()
	if err != nil {
		t.Fatal(err)
	}
	decisions := map[string]CategoryDecision{}
	for _, c := range p.Categories {
		decisions[c.Key] = CategoryDecision{Category: c.Key, Outcome: "APPROVE"}
	}
	scope := Scope{Kind: AccountClosure}
	at := time.Date(2026, 9, 15, 17, 0, 0, 0, time.UTC)
	_, plan, err := p.DecisionPlan(scope, decisions, "approve", at)
	if err != nil || len(plan.Entries) != len(p.Categories) {
		t.Fatalf("closure plan rejected: %v", err)
	}
	if len(plan.Entries) == 0 || plan.Entries[0].Category != "backup-tombstones" ||
		len(plan.Entries[0].Operations) != 1 || plan.Entries[0].Operations[0] != "BACKUP_TOMBSTONE_REPLAY" {
		t.Fatalf("closure plan does not establish restore intent before destructive work: %+v", plan.Entries)
	}
	decisions["identity-core"] = CategoryDecision{Category: "identity-core", Outcome: "RETAIN", Ground: "LEGAL_HOLD"}
	if _, _, err = p.DecisionPlan(scope, decisions, "partial", at); err == nil {
		t.Fatal("unverified legal hold accepted")
	}
}
