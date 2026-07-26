package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const announcementQueryTimeout = 5 * time.Second

// Delivery is recorded when an announcement appears in a recipient's list;
// only opening its detail page marks it read.
func announcementReadOnDetail() bool { return true }

type AnnouncementStore interface {
	ListAnnouncementProgrammes(context.Context) ([]dbgen.ListAnnouncementProgrammesRow, error)
	ListAnnouncementTeams(context.Context) ([]dbgen.ListAnnouncementTeamsRow, error)
	ListAnnouncementCategories(context.Context) ([]dbgen.ListAnnouncementCategoriesRow, error)
	ListAnnouncementModalities(context.Context) ([]dbgen.ListAnnouncementModalitiesRow, error)
	ListAnnouncementEvents(context.Context) ([]dbgen.ListAnnouncementEventsRow, error)
	ListAnnouncementsForAuthor(context.Context, dbgen.ListAnnouncementsForAuthorParams) ([]dbgen.ListAnnouncementsForAuthorRow, error)
	ListVisibleAnnouncements(context.Context, dbgen.ListVisibleAnnouncementsParams) ([]dbgen.ListVisibleAnnouncementsRow, error)
	GetAnnouncementAuthor(context.Context, uuid.UUID) (uuid.UUID, error)
	CanCoachManageEvent(context.Context, dbgen.CanCoachManageEventParams) (bool, error)
}

type AnnouncementDB interface {
	db.Beginner
	dbgen.DBTX
}
type Announcements struct {
	Store    AnnouncementStore
	DB       AnnouncementDB
	System   System
	PageMeta components.PageMeta
	Location *time.Location
}
type announcementForm struct {
	Title, Body, ExpiresAt string
	Targets                map[dbgen.AnnouncementTargetType][]uuid.UUID
	Guardian               bool
	Errors                 validation.FieldErrors
}

func (h Announcements) Index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, announcementForm{Targets: map[dbgen.AnnouncementTargetType][]uuid.UUID{}, Errors: validation.FieldErrors{}})
}
func (h Announcements) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderIndex(w, r, http.StatusBadRequest, announcementForm{Errors: validation.FieldErrors{}})
		return
	}
	form := h.validate(r)
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), announcementQueryTimeout)
	defer cancel()
	if !h.authorized(ctx, user, form) {
		form.Errors.Add("audience", "Não tem permissão para esta audiência.")
	}
	if !form.Errors.Empty() {
		h.renderIndex(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	var expiry pgtype.Timestamptz
	if form.ExpiresAt != "" {
		v, _ := time.ParseInLocation("2006-01-02T15:04", form.ExpiresAt, h.location())
		expiry = pgtype.Timestamptz{Time: v, Valid: true}
	}
	err := db.WithinTx(ctx, h.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		a, err := q.CreateAnnouncement(ctx, dbgen.CreateAnnouncementParams{Title: form.Title, Body: form.Body, AuthorID: user.ID, ExpiresAt: expiry})
		if err != nil {
			return err
		}
		for kind, ids := range form.Targets {
			for _, id := range ids {
				id := id
				if err := q.AddAnnouncementTarget(ctx, dbgen.AddAnnouncementTargetParams{AnnouncementID: a.ID, TargetType: kind, TargetID: &id}); err != nil {
					return err
				}
			}
		}
		if form.Guardian {
			if err := q.AddAnnouncementTarget(ctx, dbgen.AddAnnouncementTargetParams{AnnouncementID: a.ID, TargetType: dbgen.AnnouncementTargetTypeGUARDIAN}); err != nil {
				return err
			}
		}
		if r.PostForm.Get("action") == "publish" {
			n, err := q.PublishAnnouncement(ctx, dbgen.PublishAnnouncementParams{ID: a.ID, ActorUserID: &user.ID})
			if err != nil || n != 1 {
				return errors.New("publish announcement")
			}
		}
		return nil
	})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	httpx.Redirect(w, r, "/announcements", http.StatusSeeOther)
}
func (h Announcements) Publish(w http.ResponseWriter, r *http.Request) { h.changeStatus(w, r, true) }
func (h Announcements) Expire(w http.ResponseWriter, r *http.Request)  { h.changeStatus(w, r, false) }
func (h Announcements) changeStatus(w http.ResponseWriter, r *http.Request, publish bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), announcementQueryTimeout)
	defer cancel()
	author, err := h.Store.GetAnnouncementAuthor(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.System.NotFound(w, r)
		} else {
			h.System.InternalError(w, r)
		}
		return
	}
	if !user.IsAdmin && author != user.ID {
		h.System.Forbidden(w, r)
		return
	}
	var n int64
	if publish {
		n, err = dbgen.New(h.DB).PublishAnnouncement(ctx, dbgen.PublishAnnouncementParams{ID: id, ActorUserID: &user.ID})
	} else {
		n, err = dbgen.New(h.DB).ExpireAnnouncement(ctx, dbgen.ExpireAnnouncementParams{ID: id, ActorUserID: &user.ID})
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	if n == 0 {
		http.Error(w, "A operação não é válida para este aviso.", http.StatusConflict)
		return
	}
	httpx.Redirect(w, r, "/announcements", http.StatusSeeOther)
}
func (h Announcements) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), announcementQueryTimeout)
	defer cancel()
	items, err := h.Store.ListVisibleAnnouncements(ctx, dbgen.ListVisibleAnnouncementsParams{UserID: user.ID, RowLimit: 100})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	for _, item := range items {
		if item.ID == id {
			if err := dbgen.New(h.DB).RecordAnnouncementDelivery(ctx, dbgen.RecordAnnouncementDeliveryParams{AnnouncementID: id, UserID: user.ID}); err == nil && announcementReadOnDetail() {
				_ = dbgen.New(h.DB).MarkAnnouncementRead(ctx, dbgen.MarkAnnouncementReadParams{AnnouncementID: id, UserID: user.ID})
			}
			page := pages.AnnouncementDetailPage{Title: item.Title, Body: item.Body, PublishedAt: item.PublishedAt.Time.In(h.location()).Format("02/01/2006 15:04"), Meta: h.meta(r, user, "/announcements", "Aviso")}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = pages.AnnouncementDetail(page).Render(r.Context(), w)
			return
		}
	}
	h.System.NotFound(w, r)
}
func (h Announcements) validate(r *http.Request) announcementForm {
	f := announcementForm{Title: strings.TrimSpace(r.PostForm.Get("title")), Body: strings.TrimSpace(r.PostForm.Get("body")), ExpiresAt: strings.TrimSpace(r.PostForm.Get("expires_at")), Targets: map[dbgen.AnnouncementTargetType][]uuid.UUID{}, Guardian: r.PostForm.Get("guardian") == "true", Errors: validation.FieldErrors{}}
	if n := utf8.RuneCountInString(f.Title); n < 2 || n > 180 {
		f.Errors.Add("title", "O título deve ter entre 2 e 180 caracteres.")
	}
	if n := utf8.RuneCountInString(f.Body); n < 2 || n > 4000 {
		f.Errors.Add("body", "A mensagem deve ter entre 2 e 4000 caracteres.")
	}
	if f.ExpiresAt != "" {
		if _, err := time.ParseInLocation("2006-01-02T15:04", f.ExpiresAt, h.location()); err != nil {
			f.Errors.Add("expires_at", "Introduza uma data e hora válidas.")
		}
	}
	for name, kind := range map[string]dbgen.AnnouncementTargetType{"programme_id": dbgen.AnnouncementTargetTypePROGRAMME, "team_id": dbgen.AnnouncementTargetTypeTEAM, "category_id": dbgen.AnnouncementTargetTypeCATEGORY, "modality_id": dbgen.AnnouncementTargetTypeMODALITY, "event_id": dbgen.AnnouncementTargetTypeEVENT} {
		seen := map[uuid.UUID]bool{}
		for _, raw := range r.PostForm[name] {
			id, err := uuid.Parse(raw)
			if err != nil || seen[id] {
				f.Errors.Add(name, "Selecione destinatários válidos.")
				continue
			}
			seen[id] = true
			f.Targets[kind] = append(f.Targets[kind], id)
		}
	}
	return f
}
func (h Announcements) authorized(ctx context.Context, u CurrentUser, f announcementForm) bool {
	if u.IsAdmin {
		return true
	}
	if len(f.Targets) == 0 && !f.Guardian {
		return false
	}
	for kind, ids := range f.Targets {
		if kind == dbgen.AnnouncementTargetTypeMODALITY || kind == dbgen.AnnouncementTargetTypeCATEGORY {
			return false
		}
		for _, id := range ids {
			if kind == dbgen.AnnouncementTargetTypePROGRAMME && !u.CoachProgrammeIDs[id] {
				return false
			}
			if kind == dbgen.AnnouncementTargetTypeTEAM && !u.CoachTeamIDs[id] {
				return false
			}
			if kind == dbgen.AnnouncementTargetTypeEVENT {
				ok, err := h.Store.CanCoachManageEvent(ctx, dbgen.CanCoachManageEventParams{EventID: id, UserID: u.ID})
				if err != nil || !ok {
					return false
				}
			}
		}
	}
	return len(f.Targets) > 0
}
func (h Announcements) renderIndex(w http.ResponseWriter, r *http.Request, status int, f announcementForm) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), announcementQueryTimeout)
	defer cancel()
	visible, err := h.Store.ListVisibleAnnouncements(ctx, dbgen.ListVisibleAnnouncementsParams{UserID: user.ID, RowLimit: 100})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page := pages.AnnouncementsPage{CanManage: user.IsAdmin || user.CanManageEvents, Form: pages.AnnouncementForm{Title: f.Title, Body: f.Body, ExpiresAt: f.ExpiresAt, Errors: f.Errors, CSRFField: templ.Raw(string(csrf.TemplateField(r)))}}
	for _, item := range visible {
		if err := dbgen.New(h.DB).RecordAnnouncementDelivery(ctx, dbgen.RecordAnnouncementDeliveryParams{AnnouncementID: item.ID, UserID: user.ID}); err != nil {
			h.System.InternalError(w, r)
			return
		}
		page.Items = append(page.Items, pages.AnnouncementItem{ID: item.ID.String(), Title: item.Title, PublishedAt: item.PublishedAt.Time.In(h.location()).Format("02/01/2006 15:04"), Unread: !item.ReadAt.Valid})
	}
	if page.CanManage {
		p, _ := h.Store.ListAnnouncementProgrammes(ctx)
		t, _ := h.Store.ListAnnouncementTeams(ctx)
		c, _ := h.Store.ListAnnouncementCategories(ctx)
		m, _ := h.Store.ListAnnouncementModalities(ctx)
		e, _ := h.Store.ListAnnouncementEvents(ctx)
		for _, x := range p {
			if user.IsAdmin || user.CoachProgrammeIDs[x.ID] {
				page.Form.Programmes = append(page.Form.Programmes, pages.AnnouncementAudience{ID: x.ID.String(), Name: x.NamePt})
			}
		}
		for _, x := range t {
			if user.IsAdmin || user.CoachTeamIDs[x.ID] {
				page.Form.Teams = append(page.Form.Teams, pages.AnnouncementAudience{ID: x.ID.String(), Name: x.Name})
			}
		}
		if user.IsAdmin {
			for _, x := range c {
				page.Form.Categories = append(page.Form.Categories, pages.AnnouncementAudience{ID: x.ID.String(), Name: x.NamePt})
			}
			for _, x := range m {
				page.Form.Modalities = append(page.Form.Modalities, pages.AnnouncementAudience{ID: x.ID.String(), Name: x.NamePt})
			}
		}
		for _, x := range e {
			page.Form.Events = append(page.Form.Events, pages.AnnouncementAudience{ID: x.ID.String(), Name: x.Title})
		}
		authored, err := h.Store.ListAnnouncementsForAuthor(ctx, dbgen.ListAnnouncementsForAuthorParams{AuthorID: user.ID, RowLimit: 100})
		if err == nil {
			for _, x := range authored {
				page.Authored = append(page.Authored, pages.AuthoredAnnouncement{ID: x.ID.String(), Title: x.Title, Status: x.Status})
			}
		}
	}
	page.Meta = h.meta(r, user, "/announcements", "Avisos")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Announcements(page).Render(r.Context(), w)
}
func (h Announcements) meta(r *http.Request, u CurrentUser, path, title string) components.PageMeta {
	m := h.PageMeta
	m.Title = title + " | MyCFC"
	m.CurrentPath = path
	m.CurrentUserName = u.Name
	m.Navigation = dashboardNavigation(u)
	m.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return m
}
func (h Announcements) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
