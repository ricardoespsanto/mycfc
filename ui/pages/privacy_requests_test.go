package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
)

func privacyRender(t *testing.T, component templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := component.Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func privacyCSRF() components.PageMeta {
	return components.PageMeta{CSRFField: templ.Raw(`<input type="hidden" name="gorilla.csrf.Token" value="csrf-proof">`)}
}

func TestPrivacySafeReceiptDoesNotDiscloseProtectedFields(t *testing.T) {
	page := PrivacyRequestDetailPage{Meta: privacyCSRF(), Reference: "opaque-ref", ReceivedAt: "07/09/2026", Status: "RECEIVED", Version: "4", SafeReceipt: true, Management: true, CanCancel: true, CanClaim: true, CanVerify: true, CanDecide: true, CanExtend: true, SubjectName: "secret-subject", RequesterName: "secret-requester", ScopeLabel: "secret-scope", PolicyVersion: "secret-policy", Explanation: "secret-explanation", DueAt: "secret-deadline", Success: "secret-success", Conflict: "secret-conflict", Errors: validation.FieldErrors{"secret": "secret-error"}, Categories: []PrivacyCategory{{Label: "secret-category"}}, Dependants: []PrivacyDependant{{Name: "secret-dependant", CanResolve: true}}, History: []PrivacyHistoryItem{{Label: "secret-history"}}}
	body := privacyRender(t, privacyRequestDetailContent(page))
	if strings.Contains(body, "secret-") {
		t.Fatalf("receipt leaks protected data: %s", body)
	}
	for _, want := range []string{"opaque-ref", "07/09/2026", "Recebido", `/perfil/privacidade/opaque-ref/cancelar`, `name="version" value="4"`, "csrf-proof"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing receipt control %q", want)
		}
	}
	for _, forbidden := range []string{`value="approve"`, `value="claim"`, `value="verify"`, `value="extend"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("receipt exposes %s", forbidden)
		}
	}
}

func TestPrivacyApprovalClearlyAwaitsExecution(t *testing.T) {
	for _, status := range []string{"AWAITING_EXECUTION", "PARTIALLY_APPROVED"} {
		body := privacyRender(t, privacyRequestDetailContent(PrivacyRequestDetailPage{Status: status, History: []PrivacyHistoryItem{{At: "08/09/2026", Label: "Decisão registada"}}, Categories: []PrivacyCategory{{Label: "Fotografias", Outcome: "RETAIN", Ground: "GROUND", Grounds: []PrivacyOption{{Value: "GROUND", Label: "Fundamento aprovado"}}}}}))
		for _, want := range []string{"a aguardar execução", "Os dados ainda não foram apagados.", "Histórico", "Decisão registada", "Conservar", "Fundamento aprovado"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", status, want)
			}
		}
		if strings.Contains(body, `name="action"`) {
			t.Error("member detail exposes management action")
		}
	}
}

func TestPrivacyNewFormUsesApprovedCatalogueAndNativeControls(t *testing.T) {
	page := PrivacyRequestNewPage{Meta: privacyCSRF(), PolicyVersion: "matrix-1", RequestKey: "retry-key", SubjectID: "self", Subjects: []PrivacyOption{{Value: "self", Label: "Eu"}}, Categories: []PrivacyCategory{{Key: "photos", Label: "Fotografias", Selected: true}, {Key: "profile", Label: "Perfil"}}, Errors: validation.FieldErrors{"password": "Confirme a identidade", "policy_version": "Política indisponível"}}
	body := privacyRender(t, privacyRequestNewContent(page))
	for _, want := range []string{`method="post"`, `action="/perfil/privacidade/novo"`, `name="request_key" value="retry-key"`, `name="policy_version" value="matrix-1"`, `type="checkbox" id="category-photos" name="categories" value="photos" checked`, `type="password" autocomplete="current-password" required`, `href="#password"`, `id="password-error"`, `href="#privacy-request-form"`, "csrf-proof"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, `value="ACCOUNT_CLOSURE"`) {
		t.Error("unapproved account closure offered")
	}
	page.AccountClosureEnabled = true
	if !strings.Contains(privacyRender(t, privacyRequestNewContent(page)), `value="ACCOUNT_CLOSURE"`) {
		t.Error("approved account closure missing")
	}
}

func TestPrivacyReviewerControlsAreVersionedAndCapabilityGated(t *testing.T) {
	page := PrivacyRequestDetailPage{Meta: privacyCSRF(), Reference: "opaque", Version: "9", Management: true, CanClaim: true, CanVerify: true, CanDecide: true, CanExtend: true, Representative: true, Dependants: []PrivacyDependant{{ID: "123", Name: "Menor", CanResolve: true}}, Categories: []PrivacyCategory{{Key: "photos", Label: "Fotografias"}}, Errors: validation.FieldErrors{"identity_method": "Escolha o método", "version": "Pedido atualizado"}}
	body := privacyRender(t, privacyRequestDetailContent(page))
	for _, action := range []string{"claim", "identity-needed", "verify", "resolve-dependant", "approve", "partial", "refuse", "extend"} {
		if !strings.Contains(body, `value="`+action+`"`) {
			t.Errorf("missing action %s", action)
		}
	}
	forms := strings.Count(body, `method="post"`)
	if forms != strings.Count(body, "csrf-proof") || forms != strings.Count(body, `name="version" value="9"`) {
		t.Error("every mutation must carry CSRF and version")
	}
	for _, want := range []string{`href="#identity_method"`, `href="#privacy-receipt"`, `name="outcome_photos"`, `name="ground_photos"`, `name="representation_method"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `value="execute"`) || strings.Contains(body, `value="start-processing"`) {
		t.Error("UI must not start execution")
	}
	page.Management = false
	if strings.Contains(privacyRender(t, privacyRequestDetailContent(page)), `name="action"`) {
		t.Error("management flag must guard all review controls")
	}
}

func TestPrivacyExecutorControlShowsImmutablePlanAndExplicitConfirmation(t *testing.T) {
	page := PrivacyRequestDetailPage{Meta: privacyCSRF(), Reference: "opaque", Version: "9", Status: "AWAITING_EXECUTION", Management: true, CanViewExecution: true, CanExecute: true, ExecutionPlan: []PrivacyExecutionPlanItem{{CategoryLabel: "Perfil", Disposition: "DELETE", Owner: "PRIVACY", Operations: []string{"PROFILE_IDENTITY_DELETE"}}}}
	body := privacyRender(t, privacyRequestDetailContent(page))
	for _, want := range []string{"Plano de execução aprovado", "Perfil", "DELETE", "Iniciar processamento", `action="/admin/privacidade/opaque/executar"`, `name="execution_confirmed" value="yes" required`, `name="version" value="9"`, "csrf-proof"} {
		if !strings.Contains(body, want) {
			t.Errorf("executor surface missing %q", want)
		}
	}
	if strings.Contains(body, `value="claim"`) || strings.Contains(body, `value="approve"`) {
		t.Fatal("executor surface exposed reviewer controls")
	}
	page.ExecutionStatus = "SUCCEEDED"
	page.CanExecute = false
	body = privacyRender(t, privacyRequestDetailContent(page))
	if strings.Contains(body, "Concluído") || !strings.Contains(body, "SUCCEEDED") {
		t.Fatal("technical success was presented as legal completion")
	}
}

func TestPrivacyExecutorSurfaceListsCurrentBlockersWithoutStartControl(t *testing.T) {
	page := PrivacyRequestDetailPage{
		Meta: privacyCSRF(), Reference: "opaque", Version: "9", Status: "AWAITING_EXECUTION", Management: true, CanViewExecution: true,
		ExecutionPlan:     []PrivacyExecutionPlanItem{{CategoryLabel: "Perfil", Disposition: "DELETE", Owner: "PRIVACY", Operations: []string{"PROFILE_IDENTITY_DELETE"}}},
		ExecutionBlockers: []string{"Ainda não estão instaladas todas as operações exigidas pelo plano imutável.", "A execução permanece desativada até à validação operacional final."},
	}
	body := privacyRender(t, privacyRequestDetailContent(page))
	for _, want := range []string{"Bloqueios atuais", `aria-label="Bloqueios atuais da execução"`, page.ExecutionBlockers[0], page.ExecutionBlockers[1]} {
		if !strings.Contains(body, want) {
			t.Errorf("blocked executor surface missing %q", want)
		}
	}
	if strings.Contains(body, "Iniciar processamento") || strings.Contains(body, `action="/admin/privacidade/opaque/executar"`) {
		t.Fatal("blocked executor surface exposed irreversible start control")
	}
}

func TestPrivacyQueueFiltersAndMinorRights(t *testing.T) {
	page := PrivacyRequestsPage{Management: true, StatusFilter: "UNDER_REVIEW", StatusOptions: []PrivacyOption{{Value: "UNDER_REVIEW", Label: "Em análise"}}, DeadlineOptions: []PrivacyOption{{Value: "overdue", Label: "Ultrapassado"}}, OrderOptions: []PrivacyOption{{Value: "due", Label: "Prazo mais próximo"}}, Items: []PrivacyRequestItem{{Reference: "ref", DueAt: "08/09/2026", DeadlineWarning: "Prazo ultrapassado"}}}
	body := privacyRender(t, privacyRequestsContent(page))
	for _, want := range []string{`method="get"`, `name="status"`, `name="deadline"`, `name="order"`, "Prazo ultrapassado", "08/09/2026"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing queue content %q", want)
		}
	}
	body = privacyRender(t, privacyMinorRightsContent(PrivacyRequestsPage{}))
	for _, want := range []string{`href="/legal/privacidade-menores"`, `href="/legal/direitos"`, "Os teus direitos"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing minor rights %q", want)
		}
	}
	if strings.Contains(body, "<form") {
		t.Error("minor rights route must not submit a request")
	}
}

func TestPrivacyPageHelpersMapStatusesErrorsAndFallbacks(t *testing.T) {
	for status, want := range map[string]string{
		"RECEIVED": "Recebido", "IDENTITY_NEEDED": "A aguardar verificação", "UNDER_REVIEW": "Em análise",
		"IN_REVIEW": "Em análise", "AWAITING_EXECUTION": "Aprovado — a aguardar execução",
		"PARTIALLY_APPROVED": "Parcialmente aprovado — a aguardar execução", "REFUSED": "Recusado",
		"PROCESSING": "Em processamento", "RETRYABLE_FAILED": "Execução interrompida — nova tentativa pendente",
		"TERMINAL_FAILED": "Execução bloqueada — intervenção necessária", "COMPLETED": "Concluído",
		"CANCELLED": "Cancelado", "FUTURE": "FUTURE",
	} {
		if got := privacyStatus(status); got != want {
			t.Errorf("status %s=%q want=%q", status, got, want)
		}
	}
	if privacyBase(true) != "/admin/privacidade" || privacyBase(false) != "/perfil/privacidade" {
		t.Fatal("privacy base routes changed")
	}
	if privacyContact("") != "/legal/direitos" || privacyContact("/contact") != "/contact" {
		t.Fatal("privacy contact fallback changed")
	}
	if privacyOutcome("APPROVE") != "Apagamento aprovado — a aguardar execução" || privacyOutcome("RETAIN") != "Conservar" || privacyOutcome("FUTURE") != "FUTURE" {
		t.Fatal("privacy outcome mapping changed")
	}
	options := []PrivacyOption{{Value: "GROUND", Label: "Approved ground"}}
	if privacyOptionLabel(options, "GROUND") != "Approved ground" || privacyOptionLabel(options, "OTHER") != "OTHER" {
		t.Fatal("privacy option fallback changed")
	}

	fields := privacyErrors(validation.FieldErrors{"password": "Password", "unexpected": "Unexpected"})
	if len(fields) != 2 || fields[0].Field != "password" || fields[1].Field != "privacy-request-form" {
		t.Fatalf("new-form error targets=%+v", fields)
	}
	page := PrivacyRequestDetailPage{CanVerify: true, CanDecide: true, CanExtend: true, Representative: true,
		Categories: []PrivacyCategory{{Key: "photos"}}, Errors: validation.FieldErrors{
			"identity_method": "identity", "representation_method": "representation", "explanation": "explanation",
			"outcome_photos": "outcome", "ground_photos": "ground", "extension_months": "months",
			"extension_reason": "reason", "unexpected": "unexpected",
		}}
	targets := map[string]bool{}
	for _, item := range privacyDetailErrors(page) {
		targets[item.Field] = true
	}
	for _, want := range []string{"identity_method", "representation_method", "explanation", "outcome_photos", "ground_photos", "extension_months", "extension_reason", "privacy-receipt"} {
		if !targets[want] {
			t.Errorf("detail error target missing %q: %+v", want, targets)
		}
	}
}
