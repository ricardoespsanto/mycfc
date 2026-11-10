package handlers

import (
	"context"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type reuseWorkspaceStore struct {
	PostgresStructuredReuseStore
	row         dbgen.GetReusePlanRow
	err         error
	actor, plan uuid.UUID
	calls       int
}

func (s *reuseWorkspaceStore) ReusePlan(_ context.Context, actor, plan uuid.UUID) (dbgen.GetReusePlanRow, error) {
	s.actor, s.plan = actor, plan
	s.calls++
	return s.row, s.err
}

func TestReuseWorkspaceLinksRequireCurrentScopedPlan(t *testing.T) {
	actor, plan, group, season := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	authorized := dbgen.GetReusePlanRow{
		TrainingGroupID: &group, SeasonID: &season,
		WeekStart: pgtype.Date{Time: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), Valid: true},
	}
	for _, tc := range []struct {
		name    string
		enabled bool
		week    string
		scope   uuid.UUID
		err     error
		calls   int
	}{
		{"disabled workspace", false, plan.String(), group, nil, 0},
		{"invalid plan", true, "invalid", group, nil, 0},
		{"revoked authority", true, plan.String(), group, pgx.ErrNoRows, 1},
		{"plan from another group", true, plan.String(), uuid.New(), nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &reuseWorkspaceStore{row: authorized, err: tc.err}
			page := pages.StructuredWeekWorkspace{Enabled: tc.enabled, HasWeek: true, WeekID: tc.week, GroupID: tc.scope.String()}
			(StructuredReuse{Store: store}).workspaceLinks(t.Context(), CurrentUser{ID: actor}, &page)
			if page.CopyURL != "" || page.RoutineURL != "" || page.AdaptURL != "" || store.calls != tc.calls {
				t.Fatalf("unexpected links or lookup count: %q %q %q %d", page.CopyURL, page.RoutineURL, page.AdaptURL, store.calls)
			}
			if tc.calls > 0 && (store.actor != actor || store.plan != plan) {
				t.Fatal("lookup lost the authenticated actor or selected plan")
			}
		})
	}
}
