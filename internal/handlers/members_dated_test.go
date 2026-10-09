package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMemberLegacyToggleRetriesConcurrentSameProgrammeInsert(t *testing.T) {
	memberID, programmeID := uuid.New(), uuid.New()
	store := &memberWorkflowStore{season: dbgen.Season{ID: uuid.New()}, programmes: []dbgen.Programme{{ID: programmeID, Code: "Leisure"}},
		membershipErrors: []error{&pgconn.PgError{Code: "23505"}, nil}}
	req := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/inscricao", strings.NewReader("programme_id="+programmeID.String()+"&active=on"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", memberID.String())
	response := httptest.NewRecorder()
	(Members{Store: store, Location: time.UTC}).Membership(response, req)
	if response.Code != http.StatusSeeOther || store.membershipCalls != 2 {
		t.Fatalf("status=%d calls=%d", response.Code, store.membershipCalls)
	}
}

func TestMemberLegacyToggleRejectsUnclassifiedCompetitionAndDatedOverlap(t *testing.T) {
	memberID, programmeID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name, code  string
		writeErr    error
		wantMessage string
	}{
		{"competition requires category", "Competition", nil, "exige um escalão"},
		{"dated overlap is recoverable", "Leisure", &pgconn.PgError{Code: "23P01"}, "sobrepõe-se"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &memberWorkflowStore{season: dbgen.Season{ID: uuid.New()}, programmes: []dbgen.Programme{{ID: programmeID, Code: tc.code}}, membershipErr: tc.writeErr,
				member: dbgen.GetMemberForAdminRow{ID: memberID, Name: "Pessoa de teste", IsActive: true}}
			req := httptest.NewRequest(http.MethodPost, "/admin/membros/"+memberID.String()+"/inscricao", strings.NewReader("programme_id="+programmeID.String()+"&active=on"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("id", memberID.String())
			response := httptest.NewRecorder()
			(Members{Store: store, Location: time.UTC}).Membership(response, req)
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), tc.wantMessage) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.code == "Competition" && store.membership.UserID != uuid.Nil {
				t.Fatal("legacy competition write attempted")
			}
		})
	}
}
