// Package privacyrequests defines the unexposed request-review foundation.
// It performs no persistence, authorization lookup, account mutation or erasure.
// Callers must load trusted, current facts for the exact case inside a transaction,
// enforce version compare-and-swap, and persist the case and event atomically.
// Facts must never be populated from browser-submitted claims. Public activation
// additionally requires approved scopes and a functioning fulfilment route.
package privacyrequests

import (
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalid             = errors.New("invalid privacy request")
	ErrForbidden           = errors.New("privacy request action is not authorized")
	ErrStaleVersion        = errors.New("privacy request version is stale")
	ErrInvalidTransition   = errors.New("privacy request transition is invalid")
	ErrVerification        = errors.New("privacy request verification is unresolved")
	ErrPolicyUnresolved    = errors.New("privacy request policy is unresolved")
	ErrClosureSafeguards   = errors.New("account closure safeguards are unresolved")
	ErrExecutorUnavailable = errors.New("privacy request executor is unavailable")
)

type Status string

const (
	Received          Status = "RECEIVED"
	UnderReview       Status = "UNDER_REVIEW"
	PartiallyApproved Status = "PARTIALLY_APPROVED"
	AwaitingExecution Status = "AWAITING_EXECUTION"
	Cancelled         Status = "CANCELLED"
	Refused           Status = "REFUSED"
)

type ScopeKind string

const (
	AccountClosure ScopeKind = "ACCOUNT_CLOSURE"
	Categories     ScopeKind = "CATEGORIES"
)

// Category and ActionCode are opaque adopted-policy keys. No category, period,
// action or matrix is approved by this package.
type Category string
type ActionCode string

type Scope struct {
	Kind       ScopeKind
	Categories []Category
}

type CategoryAction struct {
	Category Category
	Action   ActionCode
}

// Policy is a trusted snapshot of the adopted matrix for this exact scope.
// Adopted must reflect actual approval of every included action, not the approval
// of the general privacy operating choices. Actions describe required execution;
// they are not evidence that any action has already occurred.
type Policy struct {
	Version string
	Adopted bool
	Scope   Scope
	Actions []CategoryAction
}

type Case struct {
	RequesterID       uuid.UUID
	SubjectID         uuid.UUID
	Scope             Scope
	Status            Status
	Version           int64
	ReceivedAt        time.Time
	UpdatedAt         time.Time
	ClosedAt          time.Time
	EvidenceExpiresAt time.Time
	DecisionPolicy    *Policy
}

// Actor capability is explicitly granted; ordinary administrator status cannot
// substitute for PrivacyReviewer. Active is rechecked on every operation.
type Actor struct {
	ID              uuid.UUID
	Active          bool
	PrivacyReviewer bool
}

// Verification is case-specific. A current guardian relationship alone does not
// prove representative authority. Both checks must be refreshed before disclosure
// or decision and after any relevant relationship or authority change.
type Verification struct {
	IdentityVerified       bool
	CurrentRelationship    bool
	RepresentationVerified bool
	Conflict               bool
}

type ClosureSafeguards struct {
	// Confirmed includes confirming that there are no dependants when applicable.
	DependantsResolved bool
	// Confirmed includes checking current active administrator counts.
	PreservesAdministrator bool
}

type Action string

const (
	Approve         Action = "APPROVE"
	PartialApprove  Action = "PARTIAL_APPROVE"
	Refuse          Action = "REFUSE"
	Cancel          Action = "CANCEL"
	StartProcessing Action = "START_PROCESSING"
)

type Command struct {
	Action          Action
	ExpectedVersion int64
	Actor           Actor
	Verification    Verification
	Safeguards      ClosureSafeguards
	Policy          Policy
	At              time.Time
}

// Event contains only transition metadata. The persistence layer must associate
// its opaque case reference and actor ID; free-form working explanations, subject
// data, object identifiers and provider identifiers belong in neither events nor logs.
type Event struct {
	From    Status
	To      Status
	Version int64
	At      time.Time
}

type Result struct {
	Case  Case
	Event Event
}

// Receive records a receipt without deciding or disclosing a representative case.
// RecentAuthentication must come from the application's recent-auth validation;
// this foundation deliberately does not invent a permitted authentication age.
// Idempotency keys and opaque references are owned by the future durable store.
func Receive(requester Actor, adult, recentAuthentication bool, subject uuid.UUID, scope Scope, currentRelationship bool, at time.Time) (Case, error) {
	if requester.ID == uuid.Nil || !requester.Active || !adult || !recentAuthentication {
		return Case{}, ErrForbidden
	}
	if subject == uuid.Nil || !scope.valid() || at.IsZero() {
		return Case{}, ErrInvalid
	}
	if requester.ID != subject && !currentRelationship {
		return Case{}, ErrForbidden
	}
	return Case{RequesterID: requester.ID, SubjectID: subject, Scope: scope.clone(), Status: Received, Version: 1, ReceivedAt: at, UpdatedAt: at}, nil
}

// CanReview prevents both requester and subject from reviewing their own case.
// Reviewer grants must be loaded afresh; general admin privileges are irrelevant.
func CanReview(c Case, actor Actor) bool {
	return c.valid() && actor.ID != uuid.Nil && actor.Active && actor.PrivacyReviewer && actor.ID != c.RequesterID && actor.ID != c.SubjectID
}

// CanDiscloseToRequester rechecks representation even for a previously approved
// case. Initial acknowledgement, without protected case content, is separate.
func CanDiscloseToRequester(c Case, actor Actor, v Verification) bool {
	return c.valid() && actor.ID != uuid.Nil && actor.Active && actor.ID == c.RequesterID && verified(c, v)
}

// Transition has no account-access side effects. Approval only queues a decision
// awaiting execution and never closes a case or starts its evidence-retention clock.
// Execution is deliberately unavailable until #111 can atomically accept work,
// disable account-closure authentication and revoke sessions. That executor must
// recheck authorization, policy, representation and closure safeguards at handoff.
func Transition(c Case, cmd Command) (Result, error) {
	if !c.valid() || cmd.At.IsZero() || cmd.At.Before(c.UpdatedAt) {
		return Result{}, ErrInvalid
	}
	if cmd.Action == Cancel {
		if cmd.Actor.ID != c.RequesterID || !cmd.Actor.Active {
			return Result{}, ErrForbidden
		}
		// Withdrawing one's own received submission does not decide or
		// disclose subject data, so outstanding verification need not block it.
		if c.RequesterID != c.SubjectID && (!cmd.Verification.CurrentRelationship || cmd.Verification.Conflict) {
			return Result{}, ErrVerification
		}
		if c.RequesterID != c.SubjectID && (c.Status == AwaitingExecution || c.Status == PartiallyApproved) && !verified(c, cmd.Verification) {
			return Result{}, ErrVerification
		}
	} else if !CanReview(c, cmd.Actor) {
		return Result{}, ErrForbidden
	}
	if cmd.ExpectedVersion != c.Version {
		return Result{}, ErrStaleVersion
	}
	if c.Status == Cancelled || c.Status == Refused {
		return Result{}, ErrInvalidTransition
	}
	if cmd.Action == StartProcessing {
		if c.Status != AwaitingExecution && c.Status != PartiallyApproved {
			return Result{}, ErrInvalidTransition
		}
		return Result{}, ErrExecutorUnavailable
	}
	if cmd.Action != Cancel && c.Status != Received && c.Status != UnderReview {
		return Result{}, ErrInvalidTransition
	}
	next := c.clone()
	switch cmd.Action {
	case Cancel:
		next.Status = Cancelled
	case Approve, PartialApprove, Refuse:
		if !verified(c, cmd.Verification) {
			return Result{}, ErrVerification
		}
		if !cmd.Policy.validFor(c.Scope) {
			return Result{}, ErrPolicyUnresolved
		}
		if (cmd.Action == Approve || cmd.Action == PartialApprove) && c.Scope.Kind == AccountClosure && (!cmd.Safeguards.DependantsResolved || !cmd.Safeguards.PreservesAdministrator) {
			return Result{}, ErrClosureSafeguards
		}
		policy := cmd.Policy.clone()
		next.DecisionPolicy = &policy
		next.Status = AwaitingExecution
		if cmd.Action == PartialApprove {
			next.Status = PartiallyApproved
		}
		if cmd.Action == Refuse {
			next.Status = Refused
		}
	default:
		return Result{}, ErrInvalidTransition
	}
	if next.Status == Cancelled || next.Status == Refused {
		next.ClosedAt = cmd.At
		next.EvidenceExpiresAt = evidenceExpiry(cmd.At)
	}
	next.Version++
	next.UpdatedAt = cmd.At
	return Result{Case: next, Event: Event{From: c.Status, To: next.Status, Version: next.Version, At: cmd.At}}, nil
}

func verified(c Case, v Verification) bool {
	if !v.IdentityVerified || v.Conflict {
		return false
	}
	return c.RequesterID == c.SubjectID || (v.CurrentRelationship && v.RepresentationVerified)
}

var policyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$`)

func (s Scope) valid() bool {
	if s.Kind == AccountClosure {
		return len(s.Categories) == 0
	}
	if s.Kind != Categories || len(s.Categories) == 0 || len(s.Categories) > 100 {
		return false
	}
	seen := make(map[Category]bool, len(s.Categories))
	for _, category := range s.Categories {
		if !policyKey.MatchString(string(category)) || seen[category] {
			return false
		}
		seen[category] = true
	}
	return true
}

func (p Policy) validFor(scope Scope) bool {
	if !p.Adopted || !policyKey.MatchString(p.Version) || !p.Scope.valid() || p.Scope.Kind != scope.Kind || len(p.Scope.Categories) != len(scope.Categories) || len(p.Actions) == 0 || len(p.Actions) > 100 {
		return false
	}
	for _, category := range scope.Categories {
		if !slices.Contains(p.Scope.Categories, category) {
			return false
		}
	}
	seen := make(map[Category]bool, len(p.Actions))
	for _, action := range p.Actions {
		if !policyKey.MatchString(string(action.Category)) || !policyKey.MatchString(string(action.Action)) || seen[action.Category] {
			return false
		}
		if scope.Kind == Categories && !slices.Contains(scope.Categories, action.Category) {
			return false
		}
		seen[action.Category] = true
	}
	return scope.Kind == AccountClosure || len(seen) == len(scope.Categories)
}

func (c Case) valid() bool {
	if c.RequesterID == uuid.Nil || c.SubjectID == uuid.Nil || !c.Scope.valid() || c.Version < 1 || c.Version == 1<<63-1 || c.ReceivedAt.IsZero() || c.UpdatedAt.Before(c.ReceivedAt) {
		return false
	}
	switch c.Status {
	case Received, UnderReview:
		return c.DecisionPolicy == nil && c.ClosedAt.IsZero() && c.EvidenceExpiresAt.IsZero()
	case AwaitingExecution, PartiallyApproved:
		return c.DecisionPolicy != nil && c.DecisionPolicy.validFor(c.Scope) && c.ClosedAt.IsZero() && c.EvidenceExpiresAt.IsZero()
	case Cancelled, Refused:
		if c.ClosedAt.IsZero() || !c.ClosedAt.Equal(c.UpdatedAt) || !c.EvidenceExpiresAt.Equal(evidenceExpiry(c.ClosedAt)) {
			return false
		}
		if c.DecisionPolicy != nil {
			return c.DecisionPolicy.validFor(c.Scope)
		}
		return c.Status == Cancelled
	default:
		return false
	}
}

func (s Scope) clone() Scope {
	s.Categories = slices.Clone(s.Categories)
	return s
}

func (p Policy) clone() Policy {
	p.Scope = p.Scope.clone()
	p.Actions = slices.Clone(p.Actions)
	return p
}

func (c Case) clone() Case {
	c.Scope = c.Scope.clone()
	if c.DecisionPolicy != nil {
		policy := c.DecisionPolicy.clone()
		c.DecisionPolicy = &policy
	}
	return c
}

// Use calendar months, clamping leap day to the final day of the target month.
// This clock applies only to minimal closed-case evidence, not working records.
func evidenceExpiry(closed time.Time) time.Time {
	year, month, day := closed.Date()
	lastDay := time.Date(year+2, month+1, 0, 0, 0, 0, 0, closed.Location()).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year+2, month, day, closed.Hour(), closed.Minute(), closed.Second(), closed.Nanosecond(), closed.Location())
}
