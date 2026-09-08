package privacyrequests

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func fixture(t *testing.T, representative bool) (Case, Command) {
	t.Helper()
	requester := Actor{ID: uuid.New(), Active: true}
	subject := requester.ID
	if representative {
		subject = uuid.New()
	}
	at := time.Date(2028, time.February, 29, 12, 30, 0, 0, time.UTC)
	c, err := Receive(requester, true, true, subject, Scope{Kind: AccountClosure}, representative, at)
	if err != nil {
		t.Fatal(err)
	}
	return c, Command{
		Action: Approve, ExpectedVersion: c.Version, At: at.Add(time.Hour),
		Actor:        Actor{ID: uuid.New(), Active: true, PrivacyReviewer: true},
		Verification: Verification{IdentityVerified: true, CurrentRelationship: representative, RepresentationVerified: representative},
		Safeguards:   ClosureSafeguards{DependantsResolved: true, PreservesAdministrator: true},
		Policy:       Policy{Version: "test-matrix-v1", Adopted: true, Scope: c.Scope, Actions: []CategoryAction{{Category: "test-category", Action: "test-action"}}},
	}
}

func TestReceiveAuthorizationAndRepresentation(t *testing.T) {
	actor := Actor{ID: uuid.New(), Active: true}
	at := time.Now()
	for _, test := range []struct {
		name                   string
		actor                  Actor
		adult, recent, related bool
		subject                uuid.UUID
		want                   error
	}{
		{"adult self", actor, true, true, false, actor.ID, nil},
		{"existing link receives without verified authority", actor, true, true, true, uuid.New(), nil},
		{"unrelated", actor, true, true, false, uuid.New(), ErrForbidden},
		{"minor", actor, false, true, false, actor.ID, ErrForbidden},
		{"old authentication", actor, true, false, false, actor.ID, ErrForbidden},
		{"inactive", Actor{ID: actor.ID}, true, true, false, actor.ID, ErrForbidden},
		{"missing actor", Actor{Active: true}, true, true, false, actor.ID, ErrForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, err := Receive(test.actor, test.adult, test.recent, test.subject, Scope{Kind: AccountClosure}, test.related, at)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if err == nil && (c.Status != Received || c.Version != 1 || !c.ReceivedAt.Equal(at) || !c.ClosedAt.IsZero() || c.DecisionPolicy != nil) {
				t.Fatalf("unexpected receipt: %+v", c)
			}
		})
	}
}

func TestDecisionRejectsUnsafeFactsWithoutChangingCase(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Case, *Command)
		want   error
	}{
		{"ordinary administrator has no capability", func(c *Case, cmd *Command) { cmd.Actor.PrivacyReviewer = false }, ErrForbidden},
		{"revoked reviewer", func(c *Case, cmd *Command) { cmd.Actor.Active = false }, ErrForbidden},
		{"requester self review", func(c *Case, cmd *Command) { cmd.Actor.ID = c.RequesterID }, ErrForbidden},
		{"subject self review", func(c *Case, cmd *Command) { cmd.Actor.ID = c.SubjectID }, ErrForbidden},
		{"stale decision", func(c *Case, cmd *Command) { cmd.ExpectedVersion-- }, ErrStaleVersion},
		{"unverified identity", func(c *Case, cmd *Command) { cmd.Verification.IdentityVerified = false }, ErrVerification},
		{"link is not representation", func(c *Case, cmd *Command) { cmd.Verification.RepresentationVerified = false }, ErrVerification},
		{"representation revoked", func(c *Case, cmd *Command) { cmd.Verification.CurrentRelationship = false }, ErrVerification},
		{"conflict", func(c *Case, cmd *Command) { cmd.Verification.Conflict = true }, ErrVerification},
		{"matrix not adopted", func(c *Case, cmd *Command) { cmd.Policy.Adopted = false }, ErrPolicyUnresolved},
		{"missing version", func(c *Case, cmd *Command) { cmd.Policy.Version = "" }, ErrPolicyUnresolved},
		{"missing actions", func(c *Case, cmd *Command) { cmd.Policy.Actions = nil }, ErrPolicyUnresolved},
		{"dependants unresolved", func(c *Case, cmd *Command) { cmd.Safeguards.DependantsResolved = false }, ErrClosureSafeguards},
		{"last administrator", func(c *Case, cmd *Command) { cmd.Safeguards.PreservesAdministrator = false }, ErrClosureSafeguards},
		{"backdated decision", func(c *Case, cmd *Command) { cmd.At = c.ReceivedAt.Add(-time.Second) }, ErrInvalid},
		{"unknown action", func(c *Case, cmd *Command) { cmd.Action = "COMPLETE" }, ErrInvalidTransition},
		{"unknown source", func(c *Case, cmd *Command) { c.Status = "PROCESSING" }, ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, cmd := fixture(t, true)
			test.change(&c, &cmd)
			before := c.clone()
			result, err := Transition(c, cmd)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if !reflect.DeepEqual(result, Result{}) || !reflect.DeepEqual(c, before) {
				t.Fatal("rejected action returned or mutated state")
			}
		})
	}
}

func TestApprovalPreservesReceiptAndRequiresIndependentReadyExecutor(t *testing.T) {
	c, cmd := fixture(t, false)
	result, err := Transition(c, cmd)
	if err != nil {
		t.Fatal(err)
	}
	approved := result.Case
	if approved.Status != AwaitingExecution || !approved.ClosedAt.IsZero() || !approved.EvidenceExpiresAt.IsZero() || !approved.ReceivedAt.Equal(c.ReceivedAt) || approved.Version != 2 {
		t.Fatalf("approval must remain open: %+v", approved)
	}
	if result.Event != (Event{From: Received, To: AwaitingExecution, Version: 2, At: cmd.At}) {
		t.Fatalf("incorrect history: %+v", result.Event)
	}
	if approved.DecisionPolicy.Version != cmd.Policy.Version || !reflect.DeepEqual(approved.DecisionPolicy.Actions, cmd.Policy.Actions) {
		t.Fatal("decision lost exact matrix snapshot")
	}
	cmd.Policy.Actions[0].Action = "changed-later"
	if approved.DecisionPolicy.Actions[0].Action != "test-action" {
		t.Fatal("decision aliases caller policy")
	}
	cmd.ExpectedVersion = approved.Version
	cmd.Action = StartProcessing
	cmd.Actor = Actor{ID: uuid.New(), Active: true, PrivacyExecutor: true}
	if _, err = Transition(approved, cmd); !errors.Is(err, ErrExecutorUnavailable) {
		t.Fatalf("unexpected executor result: %v", err)
	}
	cmd.ExecutionReady = true
	processing, err := Transition(approved, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if processing.Case.Status != Processing || processing.Case.Version != 3 || !processing.Case.ClosedAt.IsZero() {
		t.Fatalf("incorrect processing handoff: %+v", processing.Case)
	}
	for _, actor := range []Actor{
		{ID: approved.RequesterID, Active: true, PrivacyExecutor: true},
		{ID: approved.SubjectID, Active: true, PrivacyExecutor: true},
		{ID: *approved.DecidedBy, Active: true, PrivacyExecutor: true},
		{ID: uuid.New(), Active: true},
	} {
		blocked := cmd
		blocked.Actor = actor
		if _, err := Transition(approved, blocked); !errors.Is(err, ErrForbidden) {
			t.Fatalf("actor %+v started execution: %v", actor, err)
		}
	}
	cmd.Action = Approve
	cmd.Actor = Actor{ID: *approved.DecidedBy, Active: true, PrivacyReviewer: true}
	if _, err = Transition(approved, cmd); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("repeat approval accepted: %v", err)
	}
	if c.Status != Received || c.DecisionPolicy != nil {
		t.Fatal("approval modified original receipt")
	}
}

func TestProcessingHandoffFailsClosedOnChangedFacts(t *testing.T) {
	c, cmd := fixture(t, true)
	approved, err := Transition(c, cmd)
	if err != nil {
		t.Fatal(err)
	}
	start := Command{
		Action: StartProcessing, ExpectedVersion: approved.Case.Version,
		Actor:        Actor{ID: uuid.New(), Active: true, PrivacyExecutor: true},
		Verification: approvedVerification(true), Safeguards: ClosureSafeguards{DependantsResolved: true, PreservesAdministrator: true},
		ExecutionReady: true, At: cmd.At.Add(time.Hour),
	}
	for _, test := range []struct {
		name   string
		change func(*Command)
		want   error
	}{
		{name: "stale request", change: func(in *Command) { in.ExpectedVersion-- }, want: ErrStaleVersion},
		{name: "identity changed", change: func(in *Command) { in.Verification.IdentityVerified = false }, want: ErrVerification},
		{name: "relationship changed", change: func(in *Command) { in.Verification.CurrentRelationship = false }, want: ErrVerification},
		{name: "dependant unresolved", change: func(in *Command) { in.Safeguards.DependantsResolved = false }, want: ErrClosureSafeguards},
		{name: "last administrator", change: func(in *Command) { in.Safeguards.PreservesAdministrator = false }, want: ErrClosureSafeguards},
		{name: "capability graph unavailable", change: func(in *Command) { in.ExecutionReady = false }, want: ErrExecutorUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := start
			test.change(&candidate)
			if _, err := Transition(approved.Case, candidate); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func approvedVerification(representative bool) Verification {
	return Verification{IdentityVerified: true, CurrentRelationship: representative, RepresentationVerified: representative}
}

func TestProcessingAndFailureStatesRemainOpenAndCannotBeCancelledOrRestarted(t *testing.T) {
	c, cmd := fixture(t, false)
	approved, err := Transition(c, cmd)
	if err != nil {
		t.Fatal(err)
	}
	start := Command{Action: StartProcessing, ExpectedVersion: approved.Case.Version, Actor: Actor{ID: uuid.New(), Active: true, PrivacyExecutor: true}, Verification: approvedVerification(false), Safeguards: ClosureSafeguards{DependantsResolved: true, PreservesAdministrator: true}, ExecutionReady: true, At: cmd.At.Add(time.Hour)}
	processing, err := Transition(approved.Case, start)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []Status{Processing, RetryableFailed, TerminalFailed} {
		open := processing.Case
		open.Status = status
		if !open.valid() || !open.ClosedAt.IsZero() || !open.EvidenceExpiresAt.IsZero() {
			t.Fatalf("state %s is not a valid open obligation: %+v", status, open)
		}
		cancel := Command{Action: Cancel, ExpectedVersion: open.Version, Actor: Actor{ID: open.RequesterID, Active: true}, At: start.At.Add(time.Hour)}
		if _, err := Transition(open, cancel); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("state %s accepted cancellation: %v", status, err)
		}
		start.ExpectedVersion = open.Version
		start.At = cancel.At
		if _, err := Transition(open, start); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("state %s accepted duplicate start: %v", status, err)
		}
	}
}

func TestCancellationAndRefusalCloseWithCalendarExpiry(t *testing.T) {
	for _, action := range []Action{Cancel, Refuse} {
		t.Run(string(action), func(t *testing.T) {
			c, cmd := fixture(t, false)
			cmd.Action = action
			want := Refused
			if action == Cancel {
				cmd.Actor = Actor{ID: c.RequesterID, Active: true}
				cmd.Policy = Policy{} // Cancellation is independent of matrix adoption.
				cmd.Verification = Verification{}
				want = Cancelled
			}
			result, err := Transition(c, cmd)
			if err != nil {
				t.Fatal(err)
			}
			expiry := time.Date(2030, time.February, 28, 13, 30, 0, 0, time.UTC)
			if result.Case.Status != want || !result.Case.ClosedAt.Equal(cmd.At) || !result.Case.EvidenceExpiresAt.Equal(expiry) {
				t.Fatalf("incorrect terminal evidence: %+v", result.Case)
			}
			cmd.ExpectedVersion = result.Case.Version
			if _, err = Transition(result.Case, cmd); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("closed case transitioned: %v", err)
			}
		})
	}
}

func TestRepresentativeCancellationAndDisclosureRequireCurrentAuthority(t *testing.T) {
	c, cmd := fixture(t, true)
	approved, err := Transition(c, cmd)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Action = Cancel
	cmd.Actor = Actor{ID: c.RequesterID, Active: true}
	cmd.ExpectedVersion = approved.Case.Version
	cmd.Policy = Policy{}
	for _, change := range []func(*Verification){
		func(v *Verification) { v.CurrentRelationship = false },
		func(v *Verification) { v.RepresentationVerified = false },
		func(v *Verification) { v.Conflict = true },
	} {
		blocked := cmd
		change(&blocked.Verification)
		if CanDiscloseToRequester(approved.Case, blocked.Actor, blocked.Verification) {
			t.Fatal("revoked representative can read case")
		}
		if _, err := Transition(approved.Case, blocked); !errors.Is(err, ErrVerification) {
			t.Fatalf("revoked representative cancellation: %v", err)
		}
	}
	if !CanDiscloseToRequester(approved.Case, cmd.Actor, cmd.Verification) {
		t.Fatal("current verified requester denied")
	}
	if _, err := Transition(approved.Case, cmd); err != nil {
		t.Fatalf("cancel before processing: %v", err)
	}
	cmd.Actor.ID = uuid.New()
	if CanDiscloseToRequester(c, cmd.Actor, cmd.Verification) {
		t.Fatal("unrelated user can read case")
	}
	if _, err := Transition(approved.Case, cmd); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unrelated cancellation: %v", err)
	}
}

func TestRequesterCanWithdrawBeforeRepresentationVerification(t *testing.T) {
	c, cmd := fixture(t, true)
	cmd.Action = Cancel
	cmd.Actor = Actor{ID: c.RequesterID, Active: true}
	cmd.Verification = Verification{CurrentRelationship: true}
	cmd.Policy = Policy{}
	if CanDiscloseToRequester(c, cmd.Actor, cmd.Verification) {
		t.Fatal("unverified representation allows disclosure")
	}
	if _, err := Transition(c, cmd); err != nil {
		t.Fatalf("own unprocessed request cannot be withdrawn: %v", err)
	}
}

func TestCategoryDecisionRequiresExactAdoptedPlan(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Policy)
		want   error
	}{
		{"exact scope", func(p *Policy) {}, nil},
		{"different order", func(p *Policy) { p.Scope.Categories = []Category{"test-b", "test-a"} }, nil},
		{"broader scope", func(p *Policy) { p.Scope.Categories = append(p.Scope.Categories, "test-c") }, ErrPolicyUnresolved},
		{"missing action", func(p *Policy) { p.Actions = p.Actions[:1] }, ErrPolicyUnresolved},
		{"extra action", func(p *Policy) { p.Actions = append(p.Actions, CategoryAction{"test-c", "test-action"}) }, ErrPolicyUnresolved},
		{"duplicate category", func(p *Policy) { p.Actions[1].Category = "test-a" }, ErrPolicyUnresolved},
		{"empty action", func(p *Policy) { p.Actions[0].Action = "" }, ErrPolicyUnresolved},
		{"free text version", func(p *Policy) { p.Version = "sensitive explanation" }, ErrPolicyUnresolved},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, cmd := fixture(t, false)
			c.Scope = Scope{Kind: Categories, Categories: []Category{"test-a", "test-b"}}
			cmd.Policy.Scope = c.Scope.clone()
			cmd.Policy.Actions = []CategoryAction{{"test-a", "test-action"}, {"test-b", "test-other-action"}}
			cmd.Safeguards = ClosureSafeguards{} // Category scope cannot close the account.
			test.change(&cmd.Policy)
			result, err := Transition(c, cmd)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if err == nil && result.Case.Scope.Kind != Categories {
				t.Fatal("category request changed to closure")
			}
		})
	}
}

func TestInvalidScopesRejectedAtReceipt(t *testing.T) {
	actor := Actor{ID: uuid.New(), Active: true}
	for _, scope := range []Scope{
		{}, {Kind: Categories}, {Kind: AccountClosure, Categories: []Category{"test-a"}},
		{Kind: Categories, Categories: []Category{"test-a", "test-a"}},
		{Kind: Categories, Categories: []Category{"free text"}},
	} {
		if _, err := Receive(actor, true, true, actor.ID, scope, false, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("scope %+v: %v", scope, err)
		}
	}
}

func TestMalformedReconstructedCasesFailClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Case)
	}{
		{"missing refused policy", func(c *Case) { c.DecisionPolicy = nil }},
		{"unapproved refused policy", func(c *Case) { c.DecisionPolicy.Adopted = false }},
		{"closure before receipt", func(c *Case) {
			c.ClosedAt = c.ReceivedAt.Add(-time.Hour)
			c.EvidenceExpiresAt = evidenceExpiry(c.ClosedAt)
		}},
		{"update before receipt", func(c *Case) { c.UpdatedAt = c.ReceivedAt.Add(-time.Hour) }},
		{"missing closure", func(c *Case) { c.ClosedAt = time.Time{} }},
		{"wrong expiry", func(c *Case) { c.EvidenceExpiresAt = c.ClosedAt.Add(time.Hour) }},
		{"missing subject", func(c *Case) { c.SubjectID = uuid.Nil }},
		{"invalid status", func(c *Case) { c.Status = "COMPLETED" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, cmd := fixture(t, false)
			cmd.Action = Refuse
			result, err := Transition(c, cmd)
			if err != nil {
				t.Fatal(err)
			}
			malformed := result.Case
			test.change(&malformed)
			if CanReview(malformed, cmd.Actor) {
				t.Fatal("review exposed malformed case")
			}
			if CanDiscloseToRequester(malformed, Actor{ID: c.RequesterID, Active: true}, cmd.Verification) {
				t.Fatal("disclosure exposed malformed case")
			}
			cmd.ExpectedVersion = malformed.Version
			if _, err := Transition(malformed, cmd); !errors.Is(err, ErrInvalid) {
				t.Fatalf("malformed case transitioned: %v", err)
			}
		})
	}
}

func TestEvidenceExpiryPreservesCalendarDayAndLocation(t *testing.T) {
	location := time.FixedZone("test-offset", 3600)
	closed := time.Date(2026, time.January, 31, 23, 45, 12, 123, location)
	want := time.Date(2028, time.January, 31, 23, 45, 12, 123, location)
	if got := evidenceExpiry(closed); !got.Equal(want) || got.Location() != location {
		t.Fatalf("expiry got %v, want %v", got, want)
	}
}

func TestScopesAndExistingDecisionsAreCloned(t *testing.T) {
	actor := Actor{ID: uuid.New(), Active: true}
	scope := Scope{Kind: Categories, Categories: []Category{"test-a"}}
	c, err := Receive(actor, true, true, actor.ID, scope, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scope.Categories[0] = "changed-input"
	if c.Scope.Categories[0] != "test-a" {
		t.Fatal("receipt aliases input scope")
	}
	_, cmd := fixture(t, false)
	cmd.At = c.UpdatedAt.Add(time.Hour)
	cmd.Policy.Scope = c.Scope.clone()
	cmd.Policy.Actions = []CategoryAction{{"test-a", "test-action"}}
	approved, err := Transition(c, cmd)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Policy.Scope.Categories[0] = "changed-policy-input"
	if approved.Case.DecisionPolicy.Scope.Categories[0] != "test-a" {
		t.Fatal("decision aliases input policy scope")
	}
	cmd.Action = Cancel
	cmd.Actor = actor
	cmd.ExpectedVersion = approved.Case.Version
	cancelled, err := Transition(approved.Case, cmd)
	if err != nil {
		t.Fatal(err)
	}
	cancelled.Case.Scope.Categories[0] = "changed-result-scope"
	cancelled.Case.DecisionPolicy.Scope.Categories[0] = "changed-result-policy"
	cancelled.Case.DecisionPolicy.Actions[0].Action = "changed-result-action"
	if approved.Case.Scope.Categories[0] != "test-a" || approved.Case.DecisionPolicy.Scope.Categories[0] != "test-a" || approved.Case.DecisionPolicy.Actions[0].Action != "test-action" {
		t.Fatal("later transition aliases prior decision")
	}
}
