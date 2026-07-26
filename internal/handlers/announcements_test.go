package handlers

import (
	"context"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

type announcementStoreFake struct{}

func (announcementStoreFake) ListAnnouncementProgrammes(context.Context) ([]dbgen.ListAnnouncementProgrammesRow, error) {
	return nil, nil
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
