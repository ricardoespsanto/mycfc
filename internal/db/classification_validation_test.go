package db

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
	"time"
)

func TestDatedAndSportingServicesRejectInvalidInputsAndClosedPool(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://synthetic@localhost/mycfc")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	valid := OrdinaryDatedAssignment{ActorID: uuid.New(), MemberID: uuid.New(), SeasonID: uuid.New(), ProgrammeID: uuid.New(), StartsOn: time.Now()}
	if _, err := (DatedParticipationService{Pool: pool}).CreateOrdinaryAssignment(t.Context(), valid); err == nil {
		t.Fatal("closed database accepted assignment")
	}
	if _, err := (DatedParticipationService{}).CreateOrdinaryAssignment(t.Context(), valid); !errors.Is(err, ErrDatedParticipationWindow) {
		t.Fatal(err)
	}
	if err := (SportAssignmentService{}).Replace(t.Context(), SportAssignmentCorrection{Kind: SportingModalityAssignment, ActorID: uuid.New(), MemberID: uuid.New(), Codes: []string{"SUP"}, Reason: "Correction"}); !errors.Is(err, ErrSportAssignmentInvalid) {
		t.Fatalf("nil pool accepted: %v", err)
	}
	for _, in := range []SportAssignmentCorrection{
		{Kind: "UNKNOWN"},
		{Kind: SportingModalityAssignment, ActorID: uuid.New(), MemberID: uuid.New(), Codes: []string{"SUP", "SUP"}, Reason: "Correction"},
		{Kind: CanoeCraftAssignment, ActorID: uuid.New(), MemberID: uuid.New(), Codes: []string{"BAD"}, Reason: "Correction"},
	} {
		if err := (SportAssignmentService{Pool: pool}).Replace(t.Context(), in); !errors.Is(err, ErrSportAssignmentInvalid) {
			t.Fatalf("invalid correction=%v", err)
		}
	}
	if err := (SportAssignmentService{Pool: pool}).Replace(t.Context(), SportAssignmentCorrection{Kind: SportingModalityAssignment, ActorID: uuid.New(), MemberID: uuid.New(), Codes: []string{"SUP"}, Reason: "Correction"}); err == nil {
		t.Fatal("closed database accepted correction")
	}
}
