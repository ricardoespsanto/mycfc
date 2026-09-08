package privacyrequests

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"slices"
	"sort"
	"time"
)

const (
	SupportedExecutorVersion   = "privacy-erasure-executor/v1"
	SupportedPlanSchemaVersion = "privacy-erasure-plan/v1"
	SupportedActionVersion     = "v1"
	decisionRecordedAnchor     = "DECISION_RECORDED"
	caseClosureAnchor          = "CASE_CLOSURE"
	calendarDayUnit            = "CALENDAR_DAY"
)

// ExecutionRule is controller-approved input to the plan compiler. The web
// form never supplies it. Stable operation and field codes are closed here so
// prose or a future unknown integration cannot become executable by accident.
type ExecutionRule struct {
	Profile            string   `json:"profile"`
	RetainedFields     []string `json:"retained_fields,omitempty"`
	LegalGround        string   `json:"legal_ground"`
	Owner              string   `json:"owner"`
	CompleteWithinDays *int32   `json:"complete_within_days"`
	RetentionUnit      string   `json:"retention_unit,omitempty"`
	ReviewAfter        *int32   `json:"review_after,omitempty"`
	ExpireAfter        *int32   `json:"expire_after,omitempty"`
	Fallback           string   `json:"fallback"`
}

type ExecutionPlanEntry struct {
	Category        string   `json:"category"`
	Purpose         string   `json:"purpose"`
	Outcome         string   `json:"outcome"`
	Ground          string   `json:"ground,omitempty"`
	Profile         string   `json:"profile"`
	Disposition     string   `json:"disposition"`
	ActionVersion   string   `json:"action_version"`
	Operations      []string `json:"operations"`
	RetainedFields  []string `json:"retained_fields,omitempty"`
	LegalGround     string   `json:"legal_ground"`
	Owner           string   `json:"owner"`
	DeadlineAnchor  string   `json:"deadline_anchor"`
	DueAt           string   `json:"due_at"`
	RetentionAnchor string   `json:"retention_anchor,omitempty"`
	RetentionUnit   string   `json:"retention_unit,omitempty"`
	ReviewAfter     *int32   `json:"review_after,omitempty"`
	ExpireAfter     *int32   `json:"expire_after,omitempty"`
	ReviewAt        string   `json:"review_at,omitempty"`
	ExpireAt        string   `json:"expire_at,omitempty"`
	Fallback        string   `json:"fallback"`
}

type ExecutionPlan struct {
	PolicyVersion   string               `json:"policy_version"`
	ExecutorVersion string               `json:"executor_version"`
	SchemaVersion   string               `json:"schema_version"`
	RequestVersion  int64                `json:"request_version"`
	ScopeKind       string               `json:"scope_kind"`
	ScopeCategories []string             `json:"scope_categories"`
	DecisionAction  string               `json:"decision_action"`
	CreatedAt       string               `json:"created_at"`
	Entries         []ExecutionPlanEntry `json:"entries"`
}

type executionProfile struct {
	Disposition string
	Operations  []string
	Fields      map[string]bool
}

var executionProfiles = map[string]executionProfile{
	"ACTIVITY_CONNECTION_CLEAR_V1":    {"DELETE", []string{"ACTIVITY_CONNECTION_DISCONNECT", "ACTIVITY_SUBJECT_DELETE"}, nil},
	"ANNOUNCEMENT_DELIVERY_CLEAR_V1":  {"DELETE", []string{"ANNOUNCEMENT_DELIVERY_DELETE"}, nil},
	"AUDIT_ACTOR_ANONYMIZE_V1":        {"ANONYMIZE", []string{"AUDIT_ACTOR_ANONYMIZE"}, nil},
	"AUTH_ACCESS_REVOKE_V1":           {"DELETE", []string{"AUTH_ACCESS_REVOKE"}, nil},
	"AUTH_SESSION_EXPIRE_V1":          {"EXPIRE", []string{"AUTH_SESSION_EXPIRE"}, fields("users.id")},
	"AUTH_TOKEN_CLEAR_V1":             {"DELETE", []string{"AUTH_TOKEN_DELETE"}, nil},
	"BACKUP_TOMBSTONE_RESTRICT_V1":    {"RESTRICT", []string{"BACKUP_TOMBSTONE_REPLAY"}, fields("users.id")},
	"CONSENT_EVIDENCE_RESTRICT_V1":    {"RESTRICT", []string{"CONSENT_EVIDENCE_RESTRICT"}, fields("consent.decided_at", "consent.decision", "consent.document_hash", "consent.document_type", "consent.document_version", "consent.method")},
	"CONSENT_NETWORK_EXPIRE_V1":       {"EXPIRE", []string{"CONSENT_NETWORK_EXPIRE"}, fields("consent.decided_at")},
	"DEPENDANT_RELATION_CLEAR_V1":     {"DELETE", []string{"DEPENDANT_RELATIONSHIP_DELETE"}, nil},
	"EVENT_RESPONSE_CLEAR_V1":         {"DELETE", []string{"EVENT_RESPONSE_DELETE"}, nil},
	"IDENTITY_CLEAR_V1":               {"DELETE", []string{"IDENTITY_CLEAR"}, nil},
	"IDENTITY_RESTRICT_V1":            {"RESTRICT", []string{"IDENTITY_RESTRICT"}, fields("users.id")},
	"LOG_EXPIRE_V1":                   {"EXPIRE", []string{"LOG_RECORD_EXPIRE"}, fields("audit.action", "audit.occurred_at", "audit.reason_code")},
	"MEMBERSHIP_HISTORY_ANONYMIZE_V1": {"ANONYMIZE", []string{"MEMBERSHIP_ACTIVE_REVOKE", "MEMBERSHIP_HISTORY_ANONYMIZE"}, nil},
	"OBJECT_VERSIONS_CLEAR_V1":        {"DELETE", []string{"OBJECT_VERSION_DELETE"}, nil},
	"OUTBOX_EXPIRE_V1":                {"EXPIRE", []string{"OUTBOX_PAYLOAD_EXPIRE"}, fields("audit.occurred_at")},
	"PRIVACY_CASE_RESTRICT_V1":        {"RESTRICT", []string{"PRIVACY_CASE_RESTRICT"}, fields("privacy_request.completed_at", "privacy_request.decided_at", "privacy_request.decision_code", "privacy_request.evidence_expires_at", "privacy_request.public_ref", "privacy_request.received_at", "privacy_request.result_code")},
	"PROFILE_CLEAR_V1":                {"DELETE", []string{"PROFILE_IDENTITY_DELETE"}, nil},
	"PROFILE_HEALTH_CLEAR_V1":         {"DELETE", []string{"PROFILE_HEALTH_DELETE"}, nil},
	"PROFILE_RESTRICT_V1":             {"RESTRICT", []string{"PROFILE_RESTRICT"}, fields("member_profile.federation_id")},
	"PROVIDER_RECIPIENT_RESTRICT_V1":  {"RESTRICT", []string{"PROVIDER_RECIPIENT_NOTIFY"}, fields("provider.receipt")},
	"REPAIR_REPORTER_ANONYMIZE_V1":    {"ANONYMIZE", []string{"REPAIR_REPORTER_ANONYMIZE"}, nil},
	"SUGGESTION_CLEAR_V1":             {"DELETE", []string{"SUGGESTION_SUBJECT_DELETE"}, nil},
	"TRAINING_PRESCRIPTION_CLEAR_V1":  {"DELETE", []string{"TRAINING_PRESCRIPTION_DELETE"}, nil},
	"TRAINING_RESULT_CLEAR_V1":        {"DELETE", []string{"TRAINING_RESULT_DELETE"}, nil},
}

type categoryContract struct {
	Purpose                string
	DefaultProfile         string
	RetentionProfiles      map[string]bool
	Owners                 map[string]bool
	MaxCompleteDays        int32
	MaxGroundRetentionDays int32
}

func codes(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

// categoryContracts is the versioned server-side meaning of the selectable
// catalogue. Policy files may label or narrow these entries, but cannot bind a
// category to a different destructive profile, owner function or purpose.
var categoryContracts = map[string]categoryContract{
	"announcement-deliveries": {"CLUB_COMMUNICATION", "ANNOUNCEMENT_DELIVERY_CLEAR_V1", nil, codes("CONTENT", "PRIVACY"), 90, 0},
	"audit-evidence":          {"ACCOUNTABILITY", "AUDIT_ACTOR_ANONYMIZE_V1", nil, codes("SECURITY", "PRIVACY"), 0, 0},
	"backup-tombstones":       {"RESTORE_SAFETY", "BACKUP_TOMBSTONE_RESTRICT_V1", nil, codes("OPERATIONS", "SECURITY"), 0, 0},
	"consent-evidence":        {"CONSENT_ACCOUNTABILITY", "CONSENT_EVIDENCE_RESTRICT_V1", nil, codes("PRIVACY", "LEGAL"), 0, 0},
	"consent-network":         {"CONSENT_MINIMISATION", "CONSENT_NETWORK_EXPIRE_V1", nil, codes("PRIVACY", "SECURITY"), 0, 0},
	"dependant-relations":     {"CHILD_SAFEGUARDING", "DEPENDANT_RELATION_CLEAR_V1", nil, codes("PRIVACY", "SAFEGUARDING"), 0, 0},
	"event-responses":         {"EVENT_PARTICIPATION", "EVENT_RESPONSE_CLEAR_V1", nil, codes("OPERATIONS", "PRIVACY"), 90, 0},
	"health-emergency":        {"PARTICIPANT_SAFETY", "PROFILE_HEALTH_CLEAR_V1", nil, codes("SAFEGUARDING", "PRIVACY"), 0, 0},
	"identity-core":           {"ACCOUNT_IDENTITY", "IDENTITY_CLEAR_V1", codes("IDENTITY_RESTRICT_V1"), codes("PRIVACY"), 0, 36500},
	"membership-history":      {"SPORT_RELATIONSHIP", "MEMBERSHIP_HISTORY_ANONYMIZE_V1", nil, codes("SECRETARIAT", "SPORT"), 0, 0},
	"object-storage":          {"PRIVATE_MEDIA", "OBJECT_VERSIONS_CLEAR_V1", nil, codes("IT", "PRIVACY"), 30, 0},
	"operational-logs":        {"SYSTEM_SECURITY", "LOG_EXPIRE_V1", nil, codes("SECURITY", "OPERATIONS"), 0, 0},
	"outbox-email":            {"MESSAGE_DELIVERY", "OUTBOX_EXPIRE_V1", nil, codes("IT", "PRIVACY"), 0, 0},
	"privacy-cases":           {"RIGHTS_ACCOUNTABILITY", "PRIVACY_CASE_RESTRICT_V1", nil, codes("PRIVACY", "LEGAL"), 0, 0},
	"profile-core":            {"MEMBER_ADMINISTRATION", "PROFILE_CLEAR_V1", codes("PROFILE_RESTRICT_V1"), codes("SECRETARIAT", "PRIVACY"), 0, 36500},
	"profile-photo":           {"OPTIONAL_IDENTIFICATION", "OBJECT_VERSIONS_CLEAR_V1", nil, codes("IT", "PRIVACY"), 30, 0},
	"repair-history":          {"EQUIPMENT_SAFETY", "REPAIR_REPORTER_ANONYMIZE_V1", nil, codes("OPERATIONS", "PRIVACY"), 0, 0},
	"sessions":                {"AUTHENTICATION", "AUTH_SESSION_EXPIRE_V1", nil, codes("IT", "SECURITY"), 0, 0},
	"suggestions":             {"MEMBER_PARTICIPATION", "SUGGESTION_CLEAR_V1", nil, codes("MODERATION", "PRIVACY"), 0, 0},
	"training-prescriptions":  {"SPORT_DELIVERY", "TRAINING_PRESCRIPTION_CLEAR_V1", nil, codes("SPORT", "PRIVACY"), 0, 0},
	"training-results":        {"SPORT_HISTORY", "TRAINING_RESULT_CLEAR_V1", nil, codes("SPORT", "PRIVACY"), 0, 0},
	"verification-reset":      {"ACCOUNT_SECURITY", "AUTH_TOKEN_CLEAR_V1", nil, codes("IT", "SECURITY"), 0, 0},
}

// Default retained profiles use the source event that actually starts their
// approved clock. Those anchors are deliberately unresolved in a decision
// plan; #111 must resolve them from the affected record or actual case closure.
type retentionSpec struct {
	Anchor string
	Unit   string
	Max    int32
}

var categoryRetentionSpecs = map[string]retentionSpec{
	"backup-tombstones": {"CASE_CLOSURE", "CALENDAR_MONTH", 24},
	"consent-evidence":  {"CONSENT_END", "CALENDAR_YEAR", 3},
	"consent-network":   {"CONSENT_DECISION", "CALENDAR_MONTH", 12},
	"operational-logs":  {"RECORD_OCCURRED", calendarDayUnit, 90},
	"outbox-email":      {"RECORD_OCCURRED", calendarDayUnit, 90},
	"privacy-cases":     {"CASE_CLOSURE", "CALENDAR_MONTH", 24},
	"sessions":          {"SESSION_EXPIRY", "HOUR", 12},
}

func categoryRetentionSpec(category, outcome string) retentionSpec {
	if outcome == "RETAIN" {
		return retentionSpec{caseClosureAnchor, calendarDayUnit, categoryContracts[category].MaxGroundRetentionDays}
	}
	return categoryRetentionSpecs[category]
}

func categoryPurpose(category string) string {
	return categoryContracts[category].Purpose
}

func validateCategoryRule(category string, rule ExecutionRule, retentionGround bool) error {
	contract, ok := categoryContracts[category]
	if !ok || rule.validate(retentionGround) != nil || !contract.Owners[rule.Owner] || *rule.CompleteWithinDays > contract.MaxCompleteDays {
		return ErrPolicyUnresolved
	}
	if retentionGround {
		if !contract.RetentionProfiles[rule.Profile] || rule.Owner != "PRIVACY" || (rule.LegalGround != "COMPLAINT" && rule.LegalGround != "LEGAL_HOLD") {
			return ErrPolicyUnresolved
		}
	} else if rule.Profile != contract.DefaultProfile {
		return ErrPolicyUnresolved
	}
	outcome := "APPROVE"
	if retentionGround {
		outcome = "RETAIN"
	}
	spec := categoryRetentionSpec(category, outcome)
	if rule.ExpireAfter != nil {
		if rule.RetentionUnit != spec.Unit || *rule.ExpireAfter > spec.Max || (!retentionGround && *rule.ExpireAfter != spec.Max) {
			return ErrPolicyUnresolved
		}
	}
	return nil
}

func fields(codes ...string) map[string]bool {
	out := make(map[string]bool, len(codes))
	for _, code := range codes {
		out[code] = true
	}
	return out
}

var ownerCodes = map[string]bool{
	"CONTENT": true, "IT": true, "LEGAL": true, "MODERATION": true,
	"OPERATIONS": true, "PRIVACY": true, "SAFEGUARDING": true,
	"SECRETARIAT": true, "SECURITY": true, "SPORT": true,
}

func (r ExecutionRule) validate(retentionGround bool) error {
	profile, ok := executionProfiles[r.Profile]
	if !ok || !policyKey.MatchString(r.LegalGround) || !ownerCodes[r.Owner] || r.Fallback != "BLOCK" || r.CompleteWithinDays == nil || *r.CompleteWithinDays < 0 || *r.CompleteWithinDays > 36500 {
		return ErrPolicyUnresolved
	}
	seenFields := map[string]bool{}
	for _, field := range r.RetainedFields {
		if !profile.Fields[field] || seenFields[field] {
			return ErrPolicyUnresolved
		}
		seenFields[field] = true
	}
	retained := profile.Disposition == "RESTRICT" || profile.Disposition == "EXPIRE"
	if retained != (len(r.RetainedFields) > 0) || (retentionGround && !retained) {
		return ErrPolicyUnresolved
	}
	if retained {
		if r.ExpireAfter == nil || r.ReviewAfter == nil || *r.ExpireAfter < 1 || *r.ExpireAfter > 36500 || *r.ReviewAfter < 1 || *r.ReviewAfter > *r.ExpireAfter || r.RetentionUnit == "" {
			return ErrPolicyUnresolved
		}
	} else if r.ReviewAfter != nil || r.ExpireAfter != nil || r.RetentionUnit != "" {
		return ErrPolicyUnresolved
	}
	return nil
}

func (p AdoptedPolicy) DecisionPlan(scope Scope, inputs map[string]CategoryDecision, action string, at time.Time) ([]CategoryDecision, ExecutionPlan, error) {
	var empty ExecutionPlan
	if at.IsZero() || p.Validate() != nil {
		return nil, empty, ErrPolicyUnresolved
	}
	snapshot, err := p.Snapshot(scope)
	if err != nil {
		return nil, empty, err
	}
	if len(inputs) != len(snapshot.Actions) {
		return nil, empty, ErrInvalid
	}
	at = at.UTC()
	scopeCategories := make([]string, len(scope.Categories))
	for i, category := range scope.Categories {
		scopeCategories[i] = string(category)
	}
	sort.Strings(scopeCategories)
	plan := ExecutionPlan{PolicyVersion: p.Version, ExecutorVersion: p.ExecutorVersion, SchemaVersion: p.PlanSchemaVersion, ScopeKind: string(scope.Kind), ScopeCategories: scopeCategories, DecisionAction: action, CreatedAt: at.Format(time.RFC3339Nano)}
	decisions := make([]CategoryDecision, 0, len(inputs))
	approved := 0
	for _, selected := range snapshot.Actions {
		category := p.category(string(selected.Category))
		input, ok := inputs[string(selected.Category)]
		if category == nil || !ok {
			return nil, empty, ErrInvalid
		}
		input.Category = category.Key
		var rule ExecutionRule
		switch input.Outcome {
		case "APPROVE":
			if input.Ground != "" {
				return nil, empty, ErrInvalid
			}
			rule = category.Rule
			approved++
		case "RETAIN":
			ground := category.ground(input.Ground)
			if ground == nil {
				return nil, empty, ErrPolicyUnresolved
			}
			rule = ground.Rule
		default:
			return nil, empty, ErrInvalid
		}
		profile := executionProfiles[rule.Profile]
		input.Action = profile.Disposition
		decisions = append(decisions, input)
		operations := slices.Clone(profile.Operations)
		fields := slices.Clone(rule.RetainedFields)
		sort.Strings(operations)
		sort.Strings(fields)
		entry := ExecutionPlanEntry{Category: category.Key, Purpose: categoryPurpose(category.Key), Outcome: input.Outcome, Ground: input.Ground, Profile: rule.Profile, Disposition: profile.Disposition, ActionVersion: SupportedActionVersion, Operations: operations, RetainedFields: fields, LegalGround: rule.LegalGround, Owner: rule.Owner, DeadlineAnchor: decisionRecordedAnchor, DueAt: at.AddDate(0, 0, int(*rule.CompleteWithinDays)).Format(time.RFC3339Nano), Fallback: rule.Fallback}
		if rule.ReviewAfter != nil && rule.ExpireAfter != nil {
			spec := categoryRetentionSpec(category.Key, input.Outcome)
			entry.RetentionAnchor = spec.Anchor
			if entry.RetentionAnchor == "" || rule.RetentionUnit != spec.Unit {
				return nil, empty, ErrPolicyUnresolved
			}
			entry.RetentionUnit = spec.Unit
			entry.ReviewAfter = rule.ReviewAfter
			entry.ExpireAfter = rule.ExpireAfter
			// A refused case closes at this decision. Other anchors depend on a
			// future lifecycle or source-record event and must remain unresolved.
			if entry.RetentionAnchor == caseClosureAnchor && action == "refuse" {
				entry.ReviewAt = addRetentionOffset(at, *rule.ReviewAfter, spec.Unit).Format(time.RFC3339Nano)
				entry.ExpireAt = addRetentionOffset(at, *rule.ExpireAfter, spec.Unit).Format(time.RFC3339Nano)
			}
		}
		plan.Entries = append(plan.Entries, entry)
	}
	if (action == "approve" && approved != len(decisions)) || (action == "partial" && (approved == 0 || approved == len(decisions))) || (action == "refuse" && approved != 0) {
		return nil, empty, ErrInvalid
	}
	return decisions, plan, nil
}

// ReadExecutionPlan is the only supported handoff to the future executor. It
// validates the stored compatibility contract and the server-owned profile
// expansion without consulting mutable policy prose or schema relationships.
func ReadExecutionPlan(row dbgen.PrivacyRequestExecutionPlan) (ExecutionPlan, error) {
	var plan ExecutionPlan
	if row.RequestID == uuid.Nil || row.PolicyVersion == "" || row.ExecutorVersion != SupportedExecutorVersion || row.SchemaVersion != SupportedPlanSchemaVersion || len(row.PlanSha256) != 32 || json.Unmarshal(row.Plan, &plan) != nil {
		return ExecutionPlan{}, ErrPolicyUnresolved
	}
	created, err := time.Parse(time.RFC3339Nano, plan.CreatedAt)
	if err != nil || !row.CreatedAt.Valid || !created.Equal(row.CreatedAt.Time) || plan.PolicyVersion != row.PolicyVersion || plan.ExecutorVersion != row.ExecutorVersion || plan.SchemaVersion != row.SchemaVersion || plan.RequestVersion < 2 || !ScopeKind(plan.ScopeKind).validWith(plan.ScopeCategories) || (plan.DecisionAction != "approve" && plan.DecisionAction != "partial" && plan.DecisionAction != "refuse") || len(plan.Entries) == 0 || len(plan.Entries) > 50 {
		return ExecutionPlan{}, ErrPolicyUnresolved
	}
	canonical, err := json.Marshal(plan)
	if err != nil || !hmac.Equal(row.PlanSha256, executionPlanDigest(row.RequestID, row.CreatedAt.Time, row.PolicyVersion, row.ExecutorVersion, row.SchemaVersion, canonical)) {
		return ExecutionPlan{}, ErrPolicyUnresolved
	}
	seen := map[string]bool{}
	approved := 0
	for _, entry := range plan.Entries {
		profile, ok := executionProfiles[entry.Profile]
		contract, categoryOK := categoryContracts[entry.Category]
		due, dueErr := time.Parse(time.RFC3339Nano, entry.DueAt)
		if !ok || !categoryOK || seen[entry.Category] || entry.Purpose != contract.Purpose || entry.Disposition != profile.Disposition || entry.ActionVersion != SupportedActionVersion || !contract.Owners[entry.Owner] || !policyKey.MatchString(entry.LegalGround) || entry.Fallback != "BLOCK" || entry.DeadlineAnchor != decisionRecordedAnchor || dueErr != nil || due.Before(created) || due.After(created.AddDate(0, 0, int(contract.MaxCompleteDays))) {
			return ExecutionPlan{}, ErrPolicyUnresolved
		}
		seen[entry.Category] = true
		operations := slices.Clone(profile.Operations)
		sort.Strings(operations)
		if !slices.Equal(entry.Operations, operations) || !sort.StringsAreSorted(entry.RetainedFields) {
			return ExecutionPlan{}, ErrPolicyUnresolved
		}
		for i, field := range entry.RetainedFields {
			if !profile.Fields[field] || (i > 0 && entry.RetainedFields[i-1] == field) {
				return ExecutionPlan{}, ErrPolicyUnresolved
			}
		}
		retained := profile.Disposition == "RESTRICT" || profile.Disposition == "EXPIRE"
		if retained != (len(entry.RetainedFields) > 0) || (entry.Outcome == "RETAIN") != (entry.Ground != "") || (entry.Outcome != "APPROVE" && entry.Outcome != "RETAIN") {
			return ExecutionPlan{}, ErrPolicyUnresolved
		}
		if entry.Outcome == "APPROVE" {
			approved++
			if entry.Profile != contract.DefaultProfile {
				return ExecutionPlan{}, ErrPolicyUnresolved
			}
		} else if !contract.RetentionProfiles[entry.Profile] || entry.LegalGround != entry.Ground || (entry.Ground != "COMPLAINT" && entry.Ground != "LEGAL_HOLD") {
			return ExecutionPlan{}, ErrPolicyUnresolved
		}
		if retained {
			spec := categoryRetentionSpec(entry.Category, entry.Outcome)
			if entry.RetentionAnchor != spec.Anchor || entry.RetentionUnit != spec.Unit || entry.ReviewAfter == nil || entry.ExpireAfter == nil || *entry.ReviewAfter < 1 || *entry.ExpireAfter < *entry.ReviewAfter || *entry.ExpireAfter > spec.Max || (entry.Outcome == "APPROVE" && *entry.ExpireAfter != spec.Max) {
				return ExecutionPlan{}, ErrPolicyUnresolved
			}
			resolvedAtDecision := entry.RetentionAnchor == caseClosureAnchor && plan.DecisionAction == "refuse"
			if resolvedAtDecision {
				review, reviewErr := time.Parse(time.RFC3339Nano, entry.ReviewAt)
				expires, expireErr := time.Parse(time.RFC3339Nano, entry.ExpireAt)
				if reviewErr != nil || expireErr != nil || !review.Equal(addRetentionOffset(created, *entry.ReviewAfter, spec.Unit)) || !expires.Equal(addRetentionOffset(created, *entry.ExpireAfter, spec.Unit)) {
					return ExecutionPlan{}, ErrPolicyUnresolved
				}
			} else if entry.ReviewAt != "" || entry.ExpireAt != "" {
				return ExecutionPlan{}, ErrPolicyUnresolved
			}
		} else if entry.RetentionAnchor != "" || entry.RetentionUnit != "" || entry.ReviewAfter != nil || entry.ExpireAfter != nil || entry.ReviewAt != "" || entry.ExpireAt != "" {
			return ExecutionPlan{}, ErrPolicyUnresolved
		}
	}
	if (plan.DecisionAction == "approve" && approved != len(plan.Entries)) || (plan.DecisionAction == "partial" && (approved == 0 || approved == len(plan.Entries))) || (plan.DecisionAction == "refuse" && approved != 0) {
		return ExecutionPlan{}, ErrPolicyUnresolved
	}
	if plan.ScopeKind == string(AccountClosure) {
		if len(seen) != len(categoryContracts) {
			return ExecutionPlan{}, ErrPolicyUnresolved
		}
	} else if len(seen) != len(plan.ScopeCategories) {
		return ExecutionPlan{}, ErrPolicyUnresolved
	} else {
		for _, category := range plan.ScopeCategories {
			if !seen[category] {
				return ExecutionPlan{}, ErrPolicyUnresolved
			}
		}
	}
	return plan, nil
}

func addRetentionOffset(at time.Time, amount int32, unit string) time.Time {
	switch unit {
	case "HOUR":
		return at.Add(time.Duration(amount) * time.Hour)
	case calendarDayUnit:
		return at.AddDate(0, 0, int(amount))
	case "CALENDAR_MONTH":
		return CalendarDeadline(at, int(amount)).UTC()
	case "CALENDAR_YEAR":
		return at.AddDate(int(amount), 0, 0)
	default:
		return time.Time{}
	}
}

func executionPlanDigest(requestID uuid.UUID, createdAt time.Time, policyVersion, executorVersion, schemaVersion string, canonical []byte) []byte {
	hash := sha256.New()
	for _, part := range [][]byte{requestID[:], []byte(createdAt.UTC().Format(time.RFC3339Nano)), []byte(policyVersion), []byte(executorVersion), []byte(schemaVersion), canonical} {
		hash.Write(part)
		hash.Write([]byte{0x1f})
	}
	return hash.Sum(nil)
}

func (k ScopeKind) validWith(categories []string) bool {
	if k == AccountClosure {
		return len(categories) == 0
	}
	if k != Categories || len(categories) == 0 {
		return false
	}
	for i, category := range categories {
		if !policyKey.MatchString(category) || (i > 0 && categories[i-1] >= category) {
			return false
		}
	}
	return true
}

func (p AdoptedPolicy) category(key string) *CatalogueEntry {
	for i := range p.Categories {
		if p.Categories[i].Key == key {
			return &p.Categories[i]
		}
	}
	return nil
}

func (c CatalogueEntry) ground(code string) *Ground {
	for i := range c.Grounds {
		if c.Grounds[i].Code == code {
			return &c.Grounds[i]
		}
	}
	return nil
}
