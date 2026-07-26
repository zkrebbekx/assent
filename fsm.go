package assent

import (
	"context"

	"github.com/zkrebbekx/fsmx"
)

// State is a request's lifecycle state. The states and their legal transitions
// are declared once, at engine construction, in a [fsmx] machine; every state
// change goes through that machine, so an illegal transition — approving a
// withdrawn request — is impossible.
type State string

const (
	// Draft is a request that has been created but not submitted.
	Draft State = "draft"
	// Pending is a request in flight through its chain.
	Pending State = "pending"
	// Approved is a request whose last step approved: a terminal state.
	Approved State = "approved"
	// Rejected is a request an approver rejected: a terminal state.
	Rejected State = "rejected"
	// Withdrawn is a request the maker withdrew: a terminal state.
	Withdrawn State = "withdrawn"
)

// event is a lifecycle transition trigger. It is unexported: callers drive the
// engine through Submit, Decide, Tick and Withdraw, and assent chooses the
// event. "advance" and "approve" split what the design calls a single approve:
// a step completing mid-chain fires advance (Pending stays Pending on the next
// step), while the last step completing fires approve (Pending to Approved).
// fsmx allows exactly one target per (state, event) pair, so the split is what
// lets both outcomes live in one validated table.
type event string

const (
	evSubmit   event = "submit"
	evAdvance  event = "advance"
	evEscalate event = "escalate"
	evApprove  event = "approve"
	evReject   event = "reject"
	evWithdraw event = "withdraw"
)

// reqModel is the tiny model the lifecycle machine reads and writes. assent
// rehydrates one per operation from the persisted state, fires the event, and
// reads the new state back, so the machine is shared and concurrency-safe while
// each operation mutates only its own model.
type reqModel struct {
	fsmx.Field[State]
}

// buildMachine declares the request lifecycle and validates it once.
func buildMachine() (*fsmx.Machine[State, event, *reqModel], error) {
	return fsmx.NewFor[State, event, *reqModel](Draft).
		Transition(Draft, evSubmit, Pending).
		Transition(Pending, evAdvance, Pending).
		Transition(Pending, evEscalate, Pending).
		Transition(Pending, evApprove, Approved).
		Transition(Pending, evReject, Rejected).
		Transition(Pending, evWithdraw, Withdrawn).
		Build()
}

// fire applies ev to a model in state s and returns the resulting state. A
// transition the machine does not declare returns the machine's error unchanged
// (wrapping [fsmx.ErrNoTransition]).
func (e *Engine) fire(ctx context.Context, s State, ev event) (State, error) {
	m := &reqModel{}
	m.SetState(s)
	if err := e.machine.Fire(ctx, m, ev); err != nil {
		return s, err
	}
	return m.GetState(), nil
}

// pending reports whether a request in state s is pending. It asks the machine
// rather than comparing the constant, so "is a decision legal now" stays a
// property of the transition table: evReject is declared only from Pending, so
// Can is true in exactly that state.
func (e *Engine) pending(s State) bool {
	m := &reqModel{}
	m.SetState(s)
	return e.machine.Can(m, evReject)
}
