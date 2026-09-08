package privacyrequests

import (
	"encoding/json"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"strings"
	"time"
	"unicode/utf8"
)

const ApprovedWorkingRetentionDays int32 = 90

// CatalogueEntry is controller-approved metadata, never inferred from account data.
type CatalogueEntry struct {
	Key         string        `json:"key"`
	Label       string        `json:"label"`
	Description string        `json:"description"`
	Rule        ExecutionRule `json:"rule"`
	Grounds     []Ground      `json:"grounds"`
}
type Ground struct {
	Code  string        `json:"code"`
	Label string        `json:"label"`
	Rule  ExecutionRule `json:"rule"`
}
type AdoptedPolicy struct {
	Version               string           `json:"version"`
	ExecutorVersion       string           `json:"executor_version"`
	PlanSchemaVersion     string           `json:"plan_schema_version"`
	Categories            []CatalogueEntry `json:"categories"`
	AccountClosureEnabled bool             `json:"account_closure_enabled"`
	WorkingRetentionDays  int32            `json:"working_retention_days"`
	ResponseMonths        int32            `json:"response_months"`
	ExtensionMonths       int32            `json:"extension_months"`
}

func ReadPolicy(row dbgen.PrivacyRequestPolicy) (AdoptedPolicy, error) {
	p := AdoptedPolicy{Version: row.Version, AccountClosureEnabled: row.AccountClosureEnabled, ResponseMonths: row.ResponseMonths, ExtensionMonths: row.ExtensionMonths}
	if !row.AdoptedAt.Valid || row.AdoptedBy == nil || row.WorkingRetentionDays == nil || row.ExecutorVersion == nil || row.PlanSchemaVersion == nil {
		return p, ErrPolicyUnresolved
	}
	p.ExecutorVersion = *row.ExecutorVersion
	p.PlanSchemaVersion = *row.PlanSchemaVersion
	p.WorkingRetentionDays = *row.WorkingRetentionDays
	if json.Unmarshal(row.CategoryCatalogue, &p.Categories) != nil || p.Validate() != nil {
		return p, ErrPolicyUnresolved
	}
	return p, nil
}
func (p AdoptedPolicy) Validate() error {
	if !policyKey.MatchString(p.Version) || len(p.Version) > 80 || p.ExecutorVersion != SupportedExecutorVersion || p.PlanSchemaVersion != SupportedPlanSchemaVersion || p.ResponseMonths != 1 || p.ExtensionMonths != 2 || p.WorkingRetentionDays < 1 || p.WorkingRetentionDays > 36500 || len(p.Categories) == 0 || len(p.Categories) > 50 {
		return ErrPolicyUnresolved
	}
	seen := map[string]bool{}
	for _, c := range p.Categories {
		if !policyKey.MatchString(c.Key) || seen[c.Key] || !bounded(c.Label, 200) || utf8.RuneCountInString(c.Description) > 1000 || validateCategoryRule(c.Key, c.Rule, false) != nil {
			return ErrPolicyUnresolved
		}
		seen[c.Key] = true
		gs := map[string]bool{}
		for _, g := range c.Grounds {
			if !policyKey.MatchString(g.Code) || gs[g.Code] || !bounded(g.Label, 200) || g.Rule.LegalGround != g.Code || validateCategoryRule(c.Key, g.Rule, true) != nil {
				return ErrPolicyUnresolved
			}
			gs[g.Code] = true
		}
	}
	if p.AccountClosureEnabled && len(seen) != len(categoryContracts) {
		return ErrPolicyUnresolved
	}
	return nil
}
func (p AdoptedPolicy) Snapshot(scope Scope) (Policy, error) {
	if p.Validate() != nil || !scope.valid() || (scope.Kind == AccountClosure && !p.AccountClosureEnabled) {
		return Policy{}, ErrPolicyUnresolved
	}
	out := Policy{Version: p.Version, Adopted: true, Scope: scope.clone()}
	for _, c := range p.Categories {
		if scope.Kind == AccountClosure || containsCategory(scope.Categories, c.Key) {
			profile := executionProfiles[c.Rule.Profile]
			out.Actions = append(out.Actions, CategoryAction{Category: Category(c.Key), Action: ActionCode(profile.Disposition)})
		}
	}
	if !out.validFor(scope) {
		return Policy{}, ErrPolicyUnresolved
	}
	return out, nil
}
func containsCategory(cs []Category, key string) bool {
	for _, c := range cs {
		if string(c) == key {
			return true
		}
	}
	return false
}
func bounded(s string, max int) bool {
	return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max
}

// CalendarDeadline preserves Lisbon wall time and clamps the target month.
func CalendarDeadline(at time.Time, months int) time.Time {
	loc, _ := time.LoadLocation("Europe/Lisbon")
	at = at.In(loc)
	y, m, d := at.Date()
	target := time.Date(y, m+time.Month(months), 1, at.Hour(), at.Minute(), at.Second(), at.Nanosecond(), loc)
	last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, loc).Day()
	if d > last {
		d = last
	}
	return time.Date(target.Year(), target.Month(), d, at.Hour(), at.Minute(), at.Second(), at.Nanosecond(), loc)
}

type CategoryDecision struct {
	Category string `json:"category"`
	Outcome  string `json:"outcome"`
	Ground   string `json:"ground,omitempty"`
	Action   string `json:"action"`
}
