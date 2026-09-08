package pages

import (
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"sort"
)

type PrivacyOption struct{ Value, Label string }
type PrivacyCategory struct {
	Key, Label, Description, Outcome, Ground string
	Selected                                 bool
	Grounds                                  []PrivacyOption
}
type PrivacyRequestItem struct{ Reference, ReceivedAt, Status, URL, DueAt, DeadlineWarning string }
type PrivacyHistoryItem struct{ At, Label, Explanation string }
type PrivacyRequestsPage struct {
	Meta                                         components.PageMeta
	Management, MinorRights, CanSubmit           bool
	ContactURL, Success, Conflict                string
	Items                                        []PrivacyRequestItem
	StatusFilter, DeadlineFilter, Order          string
	StatusOptions, DeadlineOptions, OrderOptions []PrivacyOption
}
type PrivacyRequestNewPage struct {
	Meta                                                        components.PageMeta
	AccountClosureEnabled                                       bool
	Subjects                                                    []PrivacyOption
	Categories                                                  []PrivacyCategory
	SubjectID, PolicyVersion, ScopeKind, RequestKey, ContactURL string
	Errors                                                      validation.FieldErrors
}
type PrivacyDependant struct {
	ID, Name, Resolution, ResolvedAt string
	CanResolve                       bool
}
type PrivacyRequestDetailPage struct {
	Meta                                                                                                                          components.PageMeta
	Reference, ReceivedAt, Status, Version, SubjectName, RequesterName, ScopeLabel, PolicyVersion, DueAt, Explanation, ContactURL string
	Management, SafeReceipt, CanCancel, CanClaim, CanVerify, CanDecide, CanExtend                                                 bool
	IdentityVerified, RepresentationVerified, ConflictFlag, Representative                                                        bool
	IdentityMethod, RepresentationMethod, Success, Conflict                                                                       string
	IdentityMethods, RepresentationMethods, ResolutionOptions                                                                     []PrivacyOption
	Categories                                                                                                                    []PrivacyCategory
	Dependants                                                                                                                    []PrivacyDependant
	History                                                                                                                       []PrivacyHistoryItem
	Errors                                                                                                                        validation.FieldErrors
}

func privacyBase(management bool) string {
	if management {
		return "/admin/privacidade"
	}
	return "/perfil/privacidade"
}
func privacyStatus(status string) string {
	switch status {
	case "RECEIVED":
		return "Recebido"
	case "IDENTITY_NEEDED":
		return "A aguardar verificação"
	case "UNDER_REVIEW", "IN_REVIEW":
		return "Em análise"
	case "AWAITING_EXECUTION":
		return "Aprovado — a aguardar execução"
	case "PARTIALLY_APPROVED":
		return "Parcialmente aprovado — a aguardar execução"
	case "REFUSED":
		return "Recusado"
	case "CANCELLED":
		return "Cancelado"
	default:
		return status
	}
}
func privacyErrors(errors validation.FieldErrors) []components.FieldError {
	keys := make([]string, 0, len(errors))
	for key := range errors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]components.FieldError, 0, len(keys))
	for _, key := range keys {
		field := key
		switch key {
		case "subject_id", "scope_kind", "categories", "password":
		default:
			field = "privacy-request-form"
		}
		items = append(items, components.FieldError{Field: field, Message: errors[key]})
	}
	return items
}
func privacyContact(url string) string {
	if url != "" {
		return url
	}
	return "/legal/direitos"
}

func privacyDetailErrors(page PrivacyRequestDetailPage) []components.FieldError {
	keys := make([]string, 0, len(page.Errors))
	for key := range page.Errors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]components.FieldError, 0, len(keys))
	for _, key := range keys {
		field := "privacy-receipt"
		if page.CanVerify && (key == "identity_method" || key == "representation_method" && page.Representative) {
			field = key
		}
		if page.CanDecide {
			if key == "explanation" {
				field = key
			}
			for _, category := range page.Categories {
				if key == "outcome_"+category.Key || key == "ground_"+category.Key {
					field = key
				}
			}
		}
		if page.CanExtend && (key == "extension_months" || key == "extension_reason") {
			field = key
		}
		items = append(items, components.FieldError{Field: field, Message: page.Errors[key]})
	}
	return items
}
func privacyOutcome(value string) string {
	switch value {
	case "APPROVE":
		return "Apagamento aprovado — a aguardar execução"
	case "RETAIN":
		return "Conservar"
	}
	return value
}
func privacyOptionLabel(options []PrivacyOption, value string) string {
	for _, option := range options {
		if option.Value == value {
			return option.Label
		}
	}
	return value
}
