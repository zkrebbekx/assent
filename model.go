package assent

import (
	"time"

	"github.com/zkrebbekx/sundial"
)

// Actor identifies who acts on an approval request: the maker who raised it, an
// approver who decides a step, or the authority a delegate acts for. ID is the
// only load-bearing field; it is the identity assent counts for a quorum and
// records on the audit trail. Name is an optional display label.
type Actor struct {
	// ID identifies the actor within the caller's system. It is required on
	// every request, decision and delegation.
	ID string
	// Name is an optional human-readable label, retained so a rendering of the
	// trail is legible without a join against the caller's user table.
	Name string
}

// IsZero reports whether the actor carries no identity.
func (a Actor) IsZero() bool { return a.ID == "" }

// Assignment names one approver slot on a step. Exactly one of User or Group is
// set: a named user, or a candidate group whose membership the caller resolves.
// assent holds the candidate group and records the actor who claims it; it does
// not itself resolve group membership (see the package doc).
type Assignment struct {
	// User is a named user. Set this or Group, not both.
	User string
	// Group is a candidate group. Any actor the caller routes may claim it.
	Group string
}

// IsZero reports whether the assignment names neither a user nor a group.
func (a Assignment) IsZero() bool { return a.User == "" && a.Group == "" }

// RuleKind enumerates how a step completes over its approvers.
type RuleKind int

const (
	// KindAnyone completes the step on the first approval (1 of N).
	KindAnyone RuleKind = iota
	// KindQuorum completes the step on N distinct approvals (M of N).
	KindQuorum
	// KindUnanimous completes the step when every named approver approves.
	KindUnanimous
)

// String renders the rule kind for diagnostics and audit payloads.
func (k RuleKind) String() string {
	switch k {
	case KindAnyone:
		return "anyone"
	case KindQuorum:
		return "quorum"
	case KindUnanimous:
		return "unanimous"
	default:
		return "unknown"
	}
}

// Rule is a step's completion rule, evaluated over the distinct approving actors
// after delegation resolution. Build one with [Anyone], [Quorum] or [Unanimous].
type Rule struct {
	// Kind selects the completion rule.
	Kind RuleKind
	// N is the number of distinct approvals a [KindQuorum] rule requires. It is
	// unused for the other kinds.
	N int
}

// Anyone returns a rule that completes a step on the first approval.
func Anyone() Rule { return Rule{Kind: KindAnyone} }

// Quorum returns a rule that completes a step on n distinct approvals.
func Quorum(n int) Rule { return Rule{Kind: KindQuorum, N: n} }

// Unanimous returns a rule that completes a step only when every named approver
// approves. assent models unanimity explicitly, as "all named approvers
// approved" — it does not infer it from a count.
func Unanimous() Rule { return Rule{Kind: KindUnanimous} }

// EscalationAction is what assent does to a step when its SLA breaches.
type EscalationAction int

const (
	// EscalateUp adds the fallback approver to the step.
	EscalateUp EscalationAction = iota
	// Reassign moves the pending assignment to the fallback approver.
	Reassign
	// AutoApprove completes the step on breach (for low-risk flows only).
	AutoApprove
)

// String renders the escalation action for diagnostics and audit payloads.
func (a EscalationAction) String() string {
	switch a {
	case EscalateUp:
		return "escalate_up"
	case Reassign:
		return "reassign"
	case AutoApprove:
		return "auto_approve"
	default:
		return "unknown"
	}
}

// Escalation is a per-step SLA and the action to take on breach. assent starts a
// [sundial.Timer] at step entry with the schedule, budget and levels, polls it
// on each [Engine.Tick], records each warn level once, and applies Action once
// when the budget breaches. sundial owns the working-time math and the
// write-once firing; assent owns the action and records it on the trail.
type Escalation struct {
	// Schedule is the business calendar the SLA clock advances on.
	Schedule sundial.Schedule
	// Budget is the working time the step is allowed before it breaches.
	Budget time.Duration
	// Levels are optional warn thresholds below the budget, strictly ascending
	// by At. Each fires once and is recorded on the trail.
	Levels []sundial.Level
	// Action is applied once, when the budget breaches.
	Action EscalationAction
	// Fallback is the approver added ([EscalateUp]) or moved to ([Reassign]) on
	// breach. It is unused for [AutoApprove].
	Fallback Assignment
}

// Step is one stage of an approval chain. Its approvers act at the same time and
// it completes by its rule. An optional escalation gives it an SLA.
type Step struct {
	// Name is an optional label recorded on the trail.
	Name string
	// Approvers are the named users and candidate groups that may decide the
	// step. At least one is required.
	Approvers []Assignment
	// Rule decides when the step completes.
	Rule Rule
	// Escalation is an optional per-step SLA and breach action.
	Escalation *Escalation
}

// Request is the approval object a caller submits. Its chain of steps runs one
// after another; when the last step approves, the request is approved.
type Request struct {
	// ID is the caller's unique identifier for the request.
	ID string
	// Maker is who raised the request. The maker is barred from approving any
	// step (four-eyes).
	Maker Actor
	// Series is the request's category, used to scope delegations. It may be
	// empty.
	Series string
	// Subject is an opaque reference the caller owns — for example the ID of the
	// document under approval. assent stores the reference, never the object.
	Subject string
	// Chain is the ordered list of steps. At least one is required.
	Chain []Step
}

// Decision is a single actor's ruling on a request's current step.
type Decision struct {
	// Actor is who really acted. It is required.
	Actor Actor
	// OnBehalfOf is the authority the actor acts for, when the actor is a
	// delegate. It is nil for a direct decision. A valid delegation must be in
	// force for the request's series at At, or the decision is rejected.
	OnBehalfOf *Actor
	// Group is the candidate group the actor claims, when acting via a group
	// slot rather than as a named user. It is recorded on the trail.
	Group string
	// Approve is true to approve, false to reject. A reject fails the request.
	Approve bool
	// Reason is the free-text justification, recorded on the trail. It is
	// compliance-load-bearing but not enforced non-empty.
	Reason string
	// At is the instant the decision was made. It is required, and it is the
	// instant the delegation window is checked against.
	At time.Time
}

// Interval is a half-open time window [Start, End): the lower bound is included
// and the upper bound is excluded. It is the window a [Delegation] holds within.
type Interval struct {
	// Start is the inclusive lower bound.
	Start time.Time
	// End is the exclusive upper bound.
	End time.Time
}

// Contains reports whether t lies in [Start, End).
func (i Interval) Contains(t time.Time) bool {
	return !t.Before(i.Start) && t.Before(i.End)
}

// Delegation grants To the authority to act on behalf of From, inside Window and
// for the listed Series. A decision the delegate makes counts once, under From,
// and the trail records both the acting delegate and the authority.
type Delegation struct {
	// From is the authority whose approvals are being delegated.
	From Actor
	// To is the delegate who may act for From.
	To Actor
	// Window is the half-open interval the delegation holds within.
	Window Interval
	// Series scopes the delegation to those request series. An empty slice means
	// every series.
	Series []string
}
