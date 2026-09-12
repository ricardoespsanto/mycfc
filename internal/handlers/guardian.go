package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
)

const maximumDependentsMessage = "Já atingiu o limite de 10 menores a cargo ativos."

var (
	ErrMaximumDependents           = errors.New("maximum active dependents reached")
	ErrGuardianApplicantIneligible = errors.New("guardian applicant ineligible")
	ErrGuardianInvitationInvalid   = errors.New("guardian invitation invalid")
	ErrGuardianApplicationLimited  = errors.New("guardian application rate limited")
	ErrGuardianAuthentication      = errors.New("guardian authentication failed")
)

type GuardianDependentInput struct {
	GuardianID                                  uuid.UUID
	Name                                        string
	DateOfBirth                                 time.Time
	ResponsibilityVersion, ResponsibilitySHA256 string
	IP                                          *netip.Addr
	UserAgent                                   string
	InvitationToken                             string
}

type GuardianDependentStore interface {
	ReserveAttempt(context.Context, uuid.UUID, *netip.Addr, string) error
	Reauthenticate(context.Context, uuid.UUID, string) error
	CreateDependent(context.Context, GuardianDependentInput) error
}

type guardianDependentForm struct {
	Name, DateOfBirth      string
	ResponsibilityAccepted bool
	InvitationToken        string
	RequirePassword        bool
	Errors                 validation.FieldErrors
	Success                string
}

func (h Dashboard) AddDependent(w http.ResponseWriter, r *http.Request) {
	if h.GuardianAuthority == nil {
		h.System.InternalError(w, r)
		return
	}
	available, err := h.GuardianAuthority.PolicyAvailable(r.Context())
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if !available {
		h.renderGuardianForm(w, r, http.StatusConflict, guardianDependentForm{Errors: validation.FieldErrors{"form": "Os pedidos estão temporariamente indisponíveis porque a política de verificação ainda não foi ativada."}})
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	var ip *netip.Addr
	if value, ok := httpx.RemoteIP(r.Context()); ok {
		ip = &value
	}
	if err := h.Dependents.ReserveAttempt(r.Context(), user.ID, ip, "SUBMISSION"); err != nil {
		if errors.Is(err, ErrGuardianApplicationLimited) {
			h.logGuardianApplication(r.Context(), "submission_rate_limited")
			w.Header().Set("Retry-After", "86400")
			h.renderGuardianForm(w, r, http.StatusTooManyRequests, guardianDependentForm{Errors: validation.FieldErrors{"form": "Foram enviados demasiados pedidos. Tente novamente mais tarde."}})
			return
		}
		h.System.InternalError(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderGuardianForm(w, r, http.StatusBadRequest, guardianDependentForm{})
		return
	}
	form := h.validateDependent(r)
	if !form.Errors.Empty() {
		h.renderGuardianForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if !h.authenticationFresh(r.Context()) {
		form.RequirePassword = true
		password := r.PostForm.Get("password")
		authErr := ErrGuardianAuthentication
		if password != "" && len(password) <= 1024 {
			authErr = h.Dependents.Reauthenticate(r.Context(), user.ID, password)
		}
		if authErr != nil {
			if !errors.Is(authErr, ErrGuardianAuthentication) {
				h.System.InternalError(w, r)
				return
			}
			limitErr := h.Dependents.ReserveAttempt(r.Context(), user.ID, ip, "AUTH_INVALID")
			if errors.Is(limitErr, ErrGuardianApplicationLimited) {
				h.logGuardianApplication(r.Context(), "authentication_rate_limited")
				w.Header().Set("Retry-After", "900")
				form.Errors.Add("password", "Aguarde 15 minutos antes de voltar a confirmar a palavra-passe.")
				h.renderGuardianForm(w, r, http.StatusTooManyRequests, form)
				return
			}
			if limitErr != nil {
				h.System.InternalError(w, r)
				return
			}
			form.Errors.Add("password", "Não foi possível confirmar a palavra-passe atual.")
			h.logGuardianApplication(r.Context(), "authentication_rejected")
			h.renderGuardianForm(w, r, http.StatusUnprocessableEntity, form)
			return
		}
		if h.Sessions == nil || h.Sessions.RenewToken(r.Context()) != nil {
			h.System.InternalError(w, r)
			return
		}
		h.Sessions.Put(r.Context(), "authenticated_at", h.now().UTC().Format(time.RFC3339Nano))
	}
	err = h.Dependents.CreateDependent(r.Context(), GuardianDependentInput{
		GuardianID: user.ID, Name: form.Name, DateOfBirth: mustParseDate(form.DateOfBirth),
		ResponsibilityVersion: h.ResponsibilityVersion, ResponsibilitySHA256: h.ResponsibilitySHA256,
		IP: ip, UserAgent: truncateRunes(r.UserAgent(), 512), InvitationToken: form.InvitationToken,
	})
	if errors.Is(err, ErrGuardianAuthorityPolicyUnavailable) {
		form.Errors.Add("form", "Os pedidos estão temporariamente indisponíveis porque a política de verificação ainda não foi ativada.")
		h.renderGuardianForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if errors.Is(err, ErrMaximumDependents) {
		form.Errors.Add("form", maximumDependentsMessage)
		h.renderGuardianForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if errors.Is(err, ErrGuardianApplicantIneligible) || errors.Is(err, ErrGuardianInvitationInvalid) {
		if errors.Is(err, ErrGuardianInvitationInvalid) {
			limitErr := h.Dependents.ReserveAttempt(r.Context(), user.ID, ip, "INVITATION_INVALID")
			if errors.Is(limitErr, ErrGuardianApplicationLimited) {
				h.logGuardianApplication(r.Context(), "invitation_rate_limited")
				w.Header().Set("Retry-After", "900")
				h.renderGuardianForm(w, r, http.StatusTooManyRequests, guardianDependentForm{Errors: validation.FieldErrors{"form": "Foram enviados demasiados pedidos. Tente novamente mais tarde."}})
				return
			}
			if limitErr != nil {
				h.System.InternalError(w, r)
				return
			}
		}
		form.Errors.Add("form", "Não foi possível enviar o pedido. Confirme a sua conta, inscrição ou convite e tente novamente.")
		if errors.Is(err, ErrGuardianInvitationInvalid) {
			h.logGuardianApplication(r.Context(), "invitation_rejected")
		} else {
			h.logGuardianApplication(r.Context(), "applicant_ineligible")
		}
		h.renderGuardianForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.logGuardianApplication(r.Context(), "accepted")
	if r.Header.Get("HX-Request") == "true" {
		h.renderGuardianForm(w, r, http.StatusOK, guardianDependentForm{Success: "Pedido recebido. Não é possível consultar dados do menor enquanto a representação não for verificada pelo clube."})
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "guardian_flash", "Pedido recebido. Não é possível consultar dados do menor enquanto a representação não for verificada pelo clube.")
	}
	httpx.Redirect(w, r, "/dashboard/guardian", http.StatusSeeOther)
}

func (h Dashboard) logGuardianApplication(ctx context.Context, outcome string) {
	if h.Logger != nil {
		h.Logger.InfoContext(ctx, "guardian application event", "event", "guardian_application", "outcome", outcome)
	}
}

func (h Dashboard) validateDependent(r *http.Request) guardianDependentForm {
	form := guardianDependentForm{Name: strings.TrimSpace(r.PostForm.Get("name")), DateOfBirth: strings.TrimSpace(r.PostForm.Get("date_of_birth")), ResponsibilityAccepted: r.PostForm.Get("accept_minor_responsibility") == "on", InvitationToken: strings.TrimSpace(r.PostForm.Get("invitation_token")), Errors: validation.FieldErrors{}}
	name, err := validation.NormalizeName(form.Name)
	if err != nil {
		form.Errors.Add("name", err.Error())
	} else {
		form.Name = name
	}
	dateOfBirth, err := validation.ParseISODate(form.DateOfBirth)
	if err != nil {
		form.Errors.Add("date_of_birth", err.Error())
	} else if err := validation.ValidateDependentDateOfBirth(dateOfBirth, h.now(), h.location()); err != nil {
		form.Errors.Add("date_of_birth", err.Error())
	} else {
		form.DateOfBirth = dateOfBirth.Format("2006-01-02")
	}
	if !form.ResponsibilityAccepted {
		form.Errors.Add("accept_minor_responsibility", "Tem de aceitar a responsabilidade pelo menor a cargo.")
	}
	return form
}

func (h Dashboard) authenticationFresh(ctx context.Context) bool {
	if h.Sessions == nil {
		// Dashboard is always constructed with a session manager in the running
		// application; nil is retained only for isolated handler tests.
		return true
	}
	authenticatedAt, err := time.Parse(time.RFC3339Nano, h.Sessions.GetString(ctx, "authenticated_at"))
	now := h.now().UTC()
	freshness := h.GuardianAuthFreshness
	if freshness <= 0 {
		freshness = time.Hour
	}
	return err == nil && !authenticatedAt.After(now) && now.Sub(authenticatedAt) <= freshness
}

func (h Dashboard) renderGuardianForm(w http.ResponseWriter, r *http.Request, status int, form guardianDependentForm) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	if h.GuardianAuthority == nil {
		h.System.InternalError(w, r)
		return
	}
	dependents, err := h.GuardianAuthority.ListForGuardian(ctx, user.ID, 10)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.renderGuardian(w, r, status, dependents, form)
}

func mustParseDate(value string) time.Time {
	date, _ := time.Parse("2006-01-02", value)
	return date
}
