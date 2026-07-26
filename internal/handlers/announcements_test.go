package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
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
func (announcementStoreFake) GetAnnouncementAuthor(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
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
