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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type announcementStoreFake struct{}

func (announcementStoreFake) ListAnnouncementProgrammes(context.Context) ([]dbgen.ListAnnouncementProgrammesRow, error) {
	return nil, nil
}

func TestAnnouncementDocumentValidationAndRoundTrip(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/admin/announcements", strings.NewReader("title=Caderno&body=Consulte+o+documento.&document_url=https%3A%2F%2Fexample.org%2Fcaderno.pdf&document_source=Federa%C3%A7%C3%A3o+Portuguesa+de+Canoagem&reviewed_on=2026-07-26"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	form := (Announcements{}).validate(request)
	if !form.Errors.Empty() {
		t.Fatalf("unexpected validation errors: %#v", form.Errors)
	}
	body, document := parseOfficialDocument(documentBody(form))
	if body != form.Body || document == nil || document.URL != form.DocumentURL || document.Source != form.DocumentSource || document.ReviewedOn != form.ReviewedOn {
		t.Fatalf("document round trip failed: body=%q document=%#v", body, document)
	}
}

func TestManagedAnnouncementsMetadataUsesCoordinationWorkspace(t *testing.T) {
	meta := (Announcements{}).meta(httptest.NewRequest(http.MethodGet, "/admin/avisos", nil), CurrentUser{Name: "Treinadora"}, "/admin/avisos", "Avisos")
	if meta.AreaLabel != "Coordenação" || meta.PageLabel != "Gerir avisos" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestAnnouncementDocumentRejectsUnsafeOrIncompleteMetadata(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/admin/announcements", strings.NewReader("title=Caderno&body=Consulte+o+documento.&document_url=http%3A%2F%2Fexample.org%2Fcaderno.pdf&document_source=&reviewed_on=2999-01-01"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	form := (Announcements{}).validate(request)
	for _, field := range []string{"document_url", "document_source", "reviewed_on"} {
		if form.Errors[field] == "" {
			t.Errorf("expected error for %s", field)
		}
	}
}

func TestAnnouncementCreateWritesDraftAndSelectedAudienceInTransaction(t *testing.T) {
	announcementID, programmeID, actorID := uuid.New(), uuid.New(), uuid.New()
	tx := &announcementTransactionFake{id: announcementID}
	h := Announcements{Store: announcementStoreFake{}, DB: announcementMutationDB{tx: tx}, Location: time.UTC}
	values := url.Values{"title": {"Alteração de horário"}, "body": {"O treino começa às 18h."}, "expires_at": {"2026-09-10T18:00"}, "programme_id": {programmeID.String()}, "action": {"draft"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/avisos", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/avisos" || !tx.committed || len(tx.queryArgs) != 1 || len(tx.execArgs) != 1 {
		t.Fatalf("response=%d location=%q committed=%t query=%#v exec=%#v", w.Code, w.Header().Get("Location"), tx.committed, tx.queryArgs, tx.execArgs)
	}
	created := tx.queryArgs[0]
	if created[0] != "Alteração de horário" || created[1] != "O treino começa às 18h." || created[2] != actorID {
		t.Fatalf("create args=%#v", created)
	}
	target := tx.execArgs[0]
	targetID, ok := target[2].(*uuid.UUID)
	if target[0] != announcementID || target[1] != dbgen.AnnouncementTargetTypePROGRAMME || !ok || *targetID != programmeID {
		t.Fatalf("target args=%#v", target)
	}
}

func TestAnnouncementCreatePublishesWithinTheSameTransaction(t *testing.T) {
	announcementID, actorID := uuid.New(), uuid.New()
	tx := &announcementTransactionFake{id: announcementID}
	h := Announcements{Store: announcementStoreFake{}, DB: announcementMutationDB{tx: tx}, Location: time.UTC}
	values := url.Values{"title": {"Aviso urgente"}, "body": {"O cais encerra hoje."}, "action": {"publish"}}
	r := httptest.NewRequest(http.MethodPost, "/admin/avisos", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actorID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusSeeOther || !tx.committed || len(tx.queryArgs) != 1 || len(tx.execArgs) != 1 {
		t.Fatalf("response=%d committed=%t query=%#v exec=%#v", w.Code, tx.committed, tx.queryArgs, tx.execArgs)
	}
	published := tx.execArgs[0]
	actor, ok := published[0].(*uuid.UUID)
	if !ok || *actor != actorID || published[1] != announcementID {
		t.Fatalf("publish args=%#v", published)
	}
}
func (announcementStoreFake) ListAnnouncementTeams(context.Context) ([]dbgen.ListAnnouncementTeamsRow, error) {
	return nil, nil
}
func (announcementStoreFake) ListAnnouncementCategories(context.Context) ([]dbgen.ListAnnouncementCategoriesRow, error) {
	return nil, nil
}
func (announcementStoreFake) ListAnnouncementModalities(context.Context) ([]dbgen.ListAnnouncementModalitiesRow, error) {
	return nil, nil
}
func (announcementStoreFake) ListAnnouncementEvents(context.Context) ([]dbgen.ListAnnouncementEventsRow, error) {
	return nil, nil
}
func (announcementStoreFake) ListAnnouncementsForAuthor(context.Context, dbgen.ListAnnouncementsForAuthorParams) ([]dbgen.ListAnnouncementsForAuthorRow, error) {
	return nil, nil
}
func (announcementStoreFake) ListVisibleAnnouncements(context.Context, dbgen.ListVisibleAnnouncementsParams) ([]dbgen.ListVisibleAnnouncementsRow, error) {
	return nil, nil
}
func (announcementStoreFake) CountUnreadVisibleAnnouncements(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (announcementStoreFake) GetVisibleAnnouncement(context.Context, dbgen.GetVisibleAnnouncementParams) (dbgen.GetVisibleAnnouncementRow, error) {
	return dbgen.GetVisibleAnnouncementRow{}, nil
}
func (announcementStoreFake) GetAnnouncementAuthor(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (announcementStoreFake) GetAnnouncementForStatus(context.Context, uuid.UUID) (dbgen.GetAnnouncementForStatusRow, error) {
	return dbgen.GetAnnouncementForStatusRow{}, nil
}

func TestAnnouncementPaginationURLsAreIndependent(t *testing.T) {
	if got, want := announcementsPageURL(2, 3), "/announcements?authored_page=3&page=2"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got := announcementPageNumber("0"); got != 1 {
		t.Fatalf("page = %d, want 1", got)
	}
}

func TestAnnouncementAudienceSelectionRetainsOnlySelectedTarget(t *testing.T) {
	selected, other := uuid.New(), uuid.New()
	form := announcementForm{Targets: map[dbgen.AnnouncementTargetType][]uuid.UUID{dbgen.AnnouncementTargetTypeTEAM: {selected}}}
	if !announcementTargetSelected(form, dbgen.AnnouncementTargetTypeTEAM, selected) || announcementTargetSelected(form, dbgen.AnnouncementTargetTypeTEAM, other) || announcementTargetSelected(form, dbgen.AnnouncementTargetTypePROGRAMME, selected) {
		t.Fatalf("selection matching failed for %#v", form.Targets)
	}
}

func TestAnnouncementsManagementIndexPaginatesAuthoredItems(t *testing.T) {
	userID := uuid.New()
	store := &paginatedAnnouncementStore{}
	for range announcementPageSize + 1 {
		store.visible = append(store.visible, dbgen.ListVisibleAnnouncementsRow{ID: uuid.New(), Title: "Aviso"})
	}
	for range announcementPageSize + 1 {
		store.authored = append(store.authored, dbgen.ListAnnouncementsForAuthorRow{ID: uuid.New(), Title: "Aviso", Status: "DRAFT"})
	}
	h := Announcements{Store: store, DB: announcementDBFake{}, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/admin/avisos?authored_page=3", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, IsAdmin: true}))
	w := httptest.NewRecorder()

	h.Index(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := store.visibleParams; got != (dbgen.ListVisibleAnnouncementsParams{}) {
		t.Fatalf("visible announcements queried on management page: %#v", got)
	}
	if got, want := store.authoredParams, (dbgen.ListAnnouncementsForAuthorParams{AuthorID: userID, RowLimit: announcementPageSize + 1, RowOffset: announcementPageSize * 2}); got != want {
		t.Fatalf("authored params = %#v, want %#v", got, want)
	}
	for _, link := range []string{"/admin/avisos?authored_page=2", "/admin/avisos?authored_page=4"} {
		if !strings.Contains(w.Body.String(), link) {
			t.Errorf("body does not contain %q", link)
		}
	}
}

func TestAnnouncementsMemberIndexRecordsVisibleDeliveryAndPaginates(t *testing.T) {
	userID := uuid.New()
	store := &paginatedAnnouncementStore{}
	for range announcementPageSize + 1 {
		store.visible = append(store.visible, dbgen.ListVisibleAnnouncementsRow{ID: uuid.New(), Title: "Alteração de treino", PublishedAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC), Valid: true}})
	}
	h := Announcements{Store: store, DB: announcementDBFake{}, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/announcements?page=2", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Atleta"}))
	w := httptest.NewRecorder()

	h.Index(w, r)

	if w.Code != http.StatusOK || store.visibleParams.UserID != userID || store.visibleParams.RowLimit != announcementPageSize+1 || store.visibleParams.RowOffset != announcementPageSize || !strings.Contains(w.Body.String(), "Alteração de treino") || !strings.Contains(w.Body.String(), "/announcements?authored_page=1&amp;page=1") || !strings.Contains(w.Body.String(), "/announcements?authored_page=1&amp;page=3") {
		t.Fatalf("response=%d params=%#v body=%s", w.Code, store.visibleParams, w.Body.String())
	}
}

func TestAnnouncementDetailLooksUpVisibleIDDirectly(t *testing.T) {
	userID, announcementID := uuid.New(), uuid.New()
	store := &paginatedAnnouncementStore{detailErr: pgx.ErrNoRows}
	h := Announcements{Store: store}
	r := httptest.NewRequest(http.MethodGet, "/announcements/"+announcementID.String(), nil)
	r.SetPathValue("id", announcementID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	w := httptest.NewRecorder()

	h.Detail(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if got, want := store.detailParams, (dbgen.GetVisibleAnnouncementParams{ID: announcementID, UserID: userID}); got != want {
		t.Fatalf("detail params = %#v, want %#v", got, want)
	}
}

func TestAnnouncementDetailRendersOnlyStoreVisibleContentAndRecordsReadState(t *testing.T) {
	userID, announcementID := uuid.New(), uuid.New()
	published := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	store := &paginatedAnnouncementStore{detail: dbgen.GetVisibleAnnouncementRow{ID: announcementID, Title: "Alteração de treino", Body: "Consulte o novo horário.", PublishedAt: pgtype.Timestamptz{Time: published, Valid: true}}}
	h := Announcements{Store: store, DB: announcementDBFake{}, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/announcements/"+announcementID.String(), nil)
	r.SetPathValue("id", announcementID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Atleta"}))
	w := httptest.NewRecorder()
	h.Detail(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(w.Body.String(), "Alteração de treino") || !strings.Contains(w.Body.String(), "Consulte o novo horário") || store.detailParams != (dbgen.GetVisibleAnnouncementParams{ID: announcementID, UserID: userID}) {
		t.Fatalf("response=%d headers=%v body=%s params=%+v", w.Code, w.Header(), w.Body.String(), store.detailParams)
	}
}

func TestAnnouncementMemberReadsFailClosedOnStoreFailures(t *testing.T) {
	userID := uuid.New()
	newRequest := func(path string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	}
	for _, tc := range []struct {
		name  string
		store *paginatedAnnouncementStore
		hit   func(Announcements, http.ResponseWriter, *http.Request)
		path  string
	}{
		{"member list", &paginatedAnnouncementStore{visibleErr: errors.New("database unavailable")}, func(h Announcements, w http.ResponseWriter, r *http.Request) { h.Index(w, r) }, "/announcements"},
		{"panel count", &paginatedAnnouncementStore{unreadErr: errors.New("database unavailable")}, func(h Announcements, w http.ResponseWriter, r *http.Request) { h.Panel(w, r) }, "/announcements/panel"},
		{"panel items", &paginatedAnnouncementStore{visibleErr: errors.New("database unavailable")}, func(h Announcements, w http.ResponseWriter, r *http.Request) { h.Panel(w, r) }, "/announcements/panel"},
		{"detail lookup", &paginatedAnnouncementStore{detailErr: errors.New("database unavailable")}, func(h Announcements, w http.ResponseWriter, r *http.Request) { h.Detail(w, r) }, "/announcements/" + uuid.NewString()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRequest(tc.path)
			if tc.name == "detail lookup" {
				r.SetPathValue("id", strings.TrimPrefix(tc.path, "/announcements/"))
			}
			w := httptest.NewRecorder()
			tc.hit(Announcements{Store: tc.store, DB: announcementDBFake{}, Location: time.UTC}, w, r)
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "Aviso privado") {
				t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAnnouncementStatusChangesRequireAuthorAndValidLifecycleTransition(t *testing.T) {
	userID, announcementID := uuid.New(), uuid.New()
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/avisos/"+announcementID.String()+"/publicar", nil)
		r.SetPathValue("id", announcementID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	}

	t.Run("author can publish and expire", func(t *testing.T) {
		for _, action := range []func(http.ResponseWriter, *http.Request){Announcements{Store: &paginatedAnnouncementStore{author: userID}, DB: announcementDBFake{}}.Publish, Announcements{Store: &paginatedAnnouncementStore{author: userID}, DB: announcementDBFake{}}.Expire} {
			w := httptest.NewRecorder()
			action(w, request())
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/avisos" {
				t.Fatalf("response=%d location=%q", w.Code, w.Header().Get("Location"))
			}
		}
	})

	t.Run("unrelated user cannot change status", func(t *testing.T) {
		h := Announcements{Store: &paginatedAnnouncementStore{author: uuid.New()}, DB: announcementDBFake{}}
		w := httptest.NewRecorder()
		h.Publish(w, request())
		if w.Code != http.StatusForbidden {
			t.Fatalf("response=%d", w.Code)
		}
	})

	t.Run("invalid transition is a conflict", func(t *testing.T) {
		h := Announcements{Store: &paginatedAnnouncementStore{author: userID}, DB: announcementActionDB{tag: pgconn.NewCommandTag("UPDATE 0")}}
		w := httptest.NewRecorder()
		h.Expire(w, request())
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "operação não é válida") {
			t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestAnnouncementStatusChangesMapAuthorAndWriteFailures(t *testing.T) {
	userID, announcementID := uuid.New(), uuid.New()
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/admin/avisos/"+announcementID.String()+"/publicar", nil)
		r.SetPathValue("id", announcementID.String())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	}
	for _, tc := range []struct {
		name      string
		authorErr error
		dbErr     error
		want      int
	}{
		{name: "missing announcement", authorErr: pgx.ErrNoRows, want: http.StatusNotFound},
		{name: "author lookup failure", authorErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
		{name: "status write failure", dbErr: errors.New("database unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Announcements{Store: &paginatedAnnouncementStore{author: userID, authorErr: tc.authorErr}, DB: announcementActionDB{tag: pgconn.NewCommandTag("UPDATE 1"), err: tc.dbErr}}
			response := httptest.NewRecorder()
			h.Publish(response, request())
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestAnnouncementPanelRendersUnreadCountAndRecentItems(t *testing.T) {
	userID, announcementID := uuid.New(), uuid.New()
	store := &paginatedAnnouncementStore{
		unreadCount: 3,
		visible: []dbgen.ListVisibleAnnouncementsRow{{
			ID: announcementID, Title: "Alteração de cais",
			PublishedAt: pgtype.Timestamptz{Time: time.Date(2026, 8, 5, 9, 30, 0, 0, time.UTC), Valid: true},
		}},
	}
	h := Announcements{Store: store, Location: time.UTC}
	r := httptest.NewRequest(http.MethodGet, "/announcements/panel", nil)
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	w := httptest.NewRecorder()

	h.Panel(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	for _, want := range []string{`data-announcement-count="3"`, "Alteração de cais", `href="/announcements/` + announcementID.String() + `"`, "Por ler"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("panel does not contain %q: %q", want, w.Body.String())
		}
	}
	if store.countUserID != userID || store.visibleParams.UserID != userID || store.visibleParams.RowLimit != announcementPanelSize {
		t.Fatalf("panel query parameters = count user %s, visible %+v", store.countUserID, store.visibleParams)
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestAnnouncementPublishTaskNamesAndGuardsTheCurrentDraft(t *testing.T) {
	userID, announcementID := uuid.New(), uuid.New()
	store := &announcementStatusStore{item: dbgen.GetAnnouncementForStatusRow{ID: announcementID, Title: "Alteração de horário", Status: "DRAFT", AuthorID: userID}}
	h := Announcements{Store: store}
	r := httptest.NewRequest(http.MethodGet, "/admin/announcements/"+announcementID.String()+"/publicar", nil)
	r.SetPathValue("id", announcementID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Treinadora"}))
	w := httptest.NewRecorder()

	h.PublishPage(w, r)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Alteração de horário") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestAnnouncementStatusTaskRejectsStaleLifecycle(t *testing.T) {
	userID, announcementID := uuid.New(), uuid.New()
	store := &announcementStatusStore{item: dbgen.GetAnnouncementForStatusRow{ID: announcementID, Title: "Alteração de horário", Status: "PUBLISHED", AuthorID: userID}}
	h := Announcements{Store: store}
	r := httptest.NewRequest(http.MethodGet, "/admin/announcements/"+announcementID.String()+"/publicar", nil)
	r.SetPathValue("id", announcementID.String())
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID}))
	w := httptest.NewRecorder()

	h.PublishPage(w, r)

	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "já não está disponível") {
		t.Fatalf("response = %d, body = %s", w.Code, w.Body.String())
	}
}

type paginatedAnnouncementStore struct {
	announcementStoreFake
	visibleParams  dbgen.ListVisibleAnnouncementsParams
	authoredParams dbgen.ListAnnouncementsForAuthorParams
	detailParams   dbgen.GetVisibleAnnouncementParams
	countUserID    uuid.UUID
	unreadCount    int64
	visible        []dbgen.ListVisibleAnnouncementsRow
	visibleErr     error
	authored       []dbgen.ListAnnouncementsForAuthorRow
	unreadErr      error
	detailErr      error
	detail         dbgen.GetVisibleAnnouncementRow
	author         uuid.UUID
	authorErr      error
}

type announcementStatusStore struct {
	announcementStoreFake
	item dbgen.GetAnnouncementForStatusRow
}

func (s *announcementStatusStore) GetAnnouncementForStatus(context.Context, uuid.UUID) (dbgen.GetAnnouncementForStatusRow, error) {
	return s.item, nil
}

func (s *paginatedAnnouncementStore) ListVisibleAnnouncements(_ context.Context, params dbgen.ListVisibleAnnouncementsParams) ([]dbgen.ListVisibleAnnouncementsRow, error) {
	s.visibleParams = params
	return s.visible, s.visibleErr
}

func (s *paginatedAnnouncementStore) CountUnreadVisibleAnnouncements(_ context.Context, userID uuid.UUID) (int64, error) {
	s.countUserID = userID
	return s.unreadCount, s.unreadErr
}

type announcementDBFake struct{}

func (announcementDBFake) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return nil, nil }
func (announcementDBFake) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (announcementDBFake) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}
func (announcementDBFake) QueryRow(context.Context, string, ...interface{}) pgx.Row { return nil }

type announcementActionDB struct {
	tag pgconn.CommandTag
	err error
}

func (s announcementActionDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, nil
}
func (s announcementActionDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return s.tag, s.err
}
func (announcementActionDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}
func (announcementActionDB) QueryRow(context.Context, string, ...interface{}) pgx.Row { return nil }

type announcementTransactionFake struct {
	pgx.Tx
	id        uuid.UUID
	queryArgs [][]any
	execArgs  [][]any
	committed bool
}

func (tx *announcementTransactionFake) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	tx.queryArgs = append(tx.queryArgs, args)
	return announcementTransactionRow{id: tx.id}
}
func (tx *announcementTransactionFake) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.execArgs = append(tx.execArgs, args)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (tx *announcementTransactionFake) Commit(context.Context) error { tx.committed = true; return nil }
func (*announcementTransactionFake) Rollback(context.Context) error  { return nil }

type announcementTransactionRow struct{ id uuid.UUID }

func (row announcementTransactionRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if id, ok := dest[0].(*uuid.UUID); ok {
			*id = row.id
		}
	}
	return nil
}

type announcementMutationDB struct {
	pgx.Tx
	tx *announcementTransactionFake
}

func (db announcementMutationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return db.tx, nil
}

func (s *paginatedAnnouncementStore) ListAnnouncementsForAuthor(_ context.Context, params dbgen.ListAnnouncementsForAuthorParams) ([]dbgen.ListAnnouncementsForAuthorRow, error) {
	s.authoredParams = params
	return s.authored, nil
}

func (s *paginatedAnnouncementStore) GetVisibleAnnouncement(_ context.Context, params dbgen.GetVisibleAnnouncementParams) (dbgen.GetVisibleAnnouncementRow, error) {
	s.detailParams = params
	return s.detail, s.detailErr
}
func (s *paginatedAnnouncementStore) GetAnnouncementAuthor(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.author, s.authorErr
}
func (announcementStoreFake) CanCoachManageEvent(context.Context, dbgen.CanCoachManageEventParams) (bool, error) {
	return true, nil
}

func TestAnnouncementReadStatePolicy(t *testing.T) {
	if !announcementReadOnDetail() {
		t.Fatal("opening an announcement must mark its existing delivery read")
	}
}

func TestAnnouncementCoachScopeRejectsGlobalAndGuardianOnlyAudiences(t *testing.T) {
	programmeID := uuid.New()
	h := Announcements{Store: announcementStoreFake{}}
	coach := CurrentUser{CoachProgrammeIDs: map[uuid.UUID]bool{programmeID: true}, CoachTeamIDs: map[uuid.UUID]bool{}}
	if h.authorized(context.Background(), coach, announcementForm{Targets: map[dbgen.AnnouncementTargetType][]uuid.UUID{}}) {
		t.Fatal("coach must not publish a global announcement")
	}
	if h.authorized(context.Background(), coach, announcementForm{Guardian: true, Targets: map[dbgen.AnnouncementTargetType][]uuid.UUID{}}) {
		t.Fatal("coach must not publish to every guardian")
	}
	if !h.authorized(context.Background(), coach, announcementForm{Guardian: true, Targets: map[dbgen.AnnouncementTargetType][]uuid.UUID{dbgen.AnnouncementTargetTypePROGRAMME: {programmeID}}}) {
		t.Fatal("coach should be able to target guardians within a granted programme")
	}
}
