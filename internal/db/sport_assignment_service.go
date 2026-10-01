package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrSportAssignmentForbidden = errors.New("sport assignment requires an active administrator or a current matching coach grant")
	ErrSportAssignmentInvalid   = errors.New("invalid sport assignment correction")
)

type SportAssignmentKind string

const (
	SportingModalityAssignment SportAssignmentKind = "SPORT"
	CanoeCraftAssignment       SportAssignmentKind = "CRAFT"
)

// SportAssignmentCorrection specifies the complete desired set, not an additive patch. Empty
// removes all selections. Reason and actor are persisted with every change.
type SportAssignmentCorrection struct {
	ActorID         uuid.UUID
	MemberID        uuid.UUID
	Kind            SportAssignmentKind
	Codes           []string
	Reason          string
	ExpectedVersion string
}

type SportAssignmentService struct{ Pool *pgxpool.Pool }

// Replace serializes corrections per subject and checks current scope inside the
// same transaction. Existing additive sqlc calls remain compatibility-only; new
// staff correction surfaces must use this service, never direct table mutations.
func (s SportAssignmentService) Replace(ctx context.Context, in SportAssignmentCorrection) error {
	return s.replace(ctx, in, false)
}

// ReplaceForClassification keeps the restricted classification task's current
// programme anchor and registered-subject policy inside the write transaction.
// It does not change the existing generic sport correction service's callers.
func (s SportAssignmentService) ReplaceForClassification(ctx context.Context, in SportAssignmentCorrection) error {
	return s.replace(ctx, in, true)
}

func (s SportAssignmentService) replace(ctx context.Context, in SportAssignmentCorrection, classification bool) error {
	allowed := map[string]bool{}
	switch in.Kind {
	case SportingModalityAssignment:
		for _, v := range []string{"CANOEING", "KAYAK_POLO", "SUP"} {
			allowed[v] = true
		}
	case CanoeCraftAssignment:
		for _, v := range []string{"K1", "K2", "K4", "C1", "C2", "C4"} {
			allowed[v] = true
		}
	default:
		return ErrSportAssignmentInvalid
	}
	reason := strings.TrimSpace(in.Reason)
	if s.Pool == nil || in.ActorID == uuid.Nil || in.MemberID == uuid.Nil || len(reason) == 0 || len(reason) > 500 || len(in.Codes) > len(allowed) {
		return ErrSportAssignmentInvalid
	}
	desired := map[string]bool{}
	for _, code := range in.Codes {
		if !allowed[code] || desired[code] {
			return ErrSportAssignmentInvalid
		}
		desired[code] = true
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Lock the actor and subject before checking authority; disabling either
	// account cannot race the correction commit unnoticed.
	var active bool
	if err = tx.QueryRow(ctx, `SELECT is_active AND erased_at IS NULL FROM users WHERE id=$1 FOR SHARE`, in.ActorID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSportAssignmentForbidden
		}
		return err
	}
	if !active {
		return ErrSportAssignmentForbidden
	}
	subjectPredicate := `member.is_active AND member.erased_at IS NULL`
	if classification {
		subjectPredicate = ClassificationSubjectEligibilitySQL
	}
	if err = tx.QueryRow(ctx, `SELECT `+subjectPredicate+` FROM users member WHERE member.id=$1 FOR UPDATE`, in.MemberID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSportAssignmentForbidden
		}
		return err
	}
	if !active {
		return ErrSportAssignmentForbidden
	}
	var admin bool
	if err = tx.QueryRow(ctx, `SELECT guardian_authority_is_administrator($1)`, in.ActorID).Scan(&admin); err != nil {
		return err
	}
	var grantID *uuid.UUID
	if !admin {
		// No unscoped COACH authority: a concrete programme or team must match a
		// current participation interval on the Lisbon calendar date.
		var id uuid.UUID
		err = tx.QueryRow(ctx, `SELECT g.id FROM staff_grants g
    JOIN user_memberships m ON m.user_id=$2
      AND m.starts_on<=(now() AT TIME ZONE 'Europe/Lisbon')::date
      AND (m.ends_on IS NULL OR m.ends_on >= (now() AT TIME ZONE 'Europe/Lisbon')::date)
      AND ((g.programme_id IS NOT NULL AND g.programme_id=m.programme_id)
       OR (g.team_id IS NOT NULL AND g.team_id=m.team_id))
    WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL
      AND (NOT $3::boolean OR (g.programme_id IS NOT NULL AND g.programme_id=m.programme_id
        AND EXISTS(SELECT 1 FROM seasons season WHERE season.id=m.season_id AND season.is_current)))
    ORDER BY g.id LIMIT 1 FOR SHARE OF g`, in.ActorID, in.MemberID, classification).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSportAssignmentForbidden
		}
		if err != nil {
			return err
		}
		grantID = &id
	}
	if classification || in.ExpectedVersion != "" {
		version, e := SportingClassificationVersion(ctx, tx, in.MemberID)
		if e != nil {
			return e
		}
		if version != in.ExpectedVersion {
			return ErrClassificationStale
		}
	}
	current := map[string]bool{}
	table, column := "person_sporting_modalities", "modality_code"
	if in.Kind == CanoeCraftAssignment {
		table, column = "person_canoe_craft_classes", "craft_code"
		var parent bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM person_sporting_modalities WHERE user_id=$1 AND modality_code='CANOEING')`, in.MemberID).Scan(&parent); err != nil {
			return err
		}
		if !parent && len(desired) > 0 {
			return ErrSportAssignmentInvalid
		}
	}
	// Fixed identifiers above, never request data.
	rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE user_id=$1`, column, table), in.MemberID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var code string
		if err = rows.Scan(&code); err != nil {
			rows.Close()
			return err
		}
		current[code] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	operation := uuid.New()
	record := func(kind SportAssignmentKind, action, code string) error {
		_, e := tx.Exec(ctx, `INSERT INTO person_sport_assignment_events(operation_id,subject_user_id,actor_user_id,coach_grant_id,kind,action,code,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, operation, in.MemberID, in.ActorID, grantID, kind, action, code, reason)
		return e
	}
	// Removing Canoagem explicitly logs craft removals before its cascading FK.
	if in.Kind == SportingModalityAssignment && current["CANOEING"] && !desired["CANOEING"] {
		craftRows, e := tx.Query(ctx, `SELECT craft_code FROM person_canoe_craft_classes WHERE user_id=$1 ORDER BY craft_code`, in.MemberID)
		if e != nil {
			return e
		}
		var crafts []string
		for craftRows.Next() {
			var code string
			if e = craftRows.Scan(&code); e != nil {
				craftRows.Close()
				return e
			}
			crafts = append(crafts, code)
		}
		e = craftRows.Err()
		craftRows.Close()
		if e != nil {
			return e
		}
		for _, code := range crafts {
			if _, err = tx.Exec(ctx, `DELETE FROM person_canoe_craft_classes WHERE user_id=$1 AND craft_code=$2`, in.MemberID, code); err != nil {
				return err
			}
			if err = record(CanoeCraftAssignment, "REMOVED", code); err != nil {
				return err
			}
		}
	}
	var removed, added []string
	for code := range current {
		if !desired[code] {
			removed = append(removed, code)
		}
	}
	for code := range desired {
		if !current[code] {
			added = append(added, code)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	for _, code := range removed {
		if _, err = tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE user_id=$1 AND %s=$2`, table, column), in.MemberID, code); err != nil {
			return err
		}
		if err = record(in.Kind, "REMOVED", code); err != nil {
			return err
		}
	}
	for _, code := range added {
		if in.Kind == SportingModalityAssignment {
			_, err = tx.Exec(ctx, `INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,$2)`, in.MemberID, code)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO person_canoe_craft_classes(user_id,modality_code,craft_code) VALUES($1,'CANOEING',$2)`, in.MemberID, code)
		}
		if err != nil {
			return err
		}
		if err = record(in.Kind, "ADDED", code); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
