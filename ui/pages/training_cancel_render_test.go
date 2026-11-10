package pages

import (
	"crypto/sha256"
	"fmt"
	"github.com/a-h/templ"
	"html"
	"strings"
	"testing"
)

func cancelRenderFixture() TrainingSessionCancelPage {
	return TrainingSessionCancelPage{SessionID: "session", Title: "Água", When: "9 novembro · 16:00", ExpectedUpdatedAt: "original-version", CancellationReason: "Motivo original\nsegunda linha <copiar>", CSRFField: templ.Raw(`<input type="hidden" name="_csrf" value="synthetic">`)}
}
func TestTrainingCancellationLegacyDefaultMarkup(t *testing.T) {
	for _, kind := range []string{"ordinary", "conflict", "unsupported"} {
		p := cancelRenderFixture()
		if kind != "ordinary" {
			p.Conflict = "Consulte a sessão."
		}
		if kind == "unsupported" {
			p.WriteUnsupported = true
		}
		body := renderScheduleComponent(t, trainingSessionCancelContent(p))
		// Captured before this bounded URL extension; empty fields keep exact legacy HTML.
		want := map[string]string{"ordinary": "3d90cf66bb9b6ab779d7479e069e27aab1edb3622c626cd0f7e85fe3f9b4c46e", "conflict": "06fefc27d9d975f3cea2d428fb1b4be32ebfb0aa2079bf266f29d6bb29ecc8bf", "unsupported": "63f1fae41cbb8c65d13fd7491d1835d2698b274454868fda6e84a9006c266c5b"}[kind]
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(body))); got != want {
			t.Fatalf("%s legacy default markup changed: %s", kind, got)
		}
	}
}
func TestWorkspaceAppointmentLegacyDefaultMarkup(t *testing.T) {
	p := workspaceFixture()
	body := renderScheduleComponent(t, structuredWorkspaceBand("Tarde", p.Workspace.Days[0].Afternoon))
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(body))); got != "e558bd21497759b3d5fef5bc8c702e0d612fd728d170a6f5b0f983363c34d962" {
		t.Fatalf("empty appointment action URLs changed legacy markup: %s", got)
	}
}

func TestTrainingCancellationScopedURLsKeepExistingConfirmationContract(t *testing.T) {
	p := cancelRenderFixture()
	p.ActionURL = "/admin/treinos/estruturados/semanas/week/treinos/session/cancelar"
	p.ReturnURL = "/planner?group=group&week=week&day=2026-11-09"
	p.ReloadURL = p.ActionURL + "?day=2026-11-09"
	body := renderScheduleComponent(t, trainingSessionCancelContent(p))
	if !strings.Contains(body, `action="`+p.ActionURL+`"`) || strings.Contains(body, `action="/admin/treinos/sessoes/`) {
		t.Fatal("scoped cancellation must use provided action only")
	}
	if strings.Count(body, `href="`+html.EscapeString(p.ReturnURL)+`"`) != 2 {
		t.Fatal("task header return and cancellation action back must retain fixed context")
	}
	assertWorkoutValue(t, body, "expected_updated_at", "original-version")
	assertWorkoutValue(t, body, "_csrf", "synthetic")
	checkbox := workoutControl(t, body, "confirm_cancellation")
	if value, _ := workoutAttr(checkbox, "value"); value != "yes" {
		t.Fatal("explicit cancellation confirmation changed")
	}
	if _, required := workoutAttr(checkbox, "required"); !required {
		t.Fatal("confirmation must remain required")
	}
	reason := workoutControl(t, body, "cancellation_reason")
	for name, want := range map[string]string{"minlength": "2", "maxlength": "500"} {
		if value, _ := workoutAttr(reason, name); value != want {
			t.Fatal("reason bounds changed")
		}
	}
	if !strings.Contains(body, html.EscapeString(p.CancellationReason)) || !strings.Contains(body, `type="submit" data-task-submit`) {
		t.Fatal("own reason or explicit submit lost")
	}
	if strings.Contains(body, "Recarregar sessão atual") {
		t.Fatal("ordinary form must not manufacture a conflict")
	}
}

func TestTrainingCancellationScopedConflictAndCutoffRetainReasonAndRecovery(t *testing.T) {
	for _, unsupported := range []bool{false, true} {
		p := cancelRenderFixture()
		p.ActionURL = "/scoped/cancel"
		p.ReturnURL = "/planner?week=week&day=2026-11-09"
		p.ReloadURL = "/scoped/cancel?reload=current&day=2026-11-09"
		p.Conflict = "A sessão mudou. Copie o motivo antes de recarregar."
		p.WriteUnsupported = unsupported
		body := renderScheduleComponent(t, trainingSessionCancelContent(p))
		if !strings.Contains(body, html.EscapeString(p.CancellationReason)) || !strings.Contains(body, p.Conflict) || !strings.Contains(body, `href="`+html.EscapeString(p.ReturnURL)+`"`) || !strings.Contains(body, `href="`+html.EscapeString(p.ReloadURL)+`"`) || !strings.Contains(body, "Recarregar sessão atual") {
			t.Fatal("reason/conflict/scoped recovery lost")
		}
		if strings.Contains(body, `type="submit"`) {
			t.Fatal("stale/cutoff cancellation must offer no submit")
		}
		if unsupported {
			if strings.Contains(body, "<form") || !strings.Contains(body, "Voltar à semana") {
				t.Fatal("unsupported cancellation must stay readonly with scoped back")
			}
		} else {
			assertWorkoutValue(t, body, "expected_updated_at", "original-version")
			if !strings.Contains(body, `name="cancellation_reason"`) || !strings.Contains(body, `name="confirm_cancellation"`) {
				t.Fatal("stale reason must stay copyable with existing confirmation fields")
			}
		}
		p.ReloadURL = ""
		body = renderScheduleComponent(t, trainingSessionCancelContent(p))
		if strings.Contains(body, "Recarregar sessão atual") || !strings.Contains(body, "Voltar à semana") {
			t.Fatal("absent reload must retain provided week fallback")
		}
	}
}
