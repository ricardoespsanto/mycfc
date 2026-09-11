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

func TestPrivacyCompletionDetailHasGenericUnavailableStateAndWrapSafeEvidence(t *testing.T) {
	manifest := strings.Repeat("a", 64)
	body := privacyRender(t, privacyCompletionDetailContent(PrivacyCompletionDetailPage{
		Reference: "opaque-request", CompletedAt: "10/09/2026 19:30", ManifestSHA256: manifest,
		Categories: 3, Checkpoints: 8, ObjectTargets: 2, ProviderTargets: 1,
	}))
	for _, want := range []string{"Pedido concluído", "opaque-request", "10/09/2026 19:30", manifest, `class="privacy-fingerprint"`, "Categorias processadas", "Destinatários externos verificados"} {
		if !strings.Contains(body, want) {
			t.Errorf("completion detail missing %q", want)
		}
	}

	body = privacyRender(t, privacyCompletionDetailContent(PrivacyCompletionDetailPage{Unavailable: true}))
	for _, want := range []string{"Ligação indisponível", "inválida, já foi utilizada ou expirou", "uma única vez durante 24 horas"} {
		if !strings.Contains(body, want) {
			t.Errorf("unavailable detail missing %q", want)
		}
	}
	for _, forbidden := range []string{"opaque-request", manifest, "Pedido concluído"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("unavailable detail exposed %q", forbidden)
		}
	}

	body = privacyRender(t, privacyCompletionDetailContent(PrivacyCompletionDetailPage{Meta: privacyCSRF(), Confirm: true, ConfirmationNonce: "browser-state-nonce"}))
	for _, want := range []string{`method="post"`, `action="/privacidade/conclusao/consultar"`, `name="completion_state" value="browser-state-nonce"`, "csrf-proof", "Consultar resultado agora"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmation page missing %q", want)
		}
	}
	if strings.Contains(body, "token") || strings.Contains(body, "opaque-request") {
		t.Fatal("confirmation page exposes capability state")
	}
}

func TestPrivacyOperationalControlsAreBoundedNativeAndDualControl(t *testing.T) {
	page := PrivacyCompletionControlPage{
		Meta: privacyCSRF(), Reference: "opaque-ref", RequestStatus: "TERMINAL_FAILED", ExecutionStatus: "TERMINAL_FAILED",
		Jobs: []PrivacyControlJob{
			{ID: "job-propose", CategoryCode: "identity-core", PurposeCode: "ACCOUNT_ERASURE", Status: "TERMINAL_FAILED", AttemptCount: "5", FailureStage: "VERIFY", FailureCode: "VERIFICATION_FAILED", CanPropose: true},
			{ID: "job-approve", CategoryCode: "external-provider", PurposeCode: "PROVIDER_DISCONNECT", Status: "TERMINAL_FAILED", AttemptCount: "2", FailureStage: "EXECUTE", FailureCode: "DEPENDENCY_UNAVAILABLE", ProposedAt: "10/09/2026 20:00", CanApprove: true},
		},
	}
	body := privacyRender(t, privacyCompletionControlContent(page))
	for _, want := range []string{
		"Estado técnico limitado", "Intervenção necessária", "identity-core", "ACCOUNT_ERASURE",
		"VERIFICATION_FAILED", "DEPENDENCY_UNAVAILABLE", `name="confirmed" value="yes" required`,
		`action="/admin/privacidade/controlo/opaque-ref/reagendar"`,
		`action="/admin/privacidade/controlo/opaque-ref/reagendar/aprovar"`, "Propor nova tentativa", "Aprovar nova tentativa",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("completion control missing %q", want)
		}
	}
	if forms := strings.Count(body, `method="post"`); forms != 2 || strings.Count(body, "csrf-proof") != forms {
		t.Fatalf("every control mutation must carry CSRF; body=%s", body)
	}
	for _, forbidden := range []string{"secret-subject", "secret-recipient", "raw stack", "provider-object-id"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("control surface exposed %q", forbidden)
		}
	}
}

func TestPrivacyActivationUsesRecordedEvidenceAndIndependentNativeApproval(t *testing.T) {
	evidence := []PrivacyActivationEvidence{
		{ID: "restore-id", Kind: "RESTORE", ObservedAt: "10/09/2026 18:00"},
		{ID: "infra-id", Kind: "INFRASTRUCTURE", ObservedAt: "10/09/2026 18:01"},
		{ID: "provider-id", Kind: "PROVIDER", ObservedAt: "10/09/2026 18:02"},
		{ID: "schema-id", Kind: "SCHEMA", ObservedAt: "10/09/2026 18:03"},
	}
	body := privacyRender(t, privacyActivationControlContent(PrivacyActivationControlPage{Meta: privacyCSRF(), PolicyVersion: "policy-v2", Evidence: evidence, CanPropose: true}))
	for _, want := range []string{"Processamento", "Inativo", "quatro comprovativos", "Não envie chaves", `action="/admin/privacidade/ativacao/propor"`, "Propor ativação", "csrf-proof"} {
		if !strings.Contains(body, want) {
			t.Errorf("activation proposal missing %q", want)
		}
	}
	if strings.Contains(body, `name="evidence_id"`) || strings.Contains(body, "auth_hmac") {
		t.Fatalf("activation proposal evidence boundary changed: %s", body)
	}

	body = privacyRender(t, privacyActivationControlContent(PrivacyActivationControlPage{Meta: privacyCSRF(), PolicyVersion: "policy-v2", Evidence: evidence, ProposedAt: "10/09/2026 20:10", CanApprove: true}))
	for _, want := range []string{`action="/admin/privacidade/ativacao/aprovar"`, "pessoa administradora diferente", "Aprovar ativação", `name="confirmed" value="yes" required`, "csrf-proof"} {
		if !strings.Contains(body, want) {
			t.Errorf("activation approval missing %q", want)
		}
	}
	if strings.Contains(body, "Propor ativação") {
		t.Fatal("pending activation exposed a second proposal")
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
	page := PrivacyRequestDetailPage{CanVerify: true, CanDecide: true, CanExtend: true, CanExecute: true, Representative: true,
		Categories: []PrivacyCategory{{Key: "photos"}}, Errors: validation.FieldErrors{
			"identity_method": "identity", "representation_method": "representation", "explanation": "explanation",
			"outcome_photos": "outcome", "ground_photos": "ground", "extension_months": "months",
			"extension_reason": "reason", "execution_confirmed": "confirmation", "unexpected": "unexpected",
		}}
	targets := map[string]bool{}
	for _, item := range privacyDetailErrors(page) {
		targets[item.Field] = true
	}
	for _, want := range []string{"identity_method", "representation_method", "explanation", "outcome_photos", "ground_photos", "extension_months", "extension_reason", "execution_confirmed", "privacy-receipt"} {
		if !targets[want] {
			t.Errorf("detail error target missing %q: %+v", want, targets)
		}
	}
	for status, want := range map[string]string{
		"PENDING": "Pendente", "LEASED": "Em processamento", "RUNNING": "Em processamento", "PROCESSING": "Em processamento",
		"RETRYABLE_FAILED": "Nova tentativa pendente", "TERMINAL_FAILED": "Intervenção necessária",
		"SUCCEEDED": "Concluído", "COMPLETED": "Concluído", "UNKNOWN": "Estado indisponível",
	} {
		if got := privacyControlStatus(status); got != want {
			t.Errorf("control status %s=%q want=%q", status, got, want)
		}
	}
	for stage, want := range map[string]string{"SYNC": "Sincronização", "EXECUTE": "Execução", "VERIFY": "Verificação", "UNKNOWN": "Etapa protegida"} {
		if got := privacyFailureStage(stage); got != want {
			t.Errorf("failure stage %s=%q want=%q", stage, got, want)
		}
	}
	for code, want := range map[string]string{
		"ACTION_FAILED":          "ACTION_FAILED — a operação não terminou",
		"DEPENDENCY_UNAVAILABLE": "DEPENDENCY_UNAVAILABLE — dependência indisponível",
		"UNSUPPORTED_OPERATION":  "UNSUPPORTED_OPERATION — operação não suportada",
		"VERIFICATION_FAILED":    "VERIFICATION_FAILED — verificação sem sucesso",
		"RETRY_LIMIT_REACHED":    "RETRY_LIMIT_REACHED — limite de tentativas atingido",
		"UNKNOWN":                "OPERATIONAL_FAILURE — falha operacional protegida",
	} {
		if got := privacyFailureCode(code); got != want {
			t.Errorf("failure code %s=%q want=%q", code, got, want)
		}
	}
	for kind, want := range map[string]string{
		"RESTORE": "Restauro isolado", "INFRASTRUCTURE": "Infraestrutura", "PROVIDER": "Destinatários externos",
		"SCHEMA": "Esquema de dados", "UNKNOWN": "Evidência não reconhecida",
	} {
		if got := privacyEvidenceKind(kind); got != want {
			t.Errorf("evidence kind %s=%q want=%q", kind, got, want)
		}
	}
	if privacyReadyLabel(true) != "Ativo" || privacyReadyLabel(false) != "Inativo" || privacyCount(42) != "42" {
		t.Fatal("privacy operational label helpers changed")
	}
}
