package handlers

import (
	"context"
	"html"
	"net/http"
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
)

type reuseHTTPStore struct {
	PostgresStructuredReuseStore
	read                         reuseRead
	readErr, saveErr             error
	result                       reuseSaveResult
	revoked                      bool
	reads, saves, authorizations int
}

func (s *reuseHTTPStore) ReadReuse(context.Context, uuid.UUID, reuseSourceSelector, *reuseTargetSelector) (reuseRead, error) {
	s.reads++
	if s.revoked {
		return reuseRead{}, pgx.ErrNoRows
	}
	return s.read, s.readErr
}
func (s *reuseHTTPStore) AuthorizeReuse(context.Context, uuid.UUID, reuseSourceSelector, *reuseTargetSelector) error {
	s.authorizations++
	if s.revoked {
		return pgx.ErrNoRows
	}
	return nil
}
func (s *reuseHTTPStore) ConfirmReuse(context.Context, reuseReviewed) (reuseSaveResult, error) {
	s.saves++
	return s.result, s.saveErr
}
func (s *reuseHTTPStore) ReuseSeasons(context.Context, uuid.UUID, uuid.UUID) ([]dbgen.ListReuseTargetSeasonsRow, error) {
	return nil, nil
}
func reuseHTTPExample(t *testing.T) (reuseReviewPayload, reuseRead) {
	t.Helper()
	p := reuseExample(t)
	for name, values := range reuseSelectorValues(p.Source.Selector, &p.Target.Selector) {
		p.Values[name] = values
	}
	p.Values.Set("week_title", p.Target.Selector.Title)
	p.Values.Set("week_description", "Notas próprias\r\n\tpara conservar.")
	read := reuseRead{State: db.TrainingScheduleState{DatabaseIncarnation: p.Incarnation, StateEpoch: p.Epoch, SchemaContract: "schedule-p1", PartialV2Enabled: true}, Source: p.Source, Target: &p.Target, References: p.References, DecisionAt: time.Now()}
	return p, read
}
func reuseTestHandler(p reuseReviewPayload, store *reuseHTTPStore) http.Handler {
	h := StructuredReuse{Store: store, HTTPWriteTimeout: 30 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+structuredReusePath+"/rascunho", h.Draft)
	mux.HandleFunc("POST "+structuredReusePath+"/prever", h.Preview)
	mux.HandleFunc("POST "+structuredReusePath+"/confirmar", h.Confirm)
	return httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: p.ActorID}))
		mux.ServeHTTP(w, r)
	}))
}
func TestReuseHTTPUnusableProofRetainsRawAndCannotRefreshIdentity(t *testing.T) {
	p, read := reuseHTTPExample(t)
	for _, bad := range []string{"expiry", "restart", "changed-target"} {
		t.Run(bad, func(t *testing.T) {
			signer, issued := focusedWorkoutReviews, time.Now()
			if bad == "expiry" {
				issued = issued.Add(-focusedWorkoutReviewTTL - time.Second)
			} else if bad == "restart" {
				signer.key[0] ^= 1
			}
			raw, token, err := signer.signReuse(p, issued)
			if err != nil {
				t.Fatal(err)
			}
			values := cloneFocusedValues(p.Values)
			values.Set("prior_proposal", raw)
			values.Set("prior_review_token", token)
			values.Set("command", "refresh")
			if bad == "changed-target" {
				values.Set("season_id", uuid.NewString())
			}
			store := &reuseHTTPStore{read: read}
			response := publicationPost(t, reuseTestHandler(p, store), structuredReusePath+"/rascunho", values)
			body := response.Body.String()
			if response.Code != 409 || !strings.Contains(html.UnescapeString(body), p.Values.Get("week_description")) || !strings.Contains(body, html.EscapeString(raw)) || !strings.Contains(body, html.EscapeString(token)) {
				t.Fatal("lost raw/proof recovery", response.Code)
			}
			if regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(body) || strings.Contains(body, `name="intent" value="confirm"`) || store.saves != 0 {
				t.Fatal("unusable proof allowed renewal/write")
			}
		})
	}
}
func TestReuseHTTPGenericRollbackRechecksCompleteSourceAndRevocation(t *testing.T) {
	p, read := reuseHTTPExample(t)
	raw, token, err := focusedWorkoutReviews.signReuse(p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plan-only", "changed-source", "serialization", "nowait", "changed-destination", "revoked", "unknown"} {
		t.Run(name, func(t *testing.T) {
			current := read
			current.Source.Items = append([]reuseSourceItem(nil), read.Source.Items...)
			if name == "changed-source" || name == "serialization" || name == "nowait" {
				native := *current.Source.Items[0].Raw
				native.Session.Title = "A origem mudou depois da leitura"
				current.Source.Items[0].Raw = &native
			}
			failure := errReuseChanged
			if name == "serialization" {
				failure = reuseSaveError(db.SessionTxResult{}, &pgconn.PgError{Code: "40001"})
			}
			if name == "nowait" {
				failure = publicationNowaitError(&pgconn.PgError{Code: "55P03"})
			}
			if name == "changed-destination" {
				failure = errReuseDestinationChanged
			}
			if name == "unknown" {
				failure = db.ErrCommitOutcomeUnknown
			}
			store := &reuseHTTPStore{read: current, saveErr: failure, revoked: name == "revoked"}
			response := publicationPost(t, reuseTestHandler(p, store), structuredReusePath+"/confirmar", url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}})
			body := response.Body.String()
			if name == "revoked" {
				if response.Code != 404 || strings.Contains(body, token) || store.saves != 0 {
					t.Fatal("revocation exposed own old proof", response.Code)
				}
				return
			}
			if response.Code != 409 {
				t.Fatal(name, response.Code)
			}
			if name == "unknown" {
				if strings.Contains(body, `name="review_token"`) || strings.Contains(body, `value="preview"`) || store.reads != 0 {
					t.Fatal("unknown automatically reconciled/retried")
				}
				return
			}
			hasPreview := regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(body)
			if hasPreview != (name == "plan-only") {
				t.Fatal(name, "incorrect first-response repreview", hasPreview)
			}
			if !strings.Contains(body, html.EscapeString(raw)) || !strings.Contains(html.UnescapeString(body), p.Values.Get("week_description")) {
				t.Fatal("lost safe raw/proof")
			}
		})
	}
}
func TestReuseHTTPSignedRepeatDoesNotRequireEnlargedSourceEnumeration(t *testing.T) {
	p, read := reuseHTTPExample(t)
	raw, token, err := focusedWorkoutReviews.signReuse(p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	first := p.Proposal.Destinations[0].Graph
	store := &reuseHTTPStore{read: read, readErr: errFocusedWorkoutLimit, result: reuseSaveResult{PlanID: p.Proposal.TargetPlanID, GroupID: p.Target.Selector.GroupID, SessionID: first.SessionID, Day: first.Schedule.Date, AlreadyCopied: true}}
	response := publicationPost(t, reuseTestHandler(p, store), structuredReusePath+"/confirmar", url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}})
	if response.Code != 303 || store.reads != 0 || store.saves != 1 || store.authorizations != 1 || !strings.Contains(response.Header().Get("Location"), first.SessionID.String()) {
		t.Fatal("signed repeat enumerated/refused enlarged source", response.Code, store.reads)
	}
}

func TestReuseHTTPRollbackCrossedAppendTargetStartKeepsRawWithoutRepreview(t *testing.T) {
	for _, failure := range []error{errReuseChanged, reuseSaveError(db.SessionTxResult{}, &pgconn.PgError{Code: "40001"}), publicationNowaitError(&pgconn.PgError{Code: "55P03"})} {
		p, read := reuseHTTPExample(t)
		oldSource := focusedEditFixture(t)
		old := oldSource.Raw
		old.Session.PlanID = p.Proposal.TargetPlanID
		start := old.Session.StartsAt.Time
		if !old.Session.StartsAt.Valid || !focusedEditEligible(old.Session, start.Add(-time.Microsecond)) {
			t.Fatal("cutoff fixture must be eligible immediately before its original start")
		}
		p.Target.Existing = &old
		p.Target.Selector.SessionID = old.Session.ID
		p.Target.Configuration.ExistingPlanID = focusedPointer(p.Proposal.TargetPlanID)
		p.Proposal.NewWeek = false
		for name, values := range reuseSelectorValues(p.Source.Selector, &p.Target.Selector) {
			p.Values[name] = values
		}
		read.Target = &p.Target
		read.TargetGraph = &oldSource.Graph
		read.DecisionAt = start
		raw, token, err := focusedWorkoutReviews.signReuse(p, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		store := &reuseHTTPStore{read: read, saveErr: failure}
		response := publicationPost(t, reuseTestHandler(p, store), structuredReusePath+"/confirmar", url.Values{"intent": {"confirm"}, "proposal": {raw}, "review_token": {token}})
		body := response.Body.String()
		if response.Code != http.StatusConflict || store.saves != 1 || store.reads != 1 {
			t.Fatal("missing fresh rollback recovery", response.Code, store.saves, store.reads)
		}
		if regexp.MustCompile(`<button\b[^>]*value="preview"`).MatchString(body) || strings.Contains(body, `name="intent" value="confirm"`) {
			t.Fatal("crossed original start left futile resubmission")
		}
		if !strings.Contains(body, html.EscapeString(raw)) || !strings.Contains(body, html.EscapeString(token)) || !strings.Contains(html.UnescapeString(body), p.Values.Get("week_description")) || !strings.Contains(body, html.EscapeString(reuseTaskURL(p.Source.Selector, &p.Target.Selector))) {
			t.Fatal("lost own raw/proof or explicit reload")
		}
	}
}
