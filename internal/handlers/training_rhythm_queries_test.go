package handlers

import (
	"context"
	"errors"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type publicationRhythmQueryFixture struct {
	publicationRhythmQueries
	source         *publicationRhythmFacts
	permissions    []dbgen.LockPublicationRecipientCraftsRow
	profileLockErr error
	zoneErr        error
	craftLockErr   error
	craftReadErr   error
}

func (q publicationRhythmQueryFixture) LockPublicationRhythmProfiles(context.Context, dbgen.LockPublicationRhythmProfilesParams) ([]dbgen.WaterIntensityProfile, error) {
	return q.source.Profiles, q.profileLockErr
}

func (q publicationRhythmQueryFixture) ListPublicationRhythmZones(context.Context, dbgen.ListPublicationRhythmZonesParams) ([]dbgen.WaterIntensityZone, error) {
	return q.source.Zones, q.zoneErr
}

func (q publicationRhythmQueryFixture) LockPublicationRecipientCrafts(context.Context, dbgen.LockPublicationRecipientCraftsParams) ([]dbgen.LockPublicationRecipientCraftsRow, error) {
	return q.permissions, q.craftLockErr
}

func (q publicationRhythmQueryFixture) PublicationRecipientCrafts(context.Context, dbgen.PublicationRecipientCraftsParams) ([]dbgen.PublicationRecipientCraftsRow, error) {
	var rows []dbgen.PublicationRecipientCraftsRow
	for _, craft := range q.source.Crafts {
		rows = append(rows, dbgen.PublicationRecipientCraftsRow{SubjectID: craft.SubjectID, Kayak: craft.Kayak, Canoe: craft.Canoe})
	}
	return rows, q.craftReadErr
}

func TestReadPublicationRhythmRejectsIncompleteOrUnstableLockedFacts(t *testing.T) {
	lockUnavailable := &pgconn.PgError{Code: "55P03"}
	readFailure := errors.New("rhythm database read failed")
	tests := []struct {
		name string
		fail func(*publicationRhythmQueryFixture)
		want error
	}{
		{"locked profiles busy", func(q *publicationRhythmQueryFixture) { q.profileLockErr = lockUnavailable }, errStructuredTrainingPublicationConflict},
		{"zone read fails", func(q *publicationRhythmQueryFixture) { q.zoneErr = readFailure }, readFailure},
		{"locked recipient busy", func(q *publicationRhythmQueryFixture) { q.craftLockErr = lockUnavailable }, errStructuredTrainingPublicationConflict},
		{"recipient read fails", func(q *publicationRhythmQueryFixture) { q.craftReadErr = readFailure }, readFailure},
		{"too many locked permission rows", func(q *publicationRhythmQueryFixture) {
			q.permissions = make([]dbgen.LockPublicationRecipientCraftsRow, 32769)
		}, errTrainingPublicationReviewLimit},
		{"recipient permission changed after locking", func(q *publicationRhythmQueryFixture) {
			q.permissions = q.permissions[:len(q.permissions)-1]
		}, errStructuredTrainingPublicationConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := rhythmFixture()
			q := publicationRhythmQueryFixture{source: s.Rhythm}
			for _, craft := range s.Rhythm.Crafts {
				if craft.Kayak {
					q.permissions = append(q.permissions, dbgen.LockPublicationRecipientCraftsRow{UserID: craft.SubjectID, PermissionKind: "MODALITY", PermissionCode: "KAYAK_POLO"})
				}
				if craft.Canoe {
					q.permissions = append(q.permissions,
						dbgen.LockPublicationRecipientCraftsRow{UserID: craft.SubjectID, PermissionKind: "MODALITY", PermissionCode: "CANOEING"},
						dbgen.LockPublicationRecipientCraftsRow{UserID: craft.SubjectID, PermissionKind: "CRAFT", PermissionCode: "C1"},
					)
				}
			}
			actor := uuid.New()
			if _, err := readPublicationRhythm(t.Context(), q, actor, s, true); err != nil {
				t.Fatalf("valid locked rhythm facts: %v", err)
			}
			tc.fail(&q)
			facts, err := readPublicationRhythm(t.Context(), q, actor, s, true)
			if facts != nil || !errors.Is(err, tc.want) {
				t.Fatalf("got facts=%v, err=%v, want no facts and %v", facts, err, tc.want)
			}
		})
	}
}

func TestReadPublicationRhythmRejectsInvalidPinBeforeQueries(t *testing.T) {
	s := rhythmFixture()
	s.Overview[0].WaterIntensityProfileID = testUUIDPtr(uuid.Nil)
	facts, err := readPublicationRhythm(t.Context(), nil, uuid.New(), s, true)
	if facts != nil || !errors.Is(err, errPublicationRhythmReference) {
		t.Fatalf("got facts=%v, err=%v, want invalid profile reference", facts, err)
	}
}
