//go:build integration

package db

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
)

func TestSportingOriginalVersionRejectsCommittedAndConcurrentCorrections(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	admin, member := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, member} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Guard',$2,'hash','1980-01-01')`, id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	svc := SportAssignmentService{Pool: pool}
	version := func() string {
		v, e := SportingClassificationVersion(ctx, pool, member)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	change := SportAssignmentCorrection{ActorID: admin, MemberID: member, Kind: SportingModalityAssignment, Codes: []string{"CANOEING"}, Reason: "Test"}
	change.ExpectedVersion = version()
	if err := svc.ReplaceForClassification(ctx, change); err != nil {
		t.Fatal(err)
	}
	stale := version()
	change.ExpectedVersion = version()
	change.Kind = CanoeCraftAssignment
	change.Codes = []string{"K2"}
	if err := svc.ReplaceForClassification(ctx, change); err != nil {
		t.Fatal(err)
	}
	before := version()
	change.Kind = SportingModalityAssignment
	change.Codes = []string{"SUP"}
	change.ExpectedVersion = stale
	if err := svc.ReplaceForClassification(ctx, change); !errors.Is(err, ErrClassificationStale) {
		t.Fatalf("stale cascade: %v", err)
	}
	if version() != before {
		t.Fatal("stale correction mutated set or audit")
	}
	// A reversed set is still stale because immutable audit identity is covered.
	change.ExpectedVersion = before
	change.Kind = CanoeCraftAssignment
	change.Codes = nil
	if err := svc.ReplaceForClassification(ctx, change); err != nil {
		t.Fatal(err)
	}
	change.ExpectedVersion = version()
	change.Codes = []string{"K2"}
	if err := svc.ReplaceForClassification(ctx, change); err != nil {
		t.Fatal(err)
	}
	change.ExpectedVersion = before
	change.Codes = []string{"K4"}
	if err := svc.ReplaceForClassification(ctx, change); !errors.Is(err, ErrClassificationStale) {
		t.Fatalf("stale craft: %v", err)
	}
	staleCraft := version()
	change.Kind = SportingModalityAssignment
	change.Codes = []string{"SUP"}
	change.ExpectedVersion = staleCraft
	if err := svc.ReplaceForClassification(ctx, change); err != nil {
		t.Fatal(err)
	}
	committed := version()
	change.Kind = CanoeCraftAssignment
	change.Codes = []string{"C2"}
	change.ExpectedVersion = staleCraft
	if err := svc.ReplaceForClassification(ctx, change); !errors.Is(err, ErrClassificationStale) {
		t.Fatalf("sport-first stale craft: %v", err)
	}
	if version() != committed {
		t.Fatal("stale craft mutated audit or set")
	}
	change.Kind = SportingModalityAssignment
	change.Codes = []string{"CANOEING"}
	change.ExpectedVersion = version()
	if err := svc.ReplaceForClassification(ctx, change); err != nil {
		t.Fatal(err)
	}
	// Both requests originate from the same snapshot. Exactly one can commit,
	// regardless of which transaction reaches the subject lock first.
	for _, reverse := range []bool{false, true} {
		clear := SportAssignmentCorrection{ActorID: admin, MemberID: member, Kind: CanoeCraftAssignment, Reason: "Reset crafts"}
		if err := svc.Replace(ctx, clear); err != nil {
			t.Fatal(err)
		}
		original := version()
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		a := SportAssignmentCorrection{ActorID: admin, MemberID: member, Kind: SportingModalityAssignment, Codes: []string{"SUP"}, Reason: "Concurrent sport", ExpectedVersion: original}
		b := SportAssignmentCorrection{ActorID: admin, MemberID: member, Kind: CanoeCraftAssignment, Codes: []string{"C2"}, Reason: "Concurrent craft", ExpectedVersion: original}
		if reverse {
			a, b = b, a
		}
		for _, in := range []SportAssignmentCorrection{a, b} {
			wg.Add(1)
			go func(in SportAssignmentCorrection) {
				defer wg.Done()
				<-start
				results <- svc.ReplaceForClassification(ctx, in)
			}(in)
		}
		close(start)
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			} else if !errors.Is(err, ErrClassificationStale) && !sqlState(err, "40001") {
				t.Fatalf("unexpected concurrent failure: %v", err)
			}
		}
		if success != 1 {
			t.Fatalf("concurrent commits=%d", success)
		}
		reset := SportAssignmentCorrection{ActorID: admin, MemberID: member, Kind: SportingModalityAssignment, Codes: []string{"CANOEING"}, Reason: "Reset"}
		if err := svc.Replace(ctx, reset); err != nil {
			t.Fatal(err)
		}
	}
}
