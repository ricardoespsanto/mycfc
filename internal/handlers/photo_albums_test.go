package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPhotoAlbumMemberIndexUsesAudienceScopedQuery(t *testing.T) {
	userID, albumID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	store := &photoAlbumStoreFake{visible: []dbgen.ListVisiblePhotoAlbumsRow{{ID: albumID, Title: "Regata de verão", Description: "Momentos da equipa.", Status: dbgen.PhotoAlbumStatusOPEN, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}, ProgrammeNames: "Competição"}}}
	h := PhotoAlbums{Store: store, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/albuns", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Atleta"}))
	w := httptest.NewRecorder()

	h.Index(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Regata de verão") || !strings.Contains(w.Body.String(), "Programas: Competição") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `type="file"`) || strings.Contains(w.Body.String(), "Enviar fotografia") {
		t.Fatal("album foundation exposed an upload control")
	}
	if store.visibleParams.UserID != userID || store.visibleParams.Privileged {
		t.Fatalf("visible params = %#v", store.visibleParams)
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache control = %q", got)
	}
}

func TestPhotoAlbumManagementShowsAuthoringChoices(t *testing.T) {
	userID, programmeID, teamID := uuid.New(), uuid.New(), uuid.New()
	store := &photoAlbumStoreFake{programmes: []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}}, teams: []dbgen.ListTeamsForEventAuthoringRow{{ID: teamID, ProgrammeID: programmeID, Name: "Juniores"}}}
	h := PhotoAlbums{Store: store}
	r := httptest.NewRequest(http.MethodGet, "/admin/albuns", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Moderadora", CanModerateContent: true}))
	w := httptest.NewRecorder()

	h.Index(w, r)

	for _, expected := range []string{"Novo álbum privado", "Competição", "Juniores", `action="/admin/albuns"`} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Errorf("management page does not contain %q", expected)
		}
	}
	if !store.visibleParams.Privileged {
		t.Fatal("management query was not privileged")
	}
}

func TestPhotoAlbumCreateRequiresExplicitAudience(t *testing.T) {
	userID := uuid.New()
	store := &photoAlbumStoreFake{}
	h := PhotoAlbums{Store: store}
	values := url.Values{"title": {"Treino aberto"}, "description": {"Fotografias privadas da sessão."}}
	r := httptest.NewRequest(http.MethodPost, "/admin/albuns", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Administradora", IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Selecione pelo menos um programa ou equipa") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `data-create-panel open`) {
		t.Fatal("invalid form did not reopen the authoring panel")
	}
}

func TestPhotoAlbumArchiveUsesAuthenticatedActorAndVersion(t *testing.T) {
	actorID, albumID := uuid.New(), uuid.New()
	expected := time.Now().UTC().Truncate(time.Microsecond)
	store := &photoAlbumStoreFake{archiveResult: dbgen.PhotoAlbum{ID: albumID, Status: dbgen.PhotoAlbumStatusARCHIVED}}
	h := PhotoAlbums{Store: store, Now: func() time.Time { return expected.Add(time.Minute) }}
	values := url.Values{"updated_at": {expected.Format(time.RFC3339Nano)}}
	r := httptest.NewRequest(http.MethodPost, "/admin/albuns/"+albumID.String()+"/arquivar", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", albumID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, Name: "Moderadora", CanModerateContent: true}))
	w := httptest.NewRecorder()

	h.Archive(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/albuns" {
		t.Fatalf("response = %d, location = %q", w.Code, w.Header().Get("Location"))
	}
	if store.archiveParams.ID != albumID || store.archiveParams.ArchivedByID == nil || *store.archiveParams.ArchivedByID != actorID || !store.archiveParams.ExpectedUpdatedAt.Time.Equal(expected) {
		t.Fatalf("archive params = %#v", store.archiveParams)
	}
}

func TestPhotoAlbumDetailFailsClosed(t *testing.T) {
	albumID := uuid.New()
	store := &photoAlbumStoreFake{getErr: pgx.ErrNoRows}
	h := PhotoAlbums{Store: store, System: System{}}
	r := httptest.NewRequest(http.MethodGet, "/albuns/"+albumID.String(), nil)
	r.SetPathValue("id", albumID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: uuid.New(), Name: "Membro"}))
	w := httptest.NewRecorder()

	h.Detail(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("response = %d", w.Code)
	}
}

func TestPhotoAlbumDetailExposesOwningCollectionInDeepLinkMetadata(t *testing.T) {
	albumID := uuid.New()
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, path, breadcrumb, area string
		user                         CurrentUser
	}{
		{name: "reader", path: "/albuns/" + albumID.String(), breadcrumb: "Álbuns", area: "Atividade", user: CurrentUser{ID: uuid.New(), Name: "Membro"}},
		{name: "manager", path: "/admin/albuns/" + albumID.String(), breadcrumb: "Gerir álbuns", area: "Moderação", user: CurrentUser{ID: uuid.New(), Name: "Moderadora", CanModerateContent: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &photoAlbumStoreFake{get: dbgen.GetVisiblePhotoAlbumRow{ID: albumID, Title: "Regata de verão", Status: dbgen.PhotoAlbumStatusOPEN, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}
			h := PhotoAlbums{Store: store, Location: time.UTC}
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.SetPathValue("id", albumID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, tc.user))
			w := httptest.NewRecorder()

			h.Detail(w, r)

			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.breadcrumb) || !strings.Contains(w.Body.String(), tc.area) || !strings.Contains(w.Body.String(), `aria-current="page">Regata de verão`) {
				t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestParsePhotoAlbumIDsRejectsDuplicatesAndInvalidValues(t *testing.T) {
	id := uuid.New()
	errorsByField := validation.FieldErrors{}
	ids := parsePhotoAlbumIDs([]string{id.String(), id.String(), "invalid"}, "team_id", &errorsByField)
	if len(ids) != 1 || ids[0] != id || errorsByField["team_id"] == "" {
		t.Fatalf("ids = %#v, errors = %#v", ids, errorsByField)
	}
}

type photoAlbumStoreFake struct {
	programmes    []dbgen.Programme
	teams         []dbgen.ListTeamsForEventAuthoringRow
	visible       []dbgen.ListVisiblePhotoAlbumsRow
	visibleParams dbgen.ListVisiblePhotoAlbumsParams
	get           dbgen.GetVisiblePhotoAlbumRow
	getErr        error
	archiveResult dbgen.PhotoAlbum
	archiveParams dbgen.ArchivePhotoAlbumParams
	archiveErr    error
	audit         []dbgen.ListPhotoAlbumAuditEventsRow
}

func (f *photoAlbumStoreFake) ListProgrammes(context.Context) ([]dbgen.Programme, error) {
	return f.programmes, nil
}
func (f *photoAlbumStoreFake) ListTeamsForEventAuthoring(context.Context) ([]dbgen.ListTeamsForEventAuthoringRow, error) {
	return f.teams, nil
}
func (f *photoAlbumStoreFake) ListVisiblePhotoAlbums(_ context.Context, params dbgen.ListVisiblePhotoAlbumsParams) ([]dbgen.ListVisiblePhotoAlbumsRow, error) {
	f.visibleParams = params
	return f.visible, nil
}
func (f *photoAlbumStoreFake) GetVisiblePhotoAlbum(_ context.Context, params dbgen.GetVisiblePhotoAlbumParams) (dbgen.GetVisiblePhotoAlbumRow, error) {
	return f.get, f.getErr
}
func (f *photoAlbumStoreFake) ArchivePhotoAlbum(_ context.Context, params dbgen.ArchivePhotoAlbumParams) (dbgen.PhotoAlbum, error) {
	f.archiveParams = params
	return f.archiveResult, f.archiveErr
}
func (f *photoAlbumStoreFake) ListPhotoAlbumAuditEvents(context.Context, uuid.UUID) ([]dbgen.ListPhotoAlbumAuditEventsRow, error) {
	return f.audit, nil
}
