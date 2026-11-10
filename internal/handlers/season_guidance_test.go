package handlers

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type seasonGuidanceStatus struct {
	missing, administrator bool
	err                    error
}

func (s seasonGuidanceStatus) SeasonSetupStatus(context.Context, uuid.UUID) (bool, bool, error) {
	return s.missing, s.administrator, s.err
}

func TestSeasonMissingGuidanceRespectsSetupAuthority(t *testing.T) {
	origin := SeasonReturnContext{Kind: "member", ID: uuid.NewString(), Query: "Ana"}
	actor := uuid.New()
	message, link, err := seasonMissingGuidance(t.Context(), seasonGuidanceStatus{missing: true, administrator: true}, actor, origin)
	if err != nil || !strings.Contains(message, "datas explícitas") {
		t.Fatalf("unexpected administrator guidance: %q, %v", message, err)
	}
	u, err := url.Parse(link)
	if err != nil || u.Path != seasonsPath || u.Query().Get("return_id") != origin.ID || u.Query().Get("return_q") != origin.Query {
		t.Fatalf("administrator lost the return context: %q", link)
	}

	message, link, err = seasonMissingGuidance(t.Context(), seasonGuidanceStatus{missing: true}, actor, origin)
	if err != nil || link != "" || !strings.Contains(message, "Peça a um administrador") {
		t.Fatalf("coach received invalid setup guidance: %q, %q, %v", message, link, err)
	}

	unavailable := errors.New("lookup unavailable")
	message, link, err = seasonMissingGuidance(t.Context(), seasonGuidanceStatus{missing: true, administrator: true, err: unavailable}, actor, origin)
	if !errors.Is(err, unavailable) || message != "" || link != "" {
		t.Fatalf("failed lookup advertised setup: %q, %q, %v", message, link, err)
	}
}

func TestMemberSeasonSetupURLBoundsTheReturnContext(t *testing.T) {
	member := uuid.New()
	for _, collection := range []string{"/admin/membros?q=%ZZ%", "/admin/membros?page=100&q=" + strings.Repeat("á", 101)} {
		link := memberSeasonSetupURL(member, collection)
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		query := u.Query()
		if u.Path != seasonsPath || query.Get("return_kind") != "member" || query.Get("return_id") != member.String() || query.Has("return_q") || query.Has("return_page") {
			t.Fatalf("invalid collection context survived: %q", link)
		}
	}
}
