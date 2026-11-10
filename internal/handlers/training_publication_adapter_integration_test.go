//go:build integration

package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type publicationBeforeHeaderDB struct {
	structuredTrainingDB
	beforeHeader func()
}

func (d publicationBeforeHeaderDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.HasPrefix(sql, "-- name: PublicationPlanForReview :one") {
		d.beforeHeader()
	}
	return d.structuredTrainingDB.QueryRow(ctx, sql, args...)
}

func TestPostgresPublicationPreviewRefusesAuthorityAndSourceDrift(t *testing.T) {
	f := newPublicationDomainFixture(t)
	ctx := t.Context()
	store := PostgresStructuredTrainingStore{Pool: f.web}

	untrusted := PostgresStructuredTrainingStore{Pool: f.root}
	header, err := untrusted.AuthorizePublication(ctx, f.actor, f.plan)
	if err == nil || header.PlanID != uuid.Nil {
		t.Fatal("bootstrap connection substituted for the publisher identity", err)
	}
	header, err = store.AuthorizePublication(ctx, f.actor, f.plan)
	if err != nil || header.PlanID != f.plan || header.GroupID != f.group {
		t.Fatalf("authorized review header not returned: %+v %v", header, err)
	}

	// Materialization and the final scope/version check use separate statements.
	// Make a real intervening update to test the boundary deterministically.
	calls := 0
	store.Pool = publicationBeforeHeaderDB{structuredTrainingDB: f.web, beforeHeader: func() {
		calls++
		f.exec(t, `UPDATE training_plans SET title='Revised after materialization',updated_at=clock_timestamp() WHERE id=$1`, f.plan)
	}}
	source, err := store.ReadPublicationSource(ctx, f.actor, f.plan)
	if !errors.Is(err, errStructuredTrainingPublicationConflict) || source.Header.PlanID != uuid.Nil || calls != 1 {
		t.Fatalf("source drift produced a review: %v, header reads %d", err, calls)
	}

	// Keep the fixture's last-administrator invariant while revoking this actor.
	backup := uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth)
		VALUES($1,'Synthetic backup administrator',$2,'hash','1980-01-01')`, backup, backup.String()+"@example.test")
	f.exec(t, `INSERT INTO user_platform_roles(user_id,role_id)
		SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, backup)
	calls = 0
	store.Pool = publicationBeforeHeaderDB{structuredTrainingDB: f.web, beforeHeader: func() {
		calls++
		f.exec(t, `DELETE FROM user_platform_roles WHERE user_id=$1`, f.actor)
	}}
	source, err = store.ReadPublicationSource(ctx, f.actor, f.plan)
	if !errors.Is(err, pgx.ErrNoRows) || source.Header.PlanID != uuid.Nil || calls != 1 {
		t.Fatalf("revoked authority produced a review: %v, header reads %d", err, calls)
	}
	var publications int
	if err := f.web.QueryRow(ctx, "SELECT count(*) FROM training_plan_publications WHERE plan_id=$1", f.plan).Scan(&publications); err != nil || publications != 0 {
		t.Fatalf("read-only refusals changed publication history: %d %v", publications, err)
	}
}
