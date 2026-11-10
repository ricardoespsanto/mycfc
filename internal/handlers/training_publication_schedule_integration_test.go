//go:build integration

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setPublicationFixtureEnabled(t *testing.T, f *publicationDomainFixture, enabled bool) {
	t.Helper()
	state, err := db.TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,$3)`, state.DatabaseIncarnation, state.StateEpoch, enabled); err != nil {
		t.Fatal(err)
	}
}

func addPublicationMixedSchedules(t *testing.T, f *publicationDomainFixture) []uuid.UUID {
	t.Helper()
	ids := []uuid.UUID{f.session, uuid.New(), uuid.New(), uuid.New()}
	f.exec(t, `INSERT INTO training_sessions(id,plan_id,title,description,entry_kind,schedule_precision,scheduled_on,starts_at,ends_at,day_band,created_by_id)
 SELECT $1::uuid,id,'Native start-only','Exact native microseconds','TRAINING'::training_entry_kind,'START_ONLY',week_start+1,((week_start+1)::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours 15 minutes 27.123456 seconds',NULL::timestamptz,NULL,$4::uuid FROM training_plans WHERE id=$5
 UNION ALL SELECT $2::uuid,id,'Rest without invented time','Native rest instructions','REST','DAY',week_start+2,NULL,NULL,NULL,$4 FROM training_plans WHERE id=$5
 UNION ALL SELECT $3::uuid,id,'Afternoon logistics','Native logistics instructions','LOGISTICS','BAND',week_start+3,NULL,NULL,'AFTERNOON',$4 FROM training_plans WHERE id=$5`, ids[1], ids[2], ids[3], f.actor, f.plan)
	return ids
}

func publicationRowsJSON(t *testing.T, f *publicationDomainFixture, publication uuid.UUID) string {
	t.Helper()
	var raw string
	if err := f.web.QueryRow(t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id),'[]'::jsonb)::text FROM training_prescriptions p WHERE publication_id=$1`, publication).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPublicationMixedScheduleFreshPostgresRecipientsHistoryAndClosedGate(t *testing.T) {
	f := newPublicationDomainFixture(t)
	store := PostgresStructuredTrainingStore{Pool: f.web}
	old, err := store.ConfirmPublication(t.Context(), f.review(t, "Original exact instructions"))
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) SELECT $1,$2,id,'COMPLETED' FROM training_prescriptions WHERE publication_id=$3`, f.session, f.subject, old.PublicationID)
	history := publicationRowsJSON(t, f, old.PublicationID)
	var outcomes string
	if err = f.web.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(o) ORDER BY session_id,user_id)::text FROM training_session_outcomes o`).Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	setPublicationFixtureEnabled(t, f, true)
	ids := addPublicationMixedSchedules(t, f)
	segment, block := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$2,1,'WATER','Common water instructions')`, segment, ids[1])
	f.exec(t, `INSERT INTO training_segment_blocks(id,segment_id,position,purpose,title,instructions) VALUES($1,$2,1,'MAIN','Shared main','Nested recovery remains prescribed')`, block, segment)
	f.exec(t, `INSERT INTO training_variations(plan_id,target_membership_id,subject_kind,subject_id,operation,change_summary,patch,created_by_id) VALUES($1,$2,'SEGMENT',$3,'OVERRIDE','Explicit individual instruction','{"title":"Adult-specific water instructions"}',$4)`, f.plan, f.membership, segment, f.actor)
	// The existing synthetic authority helper runs only in this newly owned DB.
	privileged, err := pgxpool.New(t.Context(), f.raw)
	if err != nil {
		t.Fatal(err)
	}
	defer privileged.Close()
	guardian, minor, member, unrelated := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth,email_verified_at) VALUES($1,'Synthetic guardian',$2,'hash','1980-01-01',clock_timestamp()),($3,'Unrelated viewer',$4,'hash','1980-01-01',clock_timestamp())`, guardian, guardian.String()+"@example.test", unrelated, unrelated.String()+"@example.test")
	f.exec(t, `INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Synthetic minor',true,(CURRENT_DATE-interval '12 years')::date)`, minor)
	policy := enableGuardianAuthorityTestPolicy(t, t.Context(), privileged, f.actor)
	verifyGuardianAuthorityTestRelationship(t, t.Context(), privileged, guardian, minor, f.actor, policy)
	f.exec(t, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,$2,season_id,programme_id,CURRENT_DATE,CURRENT_DATE+300 FROM training_plans WHERE id=$3`, member, minor, f.plan)
	f.exec(t, `INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES($1,$2,$3)`, f.group, member, f.actor)
	input := f.review(t, "Mixed4 native schedules reviewed")
	result, err := store.ConfirmPublication(t.Context(), input)
	if err != nil || result.Revision != 2 || result.PrescriptionCount != 8 {
		t.Fatalf("mixed publication %+v %v", result, err)
	}
	q := dbgen.New(f.web)
	var minorPrescription uuid.UUID
	if err = f.web.QueryRow(t.Context(), `SELECT id FROM training_prescriptions WHERE publication_id=$1 AND athlete_user_id=$2 AND session_id=$3`, result.PublicationID, minor, ids[1]).Scan(&minorPrescription); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: minorPrescription, UserID: guardian}); err != nil {
		t.Fatal("current guardian direct read refused", err)
	}
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: minorPrescription, UserID: unrelated}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("unrelated direct reader admitted", err)
	}
	for _, viewer := range []struct {
		id    uuid.UUID
		count int
	}{{f.subject, 5}, {minor, 4}, {guardian, 4}, {unrelated, 0}} {
		rows, err := q.ListTrainingPrescriptionsForViewer(t.Context(), dbgen.ListTrainingPrescriptionsForViewerParams{UserID: viewer.id})
		if err != nil || len(rows) != viewer.count {
			t.Fatalf("scoped viewer count got%d want%d: %v", len(rows), viewer.count, err)
		}
		for _, row := range rows {
			snapshot, e := decodeStructuredPrescription(row.Snapshot, lisbonLocation())
			if e != nil {
				t.Fatal(e)
			}
			if row.SessionID == ids[1] {
				if snapshot.SchemaVersion != 2 || snapshot.Session.Schedule.Precision != "START_ONLY" || snapshot.Session.Schedule.EndsAt != "" {
					t.Fatal("recipient lost unknown end")
				}
				var typed structuredPrescriptionSnapshotV2
				if e = json.Unmarshal(row.Snapshot, &typed); e != nil || typed.Session.Schedule.StartsAt.Nanosecond() != 123456000 {
					t.Fatal("native fractional seconds lost", e)
				}
				want := "Common water instructions"
				if row.AthleteUserID == f.subject {
					want = "Adult-specific water instructions"
				}
				if len(snapshot.Session.Segments) != 1 || snapshot.Session.Segments[0].Title != want || snapshot.Session.Segments[0].Blocks[0].Instructions != "Nested recovery remains prescribed" {
					t.Fatalf("resolved native instructions changed: %+v want %s", snapshot.Session.Segments, want)
				}
			}
		}
	}
	if got := publicationRowsJSON(t, f, old.PublicationID); got != history {
		t.Fatal("old prescription bytes/identities changed")
	}
	var after string
	if err = f.web.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(o) ORDER BY session_id,user_id)::text FROM training_session_outcomes o`).Scan(&after); err != nil || after != outcomes {
		t.Fatal("historical outcome changed", err)
	}
	stale := f.review(t, "Review before draft drift")
	f.exec(t, `UPDATE training_sessions SET description='Changed explicitly after review' WHERE id=$1`, ids[1])
	if _, err = store.ConfirmPublication(t.Context(), stale); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatal("stale mixed review accepted", err)
	}
	fresh := f.review(t, "Reviewed second mixed revision")
	third, err := store.ConfirmPublication(t.Context(), fresh)
	if err != nil || third.Revision != 3 {
		t.Fatal("second mixed revision failed", err)
	}
	// Closed gate blocks both new review and a formerly valid reviewed confirm.
	closedReview := f.review(t, "Review before closing authoring")
	setPublicationFixtureEnabled(t, f, false)
	if _, err = store.ReadPublicationSource(t.Context(), f.actor, f.plan); !errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
		t.Fatal("disabled partial preview remained confirmable", err)
	}
	if _, err = store.ConfirmPublication(t.Context(), closedReview); !errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
		t.Fatal("disabled partial confirm succeeded", err)
	}
	// Guardian revocation withdraws access dynamically, without changing history.
	if _, err = privileged.Exec(t.Context(), `UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1,updated_at=clock_timestamp() WHERE subject_user_id=$1`, minor); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetTrainingPrescriptionForViewer(t.Context(), dbgen.GetTrainingPrescriptionForViewerParams{ID: minorPrescription, UserID: guardian}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked guardian direct ID admitted", err)
	}
	if rows, e := q.ListTrainingPrescriptionsForViewer(t.Context(), dbgen.ListTrainingPrescriptionsForViewerParams{UserID: guardian}); e != nil || len(rows) != 0 {
		t.Fatal("revoked guardian retained new material", e)
	}
}

func TestPublicationV2ArtifactPostgresExactBoundAndWholeRefusal(t *testing.T) {
	f := newPublicationDomainFixture(t)
	setPublicationFixtureEnabled(t, f, true)
	ids := addPublicationMixedSchedules(t, f)
	store := PostgresStructuredTrainingStore{Pool: f.web}
	q := dbgen.New(f.web)
	// Measure the actual represented artifact, then pad only its existing native
	// bounded block instructions. This is real PostgreSQL JSONB byte size, not an estimator.
	segment := uuid.New()
	f.exec(t, `INSERT INTO training_session_segments(id,session_id,position,modality,title) VALUES($1,$2,1,'WATER','Synthetic artifact-bound stress')`, segment, ids[1])
	f.exec(t, `INSERT INTO training_segment_blocks(segment_id,position,purpose,instructions) SELECT $1,n,'MAIN',repeat('x',2000) FROM generate_series(1,50)n`, segment)
	source, err := store.ReadPublicationSource(t.Context(), f.actor, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	material, err := materializePublicationSource(source, lisbonLocation())
	if err != nil {
		t.Fatal(err)
	}
	var size int
	for _, p := range material.Prescriptions {
		if p.SessionID == ids[1] {
			if err = f.web.QueryRow(t.Context(), `SELECT octet_length($1::jsonb::text)`, p.Snapshot).Scan(&size); err != nil {
				t.Fatal(err)
			}
		}
	}
	delta := focusedWorkoutMaxArtifactBytes - size
	if delta < 0 || delta >= 98000 {
		t.Fatalf("unexpected native baseline size %d", size)
	}
	f.exec(t, `UPDATE training_segment_blocks SET instructions=repeat('x',2000+LEAST(2000,GREATEST(0,$2::integer-(position-1)*2000))) WHERE segment_id=$1`, segment, delta)
	accepted := f.review(t, "Exact native200000-byte artifact")
	if _, err = store.ConfirmPublication(t.Context(), accepted); err != nil {
		t.Fatal("actual exact200000-byte artifact refused", err)
	}
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&count); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE training_segment_blocks SET instructions=instructions||'x' WHERE segment_id=$1 AND position=50`, segment)
	if _, err = store.ReadPublicationSource(t.Context(), f.actor, f.plan); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("above-bound native artifact offered preview", err)
	}
	// Independently construct the exact signed review to exercise the final
	// transactional check too; no browser/helper can bypass this DB-size fence.
	raw, err := q.PublicationRawSource(t.Context(), dbgen.PublicationRawSourceParams{ActorID: f.actor, PlanID: f.plan})
	if err != nil {
		t.Fatal(err)
	}
	source, err = decodePublicationSource(raw.Envelope, publicationSourceBudget{raw.SourceBytes, raw.RawTupleCount, raw.LatestTupleCount, raw.Overflow}, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := makePublicationPreview(source, f.actor, "Above-bound proof", trainingPublicationReturnContext{}, lisbonLocation(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := trainingPublicationReviews.verify(preview.Token, preview.Metadata, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ConfirmPublication(t.Context(), publicationConfirmInput{ActorID: f.actor, PlanID: f.plan, Review: claims}); !errors.Is(err, errTrainingPublicationReviewLimit) {
		t.Fatal("final above-bound artifact was not refused", err)
	}
	var after int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&after); err != nil || after != count {
		t.Fatal("above-bound refusal partially published", err)
	}
	found := false
	for _, p := range preview.Materialization.Prescriptions {
		if p.SessionID == ids[1] {
			found = bytes.Contains(p.Snapshot, []byte(strings.Repeat("x", 1000)))
		}
	}
	if !found {
		t.Fatal("test did not preserve oversized source")
	}
}

func TestPublicationPartialAdmissionGateContentionAndRevokedCoach(t *testing.T) {
	f := newPublicationDomainFixture(t)
	setPublicationFixtureEnabled(t, f, true)
	addPublicationMixedSchedules(t, f)
	state, err := db.TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := db.WithRecipientPublicationTx(t.Context(), f.web, focusedEditTxOptions(), func(ctx context.Context, commands dbgen.DBTX) error {
			adapter := publicationPostgresQueries{commands: commands, queries: dbgen.New(commands)}
			enabled, err := adapter.AdmitScheduleWrites(ctx)
			if err != nil || !enabled {
				return errors.New("actual partial admission failed")
			}
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("admission failed before contention proof", err)
	case <-time.After(5 * time.Second):
		t.Fatal("actual admission did not start")
	}
	if _, err = f.low.Exec(t.Context(), `SET lock_timeout='150ms'`); err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	_, blocked := f.low.Exec(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,false)`, state.DatabaseIncarnation, state.StateEpoch)
	close(release)
	if err = <-done; err != nil {
		t.Fatal("admitted transaction failed", err)
	}
	var pgError *pgconn.PgError
	if !errors.As(blocked, &pgError) || pgError.Code != "55P03" {
		t.Fatal("gate change did not wait on first admission", blocked)
	}
	if _, err = f.low.Exec(t.Context(), `SET lock_timeout=0`); err != nil {
		t.Fatal(err)
	}
	store := PostgresStructuredTrainingStore{Pool: f.web}
	coach := uuid.New()
	f.exec(t, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Scoped publication coach',$2,'hash','1980-01-01')`, coach, coach.String()+"@example.test")
	f.exec(t, `INSERT INTO staff_grants(user_id,programme_id,capability,granted_by_id) SELECT $1,programme_id,'COACH',$2 FROM training_plans WHERE id=$3`, coach, f.actor, f.plan)
	source, err := store.ReadPublicationSource(t.Context(), coach, f.plan)
	if err != nil {
		t.Fatal("authorized partial coach read refused", err)
	}
	preview, err := makePublicationPreview(source, coach, "Scoped coach reviewed", trainingPublicationReturnContext{}, lisbonLocation(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := trainingPublicationReviews.verify(preview.Token, preview.Metadata, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id=$2,revoke_reason='Synthetic revocation' WHERE user_id=$1`, coach, f.actor)
	if _, err = store.ConfirmPublication(t.Context(), publicationConfirmInput{ActorID: coach, PlanID: f.plan, Review: claims}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked coach published through partial admission", err)
	}
	var count int
	if err = f.web.QueryRow(t.Context(), `SELECT count(*) FROM training_plan_publications WHERE plan_id=$1`, f.plan).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked coach caused a publication", err)
	}
}
