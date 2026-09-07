package privacyrequests

import (
	"errors"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func testPolicy() AdoptedPolicy {
	return AdoptedPolicy{Version: "test-v1", AccountClosureEnabled: true, WorkingRetentionDays: 30, ResponseMonths: 1, ExtensionMonths: 2, Categories: []CatalogueEntry{{Key: "alpha", Label: "Categoria alfa", Action: "ERASE", Grounds: []Ground{{Code: "HOLD", Label: "Exceção aprovada"}}}, {Key: "beta", Label: "Categoria beta", Action: "ERASE", Grounds: []Ground{{Code: "HOLD", Label: "Exceção aprovada"}}}}}
}
func TestCalendarDeadlineClampsLisbonMonths(t *testing.T) {
	l, _ := time.LoadLocation("Europe/Lisbon")
	for _, tc := range []struct {
		at, want string
		months   int
	}{{"2024-01-31T12:30:00", "2024-02-29T12:30:00", 1}, {"2025-01-31T12:30:00", "2025-02-28T12:30:00", 1}, {"2025-03-29T12:30:00", "2025-04-29T12:30:00", 1}, {"2025-11-30T12:30:00", "2026-02-28T12:30:00", 3}} {
		at, _ := time.ParseInLocation("2006-01-02T15:04:05", tc.at, l)
		got := CalendarDeadline(at, tc.months)
		if got.Format("2006-01-02T15:04:05") != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}
func TestCatalogueRejectsUnapprovedAndInventedDecisionGrounds(t *testing.T) {
	p := testPolicy()
	scope := Scope{Kind: Categories, Categories: []Category{"alpha", "beta"}}
	partial := map[string]CategoryDecision{"alpha": {Outcome: "APPROVE"}, "beta": {Outcome: "RETAIN", Ground: "HOLD"}}
	if _, e := p.Decisions(scope, partial, "partial"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Decisions(scope, partial, "approve"); e == nil {
		t.Fatal("partial accepted as full")
	}
	partial["beta"] = CategoryDecision{Outcome: "RETAIN", Ground: "invented"}
	if _, e := p.Decisions(scope, partial, "partial"); e == nil {
		t.Fatal("invented ground")
	}
	for _, months := range []int32{0, 2, 12} {
		p.ResponseMonths = months
		if p.Validate() == nil {
			t.Fatal("invalid response months accepted")
		}
	}
	p = testPolicy()
	p.ExtensionMonths = 12
	if p.Validate() == nil {
		t.Fatal("invalid extension months")
	}
	p = testPolicy()
	p.Categories = append(p.Categories, p.Categories[0])
	if p.Validate() == nil {
		t.Fatal("duplicate catalogue")
	}
	p = testPolicy()
	p.AccountClosureEnabled = false
	if _, e := p.Snapshot(Scope{Kind: AccountClosure}); e == nil {
		t.Fatal("unapproved closure")
	}
}
func TestPartialLifecycleNeverClosesOrRevokesAndChecksGuardians(t *testing.T) {
	at := time.Now()
	requester, subject, reviewer := uuid.New(), uuid.New(), uuid.New()
	scope := Scope{Kind: AccountClosure}
	p, _ := testPolicy().Snapshot(scope)
	c, e := Receive(Actor{ID: requester, Active: true}, true, true, subject, scope, true, at)
	if e != nil {
		t.Fatal(e)
	}
	c.Status = UnderReview
	cmd := Command{Action: PartialApprove, ExpectedVersion: 1, Actor: Actor{ID: reviewer, Active: true, PrivacyReviewer: true}, Verification: Verification{IdentityVerified: true, RepresentationVerified: true, CurrentRelationship: true}, Policy: p, At: at}
	if _, e = Transition(c, cmd); !errors.Is(e, ErrClosureSafeguards) {
		t.Fatal(e)
	}
	cmd.Safeguards = ClosureSafeguards{true, true}
	r, e := Transition(c, cmd)
	if e != nil || r.Case.Status != PartiallyApproved || !r.Case.ClosedAt.IsZero() || !r.Case.EvidenceExpiresAt.IsZero() {
		t.Fatalf("partial=%+v %v", r, e)
	}
	cmd.Action = Cancel
	cmd.Actor = Actor{ID: requester, Active: true}
	cmd.ExpectedVersion = 2
	cmd.Verification.RepresentationVerified = false
	if _, e = Transition(r.Case, cmd); !errors.Is(e, ErrVerification) {
		t.Fatal(e)
	}
}
func TestDeliveryDomainSeparationAndNoSensitiveContent(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	d := Delivery{Recipient: "person@example.test", ContactURL: "https://example.test/legal/direitos"}
	sealed, e := SealDelivery(key, d)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(sealed), d.Recipient) {
		t.Fatal("plaintext recipient")
	}
	got, e := OpenDelivery(key, sealed)
	if e != nil || got != d {
		t.Fatal(got, e)
	}
	sealed[len(sealed)-1] ^= 1
	if _, e = OpenDelivery(key, sealed); e == nil {
		t.Fatal("tampered payload")
	}
}
