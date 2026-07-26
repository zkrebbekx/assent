package assent

import (
	"errors"
	"fmt"
)

// wrap adds identifying context to a sentinel while keeping errors.Is matches
// working through the %w verb.
func wrap(sentinel error, id string) error {
	return fmt.Errorf("%w: %q", sentinel, id)
}

// Sentinel errors returned, usually wrapped with %w, by this package. Match them
// with [errors.Is] rather than by comparing error strings.
var (
	// ErrNotFound is returned when no request exists for an ID.
	ErrNotFound = errors.New("assent: request not found")

	// ErrExists is returned by [Engine.Submit] and [Engine.Restore] when a
	// request already exists under the given ID.
	ErrExists = errors.New("assent: request already exists")

	// ErrBadRequest is returned when a submitted request is structurally
	// unusable: an empty ID, a zero maker, or an empty chain.
	ErrBadRequest = errors.New("assent: invalid request")

	// ErrEmptyChain is returned when a request has no steps.
	ErrEmptyChain = errors.New("assent: empty chain")

	// ErrEmptyStep is returned when a step lists no approvers.
	ErrEmptyStep = errors.New("assent: empty step")

	// ErrBadAssignment is returned when an approver assignment does not name
	// exactly one of a user or a group.
	ErrBadAssignment = errors.New("assent: invalid assignment")

	// ErrBadRule is returned for an unusable step rule: a quorum below one, or a
	// quorum larger than the step's named approvers can satisfy.
	ErrBadRule = errors.New("assent: invalid rule")

	// ErrBadEscalation is returned for an unusable escalation: an invalid
	// sundial config, or an escalate/reassign action with no fallback approver.
	ErrBadEscalation = errors.New("assent: invalid escalation")

	// ErrBadDecision is returned for a decision with no actor, no timestamp, or
	// a zero on-behalf-of authority.
	ErrBadDecision = errors.New("assent: invalid decision")

	// ErrNotPending is returned when a decision, withdrawal or reassignment is
	// attempted on a request that is not pending. Because Approved, Rejected and
	// Withdrawn are terminal states in the lifecycle machine, the machine itself
	// makes the illegal transition impossible; assent reports it as this error.
	ErrNotPending = errors.New("assent: request is not pending")

	// ErrStepNotActive is returned when an actor decides a step that is not the
	// current step — a later step whose turn has not come.
	ErrStepNotActive = errors.New("assent: step is not active")

	// ErrNotApprover is returned when the deciding authority is not an approver
	// of the request's chain.
	ErrNotApprover = errors.New("assent: actor is not an approver")

	// ErrMakerChecker is returned when the maker of a request tries to approve
	// it. The maker cannot be a checker (four-eyes).
	ErrMakerChecker = errors.New("assent: maker cannot be a checker")

	// ErrSeparation is returned by a segregation-of-duties predicate that a
	// decision violates. The built-in [SeparateSteps] predicate wraps it.
	ErrSeparation = errors.New("assent: segregation of duties")

	// ErrNoDelegation is returned when a decision is made on behalf of another
	// with no delegation in force for the request's series at the decision time.
	ErrNoDelegation = errors.New("assent: no delegation in force")

	// ErrNotMaker is returned when someone other than the maker tries to
	// withdraw a request.
	ErrNotMaker = errors.New("assent: only the maker may withdraw")

	// ErrBadSnapshot is returned by [Engine.Restore] when a snapshot is
	// structurally unusable.
	ErrBadSnapshot = errors.New("assent: invalid snapshot")
)
