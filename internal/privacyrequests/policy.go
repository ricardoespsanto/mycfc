package privacyrequests

import (
	"encoding/json"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"strings"
	"time"
	"unicode/utf8"
)

// CatalogueEntry is controller-approved metadata, never inferred from account data.
type CatalogueEntry struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Action      string   `json:"action"`
	Grounds     []Ground `json:"grounds"`
}
type Ground struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}
type AdoptedPolicy struct {
	Version               string           `json:"version"`
	Categories            []CatalogueEntry `json:"categories"`
	AccountClosureEnabled bool             `json:"account_closure_enabled"`
	WorkingRetentionDays  int32            `json:"working_retention_days"`
	ResponseMonths        int32            `json:"response_months"`
	ExtensionMonths       int32            `json:"extension_months"`
}

func ReadPolicy(row dbgen.PrivacyRequestPolicy) (AdoptedPolicy, error) {
	p := AdoptedPolicy{Version: row.Version, AccountClosureEnabled: row.AccountClosureEnabled, ResponseMonths: row.ResponseMonths, ExtensionMonths: row.ExtensionMonths}
	if !row.AdoptedAt.Valid || row.AdoptedBy == nil || row.WorkingRetentionDays == nil {
		return p, ErrPolicyUnresolved
	}
	p.WorkingRetentionDays = *row.WorkingRetentionDays
	if json.Unmarshal(row.CategoryCatalogue, &p.Categories) != nil || p.Validate() != nil {
		return p, ErrPolicyUnresolved
	}
	return p, nil
}
func (p AdoptedPolicy) Validate() error {
	if !policyKey.MatchString(p.Version) || len(p.Version) > 80 || p.ResponseMonths != 1 || p.ExtensionMonths != 2 || p.WorkingRetentionDays < 1 || p.WorkingRetentionDays > 36500 || len(p.Categories) == 0 || len(p.Categories) > 50 {
		return ErrPolicyUnresolved
	}
	seen := map[string]bool{}
	for _, c := range p.Categories {
		if !policyKey.MatchString(c.Key) || !policyKey.MatchString(c.Action) || seen[c.Key] || !bounded(c.Label, 200) || utf8.RuneCountInString(c.Description) > 1000 {
			return ErrPolicyUnresolved
		}
		seen[c.Key] = true
		gs := map[string]bool{}
		for _, g := range c.Grounds {
			if !policyKey.MatchString(g.Code) || gs[g.Code] || !bounded(g.Label, 200) {
				return ErrPolicyUnresolved
			}
			gs[g.Code] = true
		}
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
			out.Actions = append(out.Actions, CategoryAction{Category: Category(c.Key), Action: ActionCode(c.Action)})
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

func (p AdoptedPolicy) Decisions(scope Scope, inputs map[string]CategoryDecision, action string) ([]CategoryDecision, error) {
	snapshot, err := p.Snapshot(scope)
	if err != nil {
		return nil, err
	}
	if len(inputs) != len(snapshot.Actions) {
		return nil, ErrInvalid
	}
	approved := 0
	out := make([]CategoryDecision, 0, len(inputs))
	for _, a := range snapshot.Actions {
		d, ok := inputs[string(a.Category)]
		if !ok {
			return nil, ErrInvalid
		}
		d.Category = string(a.Category)
		d.Action = string(a.Action)
		switch d.Outcome {
		case "APPROVE":
			if d.Ground != "" {
				return nil, ErrInvalid
			}
			approved++
		case "RETAIN":
			found := false
			for _, c := range p.Categories {
				if c.Key == d.Category {
					for _, g := range c.Grounds {
						if g.Code == d.Ground {
							found = true
						}
					}
				}
			}
			if !found {
				return nil, ErrPolicyUnresolved
			}
		default:
			return nil, ErrInvalid
		}
		out = append(out, d)
	}
	if (action == "approve" && approved != len(out)) || (action == "partial" && (approved == 0 || approved == len(out))) || (action == "refuse" && approved != 0) {
		return nil, ErrInvalid
	}
	return out, nil
}
