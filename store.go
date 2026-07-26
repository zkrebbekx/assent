package assent

import (
	"context"
	"sync"
	"time"

	"github.com/zkrebbekx/sundial"
)

// Store is the persistence boundary for request state. assent defines it and
// ships [MemStore] as the reference implementation; a caller binds a durable
// store to their database and, for durability, to the same transaction that
// persists the subject.
//
// The read-modify-write of a decision must be indivisible, so [Store.Update]
// carries the whole change as a closure the store runs under its own lock (or,
// for a SQL store, inside one transaction). assent does its domain logic and its
// audit writes inside that closure, which is how a decision and its audit entry
// commit together. There is no separate non-atomic write path.
type Store interface {
	// Create inserts a new request snapshot. It returns a wrapped [ErrExists]
	// when the ID is already taken.
	Create(ctx context.Context, snap RequestSnapshot) error

	// Get returns the snapshot for id, or a wrapped [ErrNotFound]. The returned
	// snapshot is the caller's to keep; a store must not alias its own state
	// into it.
	Get(ctx context.Context, id string) (RequestSnapshot, error)

	// Update reads the snapshot for id, applies fn, and persists the result,
	// atomically. fn runs under the store's lock and must not call back into the
	// store. If fn returns an error nothing is persisted and that error is
	// returned. A missing id returns a wrapped [ErrNotFound].
	Update(ctx context.Context, id string, fn func(RequestSnapshot) (RequestSnapshot, error)) error
}

// recordedDecision is one decision as it is stored: the acting actor, the
// authority the decision counts under (the actor unless a delegate acted), the
// claimed group if any, the ruling and its reason.
type recordedDecision struct {
	Actor     Actor     `json:"actor"`
	Authority Actor     `json:"authority"`
	Group     string    `json:"group,omitempty"`
	Approve   bool      `json:"approve"`
	Reason    string    `json:"reason,omitempty"`
	At        time.Time `json:"at"`
}

// escState is the mutable escalation state assent keeps for a step: the running
// sundial timer's snapshot, the warn levels already recorded, and whether the
// breach action has been applied. It rides in the request snapshot so a restart
// resumes escalation exactly, still write-once.
type escState struct {
	Started  bool              `json:"started,omitempty"`
	Timer    *sundial.Snapshot `json:"timer,omitempty"`
	Handled  []int             `json:"handled,omitempty"`
	Actioned bool              `json:"actioned,omitempty"`
}

// StepState is one step's configuration and runtime state inside a snapshot. The
// approvers and rule are configuration; the decisions, the escalation state and
// any approver added by an escalation are runtime.
type StepState struct {
	// Name is the step's optional label.
	Name string `json:"name,omitempty"`
	// Approvers are the step's approver slots. An escalate-up action appends to
	// this slice at run time.
	Approvers []Assignment `json:"approvers"`
	// Rule is the step's completion rule.
	Rule Rule `json:"rule"`
	// Escalation is the optional per-step SLA and breach action.
	Escalation *Escalation `json:"escalation,omitempty"`
	// Decisions are the rulings recorded against the step, in order.
	Decisions []recordedDecision `json:"decisions,omitempty"`
	// Esc is the step's mutable escalation state. It is exported so a snapshot
	// serializes the running SLA timer, which is what lets a restart resume
	// escalation exactly.
	Esc escState `json:"esc,omitempty"`
}

// RequestSnapshot is the complete serializable state of a request: its
// configuration, its lifecycle state, the active step index, every step's
// decisions and escalation state, and the running count of audit events. It has
// stable JSON tags, so a caller can persist it with encoding/json and restore it
// to resume exactly. It is the value a [Store] holds and the value
// [Engine.Snapshot] returns.
type RequestSnapshot struct {
	// ID is the request's identifier.
	ID string `json:"id"`
	// Maker is who raised the request.
	Maker Actor `json:"maker"`
	// Series is the request's category, used to scope delegations.
	Series string `json:"series,omitempty"`
	// Subject is the caller's opaque reference.
	Subject string `json:"subject,omitempty"`
	// State is the request's lifecycle state.
	State State `json:"state"`
	// StepIndex is the active step, or the last step once terminal.
	StepIndex int `json:"stepIndex"`
	// Chain is the per-step configuration and runtime state.
	Chain []StepState `json:"chain"`
	// Seq is the number of audit events written for the request; the next event
	// uses this index.
	Seq int `json:"seq"`
	// CreatedAt is when the request was submitted.
	CreatedAt time.Time `json:"createdAt"`
}

// clone returns a deep copy of the snapshot's mutable parts, so a store hands
// out state a caller cannot reach into, and a failed update leaves the stored
// state untouched. The escalation configuration and the timer snapshot are
// treated as immutable and shared: assent never mutates them in place, it
// replaces the timer snapshot on each tick.
func (s RequestSnapshot) clone() RequestSnapshot {
	out := s
	out.Chain = make([]StepState, len(s.Chain))
	for i, st := range s.Chain {
		cp := st
		if st.Approvers != nil {
			cp.Approvers = append([]Assignment(nil), st.Approvers...)
		}
		if st.Decisions != nil {
			cp.Decisions = append([]recordedDecision(nil), st.Decisions...)
		}
		if st.Esc.Handled != nil {
			cp.Esc.Handled = append([]int(nil), st.Esc.Handled...)
		}
		out.Chain[i] = cp
	}
	return out
}

// MemStore is an in-memory [Store], the reference implementation the tests run
// against. It guards its map with a mutex and deep-copies snapshots crossing its
// boundary, so it is safe for concurrent use and a caller can neither corrupt it
// through state it wrote nor observe a partial update. It is not durable across
// a restart — that is a SQL store's job.
type MemStore struct {
	mu   sync.Mutex
	reqs map[string]RequestSnapshot
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{reqs: make(map[string]RequestSnapshot)}
}

// Create inserts snap, or returns a wrapped [ErrExists].
func (m *MemStore) Create(ctx context.Context, snap RequestSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.reqs[snap.ID]; ok {
		return wrap(ErrExists, snap.ID)
	}
	m.reqs[snap.ID] = snap.clone()
	return nil
}

// Get returns a deep copy of the snapshot for id, or a wrapped [ErrNotFound].
func (m *MemStore) Get(ctx context.Context, id string) (RequestSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return RequestSnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.reqs[id]
	if !ok {
		return RequestSnapshot{}, wrap(ErrNotFound, id)
	}
	return cur.clone(), nil
}

// Update applies fn to the snapshot for id under the store's lock and persists
// the result, atomically. It holds the lock across fn, so the whole
// read-modify-write — including the audit writes assent makes inside fn — is
// indivisible with respect to other operations on the same request.
func (m *MemStore) Update(ctx context.Context, id string, fn func(RequestSnapshot) (RequestSnapshot, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.reqs[id]
	if !ok {
		return wrap(ErrNotFound, id)
	}
	next, err := fn(cur.clone())
	if err != nil {
		return err
	}
	next.ID = id
	m.reqs[id] = next.clone()
	return nil
}
