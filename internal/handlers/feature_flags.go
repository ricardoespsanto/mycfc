package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/featureflags"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/jackc/pgx/v5/pgtype"
)

type FeatureFlagStore interface {
	ListFeatureFlags(context.Context) ([]dbgen.ListFeatureFlagsRow, error)
	UpdateFeatureFlag(context.Context, dbgen.UpdateFeatureFlagParams) (int64, error)
	ListFeatureFlagEvents(context.Context, int32) ([]dbgen.ListFeatureFlagEventsRow, error)
}

type featureFlagForm struct {
	Mode, ExpectedUpdatedAt, Error, Conflict string
}

func (h Dashboard) UpdateFeatureFlag(w http.ResponseWriter, r *http.Request) {
	key := featureflags.Key(r.PathValue("key"))
	if _, known := featureflags.DefinitionFor(key); !known {
		h.System.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderSystem(w, r, http.StatusBadRequest, featureFlagForm{Error: "Não foi possível ler a alteração. Tente novamente."})
		return
	}
	form := featureFlagForm{
		Mode:              strings.TrimSpace(r.PostForm.Get("mode")),
		ExpectedUpdatedAt: strings.TrimSpace(r.PostForm.Get("expected_updated_at")),
	}
	mode := featureflags.Mode(form.Mode)
	if !featureflags.ValidMode(mode) {
		form.Error = "Selecione uma disponibilidade válida."
	}
	var expected pgtype.Timestamptz
	if form.ExpectedUpdatedAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, form.ExpectedUpdatedAt)
		if err != nil || parsed.IsZero() {
			form.Error = "A configuração foi alterada. Recarregue a página e tente novamente."
		} else {
			expected = pgtype.Timestamptz{Time: parsed, Valid: true}
		}
	}
	if form.Error != "" {
		h.renderSystem(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	user, _ := currentUser(r.Context())
	if h.Features == nil {
		h.System.InternalError(w, r)
		return
	}
	actor := user.ID
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	changed, err := h.Features.UpdateFeatureFlag(ctx, dbgen.UpdateFeatureFlagParams{
		Mode:              dbgen.FeatureAvailabilityMode(mode),
		ActorUserID:       &actor,
		FeatureKey:        string(key),
		ExpectedUpdatedAt: expected,
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if changed == 0 {
		form.Conflict = "Esta configuração foi alterada por outra pessoa. Reveja o estado atual antes de tentar novamente."
		h.renderSystem(w, r, http.StatusConflict, form)
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "feature_flag_flash", "Disponibilidade atualizada.")
	}
	httpx.Redirect(w, r, "/admin/sistema", http.StatusSeeOther)
}

func (h Dashboard) renderSystem(w http.ResponseWriter, r *http.Request, status int, form featureFlagForm) {
	user, _ := CurrentUserFromContext(r.Context())
	page := pages.ReleasesPage{Release: h.releaseStatus(r.Context()), Error: form.Error, Conflict: form.Conflict}
	page.Meta = h.PageMeta
	page.Meta.Title = "Sistema | MyCFC"
	page.Meta.CurrentPath = "/admin/sistema"
	page.Meta.CurrentUserName = user.Name
	page.Meta.CurrentUserID = user.ID.String()
	page.Meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	page.Meta.Navigation = dashboardNavigation(user)
	page.Meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	if h.Sessions != nil {
		page.Success = h.Sessions.PopString(r.Context(), "feature_flag_flash")
	}

	rowsByKey := map[featureflags.Key]dbgen.ListFeatureFlagsRow{}
	if h.Features != nil {
		ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
		defer cancel()
		rows, err := h.Features.ListFeatureFlags(ctx)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		for _, row := range rows {
			rowsByKey[featureflags.Key(row.FeatureKey)] = row
		}
		events, err := h.Features.ListFeatureFlagEvents(ctx, 8)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		for _, event := range events {
			definition, known := featureflags.DefinitionFor(featureflags.Key(event.FeatureKey))
			if !known {
				continue
			}
			page.FeatureEvents = append(page.FeatureEvents, pages.FeatureFlagEvent{
				Label: definition.Label, PreviousMode: event.PreviousMode, NewMode: event.NewMode,
				ActorName: event.ActorName, OccurredAt: h.formatFeatureTime(event.OccurredAt),
			})
		}
	}
	for _, definition := range featureflags.Registry() {
		row, exists := rowsByKey[definition.Key]
		mode := definition.Default
		updatedAt := ""
		updatedLabel := ""
		updatedBy := "Configuração inicial"
		if exists {
			mode = featureflags.Mode(row.Mode)
			updatedAt = row.UpdatedAt.Time.Format(time.RFC3339Nano)
			updatedLabel = h.formatFeatureTime(row.UpdatedAt)
			if row.UpdatedByName != nil {
				updatedBy = *row.UpdatedByName
			}
		}
		page.Features = append(page.Features, pages.FeatureFlag{
			Key: string(definition.Key), Label: definition.Label, Description: definition.Description,
			Mode: string(mode), UpdatedAt: updatedAt, UpdatedLabel: updatedLabel, UpdatedBy: updatedBy,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Releases(page).Render(r.Context(), w)
}

func (h Dashboard) formatFeatureTime(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	location := h.Location
	if location == nil {
		location = time.UTC
	}
	return value.Time.In(location).Format("02/01/2006 15:04")
}
