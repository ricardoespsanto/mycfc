//go:build integration

package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

// Exercise the actual authenticated member routes, not just the audience SQL:
// a missing collection link is not an authorization check on a forged detail URL.
func TestHistoricalTeamEventHTTPAccessAndGuardianRevocation(t *testing.T) {
	ctx, pool := integrationPool(t)
	guardian, subject, author := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{guardian, author} {
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'HTTP history adult',$2,'hash','1980-01-01')`, id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	policy := enableGuardianAuthorityTestPolicy(t, ctx, pool, author)
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'HTTP history dependent',true,(now() AT TIME ZONE 'Europe/Lisbon')::date - interval '14 years')`, subject); err != nil {
		t.Fatal(err)
	}
	verifyGuardianAuthorityTestRelationship(t, ctx, pool, guardian, subject, author, policy)

	season, oldTeam, newTeam := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'HTTP history season',
		(now() AT TIME ZONE 'Europe/Lisbon')::date-20,(now() AT TIME ZONE 'Europe/Lisbon')::date+20)`, season, "HTTP_"+season.String()[:8]); err != nil {
		t.Fatal(err)
	}
	for _, team := range []uuid.UUID{oldTeam, newTeam} {
		if _, err := pool.Exec(ctx, `INSERT INTO teams(id,season_id,programme_id,code,name)
			SELECT $1,$2,id,$3,'HTTP history team' FROM programmes WHERE code='Leisure'`, team, season, "HTTP_"+team.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,team_id,starts_on,ends_on)
		SELECT $1::uuid,$2::uuid,id,$3::uuid,(now() AT TIME ZONE 'Europe/Lisbon')::date-10,(now() AT TIME ZONE 'Europe/Lisbon')::date-1 FROM programmes WHERE code='Leisure'
		UNION ALL
		SELECT $1::uuid,$2::uuid,id,$4::uuid,(now() AT TIME ZONE 'Europe/Lisbon')::date,NULL FROM programmes WHERE code='Leisure'`, subject, season, oldTeam, newTeam); err != nil {
		t.Fatal(err)
	}
	past, future := uuid.New(), uuid.New()
	const pastTitle, futureTitle = "Former team archive HTTP", "Former team future HTTP"
	for _, event := range []struct {
		id    uuid.UUID
		day   int
		title string
	}{{past, -3, pastTitle}, {future, 1, futureTitle}} {
		if _, err := pool.Exec(ctx, `INSERT INTO events(id,title,starts_at,ends_at,created_by_id)
			VALUES($1,$2,((now() AT TIME ZONE 'Europe/Lisbon')::date+$3::integer+time '10:00') AT TIME ZONE 'Europe/Lisbon',
			((now() AT TIME ZONE 'Europe/Lisbon')::date+$3::integer+time '11:00') AT TIME ZONE 'Europe/Lisbon',$4)`, event.id, event.title, event.day, author); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO event_team_audiences(event_id,team_id) VALUES($1,$2)`, event.id, oldTeam); err != nil {
			t.Fatal(err)
		}
	}

	queries := dbgen.New(pool)
	sessions := scs.New()
	auth := Auth{Users: queries, Sessions: sessions}
	events := Events{Store: queries, Location: time.UTC, Now: time.Now}
	mux := http.NewServeMux()
	mux.Handle("GET /events", auth.RequireAuthenticated(http.HandlerFunc(events.Index)))
	mux.Handle("GET /events/{id}", auth.RequireAuthenticated(http.HandlerFunc(events.Detail)))
	// Seed an ordinary versioned session; every tested request still passes through
	// session loading, account lookup, authenticated guard and real event handlers.
	mux.HandleFunc("GET /fixture/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid fixture", http.StatusBadRequest)
			return
		}
		sessions.Put(r.Context(), "user_id", id.String())
		sessions.Put(r.Context(), "credential_version", int64(1))
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(sessions.LoadAndSave(auth.Load(mux)))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	cookieFor := func(actor uuid.UUID) *http.Cookie {
		t.Helper()
		response, err := client.Get(server.URL + "/fixture/session/" + actor.String())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent || len(response.Cookies()) != 1 {
			t.Fatalf("session seed status=%d cookies=%d", response.StatusCode, len(response.Cookies()))
		}
		return response.Cookies()[0]
	}
	fetch := func(cookie *http.Cookie, path string, status int, visible, hidden string) string {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if response.StatusCode != status || (visible != "" && !strings.Contains(text, visible)) || (hidden != "" && strings.Contains(text, hidden)) {
			t.Fatalf("GET %s status=%d want=%d visible=%q hidden=%q body=%s", path, response.StatusCode, status, visible, hidden, text)
		}
		return text
	}
	pastPath := "/events/" + past.String() + "?view=past"
	futurePath := "/events/" + future.String()
	guardianPath := pastPath + "&subject_user_id=" + url.QueryEscape(subject.String())
	fetch(nil, pastPath, http.StatusSeeOther, "/login", pastTitle)
	memberCookie, guardianCookie := cookieFor(subject), cookieFor(guardian)

	// Navigation and direct URL must agree for both the subject and guardian.
	archive := fetch(memberCookie, "/events?view=past", http.StatusOK, pastTitle, futureTitle)
	archiveLink := "/events/" + past.String() + "?view=past&amp;page=1"
	if !strings.Contains(archive, `href="`+archiveLink+`"`) {
		t.Fatal("historical event missing navigable archive detail link")
	}
	fetch(memberCookie, strings.ReplaceAll(archiveLink, "&amp;", "&"), http.StatusOK, pastTitle, "")
	fetch(memberCookie, pastPath, http.StatusOK, pastTitle, "")
	fetch(memberCookie, "/events", http.StatusOK, "", futureTitle)
	fetch(memberCookie, futurePath, http.StatusNotFound, "", futureTitle)
	fetch(guardianCookie, "/events?view=past", http.StatusOK, pastTitle, futureTitle)
	fetch(guardianCookie, guardianPath, http.StatusOK, pastTitle, "")
	fetch(guardianCookie, futurePath+"?subject_user_id="+subject.String(), http.StatusNotFound, "", futureTitle)

	if _, err := pool.Exec(ctx, `UPDATE guardian_authority_relationships SET state='EXPIRED',version=version+1,
		conflict=false,conflict_actor_ref=NULL,updated_at=now() WHERE guardian_user_id=$1 AND subject_user_id=$2`, guardian, subject); err != nil {
		t.Fatal(err)
	}
	// Reuse the SAME authenticated guardian session: authority is read afresh.
	fetch(guardianCookie, "/events?view=past", http.StatusOK, "", pastTitle)
	fetch(guardianCookie, guardianPath, http.StatusNotFound, "", pastTitle)
	// Revocation rotates the minor's credential version; its old session is
	// invalidated. Its pre-revocation historical access was checked above.
}
