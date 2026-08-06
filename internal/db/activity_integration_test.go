//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestActivityIntegrationPersistsIdempotentlyAndOwnsMatches(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	queries := dbgen.New(tx)

	athleteID, otherUserID := uuid.New(), uuid.New()
	for id, email := range map[uuid.UUID]string{
		athleteID:   "activity-" + uuid.NewString() + "@example.test",
		otherUserID: "activity-" + uuid.NewString() + "@example.test",
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Atleta integração', $2, 'hash', '1990-01-01')`, id, email); err != nil {
			t.Fatal(err)
		}
	}

	keyID := "activity-key-v1"
	connection, err := queries.UpsertActivityConnection(ctx, dbgen.UpsertActivityConnectionParams{
		UserID: athleteID, Provider: "strava", ProviderUserID: "athlete-123",
		CredentialsCiphertext: []byte("sealed-credential-envelope"), CredentialKeyID: &keyID,
		Scopes: []string{"activity:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.Status != "ACTIVE" || string(connection.CredentialsCiphertext) != "sealed-credential-envelope" {
		t.Fatalf("connection = %#v", connection)
	}
	otherConnection, err := queries.UpsertActivityConnection(ctx, dbgen.UpsertActivityConnectionParams{
		UserID: otherUserID, Provider: "strava", ProviderUserID: "athlete-456",
		CredentialsCiphertext: []byte("other-sealed-envelope"), CredentialKeyID: &keyID,
		Scopes: []string{"activity:read"},
	})
	if err != nil {
		t.Fatal(err)
	}

	idempotencyKey := uuid.New()
	job, err := queries.CreateActivitySyncJob(ctx, dbgen.CreateActivitySyncJobParams{
		IdempotencyKey: idempotencyKey, ConnectionID: connection.ID, Reason: "LOGIN",
	})
	if err != nil {
		t.Fatal(err)
	}
	repeatedJob, err := queries.CreateActivitySyncJob(ctx, dbgen.CreateActivitySyncJobParams{
		IdempotencyKey: idempotencyKey, ConnectionID: connection.ID, Reason: "LOGIN",
	})
	if err != nil || repeatedJob.ID != job.ID {
		t.Fatalf("repeated job = %#v, err = %v", repeatedJob, err)
	}
	if _, err := queries.CreateActivitySyncJob(ctx, dbgen.CreateActivitySyncJobParams{
		IdempotencyKey: idempotencyKey, ConnectionID: otherConnection.ID, Reason: "LOGIN",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-connection idempotency error = %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	claimed, err := queries.ClaimNextActivitySyncJob(ctx, pgtype.Timestamptz{Time: now, Valid: true})
	if err != nil || claimed.ID != job.ID || claimed.Status != "RUNNING" || claimed.Attempts != 1 {
		t.Fatalf("claimed job = %#v, err = %v", claimed, err)
	}
	checkpoint := "page-2"
	completed, err := queries.CompleteActivitySyncJob(ctx, dbgen.CompleteActivitySyncJobParams{
		Checkpoint: &checkpoint, FinishedAt: pgtype.Timestamptz{Time: now.Add(time.Second), Valid: true}, ID: job.ID,
	})
	if err != nil || completed.Status != "SUCCEEDED" || completed.Checkpoint == nil || *completed.Checkpoint != checkpoint {
		t.Fatalf("completed job = %#v, err = %v", completed, err)
	}

	distance := 5000.25
	average, maximum := int16(145), int16(181)
	activityParams := dbgen.UpsertSyncedActivityParams{
		ConnectionID: connection.ID, UserID: athleteID, Provider: "strava", ProviderActivityID: "activity-789",
		StartsAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: now, Valid: true},
		Sport: "Kayaking", NormalizedSport: "paddling", DurationSeconds: 3600,
		DistanceMetres: &distance, AverageHeartRate: &average, MaximumHeartRate: &maximum,
		ProviderMetrics: []byte(`{"relative_effort":42}`), RawSummary: []byte(`{"private":true}`),
		PayloadSha256: make([]byte, 32), NormalizationVersion: 1,
	}
	activity, err := queries.UpsertSyncedActivity(ctx, activityParams)
	if err != nil {
		t.Fatal(err)
	}
	updatedDistance := 5100.75
	activityParams.DistanceMetres = &updatedDistance
	repeatedActivity, err := queries.UpsertSyncedActivity(ctx, activityParams)
	if err != nil || repeatedActivity.ID != activity.ID || repeatedActivity.DistanceMetres == nil || *repeatedActivity.DistanceMetres != updatedDistance {
		t.Fatalf("repeated activity = %#v, err = %v", repeatedActivity, err)
	}
	activityParams.ConnectionID = otherConnection.ID
	activityParams.UserID = otherUserID
	if _, err := queries.UpsertSyncedActivity(ctx, activityParams); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-user activity collision error = %v", err)
	}

	var programmeID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM programmes WHERE code = 'Competition'`).Scan(&programmeID); err != nil {
		t.Fatal(err)
	}
	planID, sessionID := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO training_plans (id, title, programme_id, created_by_id) VALUES ($1, 'Plano atividade', $2, $3)`, planID, programmeID, athleteID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO training_sessions (id, plan_id, title, starts_at, ends_at, created_by_id) VALUES ($1, $2, 'Sessão atividade', $3, $4, $5)`, sessionID, planID, now.Add(-time.Hour), now, athleteID); err != nil {
		t.Fatal(err)
	}
	match, err := queries.UpsertSuggestedActivityMatch(ctx, dbgen.UpsertSuggestedActivityMatchParams{
		SessionID: sessionID, ActivityID: activity.ID, UserID: athleteID, Confidence: 92,
		MatchBasis: []byte(`{"time_overlap":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := queries.DecideActivityMatch(ctx, dbgen.DecideActivityMatchParams{
		Status: "CONFIRMED", ActorUserID: &athleteID, DecidedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ID: match.ID, UserID: athleteID,
	})
	if err != nil || confirmed.Status != "CONFIRMED" {
		t.Fatalf("confirmed match = %#v, err = %v", confirmed, err)
	}
	if _, err := queries.UpsertSuggestedActivityMatch(ctx, dbgen.UpsertSuggestedActivityMatchParams{
		SessionID: sessionID, ActivityID: activity.ID, UserID: athleteID, Confidence: 99, MatchBasis: []byte(`{}`),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("confirmed match was overwritten: %v", err)
	}
	pendingJob, err := queries.CreateActivitySyncJob(ctx, dbgen.CreateActivitySyncJobParams{
		IdempotencyKey: uuid.New(), ConnectionID: connection.ID, Reason: "MANUAL",
	})
	if err != nil {
		t.Fatal(err)
	}

	disconnected, err := queries.DisconnectActivityConnection(ctx, dbgen.DisconnectActivityConnectionParams{
		DisconnectedAt: pgtype.Timestamptz{Time: now, Valid: true}, ID: connection.ID, UserID: athleteID,
	})
	if err != nil || disconnected.Status != "DISCONNECTED" || len(disconnected.CredentialsCiphertext) != 0 || disconnected.CredentialKeyID != nil {
		t.Fatalf("disconnected connection = %#v, err = %v", disconnected, err)
	}
	cancelledJob, err := queries.GetActivitySyncJob(ctx, pendingJob.ID)
	if err != nil || cancelledJob.Status != "CANCELLED" || !cancelledJob.FinishedAt.Valid {
		t.Fatalf("cancelled job = %#v, err = %v", cancelledJob, err)
	}
}

func TestActivityMigrationPreservesExistingRecords(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	userID, logID := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Preservada', $2, 'hash', '1990-01-01')`, userID, "preserved-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO training_logs (id, user_id, occurred_at, duration_seconds, distance_metres, notes) VALUES ($1, $2, now(), 3600, 8000, 'não alterar')`, logID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DROP TABLE training_session_activity_matches, synced_activities, activity_sync_jobs, activity_connections`); err != nil {
		t.Fatal(err)
	}
	migrationSQL, err := migrationFiles.ReadFile("migrations/202608060001_activity_integration_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(migrationSQL)); err != nil {
		t.Fatal(err)
	}

	var name, notes string
	var distance int
	if err := tx.QueryRow(ctx, `SELECT u.name, l.notes, l.distance_metres FROM users u JOIN training_logs l ON l.user_id = u.id WHERE u.id = $1 AND l.id = $2`, userID, logID).Scan(&name, &notes, &distance); err != nil {
		t.Fatal(err)
	}
	if name != "Preservada" || notes != "não alterar" || distance != 8000 {
		t.Fatalf("existing record changed: name=%q notes=%q distance=%d", name, notes, distance)
	}
	for _, table := range []string{"activity_connections", "activity_sync_jobs", "synced_activities", "training_session_activity_matches"} {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
	}
}
