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
