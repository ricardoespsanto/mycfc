package handlers

import (
	"context"
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
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type publicationHTTPStoreFake struct {
	source                                  publicationSourceEnvelope
	actor                                   uuid.UUID
	authorizeErr, readErr, confirmErr       error
	authorizeCalls, readCalls, confirmCalls int
	confirmedSummary                        string
}

func (s *publicationHTTPStoreFake) AuthorizePublication(context.Context, uuid.UUID, uuid.UUID) (publicationPlanHeader, error) {
	s.authorizeCalls++
	return s.source.Header, s.authorizeErr
}
func (s *publicationHTTPStoreFake) ReadPublicationSource(context.Context, uuid.UUID, uuid.UUID) (publicationSourceEnvelope, error) {
	s.readCalls++
	return s.source, s.readErr
}
func (s *publicationHTTPStoreFake) ConfirmPublication(_ context.Context, input publicationConfirmInput) (publicationSaveResult, error) {
	s.confirmCalls++
	if s.confirmErr != nil {
		return publicationSaveResult{}, s.confirmErr
	}
	material, err := materializePublicationSource(s.source, time.UTC)
	if err != nil {
		return publicationSaveResult{}, err
	}
	current := publicationCurrentReviewInput(input, s.source.Header, material.Prescriptions)
	if err := input.Review.matches(current, time.Now()); err != nil {
		return publicationSaveResult{}, err
	}
	s.confirmedSummary = input.Review.metadata.ChangeSummary
	return publicationSaveResult{PublicationID: uuid.New(), Revision: 1, PrescriptionCount: len(material.Prescriptions)}, nil
}
func publicationHTTPFixture(t *testing.T) (*publicationHTTPStoreFake, http.Handler, string) {
	t.Helper()
	store := &publicationHTTPStoreFake{source: publicationSourceFixture(), actor: uuid.New()}
	h := StructuredTraining{Publications: store, HTTPWriteTimeout: 30 * time.Second, Location: time.UTC}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/treinos/estruturados/semanas/{id}/publicar", h.PublishPlan)
	wrapped := httpx.PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: store.actor, Name: "Coach"}))
		mux.ServeHTTP(w, r)
	}))
	return store, wrapped, "/admin/treinos/estruturados/semanas/" + store.source.Header.PlanID.String() + "/publicar"
}
func publicationPost(t *testing.T, h http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func publicationConfirmForm(t *testing.T, body string) url.Values {
	t.Helper()
	// Review now has a separate read-only instruction-selection form. Read the
	// original publication confirmation metadata from its actual form only.
	for _, form := range regexp.MustCompile(`(?s)<form\b[^>]*>.*?</form>`).FindAllString(body, -1) {
		if regexp.MustCompile(`name="confirm"\s+value="yes"`).MatchString(form) {
			body = form
			break
		}
	}
	values := url.Values{}
	for _, name := range []string{"publication_intent", "confirm", "change_summary", "source_updated_at", "review_token", "reviewed_latest_id", "reviewed_latest_revision", "return_group", "return_week", "return_session", "return_day"} {
		pattern := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `"\s+value="([^"]*)"`)
		match := pattern.FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("missing confirmation field %s", name)
		}
		values.Set(name, html.UnescapeString(match[1]))
	}
	return values
}
func nativePublicationValue(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func TestPublicationHTTPNativeMultilinePreviewConfirmMAC(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native CRLF", false: "direct LF"}[native], func(t *testing.T) {
			store, h, path := publicationHTTPFixture(t)
			summary := "Primeira linha\nSegunda\tlinha"
			if native {
				summary = nativePublicationValue(summary)
			}
			preview := publicationPost(t, h, path, url.Values{"publication_intent": {"preview"}, "change_summary": {summary}, "source_updated_at": {store.source.Header.SourceUpdatedAt.Time.Format(time.RFC3339Nano)}, "return_to": {structuredPlannerURL(store.source.Header.GroupID.String(), store.source.Header.PlanID.String(), "", "2026-11-14")}})
			if preview.Code != 200 || store.confirmCalls != 0 {
				t.Fatalf("preview status%d confirmcalls%d", preview.Code, store.confirmCalls)
			}
			values := publicationConfirmForm(t, preview.Body.String())
			if native {
				// HTML normalizes attribute CRLF to LF; native form submission normalizes
				// scalar LF back to CRLF. Exercise the wire values, not visible whitespace.
				for key, entries := range values {
					values.Set(key, nativePublicationValue(strings.ReplaceAll(entries[0], "\r\n", "\n")))
				}
			}
			response := publicationPost(t, h, path, values)
			if response.Code != 303 || store.confirmCalls != 1 || store.confirmedSummary != summary {
				t.Fatalf("confirm status%d summaryretained%v body%s", response.Code, store.confirmedSummary == summary, response.Body.String())
			}
			if !strings.Contains(response.Header().Get("Location"), "day=2026-11-14") {
				t.Fatal("selected empty Saturday lost")
			}
		})
	}
}

func TestPublicationHTTPRawPostCannotPublishAndUnsignedConfirmFails(t *testing.T) {
	store, h, path := publicationHTTPFixture(t)
	response := publicationPost(t, h, path, url.Values{"change_summary": {"Old direct publish"}, "source_updated_at": {store.source.Header.SourceUpdatedAt.Time.Format(time.RFC3339Nano)}})
	if response.Code != 200 || store.confirmCalls != 0 {
		t.Fatal("legacy raw POST published")
	}
	values := publicationConfirmForm(t, response.Body.String())
	values.Del("review_token")
	failed := publicationPost(t, h, path, values)
	if failed.Code != 422 || store.confirmCalls != 0 || strings.Contains(failed.Body.String(), "name=\"confirm\"") {
		t.Fatal("unsigned confirm reached save")
	}
}

func TestPublicationHTTPRetainsSafeConflictAndUnknownWithoutAudience(t *testing.T) {
	for _, outcome := range []struct {
		name    string
		err     error
		unknown bool
	}{{"conflict", errStructuredTrainingPublicationConflict, false}, {"unknown", errors.Join(db.ErrCommitOutcomeUnknown, errors.New("lost acknowledgement")), true}} {
		t.Run(outcome.name, func(t *testing.T) {
			store, h, path := publicationHTTPFixture(t)
			preview := publicationPost(t, h, path, url.Values{"publication_intent": {"preview"}, "change_summary": {"Proposta conservada"}, "source_updated_at": {store.source.Header.SourceUpdatedAt.Time.Format(time.RFC3339Nano)}})
			values := publicationConfirmForm(t, preview.Body.String())
			original := values.Get("review_token")
			store.confirmErr = outcome.err
			failed := publicationPost(t, h, path, values)
			body := failed.Body.String()
			if failed.Code != 409 || strings.Contains(body, "Ana") || strings.Contains(body, "name=\"confirm\"") {
				t.Fatal("failed confirmation exposed audience or retry")
			}
			if !outcome.unknown && (!strings.Contains(body, "Proposta conservada") || !strings.Contains(body, original)) {
				t.Fatal("safe proposal/token rebased or discarded")
			}
			if outcome.unknown && (strings.Contains(body, "action=\""+html.EscapeString(path)+"\"") || strings.Contains(body, "Nada foi guardado")) {
				t.Fatal("unknown outcome offered automatic resend or false rollback")
			}
			if failed.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(failed.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("private error cache/referrer headers missing")
			}
		})
	}
}

func TestPublicationHTTPRevocationPrecedesRetainedFieldsAndLargeBody(t *testing.T) {
	store, h, path := publicationHTTPFixture(t)
	store.authorizeErr = pgx.ErrNoRows
	denied := publicationPost(t, h, path, url.Values{"publication_intent": {"confirm"}, "change_summary": {"Private proposal"}})
	if denied.Code != 404 || store.readCalls != 0 || store.confirmCalls != 0 || strings.Contains(denied.Body.String(), "Private proposal") {
		t.Fatal("revocation retained private form")
	}
	store.authorizeErr = nil
	previous := store.authorizeCalls
	response := publicationPost(t, h, path, url.Values{"change_summary": {strings.Repeat("a", int(httpx.PublicationConfirmMaxBytes)+1)}})
	if response.Code != 403 || store.authorizeCalls != previous || store.confirmCalls != 0 {
		t.Fatal("over-limit form reached domain work")
	}
}
