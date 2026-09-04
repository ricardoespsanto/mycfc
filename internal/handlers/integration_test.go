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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/emailverification"
	"github.com/cfcoimbra/mycfc/internal/passwordreset"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestPostgresRegistrationStorePersistsConsentsAtomically(t *testing.T) {
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
		ImageVersion: "test-v1", ImageSHA256: strings.Repeat("b", 64),
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
	if len(consents) != 2 {
		t.Fatalf("consent count = %d, want 2", len(consents))
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
	input.ImageVersion = strings.Repeat("x", 41)
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
	today := time.Now().In(time.Local)
	weekStart := time.Date(today.Year(), today.Month(), today.Day()-((int(today.Weekday())+6)%7), 0, 0, 0, 0, time.Local)
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
	today := time.Now().In(time.Local)
	weekStart := time.Date(today.Year(), today.Month(), today.Day()-((int(today.Weekday())+6)%7), 0, 0, 0, 0, time.Local)
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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE guardian_id = $1`, guardian.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, guardian.ID)
	})

	store := PostgresGuardianDependentStore{Pool: pool}
	input := GuardianDependentInput{
		GuardianID: guardian.ID, Name: "Menor de integração",
		DateOfBirth:           time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		ResponsibilityVersion: "test-v1", ResponsibilitySHA256: strings.Repeat("c", 64),
		IP: ptrAddr(netip.MustParseAddr("192.0.2.2")), UserAgent: "integration-test",
	}
	if err := store.CreateDependent(ctx, input); err != nil {
		t.Fatal(err)
	}
	dependents, err := queries.ListDependentsByGuardian(ctx, dbgen.ListDependentsByGuardianParams{GuardianID: &guardian.ID, RowLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 1 || dependents[0].GuardianID == nil || *dependents[0].GuardianID != guardian.ID {
		t.Fatalf("dependents = %#v", dependents)
	}
	consents, err := queries.ListConsentFormsForUser(ctx, dbgen.ListConsentFormsForUserParams{UserID: dependents[0].ID, RowLimit: 10})
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
	minor, err := queries.CreateDependentUser(ctx, dbgen.CreateDependentUserParams{Name: "Menor com credencial", GuardianID: &guardian.ID, DateOfBirth: pgtype.Date{Time: time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, guardian_id, is_dependent, date_of_birth) VALUES ($1, 'Menor perfil', $2, true, '2014-01-01')`, dependentID, guardianID); err != nil {
		t.Fatal(err)
	}
	store := PostgresProfileStore{Pool: pool}
	profile, err := store.View(ctx, guardianID, dependentID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.View(ctx, unrelatedID, dependentID, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unrelated view error = %v", err)
	}
	params := dbgen.UpdateMemberProfileParams{EmergencyContactName: "Responsável", EmergencyContactRelationship: "Tutor", EmergencyContactPhone: "+351 910 000 000", MedicalDeclaration: "NONE_KNOWN", ExpectedUpdatedAt: profile.UpdatedAt}
	if err := store.Update(ctx, ProfileUpdate{ActorID: guardianID, SubjectID: dependentID, Profile: params, ChangedFields: []string{"emergency_contact_name", "emergency_contact_relationship", "emergency_contact_phone", "medical_declaration"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, ProfileUpdate{ActorID: guardianID, SubjectID: dependentID, Profile: params}); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	consentVersion, consentSHA := "profile-v1", strings.Repeat("c", 64)
	key := "profiles/integration/photo.png"
	if _, err := store.SavePhoto(ctx, ProfilePhotoUpdate{ActorID: guardianID, SubjectID: dependentID, ObjectKey: key, ContentType: "image/png", Size: 128, ConsentVersion: consentVersion, ConsentSHA256: consentSHA, AcceptConsent: true, UserAgent: "integration-test"}); err != nil {
		t.Fatal(err)
	}
	avatar, err := store.Avatar(ctx, dbgen.GetMemberAvatarParams{UserID: dependentID, DocumentVersion: consentVersion, DocumentSha256: consentSHA})
	if err != nil || avatar.PhotoObjectKey == nil || *avatar.PhotoObjectKey != key || !avatar.ConsentCurrent {
		t.Fatalf("avatar = %#v, err = %v", avatar, err)
	}
	if _, err := store.RemovePhoto(ctx, guardianID, dependentID, false); err != nil {
		t.Fatal(err)
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
	for _, action := range []string{"SENSITIVE_VIEW", "PROFILE_UPDATED", "PHOTO_UPLOADED", "PHOTO_REMOVED"} {
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
			if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, date_of_birth, guardian_id, is_dependent) VALUES ($1, $2, $3, $4, true)`, user.id, user.name, user.birth, user.guardian); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, date_of_birth) VALUES ($1, $2, $3, 'hash', $4)`, user.id, user.name, uuid.NewString()+"@example.test", user.birth); err != nil {
				t.Fatal(err)
			}
		}
	}
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
	if _, err := pool.Exec(ctx, `UPDATE users SET guardian_id = $2 WHERE id = $1`, athleteID, outsiderID); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.GetTrainingPrescriptionForViewer(ctx, dbgen.GetTrainingPrescriptionForViewerParams{ID: firstPrescriptionID, UserID: guardianID, IsAdmin: false}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("former guardian retained feedback access: %v", err)
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
