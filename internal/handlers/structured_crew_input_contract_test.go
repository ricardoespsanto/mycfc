package handlers

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
)

// A legacy modalities UUID must never be interpreted as a canoe craft code,
// even for staff who are authorized to manage the training group.
func TestStructuredCrewInputRequiresCraftCodeAndGroupAuthority(t *testing.T) {
	groupID, actorID, legacyModalityID := uuid.New(), uuid.New(), uuid.New()
	for _, actor := range []struct {
		name       string
		admin      bool
		manageable bool
	}{
		{"administrator", true, false},
		{"scoped coach", false, true},
		{"unscoped coach", false, false},
	} {
		for _, input := range []struct {
			name, field, value string
			capacity           int
			validCode          bool
		}{
			{"C2 code", "craft_code", "C2", 2, true},
			{"K2 code", "craft_code", "K2", 2, true},
			{"K4 code", "craft_code", "K4", 4, true},
			{"legacy UUID in code field", "craft_code", legacyModalityID.String(), 2, false},
			{"obsolete UUID field only", "craft_modality_id", legacyModalityID.String(), 2, false},
		} {
			t.Run(actor.name+"/"+input.name, func(t *testing.T) {
				store := &structuredTrainingStoreStub{
					manageable: actor.manageable,
					crewModalities: []dbgen.CanoeCraftClass{
						{Code: "C2", NamePt: "Canoa de dois"},
						{Code: "K2", NamePt: "Kayak de dois"},
						{Code: "K4", NamePt: "Kayak de quatro"},
					},
				}
				values := url.Values{
					"training_group_id": {groupID.String()}, "kind": {"CREW"},
					"name": {"Tripulação de teste"}, "effective_from": {"2026-09-07"},
					"effective_until": {"2026-09-21"}, input.field: {input.value},
				}
				members := make([]uuid.UUID, input.capacity)
				for i := range members {
					members[i] = uuid.New()
					values.Add("membership_id", members[i].String())
				}
				response := performStructuredTrainingRequest(t,
					CurrentUser{ID: actorID, IsAdmin: actor.admin}, http.MethodPost,
					"/admin/treinos/estruturados/variacoes/grupos", values, "", "",
					(StructuredTraining{Store: store, Location: time.UTC}).CreateVariationGroup)
				allowed := input.validCode && (actor.admin || actor.manageable)
				wantStatus := http.StatusForbidden
				if allowed {
					wantStatus = http.StatusSeeOther
				}
				if response.Code != wantStatus {
					t.Fatalf("status=%d, want %d", response.Code, wantStatus)
				}
				if len(store.manageArgs) != 1 || store.manageArgs[0].GroupID != groupID ||
					store.manageArgs[0].UserID != actorID || store.manageArgs[0].IsAdmin != actor.admin {
					t.Fatalf("group authority must be checked with the session actor: %#v", store.manageArgs)
				}
				got := store.variationGroup
				if !allowed {
					if got.Params.TrainingGroupID != uuid.Nil || len(got.MembershipIDs) != 0 {
						t.Fatalf("rejected request reached persistence: %#v", got)
					}
					return
				}
				if got.Params.CraftCode == nil || *got.Params.CraftCode != input.value ||
					got.Params.TrainingGroupID != groupID || got.Params.CreatedByID != actorID ||
					got.Params.Kind != dbgen.TrainingVariationGroupKindCREW || len(got.MembershipIDs) != len(members) {
					t.Fatalf("valid code lost craft, group or session provenance: %#v", got)
				}
				for i, member := range members {
					if got.MembershipIDs[i] != member {
						t.Fatalf("membership %d changed: got %s, want %s", i, got.MembershipIDs[i], member)
					}
				}
			})
		}
	}
}
