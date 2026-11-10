//go:build integration

package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Historical feature tests still need privileged setup/inspection, but actual
// mutations use the independently authenticated current web identity and floor.
func currentTrainingFixture(t *testing.T) (context.Context, *pgxpool.Pool, *publicationDomainFixture) {
	t.Helper()
	f := newPublicationDomainFixture(t)
	root, err := pgxpool.New(t.Context(), f.raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(root.Close)
	return t.Context(), root, f
}

func currentTrainingActor(t *testing.T, root *pgxpool.Pool, actor uuid.UUID) {
	t.Helper()
	if _, err := root.Exec(t.Context(), `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, actor); err != nil {
		t.Fatal(err)
	}
}

func currentTrainingPlacement(t *testing.T, f *publicationDomainFixture, actor, person, programme, season, group uuid.UUID, on time.Time) uuid.UUID {
	t.Helper()
	scoped := *f
	scoped.actor, scoped.programme, scoped.season = actor, programme, season
	return scoped.place(t, person, group, season, on.Format("2006-01-02"))
}
