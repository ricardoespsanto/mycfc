package handlers

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFocusedWorkoutLoadRefusesInvalidCivilDayAndUnavailablePool(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://fixture@127.0.0.1:1/fixture?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	store := PostgresFocusedWorkoutStore{Pool: pool}
	input := FocusedWorkoutRequest{ActorID: uuid.New(), PlanID: uuid.New(), Day: "2026-02-30"}
	out, err := store.LoadFocusedWorkout(t.Context(), input)
	if !errors.Is(err, pgx.ErrNoRows) || out.PlanID != uuid.Nil {
		t.Fatalf("invalid day reached the pool: %v", err)
	}
	input.Day = "2026-02-28"
	out, err = store.LoadFocusedWorkout(t.Context(), input)
	if err == nil || errors.Is(err, pgx.ErrNoRows) || out.PlanID != uuid.Nil {
		t.Fatalf("unavailable pool returned ambiguous scope or data: %v", err)
	}
}
