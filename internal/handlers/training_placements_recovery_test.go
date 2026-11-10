package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func cancellationFormForTest(s *placementHTTPStoreFake) url.Values {
	return (trainingPlacementCancellation{
		ID: uuid.New(), ChangeID: s.command.ID, SubjectID: s.command.SubjectID,
		ProgrammeID: s.command.ProgrammeID, SeasonID: s.command.SeasonID,
		ContextGroupID: s.command.ContextGroupID, ExpectedRevision: s.cancellation.Change.Revision,
	}).values()
}

func TestPlacementCancellationRecoveryRespectsCurrentAuthority(t *testing.T) {
	diagnostic := errors.New("synthetic private diagnostic")
	for _, tc := range []struct {
		name              string
		save, read, scope error
		status            int
		retain            bool
	}{
		{name: "changed", save: errTrainingPlacementChanged, status: http.StatusConflict, retain: true},
		{name: "unknown commit", save: db.ErrCommitOutcomeUnknown, status: http.StatusServiceUnavailable},
		{name: "revoked writer", save: pgx.ErrNoRows, status: http.StatusNotFound},
		{name: "revoked during read", save: errTrainingPlacementChanged, read: pgx.ErrNoRows, status: http.StatusNotFound},
		{name: "revoked after read", save: errTrainingPlacementChanged, scope: pgx.ErrNoRows, status: http.StatusNotFound},
		{name: "unavailable", save: errTrainingPlacementUnavailable, status: http.StatusServiceUnavailable},
		{name: "invalid", save: errTrainingPlacementInvalid, status: http.StatusUnprocessableEntity, retain: true},
		{name: "limit", save: errTrainingPlacementLimit, status: http.StatusUnprocessableEntity, retain: true},
		{name: "eligibility changed", save: &trainingPlacementFieldFailure{Field: "form", Message: "Reveja a elegibilidade."}, status: http.StatusUnprocessableEntity, retain: true},
		{name: "unexpected failure", save: diagnostic, status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := placementHTTPFixture(t)
			preview := publicationPost(t, h, trainingPlacementsPath+"/cancelar/prever", cancellationFormForTest(s))
			if preview.Code != http.StatusOK {
				t.Fatalf("preview status=%d", preview.Code)
			}
			proof := placementHTTPProof(t, preview.Body.String())
			s.cancelErr, s.cancelReadErrAfterConfirm, s.contextErrAfterConfirm = tc.save, tc.read, tc.scope
			s.cancellation.CanCancel = false
			beforeReads := s.cancelReads
			response := publicationPost(t, h, trainingPlacementsPath+"/cancelar/confirmar", proof)
			body := response.Body.String()
			if response.Code != tc.status || s.cancelCalls != 1 || s.writes != 0 {
				t.Fatalf("status=%d attempts=%d writes=%d", response.Code, s.cancelCalls, s.writes)
			}
			if strings.Contains(body, "Pessoa privada sintética") != tc.retain || strings.Contains(body, diagnostic.Error()) {
				t.Fatalf("recovery disclosed or lost private context for %s", tc.name)
			}
			if errors.Is(tc.save, db.ErrCommitOutcomeUnknown) && (s.cancelReads != beforeReads || !strings.Contains(body, "não será repetido automaticamente")) {
				t.Fatal("unknown outcome was treated as a known rollback")
			}
		})
	}
}

func TestPlacementCancellationEditorHidesUnavailableAction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		modify   func(*placementHTTPStoreFake)
		status   int
		editable bool
	}{
		{name: "scheduled", modify: func(*placementHTTPStoreFake) {}, status: 200, editable: true},
		{name: "already effective", modify: func(s *placementHTTPStoreFake) { s.cancellation.CanCancel = false }, status: 200},
		{name: "denied", modify: func(s *placementHTTPStoreFake) { s.cancelReadErr = pgx.ErrNoRows }, status: 404},
		{name: "programme changed", modify: func(s *placementHTTPStoreFake) { s.cancellation.Change.ProgrammeID = uuid.New() }, status: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := placementHTTPFixture(t)
			tc.modify(s)
			v := url.Values{"group_id": {s.command.ContextGroupID.String()}, "person_id": {s.command.SubjectID.String()}, "change_id": {s.command.ID.String()}}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", trainingPlacementsPath+"?"+v.Encode(), nil))
			body := response.Body.String()
			if response.Code != tc.status || s.cancelReads != 1 || s.writes != 0 || strings.Contains(body, "/cancelar/prever") != tc.editable {
				t.Fatalf("status=%d reads=%d editable=%t", response.Code, s.cancelReads, strings.Contains(body, "/cancelar/prever"))
			}
			if tc.status == 404 && strings.Contains(body, "Pessoa privada sintética") {
				t.Fatal("denied cancellation exposed the person")
			}
		})
	}
}

func TestPlacementCancellationTransportRejectsChangedFormAndProof(t *testing.T) {
	s, h := placementHTTPFixture(t)
	form := cancellationFormForTest(s)
	bad := cloneFocusedValues(form)
	bad.Add("head_revision", "11")
	bad.Set("kind", "MOVE")
	bad.Set("source_placement_id", uuid.NewString())
	bad.Set("unexpected", strings.Repeat("x", trainingPlacementMaxFormBytes))
	invalid := publicationPost(t, h, trainingPlacementsPath+"/cancelar/prever", bad)
	if invalid.Code != 422 || s.writes != 0 {
		t.Fatalf("invalid form status=%d writes=%d", invalid.Code, s.writes)
	}
	preview := publicationPost(t, h, trainingPlacementsPath+"/cancelar/prever", form)
	if preview.Code != 200 {
		t.Fatalf("preview=%d", preview.Code)
	}
	proof := placementHTTPProof(t, preview.Body.String())
	changed := cloneFocusedValues(form)
	for key, values := range proof {
		changed[key] = values
	}
	changed.Set("cancellation_id", uuid.NewString())
	back := publicationPost(t, h, trainingPlacementsPath, changed)
	if back.Code != 422 {
		t.Fatalf("changed cancellation back status=%d", back.Code)
	}
	proof.Set("review_token", proof.Get("review_token")+"x")
	confirm := publicationPost(t, h, trainingPlacementsPath+"/cancelar/confirmar", proof)
	if confirm.Code != 422 || s.cancelCalls != 0 || s.writes != 0 {
		t.Fatal("changed proof reached the cancellation writer")
	}
}

func TestPlacementIndexValidatesScopeBeforeRevealingTimeline(t *testing.T) {
	for _, tc := range []struct {
		name          string
		change        func(*placementHTTPStoreFake, url.Values)
		status, views int
	}{
		{"valid date", func(*placementHTTPStoreFake, url.Values) {}, 200, 1},
		{"foreign season", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("season_id", uuid.NewString()) }, 422, 0},
		{"invalid date", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("effective_on", "2026-02-30") }, 422, 0},
		{"duplicate group", func(s *placementHTTPStoreFake, v url.Values) { v.Add("group_id", s.command.ContextGroupID.String()) }, 404, 0},
		{"malformed season", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("season_id", "bad") }, 422, 0},
		{"revoked group", func(s *placementHTTPStoreFake, _ url.Values) { s.contextErr = pgx.ErrNoRows }, 404, 0},
		{"private person", func(s *placementHTTPStoreFake, _ url.Values) { s.viewErr = pgx.ErrNoRows }, 404, 1},
		{"runtime unavailable", func(s *placementHTTPStoreFake, _ url.Values) { s.contextErr = errTrainingPlacementUnavailable }, 503, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := placementHTTPFixture(t)
			v := s.command.values()
			tc.change(s, v)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", trainingPlacementsPath+"?"+v.Encode(), nil))
			if response.Code != tc.status || s.viewCalls != tc.views || strings.Contains(response.Body.String(), "Pessoa privada sintética") != (tc.status == 200) {
				t.Fatalf("status=%d timeline reads=%d", response.Code, s.viewCalls)
			}
			if tc.status == 503 && response.Header().Get("Retry-After") != "5" {
				t.Fatal("unavailable runtime lost retry guidance")
			}
		})
	}
}

func TestPlacementTransactionErrorKeepsConflictsDistinctFromUnknownCommit(t *testing.T) {
	for _, tc := range []struct {
		code, message string
		expected      error
	}{
		{"42501", "private scope", pgx.ErrNoRows},
		{"55P03", "locked head", errTrainingPlacementChanged},
		{"22023", "malformed command", errTrainingPlacementInvalid},
		{"54000", "too many rows", errTrainingPlacementLimit},
		{"55000", "missing admission", errTrainingPlacementUnavailable},
		{"23514", "training_placement_started_boundary_unresolved", errTrainingPlacementPolicy},
		{"23514", "training_placement_return_unavailable", errTrainingPlacementReturn},
	} {
		original := &pgconn.PgError{Code: tc.code, Message: tc.message}
		if got := trainingPlacementTransactionError(db.SessionTxResult{}, original); !errors.Is(got, tc.expected) {
			t.Fatalf("%s: %v", tc.code, got)
		}
	}
	private := &pgconn.PgError{Code: "23514", Message: "private detail"}
	var field *trainingPlacementFieldFailure
	if got := trainingPlacementTransactionError(db.SessionTxResult{}, private); !errors.As(got, &field) || field.Field != "form" || strings.Contains(field.Message, private.Message) {
		t.Fatalf("constraint exposed %v", got)
	}
	if got := trainingPlacementTransactionError(db.SessionTxResult{Outcome: db.SessionTxOutcomeUnknown}, private); !errors.Is(got, db.ErrCommitOutcomeUnknown) {
		t.Fatalf("unknown outcome mapped as constraint: %v", got)
	}
}

func TestPlacementCancellationPreviewRefusesMissingOrRevokedFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		alter  func(*placementHTTPStoreFake, url.Values)
		status int
		reads  int
	}{
		{"invalid person", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("person_id", "bad") }, 404, 0},
		{"invalid change", func(_ *placementHTTPStoreFake, v url.Values) { v.Set("change_id", "bad") }, 404, 0},
		{"revoked group", func(s *placementHTTPStoreFake, _ url.Values) { s.contextErr = pgx.ErrNoRows }, 404, 0},
		{"revoked change", func(s *placementHTTPStoreFake, _ url.Values) { s.cancelReadErr = pgx.ErrNoRows }, 404, 1},
		{"invalid form identity and revision", func(_ *placementHTTPStoreFake, v url.Values) {
			v.Set("cancellation_id", "bad")
			v.Set("head_revision", "007")
		}, 422, 1},
		{"already effective", func(s *placementHTTPStoreFake, _ url.Values) { s.cancellation.CanCancel = false }, 409, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := placementHTTPFixture(t)
			v := cancellationFormForTest(s)
			tc.alter(s, v)
			w := publicationPost(t, h, trainingPlacementsPath+"/cancelar/prever", v)
			body := w.Body.String()
			if w.Code != tc.status || s.cancelReads != tc.reads || s.cancelCalls != 0 || s.writes != 0 || strings.Contains(body, "Confirmar cancelamento") {
				t.Fatalf("status=%d reads=%d cancellation attempts=%d", w.Code, s.cancelReads, s.cancelCalls)
			}
			if tc.status == 404 && strings.Contains(body, "Pessoa privada sintética") {
				t.Fatal("denial disclosed private cancellation facts")
			}
		})
	}
}

func TestPlacementCancellationBackRechecksCurrentScopeAndEffectiveState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		alter    func(*placementHTTPStoreFake)
		status   int
		editable bool
	}{
		{"group no longer accessible", func(s *placementHTTPStoreFake) { s.contextErr = pgx.ErrNoRows }, 404, false},
		{"change no longer accessible", func(s *placementHTTPStoreFake) { s.cancelReadErr = pgx.ErrNoRows }, 404, false},
		{"change already effective", func(s *placementHTTPStoreFake) { s.cancellation.CanCancel = false }, 200, false},
		{"still scheduled", func(*placementHTTPStoreFake) {}, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := placementHTTPFixture(t)
			form := cancellationFormForTest(s)
			w := publicationPost(t, h, trainingPlacementsPath+"/cancelar/prever", form)
			if w.Code != 200 {
				t.Fatalf("could not prepare review: %d", w.Code)
			}
			for k, v := range placementHTTPProof(t, w.Body.String()) {
				form[k] = v
			}
			tc.alter(s)
			back := publicationPost(t, h, trainingPlacementsPath, form)
			body := back.Body.String()
			if back.Code != tc.status || strings.Contains(body, "/cancelar/prever") != tc.editable || s.cancelCalls != 0 || s.writes != 0 {
				t.Fatalf("back status=%d editable=%t", back.Code, strings.Contains(body, "/cancelar/prever"))
			}
			if tc.status == 404 && strings.Contains(body, "Pessoa privada sintética") {
				t.Fatal("back showed revoked cancellation facts")
			}
		})
	}
}
