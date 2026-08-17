package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestAnnouncementPaginationURLsAreIndependent(t *testing.T) {
	if got, want := announcementsPageURL(2, 3), "/announcements?authored_page=3&page=2"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got := announcementPageNumber("0"); got != 1 {
		t.Fatalf("page = %d, want 1", got)
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

type paginatedAnnouncementStore struct {
	announcementStoreFake
	visibleParams  dbgen.ListVisibleAnnouncementsParams
	authoredParams dbgen.ListAnnouncementsForAuthorParams
	detailParams   dbgen.GetVisibleAnnouncementParams
	countUserID    uuid.UUID
	unreadCount    int64
	visible        []dbgen.ListVisibleAnnouncementsRow
	authored       []dbgen.ListAnnouncementsForAuthorRow
	detailErr      error
}

func (s *paginatedAnnouncementStore) ListVisibleAnnouncements(_ context.Context, params dbgen.ListVisibleAnnouncementsParams) ([]dbgen.ListVisibleAnnouncementsRow, error) {
	s.visibleParams = params
	return s.visible, nil
}

func (s *paginatedAnnouncementStore) CountUnreadVisibleAnnouncements(_ context.Context, userID uuid.UUID) (int64, error) {
	s.countUserID = userID
	return s.unreadCount, nil
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

func (s *paginatedAnnouncementStore) ListAnnouncementsForAuthor(_ context.Context, params dbgen.ListAnnouncementsForAuthorParams) ([]dbgen.ListAnnouncementsForAuthorRow, error) {
	s.authoredParams = params
	return s.authored, nil
}

func (s *paginatedAnnouncementStore) GetVisibleAnnouncement(_ context.Context, params dbgen.GetVisibleAnnouncementParams) (dbgen.GetVisibleAnnouncementRow, error) {
	s.detailParams = params
	return dbgen.GetVisibleAnnouncementRow{}, s.detailErr
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
