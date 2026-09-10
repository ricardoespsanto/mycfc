package pages

import (
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"sort"
	"strconv"
)

type PrivacyOption struct{ Value, Label string }
type PrivacyCategory struct {
	Key, Label, Description, Outcome, Ground string
	Selected                                 bool
	Grounds                                  []PrivacyOption
}
type PrivacyRequestItem struct{ Reference, ReceivedAt, Status, URL, DueAt, DeadlineWarning string }
type PrivacyHistoryItem struct{ At, Label, Explanation string }
type PrivacyExecutionPlanItem struct {
	Category, CategoryLabel, Disposition, Owner, DueAt string
	Operations                                         []string
}
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
type PrivacyCompletionDetailPage struct {
	Meta                                                                  components.PageMeta
	Reference, CompletedAt, ManifestSHA256, ContactURL, ConfirmationNonce string
	Categories, Checkpoints, ObjectTargets, ProviderTargets               int32
	Confirm, Unavailable                                                  bool
}
type PrivacyControlLookupPage struct {
	Meta             components.PageMeta
	Reference, Error string
}
type PrivacyControlJob struct {
	ID, CategoryCode, PurposeCode, Status, AttemptCount string
	FailureStage, FailureCode, ProposedAt               string
	CanPropose, CanApprove                              bool
}
type PrivacyCompletionControlPage struct {
	Meta                                                                  components.PageMeta
	Reference, RequestStatus, ExecutionStatus, Success, Error, ContactURL string
	Jobs                                                                  []PrivacyControlJob
}
type PrivacyActivationEvidence struct{ ID, Kind, ObservedAt string }
type PrivacyActivationControlPage struct {
	Meta                                    components.PageMeta
	PolicyVersion, Success, Error           string
	Ready, CanPropose, CanRenew, CanApprove bool
	Evidence                                []PrivacyActivationEvidence
	ProposedAt                              string
}
type PrivacyDependant struct {
	ID, Name, Resolution, ResolvedAt string
	CanResolve                       bool
}
type PrivacyRequestDetailPage struct {
	Meta                                                                                                                          components.PageMeta
	Reference, ReceivedAt, Status, Version, SubjectName, RequesterName, ScopeLabel, PolicyVersion, DueAt, Explanation, ContactURL string
	Management, SafeReceipt, CanCancel, CanClaim, CanVerify, CanDecide, CanExtend, CanViewExecution, CanExecute                   bool
	IdentityVerified, RepresentationVerified, ConflictFlag, Representative                                                        bool
	IdentityMethod, RepresentationMethod, Success, Conflict                                                                       string
	IdentityMethods, RepresentationMethods, ResolutionOptions                                                                     []PrivacyOption
	Categories                                                                                                                    []PrivacyCategory
	Dependants                                                                                                                    []PrivacyDependant
	History                                                                                                                       []PrivacyHistoryItem
	ExecutionPlan                                                                                                                 []PrivacyExecutionPlanItem
	ExecutionBlockers                                                                                                             []string
	ExecutionStatus                                                                                                               string
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
	case "PROCESSING":
		return "Em processamento"
	case "RETRYABLE_FAILED":
		return "Execução interrompida — nova tentativa pendente"
	case "TERMINAL_FAILED":
		return "Execução bloqueada — intervenção necessária"
	case "COMPLETED":
		return "Concluído"
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

func privacyCount(value int32) string { return strconv.FormatInt(int64(value), 10) }

func privacyControlStatus(status string) string {
	switch status {
	case "PENDING":
		return "Pendente"
	case "LEASED", "RUNNING", "PROCESSING":
		return "Em processamento"
	case "RETRYABLE_FAILED":
		return "Nova tentativa pendente"
	case "TERMINAL_FAILED":
		return "Intervenção necessária"
	case "SUCCEEDED", "COMPLETED":
		return "Concluído"
	default:
		return "Estado indisponível"
	}
}

func privacyFailureStage(stage string) string {
	switch stage {
	case "SYNC":
		return "Sincronização"
	case "EXECUTE":
		return "Execução"
	case "VERIFY":
		return "Verificação"
	default:
		return "Etapa protegida"
	}
}

func privacyFailureCode(code string) string {
	switch code {
	case "ACTION_FAILED":
		return "ACTION_FAILED — a operação não terminou"
	case "DEPENDENCY_UNAVAILABLE":
		return "DEPENDENCY_UNAVAILABLE — dependência indisponível"
	case "UNSUPPORTED_OPERATION":
		return "UNSUPPORTED_OPERATION — operação não suportada"
	case "VERIFICATION_FAILED":
		return "VERIFICATION_FAILED — verificação sem sucesso"
	case "RETRY_LIMIT_REACHED":
		return "RETRY_LIMIT_REACHED — limite de tentativas atingido"
	default:
		return "OPERATIONAL_FAILURE — falha operacional protegida"
	}
}

func privacyEvidenceKind(kind string) string {
	switch kind {
	case "RESTORE":
		return "Restauro isolado"
	case "INFRASTRUCTURE":
		return "Infraestrutura"
	case "PROVIDER":
		return "Destinatários externos"
	case "SCHEMA":
		return "Esquema de dados"
	default:
		return "Evidência não reconhecida"
	}
}

func privacyReadyLabel(ready bool) string {
	if ready {
		return "Ativo"
	}
	return "Inativo"
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
		if page.CanExecute && key == "execution_confirmed" {
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
