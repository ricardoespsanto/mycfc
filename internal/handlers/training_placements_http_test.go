package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type placementHTTPStoreFake struct {
	q                                                                                  *placementReadFake
	actor                                                                              uuid.UUID
	command                                                                            trainingPlacementCommand
	state                                                                              db.TrainingScheduleState
	context                                                                            trainingPlacementPageContext
	cancellation                                                                       trainingPlacementCancellationRead
	contextCalls, viewCalls, readCalls, confirmCalls, cancelReads, cancelCalls, writes int
	confirmErr, cancelErr, contextErrAfterConfirm                                      error
	contextErr, viewErr, cancelReadErr, cancelReadErrAfterConfirm                      error
	received                                                                           trainingPlacementReviewed
	cancelReceived                                                                     trainingPlacementCancelReviewed
	receiptFingerprint                                                                 string
	saved                                                                              trainingPlacementSaveResult
}

func (s *placementHTTPStoreFake) PlacementContext(context.Context, uuid.UUID, uuid.UUID) (trainingPlacementPageContext, error) {
	s.contextCalls++
	if s.contextErr != nil {
		return trainingPlacementPageContext{}, s.contextErr
	}
	if s.confirmCalls+s.cancelCalls > 0 && s.contextErrAfterConfirm != nil {
		return trainingPlacementPageContext{}, s.contextErrAfterConfirm
	}
	return s.context, nil
}
func (s *placementHTTPStoreFake) PlacementView(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string) (trainingPlacementView, error) {
	s.viewCalls++
	if s.viewErr != nil {
		return trainingPlacementView{}, s.viewErr
	}
	return trainingPlacementView{PersonID: s.command.SubjectID, PersonName: "Pessoa privada sintética", Revision: s.q.subject.Revision, Timeline: s.q.timeline, Groups: s.q.groups}, nil
}
func (s *placementHTTPStoreFake) ReadPlacement(ctx context.Context, actor uuid.UUID, c trainingPlacementCommand, refresh bool) (trainingPlacementRead, error) {
	s.readCalls++
	return readTrainingPlacement(ctx, s.q, actor, c, s.state, refresh)
}
func (s *placementHTTPStoreFake) ConfirmPlacement(ctx context.Context, r trainingPlacementReviewed) (trainingPlacementSaveResult, error) {
	s.confirmCalls++
	s.received = r
	if s.confirmErr != nil {
		return trainingPlacementSaveResult{}, s.confirmErr
	}
	if s.receiptFingerprint != "" {
		if r.reviewSHA256 != s.receiptFingerprint {
			return trainingPlacementSaveResult{}, errTrainingPlacementChanged
		}
		out := s.saved
		out.Replayed = true
		return out, nil
	}
	read, e := s.ReadPlacement(ctx, s.actor, r.Payload.Command, false)
	if e != nil {
		return trainingPlacementSaveResult{}, e
	}
	current, e := read.proposal(s.actor, r.Payload.Command)
	if e != nil {
		return trainingPlacementSaveResult{}, e
	}
	if e = r.matches(current, time.Now()); e != nil {
		return trainingPlacementSaveResult{}, e
	}
	s.receiptFingerprint = r.reviewSHA256
	s.writes++
	s.saved = trainingPlacementSaveResult{ChangeID: r.Payload.Command.ID, Revision: r.Payload.Command.ExpectedRevision + 1, SourcePlacementID: r.Payload.Command.SourcePlacementID, DestinationPlacementID: uuid.New(), ReturnPlacementID: uuid.New()}
	return s.saved, nil
}
func (s *placementHTTPStoreFake) ReadPlacementCancellation(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (trainingPlacementCancellationRead, error) {
	s.cancelReads++
	if s.cancelReadErr != nil {
		return trainingPlacementCancellationRead{}, s.cancelReadErr
	}
	if s.cancelCalls > 0 && s.cancelReadErrAfterConfirm != nil {
		return trainingPlacementCancellationRead{}, s.cancelReadErrAfterConfirm
	}
	return s.cancellation, nil
}
func (s *placementHTTPStoreFake) ConfirmPlacementCancellation(_ context.Context, r trainingPlacementCancelReviewed) (trainingPlacementSaveResult, error) {
	s.cancelCalls++
	s.cancelReceived = r
	if s.cancelErr != nil {
		return trainingPlacementSaveResult{}, s.cancelErr
	}
	s.writes++
	return trainingPlacementSaveResult{ChangeID: r.Payload.Command.ID, Revision: r.Payload.Command.ExpectedRevision + 1}, nil
}
func placementHTTPFixture(t *testing.T) (*placementHTTPStoreFake, http.Handler) {
	t.Helper()
	q, x, c, state := placementReadFixture()
	now := time.Now()
	date := now.In(lisbonLocation())
	c.EffectiveOn = date.AddDate(0, 0, 7).Format("2006-01-02")
	c.TemporaryUntil = date.AddDate(0, 0, 8).Format("2006-01-02")
	clock := pgtype.Timestamptz{Time: now, Valid: true}
	q.subject.DecisionAt = clock
	q.source.DecisionAt = clock
	q.source.StartsOn = placementDate(date.AddDate(0, 0, -7).Format("2006-01-02"))
	q.timeline[0].StartsOn = q.source.StartsOn
	q.instructions[0].DecisionAt = clock
	q.instructions[0].SessionOn = placementDate(date.AddDate(0, 0, 14).Format("2006-01-02"))
	start, end := placementDate(date.AddDate(-1, 0, 0).Format("2006-01-02")), placementDate(date.AddDate(1, 0, 0).Format("2006-01-02"))
	for i := range q.groups {
		q.groups[i].SeasonStartsOn = start
		q.groups[i].SeasonEndsOn = end
	}
	q.subject.SeasonStartsOn = start
	q.subject.SeasonEndsOn = end
	q.returns[0].StartsOn = start
	q.returns[0].SeasonStartsOn = start
	q.returns[0].SeasonEndsOn = end
	store := &placementHTTPStoreFake{q: q, actor: x.ActorID, command: c, state: state, context: trainingPlacementPageContext{Group: TrainingGroupRecord{ID: c.ContextGroupID, Name: "Grupo de contexto privado", ScopeName: "Participação sintética"}, ProgrammeID: c.ProgrammeID, Seasons: []dbgen.ListReuseTargetSeasonsRow{{ID: c.SeasonID, Name: "Época exclusivamente sintética", StartsOn: start, EndsOn: end}}}}
	orig := int64(3)
	current := int64(4)
	one := int64(1)
	dest, ret := uuid.New(), uuid.New()
	source, sourceGroup, target := c.SourcePlacementID, c.ContextGroupID, c.TargetGroupID
	store.cancellation = trainingPlacementCancellationRead{State: state, PersonName: "Pessoa privada sintética", CanCancel: true, Timeline: q.timeline, Change: dbgen.TrainingPlacementChangeForReviewRow{DecisionAt: clock, ID: c.ID, SubjectUserID: &c.SubjectID, ProgrammeID: c.ProgrammeID, SeasonID: c.SeasonID, Kind: "TEMPORARY", EffectiveOn: placementDate(c.EffectiveOn), TemporaryUntil: placementDate(c.TemporaryUntil), OriginalSourceStartsOn: q.source.StartsOn, OriginalSourceVersion: &orig, SourcePlacementID: &source, SourceGroupID: &sourceGroup, SourceStartsOn: q.source.StartsOn, SourceEndsOn: placementDate(date.AddDate(0, 0, 6).Format("2006-01-02")), SourceVersion: &current, DestinationPlacementID: &dest, DestinationGroupID: &target, DestinationStartsOn: placementDate(c.EffectiveOn), DestinationEndsOn: placementDate(c.TemporaryUntil), DestinationVersion: &one, ReturnPlacementID: &ret, ReturnGroupID: &sourceGroup, ReturnStartsOn: placementDate(date.AddDate(0, 0, 9).Format("2006-01-02")), ReturnVersion: &one, Revision: c.ExpectedRevision + 1, AppliedRevision: c.ExpectedRevision + 1, CanCancel: true}}
	h := TrainingPlacements{Store: store, HTTPWriteTimeout: 30 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+trainingPlacementsPath, h.Index)
	mux.HandleFunc("POST "+trainingPlacementsPath, h.Editor)
	mux.HandleFunc("POST "+trainingPlacementsPath+"/prever", h.Preview)
	mux.HandleFunc("POST "+trainingPlacementsPath+"/confirmar", h.Confirm)
	mux.HandleFunc("POST "+trainingPlacementsPath+"/cancelar/prever", h.CancelPreview)
	mux.HandleFunc("POST "+trainingPlacementsPath+"/cancelar/confirmar", h.CancelConfirm)
	wrapped := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: store.actor, Name: "Treinador sintético"}))
		mux.ServeHTTP(w, r)
	}))
	return store, wrapped
}
func placementHTTPField(t *testing.T, body, name string) string {
	t.Helper()
	re := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `"\s+value="([^"]*)"`)
	m := re.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("missing %s in native form", name)
	}
	return html.UnescapeString(m[1])
}
func placementHTTPProof(t *testing.T, body string) url.Values {
	return url.Values{"proposal": {placementHTTPField(t, body, "proposal")}, "review_token": {placementHTTPField(t, body, "review_token")}}
}
func TestTrainingPlacementHTTPNativeReviewBackConfirmAndExactReplay(t *testing.T) {
	s, h := placementHTTPFixture(t)
	preview := publicationPost(t, h, trainingPlacementsPath+"/prever", s.command.values())
	if preview.Code != 200 {
		t.Fatalf("preview%d %s", preview.Code, preview.Body.String())
	}
	if s.writes != 0 {
		t.Fatal("preview wrote")
	}
	proof := placementHTTPProof(t, preview.Body.String())
	back := s.command.values()
	for k, v := range proof {
		back[k] = v
	}
	back.Set("change_id", "")
	back.Set("cancellation_id", "")
	response := publicationPost(t, h, trainingPlacementsPath, back)
	if response.Code != 200 || s.writes != 0 || placementHTTPField(t, response.Body.String(), "command_id") != s.command.ID.String() || placementHTTPField(t, response.Body.String(), "head_revision") != "7" || strings.Contains(response.Body.String(), ">Confirmar alteração<") {
		t.Fatalf("native back changed/re-signed state: %d", response.Code)
	}
	saved := publicationPost(t, h, trainingPlacementsPath+"/confirmar", proof)
	if saved.Code != 303 || s.writes != 1 {
		t.Fatalf("save%d writes%d", saved.Code, s.writes)
	}
	fingerprint := sha256.Sum256([]byte(proof.Get("review_token")))
	if s.received.reviewSHA256 != hex.EncodeToString(fingerprint[:]) {
		t.Fatal("verified exacttoken fingerprint not retained")
	}
	// A successful command changes the head; same-proof replay must still be
	// delegated unchanged to the retained-result path, never rebuilt/re-signed.
	s.q.subject.Revision++
	s.q.source.Revision++
	s.q.timeline[0].Revision++
	duplicate := publicationPost(t, h, trainingPlacementsPath+"/confirmar", proof)
	if duplicate.Code != 303 || s.writes != 1 || duplicate.Header().Get("Location") != saved.Header().Get("Location") {
		t.Fatalf("replay%d writes%d", duplicate.Code, s.writes)
	}
}
func TestTrainingPlacementHTTPFutureOnlyDateSelectionAndInvalidRaw(t *testing.T) {
	s, h := placementHTTPFixture(t)
	request := httptest.NewRequest("GET", placementURL(s.command.ContextGroupID, s.command.SubjectID, uuid.Nil, ""), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 200 || s.viewCalls != 0 || strings.Contains(w.Body.String(), "Pessoa privada sintética") || !strings.Contains(w.Body.String(), "Escolher época e data") {
		t.Fatal("missing explicit-date step or inferred TODAY eligibility")
	}
	raw := s.command.values()
	raw.Set("effective_on", " bad own date ")
	response := publicationPost(t, h, trainingPlacementsPath+"/prever", raw)
	if response.Code != 422 || s.readCalls != 0 || s.writes != 0 || !strings.Contains(response.Body.String(), " bad own date ") {
		t.Fatalf("invalid raw lost: %d", response.Code)
	}
}
func TestTrainingPlacementHTTPKnownConflictPreservesProofUnknownDoesNotRetry(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{true: "unknown commit", false: "known rollback"}[unknown], func(t *testing.T) {
			s, h := placementHTTPFixture(t)
			preview := publicationPost(t, h, trainingPlacementsPath+"/prever", s.command.values())
			proof := placementHTTPProof(t, preview.Body.String())
			if unknown {
				s.confirmErr = errors.Join(db.ErrCommitOutcomeUnknown, &pgconn.PgError{Code: "40001"})
				s.contextErrAfterConfirm = errors.New("later read unavailable")
			} else {
				s.confirmErr = errTrainingPlacementChanged
			}
			reads := s.readCalls
			response := publicationPost(t, h, trainingPlacementsPath+"/confirmar", proof)
			want := 409
			if unknown {
				want = 503
			}
			if response.Code != want || s.confirmCalls != 1 || s.writes != 0 || !strings.Contains(response.Body.String(), html.EscapeString(proof.Get("review_token"))) || strings.Contains(response.Body.String(), ">Confirmar alteração<") {
				t.Fatalf("recovery%d want%d", response.Code, want)
			}
			if unknown && (s.readCalls != reads || strings.Contains(response.Body.String(), "Pessoa privada sintética") || strings.Contains(response.Body.String(), "Grupo de contexto privado") || strings.Contains(response.Body.String(), "não foram alterados")) {
				t.Fatal("ambiguous commit was retried/read as rollback or exposed cached labels")
			}
		})
	}
}
func TestTrainingPlacementHTTPCancelNativeProofAndBack(t *testing.T) {
	s, h := placementHTTPFixture(t)
	cmd := trainingPlacementCancellation{ID: uuid.New(), ChangeID: s.command.ID, SubjectID: s.command.SubjectID, ProgrammeID: s.command.ProgrammeID, SeasonID: s.command.SeasonID, ContextGroupID: s.command.ContextGroupID, ExpectedRevision: s.cancellation.Change.Revision}
	review := publicationPost(t, h, trainingPlacementsPath+"/cancelar/prever", cmd.values())
	if review.Code != 200 || s.writes != 0 {
		t.Fatalf("cancel preview%d", review.Code)
	}
	proof := placementHTTPProof(t, review.Body.String())
	back := cmd.values()
	for k, v := range proof {
		back[k] = v
	}
	response := publicationPost(t, h, trainingPlacementsPath, back)
	if response.Code != 200 || s.writes != 0 || strings.Contains(response.Body.String(), ">Confirmar alteração<") {
		t.Fatalf("cancel back%d", response.Code)
	}
	saved := publicationPost(t, h, trainingPlacementsPath+"/cancelar/confirmar", proof)
	if saved.Code != 303 || s.writes != 1 || s.cancelCalls != 1 || s.cancelReceived.Payload.Command.ID != cmd.ID {
		t.Fatalf("cancel confirm%d writes%d", saved.Code, s.writes)
	}
}
func TestTrainingPlacementHTTPRejectsTamperAndRechecksDeniedScope(t *testing.T) {
	s, h := placementHTTPFixture(t)
	preview := publicationPost(t, h, trainingPlacementsPath+"/prever", s.command.values())
	proof := placementHTTPProof(t, preview.Body.String())
	altered := cloneFocusedValues(proof)
	altered.Set("proposal", strings.Replace(proof.Get("proposal"), "Destino", "Outra", 1))
	response := publicationPost(t, h, trainingPlacementsPath+"/confirmar", altered)
	if response.Code != 422 || s.confirmCalls != 0 || strings.Contains(response.Body.String(), ">Confirmar alteração<") {
		t.Fatal("tampered proof reached writer")
	}
	s.confirmErr = pgx.ErrNoRows
	denied := publicationPost(t, h, trainingPlacementsPath+"/confirmar", proof)
	if denied.Code != 404 || strings.Contains(denied.Body.String(), "Pessoa privada sintética") || strings.Contains(denied.Body.String(), proof.Get("review_token")) {
		t.Fatal("denied task retained private context")
	}
}
