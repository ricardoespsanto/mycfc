package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
)

func TestNativeVariationUnknownCommitNeverClaimsRollback(t *testing.T) {
	actor, plan, member, block := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, retire := range []bool{false, true} {
		store := &structuredTrainingStoreStub{weekOK: true, planID: plan, createVariationErr: errors.Join(db.ErrCommitOutcomeUnknown, errors.New("ambiguous socket closure")), retireVariationErr: db.ErrCommitOutcomeUnknown}
		handler := StructuredTraining{Store: store}
		var values url.Values
		fn := handler.CreateVariation
		key, path := "", ""
		if retire {
			values = url.Values{"version": {"1"}}
			fn = handler.RetireVariation
			key, path = "id", uuid.NewString()
		} else {
			values = url.Values{"plan_id": {plan.String()}, "target": {"ATHLETE:" + member.String()}, "subject": {"BLOCK:" + block.String()}, "operation": {"OVERRIDE"}, "change_summary": {"Own explicit reference"}, "intensity_profile_id": {uuid.NewString()}}
		}
		res := performStructuredTrainingRequest(t, CurrentUser{ID: actor, IsAdmin: true}, http.MethodPost, "/", values, key, path, fn)
		body := res.Body.String()
		if res.Code != 503 || !strings.Contains(body, "não será repetido automaticamente") || strings.Contains(body, "Nada foi guardado") || res.Header().Get("Location") != "" {
			t.Fatalf("retire=%v status=%d unknown outcome misrepresented", retire, res.Code)
		}
	}
}
