//go:build integration

package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	mycfcdb "github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/emailverification"
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestPostgresRegistrationStorePersistsRequiredTermsConsentAtomically(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	store := PostgresRegistrationStore{Pool: pool}
	email := "registration-" + uuid.NewString() + "@example.test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email)
	})

	input := RegistrationInput{
		Name: "Pessoa de integração", Email: email, PasswordHash: "hash",
		DateOfBirth:  time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		TermsVersion: "test-v1", TermsSHA256: strings.Repeat("a", 64),
		IP: ptrAddr(netip.MustParseAddr("192.0.2.1")), UserAgent: "integration-test",
	}
	result, err := store.RegisterAdult(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	user, err := queries.GetUserByID(ctx, result.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if user.Email == nil || *user.Email != email || user.IsDependent {
		t.Fatalf("created user = %#v", user)
	}
	consents, err := queries.ListConsentFormsForUser(ctx, dbgen.ListConsentFormsForUserParams{UserID: user.ID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(consents) != 1 || consents[0].ConsentType != "Termos_Gerais" {
		t.Fatalf("consents = %#v, want only required terms", consents)
	}
	for _, consent := range consents {
		if consent.GrantedByUserID == nil || *consent.GrantedByUserID != user.ID || !consent.IsAccepted {
			t.Fatalf("consent = %#v", consent)
		}
	}
	var tokenID uuid.UUID
	var outboxStatus string
	var verifiedAt pgtype.Timestamptz
	if err := pool.QueryRow(ctx, `SELECT token.id, outbox.status, account.email_verified_at
		FROM email_verification_tokens token
		JOIN email_outbox outbox ON outbox.verification_token_id = token.id
		JOIN users account ON account.id = token.user_id
		WHERE token.user_id = $1`, user.ID).Scan(&tokenID, &outboxStatus, &verifiedAt); err != nil {
		t.Fatal(err)
	}
	if outboxStatus != "PENDING" || verifiedAt.Valid {
		t.Fatalf("verification state = status %q, verified %#v", outboxStatus, verifiedAt)
	}
	verification := emailverification.Service{Store: queries, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef")}
	if verifiedID, err := verification.Verify(ctx, tokenID.String(), verification.Signature(tokenID)); err != nil || verifiedID != user.ID {
		t.Fatalf("verify = %s, %v", verifiedID, err)
	}
	if err := pool.QueryRow(ctx, `SELECT email_verified_at FROM users WHERE id = $1`, user.ID).Scan(&verifiedAt); err != nil || !verifiedAt.Valid {
		t.Fatalf("verified timestamp = %#v, %v", verifiedAt, err)
	}

	rollbackEmail := "registration-rollback-" + uuid.NewString() + "@example.test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, rollbackEmail)
	})
	input.Email = rollbackEmail
	input.TermsVersion = strings.Repeat("x", 41)
	if _, err := store.RegisterAdult(ctx, input); err == nil {
		t.Fatal("RegisterAdult succeeded with an invalid consent version")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, rollbackEmail).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled back user count = %d, want 0", count)
	}
}

func TestPostgresStructuredTrainingStoreCopiesWeeksDaysSessionsAndBlocksIndependently(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	store := PostgresStructuredTrainingStore{Pool: pool}
	programme, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	actorID, seasonID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Treinador cópias', $2, 'hash', '1980-01-01')`, actorID, "copy-store-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	// Keep this fixture outside the date ranges used by other integration tests;
	// season inference is intentionally based on the week date.
	weekStart := time.Date(2042, time.January, 6, 0, 0, 0, 0, time.Local)
	if _, err := pool.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época cópias', $3, $4)`, seasonID, "CP_"+uuid.NewString()[:8], weekStart.AddDate(0, -1, 0), weekStart.AddDate(0, 2, 0)); err != nil {
		t.Fatal(err)
	}
	group, err := queries.CreateStructuredTrainingGroup(ctx, dbgen.CreateStructuredTrainingGroupParams{Name: "Grupo cópias " + uuid.NewString()[:8], ProgrammeID: &programme.ID, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_plans WHERE training_group_id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_groups WHERE id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM water_intensity_profiles WHERE created_by_id = $1`, actorID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM seasons WHERE id = $1`, seasonID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, actorID)
	})
	week, err := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: "M41", Description: "Semana fonte", WeekStart: pgtype.Date{Time: weekStart, Valid: true}, PlannedLoadPercentage: int16Ptr(70), CreatedByID: actorID, GroupID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := queries.UpdateStructuredTrainingWeekLoad(ctx, dbgen.UpdateStructuredTrainingWeekLoadParams{PlanID: week.ID, PlannedLoadPercentage: int16Ptr(65), IsAdmin: true, UserID: actorID}); err != nil || updated != 1 {
		t.Fatalf("updated load rows=%d err=%v", updated, err)
	}
	startsAt := weekStart.Add(17 * time.Hour)
	session, err := queries.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{PlanID: week.ID, Title: "Ginásio fonte", StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, EntryKind: dbgen.TrainingEntryKindTRAINING, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	segmentID, err := queries.CreateTrainingSessionSegment(ctx, dbgen.CreateTrainingSessionSegmentParams{SessionID: session.ID, Modality: dbgen.TrainingSegmentModalityGYM, Title: "Força"})
	if err != nil {
		t.Fatal(err)
	}
	blockID, err := queries.CreateTrainingSegmentBlock(ctx, dbgen.CreateTrainingSegmentBlockParams{SegmentID: segmentID, Purpose: dbgen.TrainingBlockPurposeMAIN, Title: "Circuito fonte", Instructions: "3 voltas"})
	if err != nil {
		t.Fatal(err)
	}
	waterSegmentID, err := queries.CreateTrainingSessionSegment(ctx, dbgen.CreateTrainingSessionSegmentParams{SessionID: session.ID, Modality: dbgen.TrainingSegmentModalityWATER, Title: "Água"})
	if err != nil {
		t.Fatal(err)
	}
	waterBlockID, err := queries.CreateTrainingSegmentBlock(ctx, dbgen.CreateTrainingSegmentBlockParams{SegmentID: waterSegmentID, Purpose: dbgen.TrainingBlockPurposeMAIN, Title: "Série fonte", Instructions: "Estrutura aninhada"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := queries.CreateWaterIntensityProfile(ctx, dbgen.CreateWaterIntensityProfileParams{Name: "Perfil cópias " + uuid.NewString()[:8], Craft: dbgen.PaddlingCraftKAYAK, Notes: "", CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := queries.CreateWaterBlockPrescription(ctx, dbgen.CreateWaterBlockPrescriptionParams{BlockID: waterBlockID, Method: dbgen.WaterWorkMethodINTERVALS, IntensityProfileID: &profile.ID}); err != nil || rows != 1 {
		t.Fatalf("water prescription rows=%d err=%v", rows, err)
	}
	parentStepID, err := queries.CreateWaterWorkStep(ctx, dbgen.CreateWaterWorkStepParams{BlockID: waterBlockID, Kind: dbgen.WaterStepKindREPEATGROUP, Name: "Três séries", Repeats: int32Ptr(3), RecoverySeconds: int32Ptr(180), Instructions: ""})
	if err != nil {
		t.Fatal(err)
	}
	durationCertainty, intensity := dbgen.TrainingMeasureCertaintyEXACT, "R7"
	if _, err := queries.CreateWaterWorkStep(ctx, dbgen.CreateWaterWorkStepParams{BlockID: waterBlockID, ParentStepID: &parentStepID, Kind: dbgen.WaterStepKindEFFORT, Name: "Dois minutos", DurationSeconds: int32Ptr(120), DurationCertainty: &durationCertainty, IntensityCode: &intensity, Instructions: "Ritmo de prova"}); err != nil {
		t.Fatal(err)
	}

	copiedWeekStart := weekStart.AddDate(0, 0, 7)
	copiedWeek, err := store.CopyStructuredTrainingWeek(ctx, StructuredWeekCopyInput{SourcePlanID: week.ID, WeekStart: copiedWeekStart, Title: "M42", ActorID: actorID})
	if err != nil || copiedWeek.Description != "Semana fonte" || copiedWeek.PlannedLoadPercentage == nil || *copiedWeek.PlannedLoadPercentage != 65 {
		t.Fatalf("copy week=%#v err=%v", copiedWeek, err)
	}
	count, err := store.CopyStructuredTrainingDay(ctx, StructuredDayCopyInput{SourcePlanID: week.ID, TargetPlanID: copiedWeek.ID, SourceDate: weekStart, TargetDate: copiedWeekStart.AddDate(0, 0, 1), ActorID: actorID})
	if err != nil || count != 1 {
		t.Fatalf("copy day count=%d err=%v", count, err)
	}
	if _, err := store.CopyTrainingSession(ctx, session.ID, copiedWeek.ID, pgtype.Timestamptz{Time: copiedWeekStart.AddDate(0, 0, 2).Add(17 * time.Hour), Valid: true}, actorID); err != nil {
		t.Fatal(err)
	}
	var copiedSegmentID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT segment.id FROM training_session_segments segment JOIN training_sessions session ON session.id = segment.session_id WHERE session.plan_id = $1 ORDER BY session.starts_at, segment.position LIMIT 1`, copiedWeek.ID).Scan(&copiedSegmentID); err != nil {
		t.Fatal(err)
	}
	copiedBlockID, err := store.CopyTrainingBlock(ctx, blockID, copiedSegmentID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE training_segment_blocks SET instructions = 'Cópia alterada' WHERE id = $1`, copiedBlockID); err != nil {
		t.Fatal(err)
	}
	copiedWaterBlockID, err := store.CopyTrainingBlock(ctx, waterBlockID, waterSegmentID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	var copiedProfileID uuid.UUID
	var copiedSteps, nestedSteps int
	if err := pool.QueryRow(ctx, `SELECT intensity_profile_id FROM water_block_prescriptions WHERE block_id = $1`, copiedWaterBlockID).Scan(&copiedProfileID); err != nil || copiedProfileID != profile.ID {
		t.Fatalf("copied water profile=%s err=%v", copiedProfileID, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*), count(parent_step_id) FROM water_work_steps WHERE block_id = $1`, copiedWaterBlockID).Scan(&copiedSteps, &nestedSteps); err != nil || copiedSteps != 2 || nestedSteps != 1 {
		t.Fatalf("copied water steps=%d nested=%d err=%v", copiedSteps, nestedSteps, err)
	}
	var sourceInstructions string
	if err := pool.QueryRow(ctx, `SELECT instructions FROM training_segment_blocks WHERE id = $1`, blockID).Scan(&sourceInstructions); err != nil || sourceInstructions != "3 voltas" {
		t.Fatalf("source block instructions=%q err=%v", sourceInstructions, err)
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_copy_events WHERE copied_by_id = $1`, actorID).Scan(&events); err != nil || events < 6 {
		t.Fatalf("copy events=%d err=%v", events, err)
	}
}

func TestPostgresStructuredTrainingCyclesRemainScopedVersionedAndCopyIndependentDrafts(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	store := PostgresStructuredTrainingStore{Pool: pool}
	programme, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	leisureProgramme, err := queries.GetProgrammeByCode(ctx, "Leisure")
	if err != nil {
		t.Fatal(err)
	}
	actorID, coachID, seasonID, eventID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{actorID: "Treinadora ciclos", coachID: "Treinador limitado"} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', '1980-01-01')`, id, name, "cycle-store-"+uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	// Use an isolated range so both weeks resolve to this fixture season even
	// when the baseline contains the club's current season.
	weekStart := time.Date(2044, time.January, 4, 0, 0, 0, 0, time.Local)
	if _, err := pool.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época ciclos', $3, $4)`, seasonID, "CY_"+uuid.NewString()[:8], weekStart.AddDate(0, -2, 0), weekStart.AddDate(0, 6, 0)); err != nil {
		t.Fatal(err)
	}
	group, err := queries.CreateStructuredTrainingGroup(ctx, dbgen.CreateStructuredTrainingGroupParams{Name: "Grupo ciclos " + uuid.NewString()[:8], ProgrammeID: &programme.ID, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO events (id, title, description, event_type, starts_at, ends_at, created_by_id) VALUES ($1, 'Taça dos ciclos', '', 'COMPETITION', $2, $3, $4)`, eventID, weekStart.AddDate(0, 1, 0), weekStart.AddDate(0, 1, 0).Add(time.Hour), actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_audiences (event_id, programme_id) VALUES ($1, $2), ($1, $3)`, eventID, programme.ID, leisureProgramme.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_grants (user_id, capability, programme_id, granted_by_id) VALUES ($1, 'COACH', $2, $3)`, coachID, programme.ID, actorID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_plans WHERE training_group_id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `UPDATE training_cycles SET parent_cycle_id = NULL WHERE training_group_id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_cycles WHERE training_group_id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id = $1`, eventID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_groups WHERE id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM seasons WHERE id = $1`, seasonID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{actorID, coachID})
	})
	weekOne, err := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: "M41", Description: "Fonte um", WeekStart: pgtype.Date{Time: weekStart, Valid: true}, PlannedLoadPercentage: int16Ptr(60), CreatedByID: actorID, GroupID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	weekTwo, err := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: "M42", Description: "Fonte dois", WeekStart: pgtype.Date{Time: weekStart.AddDate(0, 0, 7), Valid: true}, PlannedLoadPercentage: int16Ptr(80), CreatedByID: actorID, GroupID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	session, err := queries.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{PlanID: weekOne.ID, Title: "Água fonte", StartsAt: pgtype.Timestamptz{Time: weekStart.Add(9 * time.Hour), Valid: true}, EndsAt: pgtype.Timestamptz{Time: weekStart.Add(10 * time.Hour), Valid: true}, EntryKind: dbgen.TrainingEntryKindTRAINING, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	segmentID, err := queries.CreateTrainingSessionSegment(ctx, dbgen.CreateTrainingSessionSegmentParams{SessionID: session.ID, Modality: dbgen.TrainingSegmentModalityWATER, Title: "Técnica"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateTrainingSegmentBlock(ctx, dbgen.CreateTrainingSegmentBlockParams{SegmentID: segmentID, Purpose: dbgen.TrainingBlockPurposeMAIN, Title: "Principal", Instructions: "Executar a fonte"}); err != nil {
		t.Fatal(err)
	}
	cycle, err := store.SaveTrainingCycle(ctx, StructuredTrainingCycleInput{
		TrainingGroupID: group.ID, Name: "Transformação", LevelLabel: "Mesociclo", Goals: "Preparar a prova",
		PhaseFocusNotes: "Técnica", WeekIDs: []uuid.UUID{weekOne.ID, weekTwo.ID}, TargetEventIDs: []uuid.UUID{eventID}, ActorID: actorID, IsAdmin: true,
	})
	if err != nil || cycle.Version != 1 {
		t.Fatalf("create cycle=%#v err=%v", cycle, err)
	}
	coachEvents, err := queries.ListManagedStructuredCompetitionEvents(ctx, dbgen.ListManagedStructuredCompetitionEventsParams{IsAdmin: false, UserID: coachID})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range coachEvents {
		if candidate.ID == eventID {
			t.Fatal("partially authorized multi-programme competition was exposed to coach")
		}
	}
	managedCycles, err := queries.ListManagedTrainingCycles(ctx, dbgen.ListManagedTrainingCyclesParams{IsAdmin: false, UserID: coachID})
	foundManagedCycle := false
	for _, managed := range managedCycles {
		foundManagedCycle = foundManagedCycle || managed.ID == cycle.ID
	}
	if err != nil || !foundManagedCycle {
		t.Fatalf("coach cycle list=%#v err=%v", managedCycles, err)
	}
	managedTargets, err := queries.ListManagedTrainingCycleTargets(ctx, dbgen.ListManagedTrainingCycleTargetsParams{IsAdmin: false, UserID: coachID})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range managedTargets {
		if target.EventID == eventID {
			t.Fatal("partially authorized target details were exposed to coach")
		}
	}
	coachEdit, err := store.SaveTrainingCycle(ctx, StructuredTrainingCycleInput{CycleID: cycle.ID, ExpectedVersion: 1, Name: "Transformação revista pelo treinador", WeekIDs: []uuid.UUID{weekOne.ID, weekTwo.ID}, ActorID: coachID})
	if err != nil || coachEdit.Version != 2 {
		t.Fatalf("ordinary coach edit=%#v err=%v", coachEdit, err)
	}
	var preservedTargets int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_cycle_competition_targets WHERE cycle_id = $1 AND event_id = $2`, cycle.ID, eventID).Scan(&preservedTargets); err != nil || preservedTargets != 1 {
		t.Fatalf("hidden target preservation=%d err=%v", preservedTargets, err)
	}
	if _, err := store.SaveTrainingCycle(ctx, StructuredTrainingCycleInput{CycleID: cycle.ID, ExpectedVersion: 2, Name: cycle.Name, WeekIDs: []uuid.UUID{weekOne.ID, weekTwo.ID}, TargetEventIDs: []uuid.UUID{eventID}, ActorID: coachID}); !errors.Is(err, errStructuredTrainingCycleScope) {
		t.Fatalf("partially authorized target update err=%v", err)
	}
	if _, err := store.SaveTrainingCycle(ctx, StructuredTrainingCycleInput{CycleID: cycle.ID, ExpectedVersion: 99, Name: cycle.Name, WeekIDs: []uuid.UUID{weekOne.ID, weekTwo.ID}, ActorID: actorID, IsAdmin: true}); !errors.Is(err, errStructuredTrainingCycleConflict) {
		t.Fatalf("stale cycle update err=%v", err)
	}
	start := make(chan struct{})
	concurrentErrors := make(chan error, 2)
	go func() {
		<-start
		_, updateErr := store.SaveTrainingCycle(ctx, StructuredTrainingCycleInput{CycleID: cycle.ID, ExpectedVersion: 2, Name: "Transformação revista", LevelLabel: cycle.LevelLabel, Goals: cycle.Goals, PhaseFocusNotes: cycle.PhaseFocusNotes, WeekIDs: []uuid.UUID{weekTwo.ID, weekOne.ID}, TargetEventIDs: []uuid.UUID{eventID}, ActorID: actorID, IsAdmin: true})
		concurrentErrors <- updateErr
	}()
	go func() {
		<-start
		_, copyErr := store.CopyStructuredTrainingCycle(ctx, StructuredTrainingCycleCopyInput{SourceCycleID: cycle.ID, FirstMonday: weekStart.AddDate(0, 0, 42), Name: "Cópia concorrente", ActorID: actorID})
		concurrentErrors <- copyErr
	}()
	close(start)
	for range 2 {
		if err := <-concurrentErrors; err != nil {
			t.Fatalf("concurrent cycle operation err=%v", err)
		}
	}
	copied, err := store.CopyStructuredTrainingCycle(ctx, StructuredTrainingCycleCopyInput{SourceCycleID: cycle.ID, FirstMonday: weekStart.AddDate(0, 0, 28), Name: "Transformação seguinte", ActorID: actorID})
	if err != nil || copied.ID == cycle.ID || copied.Name != "Transformação seguinte" || copied.LevelLabel != "Mesociclo" {
		t.Fatalf("copied cycle=%#v err=%v", copied, err)
	}
	var copiedWeeks, copiedSessions, copiedTargets, copiedPublications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_plans WHERE cycle_id = $1`, copied.ID).Scan(&copiedWeeks); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_sessions session JOIN training_plans plan ON plan.id = session.plan_id WHERE plan.cycle_id = $1`, copied.ID).Scan(&copiedSessions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_cycle_competition_targets WHERE cycle_id = $1`, copied.ID).Scan(&copiedTargets); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications publication JOIN training_plans plan ON plan.id = publication.plan_id WHERE plan.cycle_id = $1`, copied.ID).Scan(&copiedPublications); err != nil {
		t.Fatal(err)
	}
	if copiedWeeks != 2 || copiedSessions != 1 || copiedTargets != 0 || copiedPublications != 0 {
		t.Fatalf("copy counts weeks=%d sessions=%d targets=%d publications=%d", copiedWeeks, copiedSessions, copiedTargets, copiedPublications)
	}
	if _, err := pool.Exec(ctx, `UPDATE training_sessions SET title = 'Cópia alterada' WHERE id = (SELECT session.id FROM training_sessions session JOIN training_plans plan ON plan.id = session.plan_id WHERE plan.cycle_id = $1 LIMIT 1)`, copied.ID); err != nil {
		t.Fatal(err)
	}
	var sourceTitle string
	if err := pool.QueryRow(ctx, `SELECT title FROM training_sessions WHERE id = $1`, session.ID).Scan(&sourceTitle); err != nil || sourceTitle != "Água fonte" {
		t.Fatalf("source title=%q err=%v", sourceTitle, err)
	}
	weekThree, err := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{Title: "M43", Description: "Fonte três", WeekStart: pgtype.Date{Time: weekStart.AddDate(0, 0, 14), Valid: true}, PlannedLoadPercentage: int16Ptr(50), CreatedByID: actorID, GroupID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	secondChild, err := store.SaveTrainingCycle(ctx, StructuredTrainingCycleInput{TrainingGroupID: group.ID, Name: "Realização", LevelLabel: "Mesociclo", WeekIDs: []uuid.UUID{weekThree.ID}, ActorID: actorID, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.SaveTrainingCycle(ctx, StructuredTrainingCycleInput{TrainingGroupID: group.ID, Name: "Época principal", LevelLabel: "Macrociclo", ChildCycleIDs: []uuid.UUID{cycle.ID, secondChild.ID}, ActorID: actorID, IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	cycleWeeks, err := queries.ListManagedTrainingCycleWeeks(ctx, dbgen.ListManagedTrainingCycleWeeksParams{IsAdmin: true, UserID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	aggregatedWeeks := 0
	for _, row := range cycleWeeks {
		if row.CycleID == parent.ID {
			aggregatedWeeks++
		}
	}
	if aggregatedWeeks != 3 {
		t.Fatalf("macrocycle aggregated weeks=%d", aggregatedWeeks)
	}
	rolledParent, err := store.CopyStructuredTrainingCycle(ctx, StructuredTrainingCycleCopyInput{SourceCycleID: parent.ID, FirstMonday: weekStart.AddDate(0, 0, 70), Name: "Época seguinte", ActorID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_plans WHERE cycle_id = $1`, rolledParent.ID).Scan(&copiedWeeks); err != nil || copiedWeeks != 3 {
		t.Fatalf("copied macrocycle weeks=%d err=%v", copiedWeeks, err)
	}
}

func TestPostgresStructuredTrainingVariationsResolveAthleteOverSubgroup(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	store := PostgresStructuredTrainingStore{Pool: pool}
	programme, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	actorID, athleteID, seasonID := uuid.New(), uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{actorID: "Treinador variações", athleteID: "Atleta variações"} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', '2000-01-01')`, id, name, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	today := time.Now().UTC()
	weekStart := time.Date(today.Year(), today.Month(), today.Day()-((int(today.Weekday())+6)%7), 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época variações', $3, $4)`, seasonID, "VR_"+uuid.NewString()[:8], weekStart.AddDate(0, -1, 0), weekStart.AddDate(0, 2, 0)); err != nil {
		t.Fatal(err)
	}
	membership, err := queries.CreateUserMembership(ctx, dbgen.CreateUserMembershipParams{UserID: athleteID, SeasonID: seasonID, ProgrammeID: programme.ID, StartsOn: pgtype.Date{Time: weekStart.AddDate(0, -1, 0), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup(ctx, StructuredTrainingGroupInput{Params: dbgen.CreateStructuredTrainingGroupParams{Name: "Grupo variações " + uuid.NewString()[:8], ProgrammeID: &programme.ID, CreatedByID: actorID}, MembershipIDs: []uuid.UUID{membership.ID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_plans WHERE training_group_id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM training_groups WHERE id = $1`, group.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE id = $1`, membership.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM seasons WHERE id = $1`, seasonID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{actorID, athleteID})
	})
	week, err := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{GroupID: group.ID, Title: "M variações", WeekStart: pgtype.Date{Time: weekStart, Valid: true}, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	startsAt := weekStart.Add(10 * time.Hour)
	session, err := queries.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{PlanID: week.ID, Title: "Água", StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, EntryKind: dbgen.TrainingEntryKindTRAINING, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	segmentID, err := queries.CreateTrainingSessionSegment(ctx, dbgen.CreateTrainingSessionSegmentParams{SessionID: session.ID, Modality: dbgen.TrainingSegmentModalityWATER, Title: "Séries"})
	if err != nil {
		t.Fatal(err)
	}
	crew, err := store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{Params: dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: group.ID, Name: "Tripulação teste", Kind: dbgen.TrainingVariationGroupKindCREW, CraftModalityID: nil, EffectiveFrom: pgtype.Date{Time: weekStart, Valid: true}, EffectiveUntil: pgtype.Date{Time: weekStart.AddDate(0, 0, 6), Valid: true}, CreatedByID: actorID}, MembershipIDs: []uuid.UUID{membership.ID}})
	if err == nil {
		t.Fatal("crew without craft modality unexpectedly persisted")
	}
	crew, err = store.CreateTrainingVariationGroup(ctx, StructuredVariationGroupInput{Params: dbgen.CreateTrainingVariationGroupParams{TrainingGroupID: group.ID, Name: "Subgrupo teste", Kind: dbgen.TrainingVariationGroupKindSUBGROUP, EffectiveFrom: pgtype.Date{Time: weekStart, Valid: true}, CreatedByID: actorID}, MembershipIDs: []uuid.UUID{membership.ID}})
	if err != nil {
		t.Fatal(err)
	}
	groupPatch := []byte(`{"modality":"ERGOMETER"}`)
	if _, err := queries.CreateTrainingVariation(ctx, dbgen.CreateTrainingVariationParams{PlanID: week.ID, TargetGroupID: &crew.ID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segmentID, Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Subgrupo no ergómetro", Patch: groupPatch, CreatedByID: actorID}); err != nil {
		t.Fatal(err)
	}
	athletePatch := []byte(`{"instructions":"Carga individual"}`)
	if _, err := queries.CreateTrainingVariation(ctx, dbgen.CreateTrainingVariationParams{PlanID: week.ID, TargetMembershipID: &membership.ID, SubjectKind: dbgen.TrainingVariationSubjectKindSEGMENT, SubjectID: segmentID, Operation: dbgen.TrainingVariationOperationOVERRIDE, ChangeSummary: "Carga individual", Patch: athletePatch, CreatedByID: actorID}); err != nil {
		t.Fatal(err)
	}
	matches, err := queries.ListTrainingVariationMatchesForManager(ctx, dbgen.ListTrainingVariationMatchesForManagerParams{TimeZone: "UTC", IsAdmin: true, UserID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	matched := []dbgen.ListTrainingVariationMatchesForManagerRow{}
	for _, match := range matches {
		if match.PlanID == week.ID && match.SubjectID == segmentID {
			matched = append(matched, match)
		}
	}
	if len(matched) != 2 || matched[0].Priority != 2 || matched[1].Priority != 1 {
		t.Fatalf("variation matches = %#v", matched)
	}
	if _, err := queries.ListStructuredTrainingOverviewForManager(ctx, dbgen.ListStructuredTrainingOverviewForManagerParams{IsAdmin: true, UserID: actorID}); err != nil {
		t.Fatalf("list structured overview: %v", err)
	}
	if _, err := queries.ListVisibleTrainingRoutines(ctx, dbgen.ListVisibleTrainingRoutinesParams{IsAdmin: true, UserID: actorID}); err != nil {
		t.Fatalf("list routines: %v", err)
	}
	if _, err := queries.ListActiveWaterIntensityProfiles(ctx); err != nil {
		t.Fatalf("list intensity profiles: %v", err)
	}
	if _, err := queries.ListEligibleTrainingGroupMemberships(ctx, dbgen.ListEligibleTrainingGroupMembershipsParams{IsAdmin: true, UserID: actorID}); err != nil {
		t.Fatalf("list eligible memberships: %v", err)
	}
	if _, err := queries.ListManagedTrainingGroupMembers(ctx, dbgen.ListManagedTrainingGroupMembersParams{IsAdmin: true, UserID: actorID}); err != nil {
		t.Fatalf("list variation members: %v", err)
	}
	if _, err := queries.ListStructuredCrewModalities(ctx); err != nil {
		t.Fatalf("list crew modalities: %v", err)
	}
	if _, err := queries.ListManagedStructuredCompetitionEvents(ctx, dbgen.ListManagedStructuredCompetitionEventsParams{IsAdmin: true, UserID: actorID}); err != nil {
		t.Fatalf("list competition events: %v", err)
	}
	if _, err := queries.ListManagedTrainingVariationGroups(ctx, dbgen.ListManagedTrainingVariationGroupsParams{IsAdmin: true, UserID: actorID}); err != nil {
		t.Fatalf("list variation groups: %v", err)
	}
}

func TestPostgresProfileStoreEmailChangeInvalidatesVerificationAtomically(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	email := "profile-verification-" + uuid.NewString() + "@example.test"
	account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Pessoa verificada", Email: &email, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, account.ID) })
	if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE id = $1`, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := queries.EnsureMemberProfile(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	profile, err := queries.GetMemberProfile(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	newEmail := "profile-verification-new-" + uuid.NewString() + "@example.test"
	store := PostgresProfileStore{Pool: pool, Now: func() time.Time { return time.Now().UTC().Add(time.Minute) }}
	err = store.Update(ctx, ProfileUpdate{
		ActorID: account.ID, SubjectID: account.ID, IsAdmin: true,
		Profile:        dbgen.UpdateMemberProfileParams{UserID: account.ID, MedicalDeclaration: "UNKNOWN", ExpectedUpdatedAt: profile.UpdatedAt},
		Identity:       &dbgen.UpdateMemberIdentityParams{Name: profile.Name, Email: &newEmail, DateOfBirth: profile.DateOfBirth, ExpectedUpdatedAt: profile.IdentityUpdatedAt},
		IdentityFields: []string{"email"},
		ChangedFields:  []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var activeEmail string
	var verifiedAt pgtype.Timestamptz
	var activeTokens, cancelledOutbox int
	if err := pool.QueryRow(ctx, `SELECT email, email_verified_at FROM users WHERE id = $1`, account.ID).Scan(&activeEmail, &verifiedAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_verification_tokens WHERE user_id = $1 AND consumed_at IS NULL`, account.ID).Scan(&activeTokens); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox outbox JOIN email_verification_tokens token ON token.id = outbox.verification_token_id WHERE token.user_id = $1 AND outbox.status = 'CANCELLED'`, account.ID).Scan(&cancelledOutbox); err != nil {
		t.Fatal(err)
	}
	if activeEmail != newEmail || verifiedAt.Valid || activeTokens != 1 || cancelledOutbox != 1 {
		t.Fatalf("email=%q verified=%v active_tokens=%d cancelled=%d", activeEmail, verifiedAt.Valid, activeTokens, cancelledOutbox)
	}
}

func TestEmailVerificationResendThrottleSerializesConcurrentRequests(t *testing.T) {
	ctx, pool := integrationPool(t)
	userID := uuid.New()
	email := "verification-throttle-" + uuid.NewString() + "@example.test"
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, email_verified_at, password_hash, date_of_birth) VALUES ($1, 'Pessoa concorrente', $2, now(), 'hash', '1990-01-01')`, userID, email); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })
	service := emailverification.Service{Store: dbgen.New(pool)}
	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := service.Issue(context.Background(), userID, email, true)
			errorsSeen <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	var succeeded, throttled int
	for err := range errorsSeen {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, emailverification.ErrTooSoon):
			throttled++
		default:
			t.Fatalf("unexpected issue error: %v", err)
		}
	}
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_verification_tokens WHERE user_id = $1 AND consumed_at IS NULL`, userID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || throttled != 1 || active != 1 {
		t.Fatalf("succeeded=%d throttled=%d active=%d", succeeded, throttled, active)
	}
}

func TestPostgresGuardianDependentStorePersistsResponsibilityAndEnforcesLimit(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	guardianEmail := "guardian-" + uuid.NewString() + "@example.test"
	guardian, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{
		Name: "Guardião de integração", Email: &guardianEmail, PasswordHash: integrationStringPtr("hash"),
		DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	makeGuardianApplicantEligible(t, ctx, pool, guardian.ID)
	enableGuardianAuthorityTestPolicy(t, ctx, pool, guardian.ID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE guardian_id = $1`, guardian.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, guardian.ID)
	})

	store := PostgresGuardianDependentStore{Pool: pool, Key: []byte("0123456789abcdef0123456789abcdef")}
	input := GuardianDependentInput{
		GuardianID: guardian.ID, Name: "Menor de integração",
		DateOfBirth:           time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("c", 64),
		IP: ptrAddr(netip.MustParseAddr("192.0.2.2")), UserAgent: "integration-test",
	}
	if err := store.CreateDependent(ctx, input); err != nil {
		t.Fatal(err)
	}
	relationships, err := (PostgresGuardianAuthorityStore{DB: pool}).ListForGuardian(ctx, guardian.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(relationships) != 1 || relationships[0].State != "PENDING" || relationships[0].SubjectName != "" {
		t.Fatalf("pending relationships = %#v", relationships)
	}
	consents, err := queries.ListConsentFormsForUser(ctx, dbgen.ListConsentFormsForUserParams{UserID: relationships[0].SubjectID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(consents) != 1 || consents[0].ConsentType != "Responsabilidade_Menor" || consents[0].GrantedByUserID == nil || *consents[0].GrantedByUserID != guardian.ID {
		t.Fatalf("responsibility consent = %#v", consents)
	}

	for i := 1; i < 10; i++ {
		input.Name = "Menor de integração " + string(rune('A'+i))
		if err := store.CreateDependent(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	input.Name = "Menor excedente"
	if err := store.CreateDependent(ctx, input); !errors.Is(err, ErrMaximumDependents) {
		t.Fatalf("limit error = %v, want %v", err, ErrMaximumDependents)
	}
	count, err := queries.CountDependentsByGuardian(ctx, &guardian.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("active dependant count = %d, want 10", count)
	}
}

func TestGuardianInvitationIsEmailBoundSingleUseAndAtomic(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	createAdult := func(name, email string) dbgen.CreateAdultUserRow {
		account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: name, Email: &email, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at=clock_timestamp() WHERE id=$1`, account.ID); err != nil {
			t.Fatal(err)
		}
		return account
	}
	administrator := createAdult("Invitation administrator", "invite-admin-"+uuid.NewString()+"@example.test")
	guardian := createAdult("Invited guardian", "invite-guardian-"+uuid.NewString()+"@example.test")
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: administrator.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	enableGuardianAuthorityTestPolicy(t, ctx, pool, administrator.ID)
	key := []byte("0123456789abcdef0123456789abcdef")
	token := "random-high-entropy-invitation-token-value"
	authority := PostgresGuardianAuthorityStore{DB: pool}
	issued, err := authority.IssueInvitation(ctx, administrator.ID, *guardian.Email, guardianInvitationDigest(key, token))
	if err != nil {
		t.Fatal(err)
	}
	if lifetime := issued.ExpiresAt.Sub(issued.IssuedAt); lifetime < 30*24*time.Hour-time.Second || lifetime > 30*24*time.Hour+time.Second {
		t.Fatalf("invitation lifetime = %s", lifetime)
	}
	store := PostgresGuardianDependentStore{Pool: pool, Key: key}
	input := GuardianDependentInput{GuardianID: guardian.ID, Name: "Invited minor", DateOfBirth: time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("a", 64), InvitationToken: token}
	if err := store.CreateDependent(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Name = "Duplicate invitation minor"
	if err := store.CreateDependent(ctx, input); !errors.Is(err, ErrGuardianInvitationInvalid) {
		t.Fatalf("reused invitation error = %v", err)
	}
	var consumed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM guardian_authority_invitations WHERE public_ref=$1 AND consumed_by=$2 AND consumed_at IS NOT NULL AND invited_email IS NULL`, issued.Reference, guardian.ID).Scan(&consumed); err != nil || consumed != 1 {
		t.Fatalf("consumed invitation count=%d err=%v", consumed, err)
	}
	var duplicateRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE name='Duplicate invitation minor'`).Scan(&duplicateRows); err != nil || duplicateRows != 0 {
		t.Fatalf("reused invitation left partial users=%d err=%v", duplicateRows, err)
	}
	concurrentToken := "second-random-high-entropy-invitation-token"
	if _, err := authority.IssueInvitation(ctx, administrator.ID, *guardian.Email, guardianInvitationDigest(key, concurrentToken)); err != nil {
		t.Fatal(err)
	}
	concurrentResults := make(chan error, 2)
	var group sync.WaitGroup
	for _, name := range []string{"Concurrent invited minor A", "Concurrent invited minor B"} {
		group.Add(1)
		go func(name string) {
			defer group.Done()
			concurrentResults <- store.CreateDependent(ctx, GuardianDependentInput{GuardianID: guardian.ID, Name: name, DateOfBirth: time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC), ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("b", 64), InvitationToken: concurrentToken})
		}(name)
	}
	group.Wait()
	close(concurrentResults)
	var concurrentAccepted, concurrentRejected int
	for err := range concurrentResults {
		if err == nil {
			concurrentAccepted++
		} else if errors.Is(err, ErrGuardianInvitationInvalid) {
			concurrentRejected++
		} else {
			t.Fatalf("concurrent invitation error: %v", err)
		}
	}
	if concurrentAccepted != 1 || concurrentRejected != 1 {
		t.Fatalf("concurrent accepted=%d rejected=%d", concurrentAccepted, concurrentRejected)
	}
	unverifiedEmail := "unverified-invite-" + uuid.NewString() + "@example.test"
	unverified, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Unverified invited guardian", Email: &unverifiedEmail, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	unverifiedToken := "unverified-account-invitation-token-value"
	if _, err := authority.IssueInvitation(ctx, administrator.ID, unverifiedEmail, guardianInvitationDigest(key, unverifiedToken)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDependent(ctx, GuardianDependentInput{GuardianID: unverified.ID, Name: "Must not be created", DateOfBirth: time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC), ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("c", 64), InvitationToken: unverifiedToken}); !errors.Is(err, ErrGuardianApplicantIneligible) {
		t.Fatalf("unverified applicant error = %v", err)
	}
	wrongEmailToken := "wrong-email-invitation-token-value"
	if _, err := authority.IssueInvitation(ctx, administrator.ID, "someone-else-"+uuid.NewString()+"@example.test", guardianInvitationDigest(key, wrongEmailToken)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDependent(ctx, GuardianDependentInput{GuardianID: guardian.ID, Name: "Wrong email must not be created", DateOfBirth: time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC), ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("d", 64), InvitationToken: wrongEmailToken}); !errors.Is(err, ErrGuardianInvitationInvalid) {
		t.Fatalf("wrong-email invitation error = %v", err)
	}
	var forbiddenRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE name IN('Must not be created','Wrong email must not be created')`).Scan(&forbiddenRows); err != nil || forbiddenRows != 0 {
		t.Fatalf("denied applicants left partial users=%d err=%v", forbiddenRows, err)
	}
}

func TestGuardianApplicantEligibilityAndInvitationStateFailClosedWithoutPartialRows(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	createAdult := func(label string, birth time.Time, active bool) dbgen.CreateAdultUserRow {
		email := strings.ToLower(strings.ReplaceAll(label, " ", "-")) + "-" + uuid.NewString() + "@example.test"
		account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: label, Email: &email, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: birth, Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at=clock_timestamp(),is_active=$2 WHERE id=$1`, account.ID, active); err != nil {
			t.Fatal(err)
		}
		return account
	}
	administrator := createAdult("Eligibility administrator", time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), true)
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: administrator.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	enableGuardianAuthorityTestPolicy(t, ctx, pool, administrator.ID)

	key := []byte("eligibility-guardian-key-32-bytes!")
	store := PostgresGuardianDependentStore{Pool: pool, Key: key}
	authority := PostgresGuardianAuthorityStore{DB: pool}
	baseInput := GuardianDependentInput{DateOfBirth: time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC), ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("e", 64)}

	inactive := createAdult("Inactive guardian", time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), false)
	underage := createAdult("Underage applicant", time.Now().UTC().AddDate(-16, 0, 0), true)
	for _, applicant := range []dbgen.CreateAdultUserRow{inactive, underage} {
		token := "applicant-invitation-" + uuid.NewString()
		if _, err := authority.IssueInvitation(ctx, administrator.ID, *applicant.Email, guardianInvitationDigest(key, token)); err != nil {
			t.Fatal(err)
		}
		input := baseInput
		input.GuardianID, input.Name, input.InvitationToken = applicant.ID, "Rejected applicant "+uuid.NewString(), token
		if err := store.CreateDependent(ctx, input); !errors.Is(err, ErrGuardianApplicantIneligible) {
			t.Fatalf("applicant %s error=%v", applicant.ID, err)
		}
	}

	seasonID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Eligibility boundary season',CURRENT_DATE-365,CURRENT_DATE+365)`, seasonID, "eligibility-"+seasonID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	for _, membership := range []struct {
		label    string
		startsOn string
		endsOn   string
	}{
		{label: "Future membership guardian", startsOn: "CURRENT_DATE+1", endsOn: "NULL"},
		{label: "Expired membership guardian", startsOn: "CURRENT_DATE-10", endsOn: "CURRENT_DATE-1"},
	} {
		applicant := createAdult(membership.label, time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), true)
		statement := `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) SELECT $1,$2,id,` + membership.startsOn + `,` + membership.endsOn + ` FROM programmes WHERE code='Leisure'`
		if _, err := pool.Exec(ctx, statement, applicant.ID, seasonID); err != nil {
			t.Fatal(err)
		}
		input := baseInput
		input.GuardianID, input.Name = applicant.ID, "Rejected membership "+uuid.NewString()
		if err := store.CreateDependent(ctx, input); !errors.Is(err, ErrGuardianApplicantIneligible) {
			t.Fatalf("%s error=%v", membership.label, err)
		}
	}

	withoutMembership := createAdult("No membership guardian", time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), true)
	input := baseInput
	input.GuardianID, input.Name = withoutMembership.ID, "Rejected no membership "+uuid.NewString()
	if err := store.CreateDependent(ctx, input); !errors.Is(err, ErrGuardianApplicantIneligible) {
		t.Fatalf("no membership or invitation error=%v", err)
	}

	for _, invitationState := range []string{"expired", "revoked"} {
		applicant := createAdult(strings.Title(invitationState)+" invitation guardian", time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), true)
		token := invitationState + "-invitation-" + uuid.NewString()
		issued, err := authority.IssueInvitation(ctx, administrator.ID, *applicant.Email, guardianInvitationDigest(key, token))
		if err != nil {
			t.Fatal(err)
		}
		if invitationState == "expired" {
			if _, err := pool.Exec(ctx, `UPDATE guardian_authority_invitations SET issued_at=clock_timestamp()-interval '31 days',expires_at=clock_timestamp()-interval '1 day' WHERE public_ref=$1`, issued.Reference); err != nil {
				t.Fatal(err)
			}
		} else if err := authority.RevokeInvitation(ctx, administrator.ID, issued.Reference); err != nil {
			t.Fatal(err)
		}
		input := baseInput
		input.GuardianID, input.Name, input.InvitationToken = applicant.ID, "Rejected "+invitationState+" invitation "+uuid.NewString(), token
		if err := store.CreateDependent(ctx, input); !errors.Is(err, ErrGuardianInvitationInvalid) {
			t.Fatalf("%s invitation error=%v", invitationState, err)
		}
	}
	if _, err := queries.PruneGuardianApplicationRateEvents(ctx); err != nil {
		t.Fatal(err)
	}
	var retainedTerminalEmails int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM guardian_authority_invitations
		WHERE invited_email IS NOT NULL AND (consumed_at IS NOT NULL OR revoked_at IS NOT NULL OR expires_at<=clock_timestamp())`).Scan(&retainedTerminalEmails); err != nil || retainedTerminalEmails != 0 {
		t.Fatalf("terminal invitations retained emails=%d err=%v", retainedTerminalEmails, err)
	}

	var partialRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE name LIKE 'Rejected %'`).Scan(&partialRows); err != nil || partialRows != 0 {
		t.Fatalf("rejected cases left partial users=%d err=%v", partialRows, err)
	}
}

func TestGuardianSubmissionRateLimitSerializesConcurrentAttempts(t *testing.T) {
	ctx, pool := integrationPool(t)
	key := []byte("fedcba9876543210fedcba9876543210")
	store := PostgresGuardianDependentStore{Pool: pool, Key: key}
	actor := uuid.New()
	ip := ptrAddr(netip.MustParseAddr("198.51.100.44"))
	results := make(chan error, 20)
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() { defer group.Done(); results <- store.ReserveAttempt(ctx, actor, ip, "SUBMISSION") }()
	}
	group.Wait()
	close(results)
	var accepted, limited int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrGuardianApplicationLimited):
			limited++
		default:
			t.Fatalf("unexpected rate error: %v", err)
		}
	}
	if accepted != 10 || limited != 10 {
		t.Fatalf("accepted=%d limited=%d", accepted, limited)
	}
	accountDigest := guardianApplicationDigest(key, "account", actor.String())
	networkDigest := guardianApplicationDigest(key, "network", guardianApplicationNetworkBucket(ip))
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM guardian_application_rate_events WHERE bucket_digest IN($1,$2)`, accountDigest, networkDigest).Scan(&count); err != nil || count != 20 {
		t.Fatalf("pseudonymous evidence count=%d err=%v", count, err)
	}
}

func TestGuardianSubmissionSharedNetworkBoundarySerializesOneHundredAndOneAccounts(t *testing.T) {
	ctx, pool := integrationPool(t)
	key := []byte("shared-network-guardian-key-32byte")
	store := PostgresGuardianDependentStore{Pool: pool, Key: key}
	ip := ptrAddr(netip.MustParseAddr("203.0.113.101"))
	results := make(chan error, 101)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 101 {
		actor := uuid.New()
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- store.ReserveAttempt(context.Background(), actor, ip, "SUBMISSION")
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var accepted, limited int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrGuardianApplicationLimited):
			limited++
		default:
			t.Fatalf("unexpected shared-network rate error: %v", err)
		}
	}
	if accepted != 100 || limited != 1 {
		t.Fatalf("accepted=%d limited=%d", accepted, limited)
	}
	networkDigest := guardianApplicationDigest(key, "network", guardianApplicationNetworkBucket(ip))
	var networkEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM guardian_application_rate_events WHERE bucket_kind='SUBMISSION_NETWORK' AND bucket_digest=$1`, networkDigest).Scan(&networkEvents); err != nil || networkEvents != 100 {
		t.Fatalf("network evidence count=%d err=%v", networkEvents, err)
	}
}

func TestPostgresGuardianAuthorityStoreCoversAdministratorReviewLifecycle(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	createAdult := func(name string) dbgen.CreateAdultUserRow {
		email := strings.ToLower(strings.ReplaceAll(name, " ", "-")) + "-" + uuid.NewString() + "@example.test"
		account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: name, Email: &email, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		return account
	}
	administrator := createAdult("Authority admin")
	verifier := createAdult("Authority verifier")
	guardian := createAdult("Authority guardian")
	makeGuardianApplicantEligible(t, ctx, pool, guardian.ID)
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: administrator.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: verifier.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	policy := enableGuardianAuthorityTestPolicy(t, ctx, pool, administrator.ID)
	if _, err := pool.Exec(ctx, `SELECT guardian_authority_grant_verifier($1,$2)`, administrator.ID, verifier.ID); err != nil {
		t.Fatal(err)
	}
	dependentStore := PostgresGuardianDependentStore{Pool: pool, Key: []byte("0123456789abcdef0123456789abcdef")}
	if err := dependentStore.CreateDependent(ctx, GuardianDependentInput{GuardianID: guardian.ID, Name: "Authority subject", DateOfBirth: time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("d", 64)}); err != nil {
		t.Fatal(err)
	}
	store := PostgresGuardianAuthorityStore{DB: pool}
	available, err := store.PolicyAvailable(ctx)
	if err != nil || !available {
		t.Fatalf("policy available=%t err=%v", available, err)
	}
	evidenceTypes, err := store.EvidenceTypes(ctx)
	if err != nil || len(evidenceTypes) != 1 || evidenceTypes[0].Code != "TEST_EVIDENCE" {
		t.Fatalf("evidence types=%#v err=%v", evidenceTypes, err)
	}
	reasons, err := store.ReasonCodes(ctx)
	if err != nil || len(reasons) == 0 {
		t.Fatalf("reason codes=%#v err=%v", reasons, err)
	}
	pending, err := store.ListPending(ctx, verifier.ID, 100, 0)
	if err != nil {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	var relationship GuardianAuthorityRelationship
	for _, candidate := range pending {
		if candidate.GuardianID == guardian.ID {
			relationship = candidate
			break
		}
	}
	if relationship.Reference == uuid.Nil {
		t.Fatalf("created relationship missing from pending queue: %#v", pending)
	}
	detail, err := store.GetForVerifier(ctx, relationship.Reference, verifier.ID)
	if err != nil || detail.StoredState != "PENDING" || detail.GuardianName == "" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if err := store.Transition(ctx, GuardianAuthorityTransitionInput{Reference: detail.Reference, ActorID: verifier.ID, ExpectedVersion: detail.Version, Action: "APPROVE", EvidenceCategory: "COURT_OR_LEGAL_AUTHORITY", ReasonCode: "RELATIONSHIP_CONFIRMED"}); err != nil {
		t.Fatal(err)
	}
	relationships, err := store.ListForGuardian(ctx, guardian.ID, 10)
	if err != nil || len(relationships) != 1 || relationships[0].State != "VERIFIED" || relationships[0].VerifiedUntil == nil || relationships[0].ReviewDueAt == nil {
		t.Fatalf("relationships=%#v err=%v policy=%s", relationships, err, policy)
	}
	if err := store.Transition(ctx, GuardianAuthorityTransitionInput{Reference: detail.Reference, ActorID: verifier.ID, ExpectedVersion: 2, Action: "SUSPEND", EvidenceCategory: "CLUB_REGISTRATION_RECORD", ReasonCode: "CONFLICT"}); err != nil {
		t.Fatal(err)
	}
}

func TestMinorCredentialRequiresCurrentGuardianAndWritesAudit(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	guardianEmail := "credential-guardian-" + uuid.NewString() + "@example.test"
	actorEmail := "credential-admin-" + uuid.NewString() + "@example.test"
	guardian, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Guardião de credencial", Email: &guardianEmail, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Administrador de credencial", Email: &actorEmail, PasswordHash: integrationStringPtr("hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM minor_credential_audit WHERE guardian_user_id = $1`, guardian.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE guardian_id = $1`, guardian.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1, $2)`, guardian.ID, actor.ID)
	})
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: actor.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	makeGuardianApplicantEligible(t, ctx, pool, guardian.ID)
	policy := enableGuardianAuthorityTestPolicy(t, ctx, pool, actor.ID)
	minor, err := queries.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{Name: "Menor com credencial", GuardianID: guardian.ID, DateOfBirth: pgtype.Date{Time: time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	verifyGuardianAuthorityTestRelationship(t, ctx, pool, guardian.ID, minor.ID, actor.ID, policy)
	loginID, passwordHash := "CFC-TEST0001", "hash"
	if _, err := queries.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{MinorLoginID: &loginID, PasswordHash: &passwordHash, MinorUserID: minor.ID, GuardianUserID: uuid.New(), ActorUserID: actor.ID, Action: "ISSUED"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("wrong guardian error = %v", err)
	}
	if _, err := queries.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{MinorLoginID: &loginID, PasswordHash: &passwordHash, MinorUserID: minor.ID, GuardianUserID: guardian.ID, ActorUserID: actor.ID, Action: "ISSUED"}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.GetActiveDependentByLoginID(ctx, &loginID); err != nil {
		t.Fatalf("issued minor cannot log in: %v", err)
	}
	var credentialVersion int64
	if err := pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id = $1`, minor.ID).Scan(&credentialVersion); err != nil || credentialVersion != 2 {
		t.Fatalf("issued credential version = %d, err = %v", credentialVersion, err)
	}
	recoveredHash := "recovered-hash"
	if _, err := queries.IssueMinorCredential(ctx, dbgen.IssueMinorCredentialParams{MinorLoginID: &loginID, PasswordHash: &recoveredHash, MinorUserID: minor.ID, GuardianUserID: guardian.ID, ActorUserID: actor.ID, Action: "RECOVERED"}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT credential_version FROM users WHERE id = $1`, minor.ID).Scan(&credentialVersion); err != nil || credentialVersion != 3 {
		t.Fatalf("recovered credential version = %d, err = %v", credentialVersion, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM minor_credential_audit WHERE minor_user_id = $1`, minor.ID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("audit count = %d, err = %v", auditCount, err)
	}
}

func TestAdministratorPasswordReplacementRevokesOlderSessions(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	email := "credential-admin-reset-" + uuid.NewString() + "@example.test"
	account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Administrador direto", Email: &email, PasswordHash: integrationStringPtr("old-hash"), DateOfBirth: pgtype.Date{Time: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, account.ID) })
	if err := queries.GrantPlatformRoleByCode(ctx, dbgen.GrantPlatformRoleByCodeParams{UserID: account.ID, RoleCode: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	newHash := "new-hash"
	if err := queries.SetUserPasswordHash(ctx, dbgen.SetUserPasswordHashParams{ID: account.ID, PasswordHash: &newHash}); err != nil {
		t.Fatal(err)
	}
	var storedHash string
	var credentialVersion int64
	if err := pool.QueryRow(ctx, `SELECT password_hash, credential_version FROM users WHERE id = $1`, account.ID).Scan(&storedHash, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if storedHash != newHash || credentialVersion != 2 {
		t.Fatalf("administrator credential = %q, version %d", storedHash, credentialVersion)
	}
}

func TestPasswordResetFlowRejectsPreResetSession(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	email := "session-reset-" + uuid.NewString() + "@example.test"
	oldPassword, newPassword := "old password 7", "new password 8"
	oldHash, err := bcrypt.GenerateFromPassword([]byte(oldPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	account, err := queries.CreateAdultUser(ctx, dbgen.CreateAdultUserParams{Name: "Sessão anterior", Email: &email, PasswordHash: integrationStringPtr(string(oldHash)), DateOfBirth: pgtype.Date{Time: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, account.ID) })

	sessions := scs.New()
	auth := Auth{Users: queries, Sessions: sessions}
	protected := sessions.LoadAndSave(auth.Load(auth.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))))
	seed := httptest.NewRecorder()
	sessions.LoadAndSave(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sessions.Put(r.Context(), "user_id", account.ID.String())
		sessions.Put(r.Context(), "credential_version", account.CredentialVersion)
	})).ServeHTTP(seed, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := seed.Result().Cookies()[0]
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("pre-reset session status = %d", response.Code)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	rawToken := bytes.Repeat([]byte{0x42}, passwordreset.TokenBytes)
	service := passwordreset.Service{
		Store: queries, BaseURL: "https://mycfc.example", Key: []byte("0123456789abcdef0123456789abcdef"),
		Rand: bytes.NewReader(append(append([]byte{}, rawToken...), bytes.Repeat([]byte{0x24}, 12)...)), Now: func() time.Time { return now },
	}
	if _, err := service.Issue(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	var outboxStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM email_outbox outbox JOIN password_reset_tokens token ON token.id = outbox.password_reset_token_id WHERE token.user_id = $1`, account.ID).Scan(&outboxStatus); err != nil || outboxStatus != "PENDING" {
		t.Fatalf("outbox status = %q, err = %v", outboxStatus, err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	if _, err := service.Consume(ctx, token, newPassword); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?next=%2Fprotected" {
		t.Fatalf("post-reset session response = %d %q", response.Code, response.Header().Get("Location"))
	}
	var storedHash string
	var credentialVersion int64
	if err := pool.QueryRow(ctx, `SELECT password_hash, credential_version FROM users WHERE id = $1`, account.ID).Scan(&storedHash, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if credentialVersion != account.CredentialVersion+1 || bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(newPassword)) != nil || bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(oldPassword)) == nil {
		t.Fatalf("password reset did not replace credential at version %d", credentialVersion)
	}
}

func TestPostgresProfileStoreEnforcesGuardianConsentConflictAndAudit(t *testing.T) {
	ctx, pool := integrationPool(t)
	guardianID, dependentID, unrelatedID := uuid.New(), uuid.New(), uuid.New()
	for id, email := range map[uuid.UUID]string{guardianID: "guardian-" + uuid.NewString() + "@example.test", unrelatedID: "unrelated-" + uuid.NewString() + "@example.test"} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, 'Adulto perfil', $2, 'hash', '1990-01-01')`, id, email); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, is_dependent, date_of_birth) VALUES ($1, 'Menor perfil', true, '2014-01-01')`, dependentID); err != nil {
		t.Fatal(err)
	}
	policy := enableGuardianAuthorityTestPolicy(t, ctx, pool, unrelatedID)
	verifyGuardianAuthorityTestRelationship(t, ctx, pool, guardianID, dependentID, unrelatedID, policy)
	store := PostgresProfileStore{Pool: pool}
	profile, err := store.View(ctx, guardianID, dependentID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.View(ctx, unrelatedID, dependentID, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unrelated view error = %v", err)
	}
	params := dbgen.UpdateMemberProfileParams{EmergencyContactName: "Responsável", EmergencyContactRelationship: "Tutor", EmergencyContactPhone: "+351 910 000 000", MedicalDeclaration: "UNKNOWN", ExpectedUpdatedAt: profile.UpdatedAt}
	if err := store.Update(ctx, ProfileUpdate{ActorID: guardianID, SubjectID: dependentID, Profile: params, ChangedFields: []string{"emergency_contact_name", "emergency_contact_relationship", "emergency_contact_phone", "medical_declaration"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, ProfileUpdate{ActorID: guardianID, SubjectID: dependentID, Profile: params}); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	current, err := store.View(ctx, guardianID, dependentID, false)
	if err != nil {
		t.Fatal(err)
	}
	healthParams := dbgen.UpdateMemberProfileParams{MedicalDeclaration: "PROVIDED", Allergies: "Pólen", MedicalNotes: "Levar medicação", ExpectedUpdatedAt: current.UpdatedAt}
	healthUpdate := ProfileUpdate{ActorID: guardianID, SubjectID: dependentID, Profile: healthParams, ChangedFields: []string{"medical_declaration", "allergies", "medical_notes"}, HealthVersion: "health-v1", HealthSHA256: strings.Repeat("a", 64), AcceptHealthConsent: true}
	if err := store.Update(ctx, healthUpdate); err != nil {
		t.Fatalf("guardian update with ignored health fields: %v", err)
	}
	var dependentMedicalDeclaration, dependentAllergies, dependentMedicalNotes string
	if err := pool.QueryRow(ctx, `SELECT medical_declaration, allergies, medical_notes FROM member_profiles WHERE user_id = $1`, dependentID).Scan(&dependentMedicalDeclaration, &dependentAllergies, &dependentMedicalNotes); err != nil {
		t.Fatal(err)
	}
	if dependentMedicalDeclaration != current.MedicalDeclaration || dependentAllergies != current.Allergies || dependentMedicalNotes != current.MedicalNotes {
		t.Fatalf("guardian changed dependent health data: declaration=%q allergies=%q notes=%q", dependentMedicalDeclaration, dependentAllergies, dependentMedicalNotes)
	}
	var guardianChangedFields []string
	if err := pool.QueryRow(ctx, `SELECT changed_fields FROM member_profile_audit_events WHERE actor_user_id = $1 AND subject_user_id = $2 AND action = 'PROFILE_UPDATED' ORDER BY occurred_at DESC LIMIT 1`, guardianID, dependentID).Scan(&guardianChangedFields); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(guardianChangedFields, isHealthField) {
		t.Fatalf("guardian health fields recorded in audit: %v", guardianChangedFields)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member_profiles (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, guardianID); err != nil {
		t.Fatal(err)
	}
	current, err = store.View(ctx, guardianID, guardianID, false)
	if err != nil {
		t.Fatal(err)
	}
	healthParams.ExpectedUpdatedAt = current.UpdatedAt
	healthUpdate = ProfileUpdate{ActorID: guardianID, SubjectID: guardianID, Profile: healthParams, ChangedFields: []string{"medical_declaration", "allergies", "medical_notes"}, HealthVersion: "health-v1", HealthSHA256: strings.Repeat("a", 64)}
	if err := store.Update(ctx, healthUpdate); !errors.Is(err, ErrHealthConsentRequired) {
		t.Fatalf("adult health update without consent error = %v", err)
	}
	healthUpdate.AcceptHealthConsent = true
	if err := store.Update(ctx, healthUpdate); err != nil {
		t.Fatalf("adult health update with consent: %v", err)
	}
	current, err = store.View(ctx, guardianID, guardianID, false)
	if err != nil {
		t.Fatal(err)
	}
	healthParams = dbgen.UpdateMemberProfileParams{MedicalDeclaration: "PROVIDED", Allergies: "Pólen e ácaros", MedicalNotes: "Levar medicação", ExpectedUpdatedAt: current.UpdatedAt}
	if err := store.Update(ctx, ProfileUpdate{ActorID: guardianID, SubjectID: guardianID, Profile: healthParams, ChangedFields: []string{"allergies"}, HealthVersion: "health-v1", HealthSHA256: strings.Repeat("b", 64)}); !errors.Is(err, ErrHealthConsentRequired) {
		t.Fatalf("health update with mismatched document hash error = %v", err)
	}
	healthParams.Allergies = "Pólen"
	healthParams.MedicalNotes = ""
	if err := store.Update(ctx, ProfileUpdate{ActorID: unrelatedID, SubjectID: guardianID, IsAdmin: true, Profile: healthParams, ChangedFields: []string{"medical_notes"}, HealthVersion: "health-v1", HealthSHA256: strings.Repeat("a", 64)}); err != nil {
		t.Fatalf("administrator could not minimize legacy health data: %v", err)
	}
	consentVersion, consentSHA := "profile-v1", strings.Repeat("c", 64)
	key := "profiles/integration/photo.png"
	upload := testPreparedUpload(key, "image/png", 128)
	if _, err := pool.Exec(ctx, `SELECT privacy_upload_begin($1,$2,$3,'MEMBER_PROFILE_PHOTO',$2,'private-media','image/png',128,$4)`, upload.IntentID, guardianID, guardianID, upload.HoldToken); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT privacy_upload_finalize($1,$2,'x25519-aes256gcm-hkdfsha256/upload-intent-v1','X25519-HKDF-SHA256-AES-256-GCM','upload-key-test',$3,$4,$5,'upload-digest-test',$6,digest(convert_to($7,'UTF8'),'sha256'))`, upload.IntentID, upload.HoldToken, bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 12), bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), key); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT privacy_upload_confirm_put($1,$2)`, upload.IntentID, upload.HoldToken); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SavePhoto(ctx, ProfilePhotoUpdate{ActorID: guardianID, SubjectID: guardianID, Upload: upload, ConsentVersion: consentVersion, ConsentSHA256: consentSHA, AcceptConsent: true, UserAgent: "integration-test"}); err != nil {
		t.Fatal(err)
	}
	avatar, err := store.Avatar(ctx, dbgen.GetMemberAvatarParams{UserID: guardianID, DocumentVersion: consentVersion, DocumentSha256: consentSHA})
	if err != nil || avatar.PhotoObjectKey == nil || *avatar.PhotoObjectKey != key || !avatar.ConsentCurrent {
		t.Fatalf("avatar = %#v, err = %v", avatar, err)
	}
	if _, err := store.RemovePhoto(ctx, guardianID, guardianID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET date_of_birth = '2000-01-01' WHERE id = $1`, dependentID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.View(ctx, guardianID, dependentID, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("guardian retained access to adult dependant: %v", err)
	}
	var actions []string
	rows, err := pool.Query(ctx, `SELECT action FROM member_profile_audit_events WHERE subject_user_id = $1 ORDER BY occurred_at, id`, dependentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	joined := strings.Join(actions, ",")
	for _, action := range []string{"SENSITIVE_VIEW", "PROFILE_UPDATED"} {
		if !strings.Contains(joined, action) {
			t.Fatalf("audit actions = %v, missing %s", actions, action)
		}
	}
}

func TestPostgresTrainingPublicationsPreservePrivateRevisionLineage(t *testing.T) {
	ctx, pool := integrationPool(t)
	queries := dbgen.New(pool)
	store := PostgresStructuredTrainingStore{Pool: pool}
	programme, err := queries.GetProgrammeByCode(ctx, "Competition")
	if err != nil {
		t.Fatal(err)
	}
	actorID, guardianID, athleteID, outsiderID, seasonID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	users := []struct {
		id, guardian uuid.UUID
		name, birth  string
	}{
		{actorID, uuid.Nil, "Treinador publicação", "1985-01-01"},
		{guardianID, uuid.Nil, "Tutor publicação", "1980-01-01"},
		{athleteID, guardianID, "Atleta menor publicação", "2012-01-01"},
		{outsiderID, uuid.Nil, "Pessoa sem acesso", "1990-01-01"},
	}
	for _, user := range users {
		if user.guardian != uuid.Nil {
			if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, date_of_birth, is_dependent) VALUES ($1, $2, $3, true)`, user.id, user.name, user.birth); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', $4)`, user.id, user.name, uuid.NewString()+"@example.test", user.birth); err != nil {
				t.Fatal(err)
			}
		}
	}
	policy := enableGuardianAuthorityTestPolicy(t, ctx, pool, actorID)
	verifyGuardianAuthorityTestRelationship(t, ctx, pool, guardianID, athleteID, actorID, policy)
	today := time.Now().UTC()
	weekStart := time.Date(today.Year(), today.Month(), today.Day()-((int(today.Weekday())+6)%7), 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `INSERT INTO seasons (id, code, name, starts_on, ends_on) VALUES ($1, $2, 'Época publicação', $3, $4)`, seasonID, "PUB_"+uuid.NewString()[:8], weekStart.AddDate(0, -1, 0), weekStart.AddDate(0, 2, 0)); err != nil {
		t.Fatal(err)
	}
	membership, err := queries.CreateUserMembership(ctx, dbgen.CreateUserMembershipParams{UserID: athleteID, SeasonID: seasonID, ProgrammeID: programme.ID, StartsOn: pgtype.Date{Time: weekStart.AddDate(0, -1, 0), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup(ctx, StructuredTrainingGroupInput{Params: dbgen.CreateStructuredTrainingGroupParams{Name: "Grupo publicação " + uuid.NewString()[:8], ProgrammeID: &programme.ID, CreatedByID: actorID}, MembershipIDs: []uuid.UUID{membership.ID}})
	if err != nil {
		t.Fatal(err)
	}
	week, err := queries.CreateStructuredTrainingWeek(ctx, dbgen.CreateStructuredTrainingWeekParams{GroupID: group.ID, Title: "Semana publicada", WeekStart: pgtype.Date{Time: weekStart, Valid: true}, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	startsAt := weekStart.Add(10 * time.Hour)
	session, err := queries.CreateStructuredTrainingSession(ctx, dbgen.CreateStructuredTrainingSessionParams{PlanID: week.ID, Title: "Água publicada", StartsAt: pgtype.Timestamptz{Time: startsAt, Valid: true}, EndsAt: pgtype.Timestamptz{Time: startsAt.Add(time.Hour), Valid: true}, EntryKind: dbgen.TrainingEntryKindTRAINING, CreatedByID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_grants (user_id, capability, programme_id, granted_by_id) VALUES ($1, 'COACH', $2, $1)`, actorID, programme.ID); err != nil {
		t.Fatal(err)
	}
	readSource := func() pgtype.Timestamptz {
		var value time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM training_plans WHERE id = $1`, week.ID).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return pgtype.Timestamptz{Time: value, Valid: true}
	}
	publish := func(source pgtype.Timestamptz, summary string, snapshot []byte) dbgen.TrainingPlanPublication {
		sum := sha256.Sum256(snapshot)
		publication, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: week.ID, SourceUpdatedAt: source, ChangeSummary: summary, PublishedByID: actorID, Prescriptions: []StructuredPrescriptionInput{{SessionID: session.ID, MembershipID: membership.ID, AthleteUserID: athleteID, Snapshot: snapshot, SnapshotSHA256: hex.EncodeToString(sum[:])}}})
		if err != nil {
			t.Fatal(err)
		}
		return publication
	}
	snapshot1 := []byte(`{"schema_version":1,"session":{"title":"Versão um"}}`)
	publication1 := publish(readSource(), "Publicação inicial", snapshot1)
	if publication1.Revision != 1 {
		t.Fatalf("first revision = %d", publication1.Revision)
	}
	duration, exertion, feeling, note := int32(64), int16(7), int16(4), "Boa resposta à carga"
	if rows, err := queries.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{
		SessionID: session.ID, UserID: athleteID, Status: dbgen.TrainingOutcomeStatusCOMPLETED,
		ActualDurationMinutes: &duration, PerceivedExertion: &exertion, RecoveryFeeling: &feeling, PerceptionNote: &note,
	}); err != nil || rows != 1 {
		t.Fatalf("save outcome rows=%d err=%v", rows, err)
	}
	for _, forbiddenActor := range []uuid.UUID{guardianID, actorID, outsiderID} {
		if rows, err := queries.SaveTrainingSessionOutcome(ctx, dbgen.SaveTrainingSessionOutcomeParams{SessionID: session.ID, UserID: forbiddenActor, Status: dbgen.TrainingOutcomeStatusCOMPLETED, PerceivedExertion: &exertion}); err != nil || rows != 0 {
			t.Fatalf("non-athlete %s saved feedback rows=%d err=%v", forbiddenActor, rows, err)
		}
	}
	var firstPrescriptionID, outcomePrescriptionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM training_prescriptions WHERE publication_id = $1`, publication1.ID).Scan(&firstPrescriptionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT prescription_id FROM training_session_outcomes WHERE session_id = $1 AND user_id = $2`, session.ID, athleteID).Scan(&outcomePrescriptionID); err != nil || outcomePrescriptionID != firstPrescriptionID {
		t.Fatalf("outcome prescription=%s want=%s err=%v", outcomePrescriptionID, firstPrescriptionID, err)
	}
	correctedDuration, correctedExertion, correctedFeeling, correctedNote := int32(69), int16(8), int16(3), "Corrente mais forte no regresso"
	if rows, err := queries.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{
		SessionID: session.ID, UserID: athleteID, ExpectedVersion: 1, ActualDurationMinutes: &correctedDuration,
		PerceivedExertion: &correctedExertion, RecoveryFeeling: &correctedFeeling, PerceptionNote: &correctedNote,
	}); err != nil || rows != 1 {
		t.Fatalf("correct feedback rows=%d err=%v", rows, err)
	}
	if rows, err := queries.UpdateOwnCompletedSessionFeedback(ctx, dbgen.UpdateOwnCompletedSessionFeedbackParams{SessionID: session.ID, UserID: athleteID, ExpectedVersion: 1, PerceivedExertion: &exertion}); err != nil || rows != 0 {
		t.Fatalf("stale feedback rows=%d err=%v", rows, err)
	}
	staleSource := readSource()
	if _, err := pool.Exec(ctx, `UPDATE training_sessions SET title = 'Água republicada' WHERE id = $1`, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: week.ID, SourceUpdatedAt: staleSource, ChangeSummary: "Versão obsoleta", PublishedByID: actorID}); !errors.Is(err, errStructuredTrainingPublicationConflict) {
		t.Fatalf("stale publication err=%v", err)
	}
	snapshot2 := []byte(`{"schema_version":1,"session":{"title":"Versão dois"}}`)
	publication2 := publish(readSource(), "Ajuste da carga", snapshot2)
	if publication2.Revision != 2 || publication2.SupersedesID == nil || *publication2.SupersedesID != publication1.ID {
		t.Fatalf("second publication = %#v", publication2)
	}
	for _, viewer := range []struct {
		id      uuid.UUID
		admin   bool
		minimum int
	}{{athleteID, false, 2}, {guardianID, false, 2}, {actorID, false, 2}, {outsiderID, false, 0}} {
		rows, err := queries.ListTrainingPrescriptionsForViewer(ctx, dbgen.ListTrainingPrescriptionsForViewerParams{UserID: viewer.id, IsAdmin: viewer.admin})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, row := range rows {
			if row.PlanID == week.ID {
				count++
			}
		}
		if count != viewer.minimum {
			t.Fatalf("viewer %s received %d publication rows, want %d", viewer.id, count, viewer.minimum)
		}
	}
	oldRow, err := queries.GetTrainingPrescriptionForViewer(ctx, dbgen.GetTrainingPrescriptionForViewerParams{ID: firstPrescriptionID, UserID: guardianID, IsAdmin: false})
	if err != nil || !strings.Contains(string(oldRow.Snapshot), `"Versão um"`) || oldRow.IsCurrent || oldRow.ActualDurationMinutes == nil || *oldRow.ActualDurationMinutes != correctedDuration || oldRow.PerceivedExertion == nil || *oldRow.PerceivedExertion != correctedExertion || oldRow.RecoveryFeeling == nil || *oldRow.RecoveryFeeling != correctedFeeling || oldRow.PerceptionNote == nil || *oldRow.PerceptionNote != correctedNote || oldRow.OutcomeVersion != 2 {
		t.Fatalf("historical snapshot changed or hidden: row=%#v err=%v", oldRow, err)
	}
	var currentPrescriptionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM training_prescriptions WHERE publication_id = $1`, publication2.ID).Scan(&currentPrescriptionID); err != nil {
		t.Fatal(err)
	}
	currentRow, err := queries.GetTrainingPrescriptionForViewer(ctx, dbgen.GetTrainingPrescriptionForViewerParams{ID: currentPrescriptionID, UserID: guardianID, IsAdmin: false})
	if err != nil || currentRow.OutcomeStatus != "" || currentRow.PerceivedExertion != nil {
		t.Fatalf("feedback leaked onto a prescription revision not performed: row=%#v err=%v", currentRow, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE training_prescriptions SET snapshot = '{"changed":true}' WHERE id = $1`, firstPrescriptionID); err == nil {
		t.Fatal("immutable prescription accepted an update")
	}
	beforeMembershipChange := readSource()
	if _, err := pool.Exec(ctx, `UPDATE user_memberships SET ends_on = $2 WHERE id = $1`, membership.ID, weekStart.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	afterMembershipChange := readSource()
	if !afterMembershipChange.Time.After(beforeMembershipChange.Time) {
		t.Fatal("membership eligibility change did not invalidate the publication source version")
	}
	sum := sha256.Sum256(snapshot2)
	if _, err := store.PublishStructuredTrainingPlan(ctx, StructuredPublicationInput{PlanID: week.ID, SourceUpdatedAt: afterMembershipChange, ChangeSummary: "Destinatário já inelegível", PublishedByID: actorID, Prescriptions: []StructuredPrescriptionInput{{SessionID: session.ID, MembershipID: membership.ID, AthleteUserID: athleteID, Snapshot: snapshot2, SnapshotSHA256: hex.EncodeToString(sum[:])}}}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ineligible prescription publication err=%v", err)
	}
	var publicationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_plan_publications WHERE plan_id = $1`, week.ID).Scan(&publicationCount); err != nil || publicationCount != 2 {
		t.Fatalf("ineligible publication did not roll back: count=%d err=%v", publicationCount, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM training_group_members WHERE group_id = $1 AND membership_id = $2`, group.ID, membership.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.GetTrainingPrescriptionForViewer(ctx, dbgen.GetTrainingPrescriptionForViewerParams{ID: firstPrescriptionID, UserID: guardianID, IsAdmin: false}); err != nil {
		t.Fatalf("historical prescription disappeared after group change: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT prescription_id FROM training_session_outcomes WHERE session_id = $1 AND user_id = $2`, session.ID, athleteID).Scan(&outcomePrescriptionID); err != nil || outcomePrescriptionID != firstPrescriptionID {
		t.Fatalf("outcome lineage moved after republish: %s err=%v", outcomePrescriptionID, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1,updated_at=clock_timestamp() WHERE subject_user_id=$1`, athleteID); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.GetTrainingPrescriptionForViewer(ctx, dbgen.GetTrainingPrescriptionForViewerParams{ID: firstPrescriptionID, UserID: guardianID, IsAdmin: false}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("former guardian retained feedback access: %v", err)
	}
}

func enableGuardianAuthorityTestPolicy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, actorID uuid.UUID) string {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS mycfc_meta;
		CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range mycfcdb.EmbeddedMigrationInventory() {
		if _, err := pool.Exec(ctx, `INSERT INTO mycfc_meta.schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id)
		SELECT $1,id FROM platform_roles WHERE code='ADMIN' ON CONFLICT DO NOTHING`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE guardian_application_intake_release SET enabled=false,policy_version=NULL,policy_sha256=NULL,
		approval_sha256=NULL,image_digest=NULL,schema_migration_digest=NULL,enabled_by=NULL,enabled_at=NULL WHERE singleton;
		UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`); err != nil {
		t.Fatal(err)
	}
	version := "handler-test-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO guardian_authority_policies
		(version,evidence_types,reason_codes,validity_days,review_days,adopted_at,adopted_by,enabled,enabled_at,enabled_by)
		VALUES($1,'{TEST_EVIDENCE}','{EVIDENCE_CONFIRMED,EVIDENCE_INSUFFICIENT,AUTHORITY_CHANGED,CONFLICT,VALIDITY_ENDED}',365,180,clock_timestamp(),$2,false,NULL,NULL)`, version, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO guardian_authority_policy_approvals(policy_version,policy_sha256,approval_sha256,approval_contract,approval_canonical,
		expected_database,authorized_operator_actor_ref,controller_role,controller_approval_reference,controller_approved_on,effective_on,review_due_on,
		legal_reviewer_reference,legal_review_reference,legal_reviewed_on,legal_review_conclusion,bound_image_digest,bound_schema_migration_digest,bound_by)
		VALUES($1::text,digest(convert_to($1::text,'UTF8'),'sha256'),digest(convert_to('approval/'||$1::text,'UTF8'),'sha256'),'mycfc/guardian-authority-policy-approval/v1',convert_to('{}','UTF8'),
		current_database(),$2,'CLUB_DIRECTION','integration/controller',CURRENT_DATE,CURRENT_DATE,CURRENT_DATE+365,
		'integration/legal-reviewer','integration/legal-review',CURRENT_DATE,'APPROVED','sha256:'||repeat('a',64),$3,$2)`,
		version, actorID, mycfcdb.EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE guardian_ops.runtime_release_binding SET database_name=current_database(),
		image_digest='sha256:'||repeat('a',64),schema_migration_digest=$1,generation=generation+1,bound_at=clock_timestamp()
		WHERE singleton`, mycfcdb.EmbeddedMigrationDigest()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE guardian_authority_policies SET enabled=true,enabled_at=clock_timestamp(),enabled_by=$2 WHERE version=$1`, version, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE guardian_application_intake_release gate SET enabled=true,policy_version=$1,policy_sha256=approval.policy_sha256,
		approval_sha256=approval.approval_sha256,image_digest=approval.bound_image_digest,
		schema_migration_digest=approval.bound_schema_migration_digest,enabled_by=$2,enabled_at=clock_timestamp()
		FROM guardian_authority_policy_approvals approval WHERE gate.singleton AND approval.policy_version=$1`, version, actorID); err != nil {
		t.Fatal(err)
	}
	return version
}

func makeGuardianApplicantEligible(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	seasonID := uuid.New()
	if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at=clock_timestamp() WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Guardian application test',CURRENT_DATE-1,CURRENT_DATE+1)`, seasonID, "guardian-"+seasonID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) SELECT $1,$2,id,CURRENT_DATE FROM programmes WHERE code='Leisure'`, userID, seasonID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_memberships WHERE season_id=$1;DELETE FROM seasons WHERE id=$1`, seasonID)
	})
}

func verifyGuardianAuthorityTestRelationship(t *testing.T, ctx context.Context, pool *pgxpool.Pool, guardianID, subjectID, verifierID uuid.UUID, policy string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `WITH inserted AS (
		INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state)
		SELECT $1,$2,name,'PENDING' FROM users WHERE id=$2
		ON CONFLICT(subject_user_id) DO NOTHING RETURNING id,created_at)
		INSERT INTO guardian_authority_events(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,occurred_at)
		SELECT id,1,$1,'GUARDIAN','DECLARED',NULL,'PENDING',created_at FROM inserted`, guardianID, subjectID); err != nil {
		t.Fatal(err)
	}
	var relationshipID uuid.UUID
	if err := pool.QueryRow(ctx, `UPDATE guardian_authority_relationships SET state='VERIFIED',version=2,policy_version=$3,
		verified_at=clock_timestamp(),verified_by=$4,verified_until=clock_timestamp()+interval '365 days',
		review_due_at=clock_timestamp()+interval '180 days',updated_at=clock_timestamp()
		WHERE guardian_user_id=$1 AND subject_user_id=$2 RETURNING id`, guardianID, subjectID, policy, verifierID).Scan(&relationshipID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO guardian_authority_events
		(relationship_id,relationship_version,actor_ref,actor_role,action,from_state,to_state,policy_version,evidence_type,evidence_reference,evidence_sha256,occurred_at,verified_until,review_due_at)
		SELECT id,2,$2,'VERIFIER','VERIFIED','PENDING','VERIFIED',$3,'TEST_EVIDENCE','handler-fixture/'||id::text,digest(id::text,'sha256'),verified_at,verified_until,review_due_at
		FROM guardian_authority_relationships WHERE id=$1`, relationshipID, verifierID, policy); err != nil {
		t.Fatal(err)
	}
}

func integrationPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

func ptrAddr(value netip.Addr) *netip.Addr { return &value }

func integrationStringPtr(value string) *string { return &value }
