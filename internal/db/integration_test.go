//go:build integration

package db

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestScheduleMaintenanceTaskUpdatesOnlyDueEquipment(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	queries := dbgen.New(pool)
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name          string
		scheduledFor  time.Time
		wantEquipment string
	}{
		{name: "due", scheduledFor: now.Add(-time.Minute), wantEquipment: "Maintenance"},
		{name: "future", scheduledFor: now.Add(time.Hour), wantEquipment: "Operational"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			equipmentID := uuid.New()
			assetTag := "IT-" + uuid.NewString()[:8]
			if _, err := pool.Exec(ctx, `INSERT INTO equipment (id, asset_tag, name, type) VALUES ($1, $2, 'Barco de teste', 'Boat')`, equipmentID, assetTag); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM maintenance_tasks WHERE equipment_id = $1`, equipmentID)
				_, _ = pool.Exec(context.Background(), `DELETE FROM equipment WHERE id = $1`, equipmentID)
			})

			task, err := queries.ScheduleMaintenanceTask(ctx, dbgen.ScheduleMaintenanceTaskParams{
				EquipmentID:  equipmentID,
				ScheduledFor: pgtype.Timestamptz{Time: tc.scheduledFor, Valid: true},
				Description:  "Manutenção de integração",
			})
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != "Scheduled" {
				t.Fatalf("task status = %q", task.Status)
			}
			equipment, err := queries.GetEquipmentByID(ctx, equipmentID)
			if err != nil {
				t.Fatal(err)
			}
			if equipment.Status != tc.wantEquipment {
				t.Fatalf("equipment status = %q, want %q", equipment.Status, tc.wantEquipment)
			}
		})
	}
}

func TestCreateRepairRequestEnforcesIdempotencyKeyDuringConcurrentRetries(t *testing.T) {
	ctx := context.Background()
	pool, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(ctx) })

	userID, equipmentID, key := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, role, squad_category, date_of_birth) VALUES ($1, 'Utilizador de teste', $2, 'hash', 'Competitor', 'Iniciante', '1990-01-01')`, userID, "repair-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO equipment (id, asset_tag, name, type) VALUES ($1, $2, 'Barco de teste', 'Boat')`, equipmentID, "IT-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM repair_requests WHERE idempotency_key = $1`, key)
		_, _ = pool.Exec(context.Background(), `DELETE FROM equipment WHERE id = $1`, equipmentID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})

	queries := dbgen.New(pool)
	input := dbgen.CreateRepairRequestParams{
		IdempotencyKey:   key,
		EquipmentID:      equipmentID,
		ReportedByID:     &userID,
		IssueDescription: "Avaria criada por um teste de integração.",
	}
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := queries.CreateRepairRequest(ctx, input)
			results <- err
		}()
	}
	group.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent inserts = %d, want 1", successes)
	}

	repair, err := queries.GetRepairByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if repair.EquipmentID != equipmentID || repair.ReportedByID == nil || *repair.ReportedByID != userID || repair.IssueDescription != input.IssueDescription {
		t.Fatalf("persisted repair = %#v", repair)
	}
}
