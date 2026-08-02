package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
)

const maximumDependentsMessage = "Já atingiu o limite de 10 menores a cargo ativos."

var ErrMaximumDependents = errors.New("maximum active dependents reached")

type GuardianDependentInput struct {
	GuardianID                                  uuid.UUID
	Name                                        string
	DateOfBirth                                 time.Time
	ResponsibilityVersion, ResponsibilitySHA256 string
	IP                                          *netip.Addr
	UserAgent                                   string
}

type GuardianDependentStore interface {
	CreateDependent(context.Context, GuardianDependentInput) error
}

type guardianDependentForm struct {
	Name, DateOfBirth      string
	ResponsibilityAccepted bool
	Errors                 validation.FieldErrors
	Success                string
}

func (h Dashboard) AddDependent(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderGuardianForm(w, r, http.StatusBadRequest, guardianDependentForm{})
		return
	}
	form := h.validateDependent(r)
	if !form.Errors.Empty() {
		h.renderGuardianForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	var ip *netip.Addr
	if value, ok := httpx.RemoteIP(r.Context()); ok {
		ip = &value
	}
	err := h.Dependents.CreateDependent(r.Context(), GuardianDependentInput{
		GuardianID: user.ID, Name: form.Name, DateOfBirth: mustParseDate(form.DateOfBirth),
		ResponsibilityVersion: h.ResponsibilityVersion, ResponsibilitySHA256: h.ResponsibilitySHA256,
		IP: ip, UserAgent: truncateRunes(r.UserAgent(), 512),
	})
	if errors.Is(err, ErrMaximumDependents) {
		form.Errors.Add("form", maximumDependentsMessage)
		h.renderGuardianForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.renderGuardianForm(w, r, http.StatusOK, guardianDependentForm{Success: "Menor a cargo adicionado."})
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "guardian_flash", "Menor a cargo adicionado.")
	}
	httpx.Redirect(w, r, "/dashboard/guardian", http.StatusSeeOther)
}

func (h Dashboard) validateDependent(r *http.Request) guardianDependentForm {
	form := guardianDependentForm{Name: strings.TrimSpace(r.PostForm.Get("name")), DateOfBirth: strings.TrimSpace(r.PostForm.Get("date_of_birth")), ResponsibilityAccepted: r.PostForm.Get("accept_minor_responsibility") == "on", Errors: validation.FieldErrors{}}
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

func (h Dashboard) renderGuardianForm(w http.ResponseWriter, r *http.Request, status int, form guardianDependentForm) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	dependents, err := h.Store.ListDependentsByGuardian(ctx, dbgen.ListDependentsByGuardianParams{GuardianID: &user.ID, RowLimit: 10})
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
