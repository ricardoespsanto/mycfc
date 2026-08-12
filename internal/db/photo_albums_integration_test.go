//go:build integration

package db

import (
	"context"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPhotoAlbumIntegrationScopesLifecycleAndAudit(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	queries := dbgen.New(tx)

	creatorID, memberID, guardianID, dependentID, unrelatedID, coachID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{creatorID: "Criadora", memberID: "Atleta", guardianID: "Tutora", unrelatedID: "Sem acesso", coachID: "Treinador"} {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', '1990-01-01')`, id, name, "album-"+uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users (id, name, guardian_id, is_dependent, date_of_birth) VALUES ($1, 'Dependente', $2, true, '2012-01-01')`, dependentID, guardianID); err != nil {
		t.Fatal(err)
	}

	var programmeID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code = 'Competition'`).Scan(&programmeID); err != nil {
		t.Fatal(err)
	}
	seasonID := uuid.New()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if _, err := tx.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época dos álbuns', $3, $4)`, seasonID, "Album"+uuid.NewString()[:8], today.AddDate(0, -1, 0), today.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	teamID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO teams (id, season_id, programme_id, code, name) VALUES ($1, $2, $3, $4, 'Equipa de integração')`, teamID, seasonID, programmeID, "Album"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	for _, membership := range []struct {
		userID uuid.UUID
		teamID *uuid.UUID
	}{{memberID, nil}, {dependentID, &teamID}} {
		if _, err := tx.Exec(ctx, `INSERT INTO user_memberships (user_id, season_id, programme_id, team_id, starts_on) VALUES ($1, $2, $3, $4, $5)`, membership.userID, seasonID, programmeID, membership.teamID, today.AddDate(0, 0, -1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO staff_grants (user_id, capability, programme_id, granted_by_id) VALUES ($1, 'COACH', $2, $3)`, coachID, programmeID, creatorID); err != nil {
		t.Fatal(err)
	}

	assertAudienceRequired := func() {
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer savepoint.Rollback(ctx)
		if _, err := savepoint.Exec(ctx, `INSERT INTO photo_albums (title, created_by_id) VALUES ('Sem audiência', $1)`, creatorID); err != nil {
			t.Fatal(err)
		}
		if _, err := savepoint.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err == nil {
			t.Fatal("album without an audience passed deferred constraints")
		}
	}
	assertAudienceRequired()

	album, err := queries.CreatePhotoAlbum(ctx, dbgen.CreatePhotoAlbumParams{Title: "Regata privada", Description: "Fotografias da equipa.", CreatedByID: creatorID})
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.AddPhotoAlbumProgrammeAudience(ctx, dbgen.AddPhotoAlbumProgrammeAudienceParams{AlbumID: album.ID, ProgrammeID: programmeID}); err != nil {
		t.Fatal(err)
	}
	if err := queries.AddPhotoAlbumTeamAudience(ctx, dbgen.AddPhotoAlbumTeamAudienceParams{AlbumID: album.ID, TeamID: teamID}); err != nil {
		t.Fatal(err)
	}

	for name, userID := range map[string]uuid.UUID{"member": memberID, "guardian": guardianID, "coach": coachID} {
		t.Run(name+" sees open album", func(t *testing.T) {
			rows, err := queries.ListVisiblePhotoAlbums(ctx, dbgen.ListVisiblePhotoAlbumsParams{UserID: userID})
			if err != nil || len(rows) != 1 || rows[0].ID != album.ID {
				t.Fatalf("rows = %#v, err = %v", rows, err)
			}
		})
	}
	rows, err := queries.ListVisiblePhotoAlbums(ctx, dbgen.ListVisiblePhotoAlbumsParams{UserID: unrelatedID})
	if err != nil || len(rows) != 0 {
		t.Fatalf("unrelated rows = %#v, err = %v", rows, err)
	}
	if _, err := queries.GetVisiblePhotoAlbum(ctx, dbgen.GetVisiblePhotoAlbumParams{ID: album.ID, UserID: unrelatedID}); err != pgx.ErrNoRows {
		t.Fatalf("unrelated detail error = %v", err)
	}

	archivedAt := time.Now().UTC().Truncate(time.Microsecond)
	archived, err := queries.ArchivePhotoAlbum(ctx, dbgen.ArchivePhotoAlbumParams{ArchivedByID: &creatorID, ArchivedAt: pgtype.Timestamptz{Time: archivedAt, Valid: true}, ID: album.ID, ExpectedUpdatedAt: album.UpdatedAt})
	if err != nil || archived.Status != dbgen.PhotoAlbumStatusARCHIVED {
		t.Fatalf("archived = %#v, err = %v", archived, err)
	}
	rows, err = queries.ListVisiblePhotoAlbums(ctx, dbgen.ListVisiblePhotoAlbumsParams{UserID: memberID})
	if err != nil || len(rows) != 0 {
		t.Fatalf("member archived rows = %#v, err = %v", rows, err)
	}
	rows, err = queries.ListVisiblePhotoAlbums(ctx, dbgen.ListVisiblePhotoAlbumsParams{UserID: creatorID, Privileged: true})
	if err != nil || len(rows) != 1 || rows[0].Status != dbgen.PhotoAlbumStatusARCHIVED {
		t.Fatalf("privileged archived rows = %#v, err = %v", rows, err)
	}

	audit, err := queries.ListPhotoAlbumAuditEvents(ctx, album.ID)
	if err != nil || len(audit) != 2 || audit[0].Action != "CREATED" || audit[1].Action != "ARCHIVED" {
		t.Fatalf("audit = %#v, err = %v", audit, err)
	}
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := savepoint.Exec(ctx, `UPDATE photo_album_audit_events SET action = 'CREATED' WHERE id = $1`, audit[1].ID); err == nil {
		t.Fatal("photo album audit event was mutable")
	}
	_ = savepoint.Rollback(ctx)
}
