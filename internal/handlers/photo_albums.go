package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const photoAlbumQueryTimeout = 5 * time.Second

type PhotoAlbumStore interface {
	ListProgrammes(context.Context) ([]dbgen.Programme, error)
	ListTeamsForEventAuthoring(context.Context) ([]dbgen.ListTeamsForEventAuthoringRow, error)
	ListVisiblePhotoAlbums(context.Context, dbgen.ListVisiblePhotoAlbumsParams) ([]dbgen.ListVisiblePhotoAlbumsRow, error)
	GetVisiblePhotoAlbum(context.Context, dbgen.GetVisiblePhotoAlbumParams) (dbgen.GetVisiblePhotoAlbumRow, error)
	ArchivePhotoAlbum(context.Context, dbgen.ArchivePhotoAlbumParams) (dbgen.PhotoAlbum, error)
	ListPhotoAlbumAuditEvents(context.Context, uuid.UUID) ([]dbgen.ListPhotoAlbumAuditEventsRow, error)
}

type PhotoAlbumDB interface {
	db.Beginner
	dbgen.DBTX
}

type PhotoAlbums struct {
	Store    PhotoAlbumStore
	DB       PhotoAlbumDB
	System   System
	PageMeta components.PageMeta
	Location *time.Location
	Sessions *scs.SessionManager
	Now      func() time.Time
}

type photoAlbumForm struct {
	Title, Description    string
	ProgrammeIDs, TeamIDs []uuid.UUID
	Errors                validation.FieldErrors
}

func (h PhotoAlbums) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, photoAlbumForm{Errors: validation.FieldErrors{}})
}

func (h PhotoAlbums) Detail(w http.ResponseWriter, r *http.Request) {
	h.renderDetail(w, r, http.StatusOK, "")
}

func (h PhotoAlbums) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, photoAlbumForm{Errors: validation.FieldErrors{}})
		return
	}
	form := photoAlbumForm{Title: strings.TrimSpace(r.PostForm.Get("title")), Description: strings.TrimSpace(r.PostForm.Get("description")), Errors: validation.FieldErrors{}}
	if !validPhotoAlbumText(form.Title, 2, 180) {
		form.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if !validPhotoAlbumText(form.Description, 0, 2000) {
		form.Errors.Add("description", "A descrição não pode exceder 2000 caracteres.")
	}
	form.ProgrammeIDs = parsePhotoAlbumIDs(r.PostForm["programme_id"], "programme_id", &form.Errors)
	form.TeamIDs = parsePhotoAlbumIDs(r.PostForm["team_id"], "team_id", &form.Errors)
	if len(form.ProgrammeIDs) == 0 && len(form.TeamIDs) == 0 {
		form.Errors.Add("audience", "Selecione pelo menos um programa ou equipa.")
	}

	ctx, cancel := context.WithTimeout(r.Context(), photoAlbumQueryTimeout)
	defer cancel()
	programmes, teams, ok := h.authoringChoices(ctx, w, r)
	if !ok {
		return
	}
	knownProgrammes := make(map[uuid.UUID]bool, len(programmes))
	for _, programme := range programmes {
		knownProgrammes[programme.ID] = true
	}
	for _, id := range form.ProgrammeIDs {
		if !knownProgrammes[id] {
			form.Errors.Add("programme_id", "Selecione programas válidos.")
		}
	}
	knownTeams := make(map[uuid.UUID]bool, len(teams))
	for _, team := range teams {
		knownTeams[team.ID] = true
	}
	for _, id := range form.TeamIDs {
		if !knownTeams[id] {
			form.Errors.Add("team_id", "Selecione equipas válidas.")
		}
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	err := db.WithinTx(ctx, h.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		album, err := q.CreatePhotoAlbum(ctx, dbgen.CreatePhotoAlbumParams{Title: form.Title, Description: form.Description, CreatedByID: user.ID})
		if err != nil {
			return err
		}
		for _, id := range form.ProgrammeIDs {
			if err := q.AddPhotoAlbumProgrammeAudience(ctx, dbgen.AddPhotoAlbumProgrammeAudienceParams{AlbumID: album.ID, ProgrammeID: id}); err != nil {
				return err
			}
		}
		for _, id := range form.TeamIDs {
			if err := q.AddPhotoAlbumTeamAudience(ctx, dbgen.AddPhotoAlbumTeamAudienceParams{AlbumID: album.ID, TeamID: id}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Álbum privado criado.")
	httpx.Redirect(w, r, "/admin/albuns", http.StatusSeeOther)
}

func (h PhotoAlbums) Archive(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.System.NotFound(w, r)
		return
	}
	expected, err := time.Parse(time.RFC3339Nano, r.PostForm.Get("updated_at"))
	if err != nil {
		h.renderDetail(w, r, http.StatusConflict, "O formulário deixou de ser válido. Atualize a página.")
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	now := h.now()
	ctx, cancel := context.WithTimeout(r.Context(), photoAlbumQueryTimeout)
	defer cancel()
	_, err = h.Store.ArchivePhotoAlbum(ctx, dbgen.ArchivePhotoAlbumParams{ArchivedByID: &user.ID, ArchivedAt: pgtype.Timestamptz{Time: now, Valid: true}, ID: id, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		h.renderDetail(w, r, http.StatusConflict, "O álbum foi alterado entretanto. Reveja o estado atual.")
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.flash(r, "Álbum arquivado.")
	httpx.Redirect(w, r, "/admin/albuns", http.StatusSeeOther)
}

func (h PhotoAlbums) renderIndex(w http.ResponseWriter, r *http.Request, status int, form photoAlbumForm) {
	user, _ := CurrentUserFromContext(r.Context())
	management := strings.HasPrefix(r.URL.Path, "/admin/")
	privileged := user.IsAdmin || user.CanModerateContent
	ctx, cancel := context.WithTimeout(r.Context(), photoAlbumQueryTimeout)
	defer cancel()
	rows, err := h.Store.ListVisiblePhotoAlbums(ctx, dbgen.ListVisiblePhotoAlbumsParams{Privileged: management && privileged, UserID: user.ID})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.PhotoAlbumsPage{Meta: h.meta(r, user, management), Management: management, CanManage: privileged, Success: h.takeFlash(r), Form: pages.PhotoAlbumForm{Title: form.Title, Description: form.Description, Errors: form.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}}
	if management {
		programmes, teams, ok := h.authoringChoices(ctx, w, r)
		if !ok {
			return
		}
		selectedProgrammes := uuidSelection(form.ProgrammeIDs)
		for _, programme := range programmes {
			page.Form.Programmes = append(page.Form.Programmes, pages.PhotoAlbumOption{ID: programme.ID.String(), Name: programme.NamePt, Selected: selectedProgrammes[programme.ID]})
		}
		selectedTeams := uuidSelection(form.TeamIDs)
		for _, team := range teams {
			page.Form.Teams = append(page.Form.Teams, pages.PhotoAlbumOption{ID: team.ID.String(), Name: team.Name, Selected: selectedTeams[team.ID]})
		}
	}
	for _, row := range rows {
		page.Items = append(page.Items, h.albumItem(row.ID, row.Title, row.Description, string(row.Status), row.ProgrammeNames, row.TeamNames, row.CreatedAt.Time, row.UpdatedAt.Time))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.PhotoAlbums(page).Render(r.Context(), w)
}

func (h PhotoAlbums) renderDetail(w http.ResponseWriter, r *http.Request, status int, conflict string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	privileged := user.IsAdmin || user.CanModerateContent
	ctx, cancel := context.WithTimeout(r.Context(), photoAlbumQueryTimeout)
	defer cancel()
	row, err := h.Store.GetVisiblePhotoAlbum(ctx, dbgen.GetVisiblePhotoAlbumParams{ID: id, Privileged: privileged, UserID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	management := strings.HasPrefix(r.URL.Path, "/admin/") && privileged
	page := pages.PhotoAlbumDetailPage{Meta: h.meta(r, user, management), Management: management, CanManage: privileged, Conflict: conflict, Album: h.albumItem(row.ID, row.Title, row.Description, string(row.Status), row.ProgrammeNames, row.TeamNames, row.CreatedAt.Time, row.UpdatedAt.Time), CSRFField: templ.Raw(string(csrf.TemplateField(r)))}
	if privileged {
		events, err := h.Store.ListPhotoAlbumAuditEvents(ctx, id)
		if err != nil {
			h.System.InternalError(w, r)
			return
		}
		for _, event := range events {
			page.Audit = append(page.Audit, pages.PhotoAlbumAuditItem{Action: string(event.Action), Actor: event.ActorName, OccurredAt: h.formatTime(event.OccurredAt.Time)})
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = pages.PhotoAlbumDetail(page).Render(r.Context(), w)
}

func (h PhotoAlbums) authoringChoices(ctx context.Context, w http.ResponseWriter, r *http.Request) ([]dbgen.Programme, []dbgen.ListTeamsForEventAuthoringRow, bool) {
	programmes, err := h.Store.ListProgrammes(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return nil, nil, false
	}
	teams, err := h.Store.ListTeamsForEventAuthoring(ctx)
	if err != nil {
		h.System.InternalError(w, r)
		return nil, nil, false
	}
	return programmes, teams, true
}

func (h PhotoAlbums) albumItem(id uuid.UUID, title, description, status, programmes, teams string, createdAt, updatedAt time.Time) pages.PhotoAlbumItem {
	return pages.PhotoAlbumItem{ID: id.String(), Title: title, Description: description, Status: status, Audience: photoAlbumAudience(programmes, teams), CreatedAt: h.formatTime(createdAt), UpdatedAt: updatedAt.Format(time.RFC3339Nano)}
}

func (h PhotoAlbums) meta(r *http.Request, user CurrentUser, management bool) components.PageMeta {
	meta := h.PageMeta
	meta.Title = "Álbuns privados | MyCFC"
	meta.CurrentPath = "/albuns"
	meta.AreaLabel = "Atividade"
	if management {
		meta.CurrentPath = "/admin/albuns"
		meta.AreaLabel = "Administração"
	}
	meta.PageLabel = "Álbuns privados"
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func (h PhotoAlbums) flash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "photo_album_flash", message)
	}
}

func (h PhotoAlbums) takeFlash(r *http.Request) string {
	if h.Sessions == nil {
		return ""
	}
	return h.Sessions.PopString(r.Context(), "photo_album_flash")
}

func (h PhotoAlbums) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h PhotoAlbums) formatTime(value time.Time) string {
	location := h.Location
	if location == nil {
		location = time.UTC
	}
	return value.In(location).Format("02/01/2006 15:04")
}

func validPhotoAlbumText(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return value == strings.TrimSpace(value) && length >= minimum && length <= maximum
}

func parsePhotoAlbumIDs(values []string, field string, errorsByField *validation.FieldErrors) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(values))
	for _, raw := range values {
		id, err := uuid.Parse(raw)
		if err != nil || seen[id] {
			errorsByField.Add(field, "Selecione destinatários válidos.")
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func uuidSelection(ids []uuid.UUID) map[uuid.UUID]bool {
	selected := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return selected
}

func photoAlbumAudience(programmes, teams string) string {
	parts := make([]string, 0, 2)
	if programmes != "" {
		parts = append(parts, "Programas: "+programmes)
	}
	if teams != "" {
		parts = append(parts, "Equipas: "+teams)
	}
	return strings.Join(parts, " · ")
}
