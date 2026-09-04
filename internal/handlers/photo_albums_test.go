package handlers

import (
	"context"
	"errors"
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
	if !strings.Contains(w.Body.String(), `data-task-surface`) {
		t.Fatal("invalid form did not reopen the authoring panel")
	}
}

func TestPhotoAlbumCreateWritesSelectedAudiencesInOneTransaction(t *testing.T) {
	actorID, programmeID, teamID, albumID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tx := &eventTransactionFake{eventID: albumID}
	store := &photoAlbumStoreFake{
		programmes: []dbgen.Programme{{ID: programmeID, NamePt: "Competição"}},
		teams:      []dbgen.ListTeamsForEventAuthoringRow{{ID: teamID, Name: "Juniores"}},
	}
	h := PhotoAlbums{Store: store, DB: eventMutationDB{tx: tx}}
	values := url.Values{"title": {"Regata de verão"}, "description": {"Momentos privados da equipa."}, "programme_id": {programmeID.String()}, "team_id": {teamID.String()}}
	r := httptest.NewRequest(http.MethodPost, "/admin/albuns", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/albuns" || !tx.committed {
		t.Fatalf("response=%d location=%q committed=%t", w.Code, w.Header().Get("Location"), tx.committed)
	}
	if len(tx.queryCalls) != 1 || len(tx.execCalls) != 2 {
		t.Fatalf("query calls=%#v exec calls=%#v", tx.queryCalls, tx.execCalls)
	}
	if created := tx.queryCalls[0].args; created[0] != "Regata de verão" || created[1] != "Momentos privados da equipa." || created[2] != actorID {
		t.Fatalf("create arguments=%#v", created)
	}
	if programme := tx.execCalls[0].args; programme[0] != albumID || programme[1] != programmeID {
		t.Fatalf("programme audience=%#v", programme)
	}
	if team := tx.execCalls[1].args; team[0] != albumID || team[1] != teamID {
		t.Fatalf("team audience=%#v", team)
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

func TestPhotoAlbumArchivePageNamesAlbumAndRecoversConflictInTask(t *testing.T) {
	actorID, albumID := uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := &photoAlbumStoreFake{get: dbgen.GetVisiblePhotoAlbumRow{ID: albumID, Title: "Regata de verão", Status: dbgen.PhotoAlbumStatusOPEN, ProgrammeNames: "Competição", CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}
	h := PhotoAlbums{Store: store, Location: time.UTC}
	request := httptest.NewRequest(http.MethodGet, "/admin/albuns/"+albumID.String()+"/arquivar?return_to=%2Fadmin%2Falbuns", nil)
	request.SetPathValue("id", albumID.String())
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: actorID, Name: "Moderadora", CanModerateContent: true}))
	response := httptest.NewRecorder()

	h.ArchivePage(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Regata de verão") || !strings.Contains(response.Body.String(), `action="/admin/albuns/`+albumID.String()+`/arquivar"`) || !strings.Contains(response.Body.String(), `name="updated_at" value="`+now.Format(time.RFC3339Nano)+`"`) {
		t.Fatalf("response = %d, body = %s", response.Code, response.Body.String())
	}

	store.get.Status = dbgen.PhotoAlbumStatusARCHIVED
	response = httptest.NewRecorder()
	h.ArchivePage(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "já está arquivado") {
		t.Fatalf("archived album = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestPhotoAlbumHandlersFailClosedForScopedReadAndArchiveFailures(t *testing.T) {
	actorID, albumID := uuid.New(), uuid.New()
	admin := CurrentUser{ID: actorID, IsAdmin: true}
	for _, tc := range []struct {
		name   string
		store  *photoAlbumStoreFake
		hit    func(PhotoAlbums, http.ResponseWriter, *http.Request)
		path   string
		method string
		body   string
		want   int
	}{
		{"visible albums", &photoAlbumStoreFake{visibleErr: errors.New("database unavailable")}, func(h PhotoAlbums, w http.ResponseWriter, r *http.Request) { h.Index(w, r) }, "/albuns", http.MethodGet, "", http.StatusInternalServerError},
		{"programmes", &photoAlbumStoreFake{programmesErr: errors.New("database unavailable")}, func(h PhotoAlbums, w http.ResponseWriter, r *http.Request) { h.Index(w, r) }, "/admin/albuns", http.MethodGet, "", http.StatusInternalServerError},
		{"teams", &photoAlbumStoreFake{teamsErr: errors.New("database unavailable")}, func(h PhotoAlbums, w http.ResponseWriter, r *http.Request) { h.Index(w, r) }, "/admin/albuns", http.MethodGet, "", http.StatusInternalServerError},
		{"detail lookup", &photoAlbumStoreFake{getErr: errors.New("database unavailable")}, func(h PhotoAlbums, w http.ResponseWriter, r *http.Request) { h.Detail(w, r) }, "/albuns/" + albumID.String(), http.MethodGet, "", http.StatusInternalServerError},
		{"audit trail", &photoAlbumStoreFake{get: dbgen.GetVisiblePhotoAlbumRow{ID: albumID, Title: "Regata", Status: dbgen.PhotoAlbumStatusOPEN}, auditErr: errors.New("database unavailable")}, func(h PhotoAlbums, w http.ResponseWriter, r *http.Request) { h.Detail(w, r) }, "/admin/albuns/" + albumID.String(), http.MethodGet, "", http.StatusInternalServerError},
		{"archive stale", &photoAlbumStoreFake{archiveErr: pgx.ErrNoRows}, func(h PhotoAlbums, w http.ResponseWriter, r *http.Request) { h.Archive(w, r) }, "/admin/albuns/" + albumID.String() + "/arquivar", http.MethodPost, "updated_at=" + url.QueryEscape(time.Now().UTC().Format(time.RFC3339Nano)), http.StatusConflict},
		{"archive write", &photoAlbumStoreFake{archiveErr: errors.New("database unavailable")}, func(h PhotoAlbums, w http.ResponseWriter, r *http.Request) { h.Archive(w, r) }, "/admin/albuns/" + albumID.String() + "/arquivar", http.MethodPost, "updated_at=" + url.QueryEscape(time.Now().UTC().Format(time.RFC3339Nano)), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			r.SetPathValue("id", albumID.String())
			r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, admin))
			w := httptest.NewRecorder()
			tc.hit(PhotoAlbums{Store: tc.store, Location: time.UTC}, w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
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
	programmesErr error
	teams         []dbgen.ListTeamsForEventAuthoringRow
	teamsErr      error
	visible       []dbgen.ListVisiblePhotoAlbumsRow
	visibleErr    error
	visibleParams dbgen.ListVisiblePhotoAlbumsParams
	get           dbgen.GetVisiblePhotoAlbumRow
	getErr        error
	archiveResult dbgen.PhotoAlbum
	archiveParams dbgen.ArchivePhotoAlbumParams
	archiveErr    error
	audit         []dbgen.ListPhotoAlbumAuditEventsRow
	auditErr      error
}

func (f *photoAlbumStoreFake) ListProgrammes(context.Context) ([]dbgen.Programme, error) {
	return f.programmes, f.programmesErr
}
func (f *photoAlbumStoreFake) ListTeamsForEventAuthoring(context.Context) ([]dbgen.ListTeamsForEventAuthoringRow, error) {
	return f.teams, f.teamsErr
}
func (f *photoAlbumStoreFake) ListVisiblePhotoAlbums(_ context.Context, params dbgen.ListVisiblePhotoAlbumsParams) ([]dbgen.ListVisiblePhotoAlbumsRow, error) {
	f.visibleParams = params
	return f.visible, f.visibleErr
}
func (f *photoAlbumStoreFake) GetVisiblePhotoAlbum(_ context.Context, params dbgen.GetVisiblePhotoAlbumParams) (dbgen.GetVisiblePhotoAlbumRow, error) {
	return f.get, f.getErr
}
func (f *photoAlbumStoreFake) ArchivePhotoAlbum(_ context.Context, params dbgen.ArchivePhotoAlbumParams) (dbgen.PhotoAlbum, error) {
	f.archiveParams = params
	return f.archiveResult, f.archiveErr
}
func (f *photoAlbumStoreFake) ListPhotoAlbumAuditEvents(context.Context, uuid.UUID) ([]dbgen.ListPhotoAlbumAuditEventsRow, error) {
	return f.audit, f.auditErr
}
