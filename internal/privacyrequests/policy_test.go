package privacyrequests

import (
	"encoding/json"
	"errors"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func testPolicy() AdoptedPolicy {
	p := AdoptedPolicy{Version: "test-v1", ExecutorVersion: SupportedExecutorVersion, PlanSchemaVersion: SupportedPlanSchemaVersion, AccountClosureEnabled: true, WorkingRetentionDays: 30, ResponseMonths: 1, ExtensionMonths: 2}
	keys := make([]string, 0, len(categoryContracts))
	for key := range categoryContracts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		contract := categoryContracts[key]
		owners := make([]string, 0, len(contract.Owners))
		for owner := range contract.Owners {
			owners = append(owners, owner)
		}
		sort.Strings(owners)
		zero := int32(0)
		rule := ExecutionRule{Profile: contract.DefaultProfile, LegalGround: "APPROVED_POLICY", Owner: owners[0], CompleteWithinDays: &zero, Fallback: "BLOCK"}
		profile := executionProfiles[rule.Profile]
		if profile.Disposition == "RESTRICT" || profile.Disposition == "EXPIRE" {
			spec := categoryRetentionSpec(key, "APPROVE")
			one, expiry := int32(1), spec.Max
			rule.RetentionUnit, rule.ReviewAfter, rule.ExpireAfter = spec.Unit, &one, &expiry
			for field := range profile.Fields {
				rule.RetainedFields = append(rule.RetainedFields, field)
			}
			sort.Strings(rule.RetainedFields)
		}
		entry := CatalogueEntry{Key: key, Label: "Categoria " + key, Rule: rule}
		for retentionProfile := range contract.RetentionProfiles {
			one, expiry := int32(1), int32(90)
			if contract.MaxGroundRetentionDays < expiry {
				expiry = contract.MaxGroundRetentionDays
			}
			retention := ExecutionRule{Profile: retentionProfile, LegalGround: "LEGAL_HOLD", Owner: owners[0], CompleteWithinDays: &zero, RetentionUnit: calendarDayUnit, ReviewAfter: &one, ExpireAfter: &expiry, Fallback: "BLOCK"}
			for field := range executionProfiles[retentionProfile].Fields {
				retention.RetainedFields = append(retention.RetainedFields, field)
			}
			sort.Strings(retention.RetainedFields)
			entry.Grounds = append(entry.Grounds, Ground{Code: "LEGAL_HOLD", Label: "Retenção legal aprovada", Rule: retention})
		}
		p.Categories = append(p.Categories, entry)
	}
	return p
}
func TestCalendarDeadlineClampsLisbonMonths(t *testing.T) {
	l, _ := time.LoadLocation("Europe/Lisbon")
	for _, tc := range []struct {
		at, want string
		months   int
	}{{"2024-01-31T12:30:00", "2024-02-29T12:30:00", 1}, {"2025-01-31T12:30:00", "2025-02-28T12:30:00", 1}, {"2025-03-29T12:30:00", "2025-04-29T12:30:00", 1}, {"2025-11-30T12:30:00", "2026-02-28T12:30:00", 3}} {
		at, _ := time.ParseInLocation("2006-01-02T15:04:05", tc.at, l)
		got := CalendarDeadline(at, tc.months)
		if got.Format("2006-01-02T15:04:05") != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}
func TestCatalogueRejectsUnapprovedAndInventedDecisionGrounds(t *testing.T) {
	p := testPolicy()
	scope := Scope{Kind: Categories, Categories: []Category{"identity-core", "profile-core"}}
	partial := map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if _, _, e := p.DecisionPlan(scope, partial, "partial", at); e != nil {
		t.Fatal(e)
	}
	if _, _, e := p.DecisionPlan(scope, partial, "approve", at); e == nil {
		t.Fatal("partial accepted as full")
	}
	partial["profile-core"] = CategoryDecision{Outcome: "RETAIN", Ground: "invented"}
	if _, _, e := p.DecisionPlan(scope, partial, "partial", at); e == nil {
		t.Fatal("invented ground")
	}
	for _, months := range []int32{0, 2, 12} {
		p.ResponseMonths = months
		if p.Validate() == nil {
			t.Fatal("invalid response months accepted")
		}
	}
	p = testPolicy()
	p.ExtensionMonths = 12
	if p.Validate() == nil {
		t.Fatal("invalid extension months")
	}
	p = testPolicy()
	p.Categories = append(p.Categories, p.Categories[0])
	if p.Validate() == nil {
		t.Fatal("duplicate catalogue")
	}
	p = testPolicy()
	p.AccountClosureEnabled = false
	if _, e := p.Snapshot(Scope{Kind: AccountClosure}); e == nil {
		t.Fatal("unapproved closure")
	}
}

func TestExecutablePolicyFailsClosedAndPlanIsDeterministic(t *testing.T) {
	p := testPolicy()
	identityIndex := slices.IndexFunc(p.Categories, func(category CatalogueEntry) bool { return category.Key == "identity-core" })
	sessionIndex := slices.IndexFunc(p.Categories, func(category CatalogueEntry) bool { return category.Key == "sessions" })
	for _, mutate := range []func(*AdoptedPolicy){
		func(p *AdoptedPolicy) { p.ExecutorVersion = "future-executor/v9" },
		func(p *AdoptedPolicy) { p.PlanSchemaVersion = "future-plan/v9" },
		func(p *AdoptedPolicy) { p.Categories[0].Key = "unknown-category" },
		func(p *AdoptedPolicy) { p.Categories[0].Rule.Profile = "UNKNOWN_PROFILE" },
		func(p *AdoptedPolicy) { p.Categories[len(p.Categories)-1].Rule.Profile = "PROFILE_CLEAR_V1" },
		func(p *AdoptedPolicy) { p.Categories[0].Rule.Fallback = "CONTINUE" },
		func(p *AdoptedPolicy) { p.Categories[identityIndex].Grounds[0].Rule.RetainedFields = nil },
		func(p *AdoptedPolicy) {
			p.Categories[identityIndex].Grounds[0].Rule.RetainedFields = []string{"users.email"}
		},
		func(p *AdoptedPolicy) { p.Categories[0].Rule.CompleteWithinDays = nil },
		func(p *AdoptedPolicy) { p.Categories[identityIndex].Grounds[0].Rule.ExpireAfter = nil },
		func(p *AdoptedPolicy) { p.Categories[sessionIndex].Rule.RetentionUnit = calendarDayUnit },
		func(p *AdoptedPolicy) { eleven := int32(11); p.Categories[sessionIndex].Rule.ExpireAfter = &eleven },
	} {
		candidate := testPolicy()
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("unsupported policy accepted: %+v", candidate)
		}
	}
	scope := Scope{Kind: Categories, Categories: []Category{"identity-core", "profile-core"}}
	at := time.Date(2026, 9, 8, 12, 0, 0, 123, time.FixedZone("test", 3600))
	inputsA := map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}
	inputsB := map[string]CategoryDecision{"profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}, "identity-core": {Outcome: "APPROVE"}}
	_, planA, err := p.DecisionPlan(scope, inputsA, "partial", at)
	if err != nil {
		t.Fatal(err)
	}
	_, planB, err := p.DecisionPlan(scope, inputsB, "partial", at)
	if err != nil {
		t.Fatal(err)
	}
	planA.RequestVersion, planB.RequestVersion = 2, 2
	a, _ := json.Marshal(planA)
	b, _ := json.Marshal(planB)
	if string(a) != string(b) {
		t.Fatalf("plan depends on map order:\n%s\n%s", a, b)
	}
	if planA.CreatedAt != "2026-09-08T11:00:00.000000123Z" || len(planA.Entries) != 2 || planA.Entries[1].RetentionAnchor != caseClosureAnchor || planA.Entries[1].RetentionUnit != calendarDayUnit || planA.Entries[1].ReviewAfter == nil || planA.Entries[1].ExpireAfter == nil || planA.Entries[1].ReviewAt != "" || planA.Entries[1].ExpireAt != "" {
		t.Fatalf("incomplete frozen plan: %+v", planA)
	}
	requestID := uuid.New()
	created, _ := time.Parse(time.RFC3339Nano, planA.CreatedAt)
	row := dbgen.PrivacyRequestExecutionPlan{RequestID: requestID, PolicyVersion: p.Version, ExecutorVersion: SupportedExecutorVersion, SchemaVersion: SupportedPlanSchemaVersion, Plan: a, CreatedAt: stamp(created)}
	row.PlanSha256 = executionPlanDigest(requestID, created, row.PolicyVersion, row.ExecutorVersion, row.SchemaVersion, a)
	if _, err = ReadExecutionPlan(row); err != nil {
		t.Fatalf("compiled plan rejected: %v", err)
	}
	legacyPlan := planA
	legacyPlan.ExecutorVersion = LegacyExecutorVersion
	legacyPlan.SchemaVersion = LegacyPlanSchemaVersion
	legacyPayload, _ := json.Marshal(legacyPlan)
	legacyRow := row
	legacyRow.ExecutorVersion = LegacyExecutorVersion
	legacyRow.SchemaVersion = LegacyPlanSchemaVersion
	legacyRow.Plan = legacyPayload
	legacyRow.PlanSha256 = executionPlanDigest(requestID, created, legacyRow.PolicyVersion, legacyRow.ExecutorVersion, legacyRow.SchemaVersion, legacyPayload)
	if _, err = ReadExecutionPlan(legacyRow); err != nil {
		t.Fatalf("historical v1 plan is no longer readable: %v", err)
	}
	legacyPolicy := p
	legacyPolicy.ExecutorVersion = LegacyExecutorVersion
	legacyPolicy.PlanSchemaVersion = LegacyPlanSchemaVersion
	if legacyPolicy.validateCompatible() != nil || legacyPolicy.Validate() == nil {
		t.Fatal("historical policy must remain readable without becoming newly importable")
	}
	row.RequestID = uuid.New()
	if _, err = ReadExecutionPlan(row); !errors.Is(err, ErrPolicyUnresolved) {
		t.Fatalf("transplanted plan accepted: %v", err)
	}
	row.RequestID = requestID
	planA.Entries[0].Profile = "UNKNOWN_PROFILE"
	row.Plan, _ = json.Marshal(planA)
	row.PlanSha256 = executionPlanDigest(requestID, created, row.PolicyVersion, row.ExecutorVersion, row.SchemaVersion, row.Plan)
	if _, err = ReadExecutionPlan(row); !errors.Is(err, ErrPolicyUnresolved) {
		t.Fatalf("tampered plan accepted: %v", err)
	}
	planA.Entries[0].Profile = p.category(planA.Entries[0].Category).Rule.Profile
	planA.Entries[0].Purpose = "INVENTED_PURPOSE"
	row.Plan, _ = json.Marshal(planA)
	row.PlanSha256 = executionPlanDigest(requestID, created, row.PolicyVersion, row.ExecutorVersion, row.SchemaVersion, row.Plan)
	if _, err = ReadExecutionPlan(row); !errors.Is(err, ErrPolicyUnresolved) {
		t.Fatalf("category-purpose remapping accepted: %v", err)
	}
}

func TestRetentionSpecsPreserveApprovedUnits(t *testing.T) {
	for category, want := range map[string]retentionSpec{
		"backup-tombstones": {Anchor: caseClosureAnchor, Unit: "CALENDAR_MONTH", Max: 24},
		"consent-evidence":  {Anchor: "CONSENT_END", Unit: "CALENDAR_YEAR", Max: 3},
		"privacy-cases":     {Anchor: caseClosureAnchor, Unit: "CALENDAR_MONTH", Max: 24},
		"sessions":          {Anchor: "SESSION_EXPIRY", Unit: "HOUR", Max: 12},
	} {
		if got := categoryRetentionSpec(category, "APPROVE"); got != want {
			t.Errorf("%s spec=%+v want=%+v", category, got, want)
		}
	}
}
func TestPartialLifecycleNeverClosesOrRevokesAndChecksGuardians(t *testing.T) {
	at := time.Now()
	requester, subject, reviewer := uuid.New(), uuid.New(), uuid.New()
	scope := Scope{Kind: AccountClosure}
	p, _ := testPolicy().Snapshot(scope)
	c, e := Receive(Actor{ID: requester, Active: true}, true, true, subject, scope, true, at)
	if e != nil {
		t.Fatal(e)
	}
	c.Status = UnderReview
	cmd := Command{Action: PartialApprove, ExpectedVersion: 1, Actor: Actor{ID: reviewer, Active: true, PrivacyReviewer: true}, Verification: Verification{IdentityVerified: true, RepresentationVerified: true, CurrentRelationship: true}, Policy: p, At: at}
	if _, e = Transition(c, cmd); !errors.Is(e, ErrClosureSafeguards) {
		t.Fatal(e)
	}
	cmd.Safeguards = ClosureSafeguards{true, true}
	r, e := Transition(c, cmd)
	if e != nil || r.Case.Status != PartiallyApproved || !r.Case.ClosedAt.IsZero() || !r.Case.EvidenceExpiresAt.IsZero() {
		t.Fatalf("partial=%+v %v", r, e)
	}
	cmd.Action = Cancel
	cmd.Actor = Actor{ID: requester, Active: true}
	cmd.ExpectedVersion = 2
	cmd.Verification.RepresentationVerified = false
	if _, e = Transition(r.Case, cmd); !errors.Is(e, ErrVerification) {
		t.Fatal(e)
	}
}
func TestDeliveryDomainSeparationAndNoSensitiveContent(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	d := Delivery{Recipient: "person@example.test", ContactURL: "https://example.test/legal/direitos"}
	sealed, e := SealDelivery(key, d)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(sealed), d.Recipient) {
		t.Fatal("plaintext recipient")
	}
	got, e := OpenDelivery(key, sealed)
	if e != nil || got != d {
		t.Fatal(got, e)
	}
	sealed[len(sealed)-1] ^= 1
	if _, e = OpenDelivery(key, sealed); e == nil {
		t.Fatal("tampered payload")
	}
}

func TestDeliveryRejectsInvalidEnvelopeInputsAndPayloads(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	for _, delivery := range []Delivery{
		{},
		{Recipient: "not-an-email", ContactURL: "https://example.test/legal/direitos"},
		{Recipient: "Person <person@example.test>", ContactURL: "https://example.test/legal/direitos"},
		{Recipient: "person@example.test", ContactURL: "/legal/direitos"},
		{Recipient: "person@example.test", ContactURL: "ftp://example.test/legal/direitos"},
		{Recipient: "person@example.test", ContactURL: "https://example.test/legal/direitos\r\nInjected: true"},
	} {
		if validDelivery(delivery) {
			t.Errorf("invalid delivery accepted: %+v", delivery)
		}
		if _, err := SealDelivery(key, delivery); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid delivery seal error=%v", err)
		}
	}
	valid := Delivery{Recipient: "person@example.test", ContactURL: "https://example.test/legal/direitos"}
	if _, err := SealDelivery([]byte("short"), valid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short key seal error=%v", err)
	}
	for _, payload := range [][]byte{nil, {1, 2, 3}} {
		if _, err := OpenDelivery(key, payload); !errors.Is(err, ErrInvalid) {
			t.Errorf("short payload error=%v", err)
		}
	}
	if _, err := OpenDelivery([]byte("short"), make([]byte, 32)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short key open error=%v", err)
	}
	aead, err := deliveryCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealRaw := func(raw []byte) []byte {
		nonce := make([]byte, aead.NonceSize())
		return aead.Seal(nonce, nonce, raw, []byte("privacy-notification-v1"))
	}
	for _, raw := range [][]byte{[]byte("not-json"), []byte(`{"recipient":"invalid","contact_url":"https://example.test"}`)} {
		if _, err := OpenDelivery(key, sealRaw(raw)); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid decrypted payload error=%v", err)
		}
	}
}

func TestDecisionPlanRejectsIncompleteOrContradictoryInputs(t *testing.T) {
	policy := testPolicy()
	scope := Scope{Kind: Categories, Categories: []Category{"identity-core", "profile-core"}}
	at := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	valid := map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}
	for _, tc := range []struct {
		name   string
		action string
		at     time.Time
		inputs map[string]CategoryDecision
	}{
		{name: "zero decision time", action: "partial", inputs: valid},
		{name: "missing category", action: "partial", at: at, inputs: map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}}},
		{name: "extra category", action: "partial", at: at, inputs: map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}, "invented": {Outcome: "APPROVE"}}},
		{name: "approve with retention ground", action: "partial", at: at, inputs: map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE", Ground: "LEGAL_HOLD"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}},
		{name: "unknown outcome", action: "partial", at: at, inputs: map[string]CategoryDecision{"identity-core": {Outcome: "UNKNOWN"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}},
		{name: "retain without ground", action: "refuse", at: at, inputs: map[string]CategoryDecision{"identity-core": {Outcome: "RETAIN"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}},
		{name: "partial declared approve", action: "approve", at: at, inputs: valid},
		{name: "all approved declared partial", action: "partial", at: at, inputs: map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "APPROVE"}}},
		{name: "approval declared refusal", action: "refuse", at: at, inputs: map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := policy.DecisionPlan(scope, tc.inputs, tc.action, tc.at); err == nil {
				t.Fatal("contradictory decision plan accepted")
			}
		})
	}
}

func TestStoredExecutionPlanRejectsSemanticTampering(t *testing.T) {
	policy := testPolicy()
	scope := Scope{Kind: Categories, Categories: []Category{"identity-core", "profile-core"}}
	created := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	_, base, err := policy.DecisionPlan(scope, map[string]CategoryDecision{"identity-core": {Outcome: "APPROVE"}, "profile-core": {Outcome: "RETAIN", Ground: "LEGAL_HOLD"}}, "partial", created)
	if err != nil {
		t.Fatal(err)
	}
	base.RequestVersion = 2
	requestID := uuid.New()
	rowFor := func(plan ExecutionPlan) dbgen.PrivacyRequestExecutionPlan {
		payload, marshalErr := json.Marshal(plan)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return dbgen.PrivacyRequestExecutionPlan{RequestID: requestID, PolicyVersion: policy.Version, ExecutorVersion: SupportedExecutorVersion, SchemaVersion: SupportedPlanSchemaVersion, Plan: payload, PlanSha256: executionPlanDigest(requestID, created, policy.Version, SupportedExecutorVersion, SupportedPlanSchemaVersion, payload), CreatedAt: stamp(created)}
	}
	if _, err := ReadExecutionPlan(rowFor(base)); err != nil {
		t.Fatalf("valid stored plan rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ExecutionPlan)
	}{
		{name: "old request version", mutate: func(p *ExecutionPlan) { p.RequestVersion = 1 }},
		{name: "unknown decision action", mutate: func(p *ExecutionPlan) { p.DecisionAction = "future" }},
		{name: "empty plan", mutate: func(p *ExecutionPlan) { p.Entries = nil }},
		{name: "operation remapping", mutate: func(p *ExecutionPlan) { p.Entries[0].Operations = []string{"INVENTED"} }},
		{name: "duplicate category", mutate: func(p *ExecutionPlan) { p.Entries[1].Category = p.Entries[0].Category }},
		{name: "invalid deadline anchor", mutate: func(p *ExecutionPlan) { p.Entries[0].DeadlineAnchor = "REQUEST_RECEIVED" }},
		{name: "deadline before decision", mutate: func(p *ExecutionPlan) { p.Entries[0].DueAt = created.Add(-time.Second).Format(time.RFC3339Nano) }},
		{name: "unexpected retained field", mutate: func(p *ExecutionPlan) { p.Entries[0].RetainedFields = []string{"users.id"} }},
		{name: "unsorted retained fields", mutate: func(p *ExecutionPlan) {
			p.Entries[1].RetainedFields = []string{"member_profile.federation_id", "member_profile.federation_id"}
		}},
		{name: "unresolved anchor has timestamp", mutate: func(p *ExecutionPlan) { p.Entries[1].ExpireAt = created.AddDate(0, 0, 90).Format(time.RFC3339Nano) }},
		{name: "wrong retention unit", mutate: func(p *ExecutionPlan) { p.Entries[1].RetentionUnit = "HOUR" }},
		{name: "expiry beyond contract", mutate: func(p *ExecutionPlan) { tooLong := int32(36501); p.Entries[1].ExpireAfter = &tooLong }},
		{name: "scope entry missing", mutate: func(p *ExecutionPlan) { p.ScopeCategories = []string{"identity-core"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			candidate.ScopeCategories = append([]string(nil), base.ScopeCategories...)
			candidate.Entries = append([]ExecutionPlanEntry(nil), base.Entries...)
			for i := range candidate.Entries {
				candidate.Entries[i].Operations = append([]string(nil), base.Entries[i].Operations...)
				candidate.Entries[i].RetainedFields = append([]string(nil), base.Entries[i].RetainedFields...)
			}
			tc.mutate(&candidate)
			if _, err := ReadExecutionPlan(rowFor(candidate)); !errors.Is(err, ErrPolicyUnresolved) {
				t.Fatalf("tampered plan error=%v", err)
			}
		})
	}

	badDigest := rowFor(base)
	badDigest.PlanSha256[0] ^= 1
	if _, err := ReadExecutionPlan(badDigest); !errors.Is(err, ErrPolicyUnresolved) {
		t.Fatalf("bad digest error=%v", err)
	}
}

func TestRetentionOffsetsAndScopeValidationUseClosedContracts(t *testing.T) {
	at := time.Date(2026, time.January, 31, 12, 0, 0, 0, time.UTC)
	if got := addRetentionOffset(at, 12, "HOUR"); !got.Equal(at.Add(12 * time.Hour)) {
		t.Fatalf("hour offset=%v", got)
	}
	if got := addRetentionOffset(at, 12, calendarDayUnit); !got.Equal(at.AddDate(0, 0, 12)) {
		t.Fatalf("day offset=%v", got)
	}
	if got := addRetentionOffset(at, 1, "CALENDAR_MONTH"); !got.Equal(time.Date(2026, time.February, 28, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("month offset=%v", got)
	}
	if got := addRetentionOffset(at, 3, "CALENDAR_YEAR"); !got.Equal(at.AddDate(3, 0, 0)) {
		t.Fatalf("year offset=%v", got)
	}
	if got := addRetentionOffset(at, 1, "UNKNOWN"); !got.IsZero() {
		t.Fatalf("unknown unit offset=%v", got)
	}
	if !AccountClosure.validWith(nil) || AccountClosure.validWith([]string{"identity-core"}) || Categories.validWith(nil) || !Categories.validWith([]string{"identity-core"}) || ScopeKind("UNKNOWN").validWith(nil) {
		t.Fatal("scope/category contract accepted an invalid combination")
	}
}
