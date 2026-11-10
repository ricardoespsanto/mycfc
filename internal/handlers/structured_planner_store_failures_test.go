package handlers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type plannerFailureTx struct {
	structuredTrainingSuccessTransactionFake
	faultName string
	lockKind  string
	err       error
	row       map[string]pgx.Row
	rows      map[string]pgx.Rows
	faultSeen bool
	rolled    bool
	restored  bool
}

func plannerQueryNamed(sql, name string) bool {
	return strings.HasPrefix(sql, "-- name: "+name+" ")
}

func (tx *plannerFailureTx) rejects(sql string, args []any) bool {
	if !plannerQueryNamed(sql, tx.faultName) || tx.lockKind != "" && (len(args) < 2 || args[1] != tx.lockKind) {
		return false
	}
	tx.faultSeen = true
	return true
}

func (tx *plannerFailureTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx.rejects(sql, args) {
		return pgconn.CommandTag{}, tx.err
	}
	return tx.structuredTrainingSuccessTransactionFake.Exec(ctx, sql, args...)
}

func (tx *plannerFailureTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx.rejects(sql, args) {
		return nil, tx.err
	}
	for name, rows := range tx.rows {
		if plannerQueryNamed(sql, name) {
			return rows, nil
		}
	}
	return tx.structuredTrainingSuccessTransactionFake.Query(ctx, sql, args...)
}

func (tx *plannerFailureTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx.rejects(sql, args) {
		return structuredTrainingRowFake{err: tx.err}
	}
	for name, row := range tx.row {
		if plannerQueryNamed(sql, name) {
			return row
		}
	}
	tx.restored = tx.restored || plannerQueryNamed(sql, "RestoreTrainingSession") || plannerQueryNamed(sql, "RestoreTrainingBlock")
	return tx.structuredTrainingSuccessTransactionFake.QueryRow(ctx, sql, args...)
}

func (tx *plannerFailureTx) Rollback(context.Context) error {
	tx.rolled = true
	return nil
}

type plannerStoreFixture struct{ *plannerFailureTx }

func (f plannerStoreFixture) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return f.plannerFailureTx, nil
}

type plannerResultRow[T any] struct{ value T }

func (row plannerResultRow[T]) Scan(dest ...any) error {
	value := reflect.ValueOf(row.value)
	for i, target := range dest {
		reflect.ValueOf(target).Elem().Set(value.Field(i))
	}
	return nil
}

type plannerResultRows[T any] struct {
	structuredTrainingEmptyRowsFake
	items []T
	at    int
}

func (rows *plannerResultRows[T]) Next() bool {
	rows.at++
	return rows.at <= len(rows.items)
}

func (rows *plannerResultRows[T]) Scan(dest ...any) error {
	return (plannerResultRow[T]{value: rows.items[rows.at-1]}).Scan(dest...)
}

func TestStructuredPlannerStoreAbortsOnLostCopyInputs(t *testing.T) {
	ctx := t.Context()
	actor, id, target := uuid.New(), uuid.New(), uuid.New()
	day := time.Date(2026, 3, 23, 0, 0, 0, 0, time.UTC)
	failed := errors.New("planner query unavailable")
	block := func(s PostgresStructuredTrainingStore) error {
		_, err := s.CopyTrainingBlock(ctx, id, target, actor)
		return err
	}
	session := func(s PostgresStructuredTrainingStore) error {
		_, err := s.CopyTrainingSession(ctx, id, target, pgtype.Timestamptz{Time: day, Valid: true}, actor)
		return err
	}
	dayCopy := func(s PostgresStructuredTrainingStore) error {
		count, err := s.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{SourcePlanID: id, TargetPlanID: target, SourceDate: day, TargetDate: day, ActorID: actor})
		if count != 0 {
			t.Fatal("aborted copy reported saved sessions")
		}
		return err
	}
	weekCopy := func(s PostgresStructuredTrainingStore) error {
		_, err := s.CopyStructuredTrainingWeek(ctx, StructuredWeekCopyInput{SourcePlanID: id, WeekStart: day, ActorID: actor, Title: "Disposable review"})
		return err
	}
	routine := func(kind dbgen.TrainingRoutineKind) func(PostgresStructuredTrainingStore) error {
		return func(s PostgresStructuredTrainingStore) error {
			_, err := s.InsertTrainingRoutine(ctx, StructuredRoutineInsertInput{
				Routine:  dbgen.TrainingRoutine{Kind: kind, ID: id, Snapshot: []byte("{}")},
				TargetID: target, ActorID: actor, StartsAt: pgtype.Timestamptz{Time: day, Valid: true},
			})
			return err
		}
	}
	for _, tc := range []struct {
		name, query, lockKind string
		run                   func(PostgresStructuredTrainingStore) error
		routine               dbgen.TrainingRoutineKind
	}{
		{"block source plan guard", "TrainingScheduleGuardPlanV1", "", block, ""},
		{"block destination lock", "TrainingScheduleLockTargetV1", "SEGMENT", block, ""},
		{"block source lock", "TrainingScheduleLockTargetV1", "BLOCK", block, ""},
		{"block snapshot", "GetBlockRoutineSource", "", block, ""},
		{"block restoration", "RestoreTrainingBlock", "", block, ""},
		{"session source lock", "TrainingScheduleLockTargetV1", "SESSION", session, ""},
		{"session snapshot", "GetSessionRoutineSource", "", session, ""},
		{"day snapshots", "ListStructuredSessionSnapshotsForDay", "", dayCopy, ""},
		{"week snapshots", "ListStructuredSessionSnapshotsForPlan", "", weekCopy, ""},
		{"routine block target", "TrainingScheduleLockTargetV1", "SEGMENT", routine(dbgen.TrainingRoutineKindBLOCK), dbgen.TrainingRoutineKindBLOCK},
		{"routine segment target", "TrainingScheduleLockTargetV1", "SESSION", routine(dbgen.TrainingRoutineKindSEGMENT), dbgen.TrainingRoutineKindSEGMENT},
		{"routine session target", "TrainingScheduleGuardPlanV1", "", routine(dbgen.TrainingRoutineKindSESSION), dbgen.TrainingRoutineKindSESSION},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &plannerFailureTx{faultName: tc.query, lockKind: tc.lockKind, err: failed}
			if tc.routine != "" {
				tx.row = map[string]pgx.Row{"GetVisibleTrainingRoutine": plannerResultRow[dbgen.TrainingRoutine]{value: dbgen.TrainingRoutine{ID: id, Kind: tc.routine, Snapshot: []byte("{}")}}}
			}
			err := tc.run(PostgresStructuredTrainingStore{Pool: plannerStoreFixture{tx}})
			if !errors.Is(err, failed) || !tx.faultSeen || tx.committed || !tx.rolled || tx.restored {
				t.Fatalf("err=%v fault=%t committed=%t rollback=%t restored=%t", err, tx.faultSeen, tx.committed, tx.rolled, tx.restored)
			}
		})
	}
}

func TestStructuredPlannerStoreRejectsIncompleteAndAmbiguousCopyTimes(t *testing.T) {
	ctx := t.Context()
	actor, plan, group, target := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	// Sunday preceding the Lisbon spring transition. In the destination week
	// 01:30 does not exist, while a 00:30–02:30 interval changes endpoint offset.
	fromMonday := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	toMonday := fromMonday.AddDate(0, 0, 7)
	fromSunday := fromMonday.AddDate(0, 0, 6)
	toSunday := toMonday.AddDate(0, 0, 6)
	stamp := func(value time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: value, Valid: true} }
	date := func(value time.Time) pgtype.Date { return pgtype.Date{Time: value, Valid: true} }
	for _, tc := range []struct {
		name   string
		start  pgtype.Timestamptz
		end    pgtype.Timestamptz
		raw    []byte
		expect error
	}{
		{"unknown snapshot version", stamp(fromSunday), stamp(fromSunday.Add(time.Hour)), []byte(`{"schema_version":2}`), ErrTrainingScheduleWriteUnsupported},
		{"nonexistent destination start", stamp(fromSunday.Add(90 * time.Minute)), stamp(fromSunday.Add(3 * time.Hour)), []byte("{}"), ErrTrainingScheduleInvalid},
		{"missing end", stamp(fromSunday.Add(30 * time.Minute)), pgtype.Timestamptz{}, []byte("{}"), ErrTrainingScheduleInvalid},
		{"changed end offset", stamp(fromSunday.Add(30 * time.Minute)), stamp(fromSunday.Add(150 * time.Minute)), []byte("{}"), ErrTrainingScheduleWriteUnsupported},
	} {
		for _, kind := range []string{"day", "week", "cycle"} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				record := dbgen.ListStructuredSessionSnapshotsForPlanRow{ID: uuid.New(), StartsAt: tc.start, EndsAt: tc.end, Snapshot: tc.raw}
				tx := &plannerFailureTx{
					row:  map[string]pgx.Row{"GetStructuredPlanCopySource": plannerResultRow[dbgen.GetStructuredPlanCopySourceRow]{value: dbgen.GetStructuredPlanCopySourceRow{TrainingGroupID: &group, WeekStart: date(fromMonday)}}},
					rows: map[string]pgx.Rows{"ListStructuredSessionSnapshotsForDay": &plannerResultRows[dbgen.ListStructuredSessionSnapshotsForPlanRow]{items: []dbgen.ListStructuredSessionSnapshotsForPlanRow{record}}, "ListStructuredSessionSnapshotsForPlan": &plannerResultRows[dbgen.ListStructuredSessionSnapshotsForPlanRow]{items: []dbgen.ListStructuredSessionSnapshotsForPlanRow{record}}},
				}
				store := PostgresStructuredTrainingStore{Pool: plannerStoreFixture{tx}}
				var err error
				switch kind {
				case "day":
					var copied int
					copied, err = store.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{SourcePlanID: plan, TargetPlanID: target, SourceDate: fromSunday, TargetDate: toSunday, ActorID: actor})
					if copied != 0 {
						t.Fatal("aborted day copy reported saved sessions")
					}
				case "week":
					_, err = store.CopyStructuredTrainingWeek(ctx, StructuredWeekCopyInput{SourcePlanID: plan, WeekStart: toMonday, ActorID: actor, Title: "Disposable week"})
				case "cycle":
					fake := &structuredTrainingCycleQueriesFake{
						copySource:   dbgen.TrainingCycle{ID: uuid.New(), TrainingGroupID: group},
						copyWeeks:    []dbgen.ListTrainingCycleWeekCopySourcesRow{{ID: plan, TrainingGroupID: &group, WeekStart: date(fromMonday)}},
						snapshots:    map[uuid.UUID][]dbgen.ListStructuredSessionSnapshotsForPlanRow{plan: {record}},
						createdPlans: []dbgen.TrainingPlan{{ID: target, TrainingGroupID: &group}},
					}
					store.beginCycleTx = func(context.Context) (structuredTrainingCycleTx, structuredTrainingCycleQueries, error) {
						return tx, fake, nil
					}
					_, err = store.CopyStructuredTrainingCycle(ctx, StructuredTrainingCycleCopyInput{SourceCycleID: fake.copySource.ID, FirstMonday: toMonday, ActorID: actor})
					if len(fake.restored) != 0 || len(fake.copyEvents) != 0 {
						t.Fatal("aborted cycle attempted restoration or wrote provenance")
					}
				}
				if !errors.Is(err, tc.expect) || tx.committed || !tx.rolled || tx.restored {
					t.Fatalf("err=%v want=%v committed=%t rollback=%t restored=%t", err, tc.expect, tx.committed, tx.rolled, tx.restored)
				}
			})
		}
	}
}
