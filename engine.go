package assent

import (
	"context"
	"time"

	"github.com/zkrebbekx/chronicle"
	"github.com/zkrebbekx/fsmx"
	"github.com/zkrebbekx/sundial"
)

// defaultKind is the chronicle entity kind assent writes audit events under.
const defaultKind = "approval.event"

// Engine drives approval requests through their chains. It owns the approval
// logic — chain advancement, the quorum math, delegation resolution,
// segregation of duties, and idempotency — and composes three siblings for the
// rest: a [fsmx] machine for the lifecycle, [sundial] timers for per-step SLAs,
// and a [chronicle] log for the audit trail.
//
// An Engine is safe for concurrent use. Every mutating operation runs its whole
// read-modify-write inside [Store.Update], so two approvers who act at the same
// instant never double-count.
type Engine struct {
	store   Store
	log     *chronicle.Log
	machine *fsmx.Machine[State, event, *reqModel]
	deleg   DelegationResolver
	sod     SoD
	system  Actor
	kind    string
}

// Option configures an [Engine].
type Option func(*Engine)

// WithDelegations sets the resolver assent consults for on-behalf-of decisions.
// The default resolver grants nothing, so delegated decisions are rejected until
// one is configured.
func WithDelegations(r DelegationResolver) Option {
	return func(e *Engine) {
		if r != nil {
			e.deleg = r
		}
	}
}

// WithSoD adds a segregation-of-duties predicate that runs after the built-in
// maker check. Use [SeparateSteps], or your own.
func WithSoD(pred SoD) Option {
	return func(e *Engine) { e.sod = pred }
}

// WithSystemActor sets the actor recorded for engine-driven events — escalation
// and auto-approval. It defaults to {ID: "assent"}.
func WithSystemActor(a Actor) Option {
	return func(e *Engine) {
		if !a.IsZero() {
			e.system = a
		}
	}
}

// New returns an engine over the given request store and audit log. It builds
// and validates the lifecycle machine once. store and log are required.
func New(store Store, log *chronicle.Log, opts ...Option) (*Engine, error) {
	if store == nil || log == nil {
		return nil, ErrBadRequest
	}
	m, err := buildMachine()
	if err != nil {
		return nil, err
	}
	e := &Engine{
		store:   store,
		log:     log,
		machine: m,
		deleg:   noDelegations{},
		system:  Actor{ID: "assent", Name: "assent"},
		kind:    defaultKind,
	}
	for _, o := range opts {
		o(e)
	}
	return e, nil
}

// Submit validates a request, creates it, and moves it from Draft to Pending at
// instant at, starting the first step's SLA clock. It records a submit event
// attributed to the maker. A duplicate ID returns a wrapped [ErrExists].
func (e *Engine) Submit(ctx context.Context, req Request, at time.Time) error {
	if at.IsZero() {
		return ErrBadDecision
	}
	if err := validateRequest(req, at); err != nil {
		return err
	}
	snap := RequestSnapshot{
		ID:        req.ID,
		Maker:     req.Maker,
		Series:    req.Series,
		Subject:   req.Subject,
		State:     Draft,
		StepIndex: 0,
		Chain:     make([]StepState, len(req.Chain)),
		CreatedAt: at,
	}
	for i, st := range req.Chain {
		snap.Chain[i] = StepState{
			Name:       st.Name,
			Approvers:  append([]Assignment(nil), st.Approvers...),
			Rule:       st.Rule,
			Escalation: st.Escalation,
		}
	}
	// Reserve the ID first, so a duplicate submit writes no audit event.
	if err := e.store.Create(ctx, snap); err != nil {
		return err
	}
	// Then transition and record inside one atomic update.
	return e.store.Update(ctx, req.ID, func(s RequestSnapshot) (RequestSnapshot, error) {
		ns, err := e.fire(ctx, s.State, evSubmit)
		if err != nil {
			return s, err
		}
		s.State = ns
		if err := e.enterStep(&s, 0, at); err != nil {
			return s, err
		}
		if err := e.audit(ctx, &s, auditArgs{
			typ: EventSubmitted, step: 0, actor: s.Maker, at: at,
		}); err != nil {
			return s, err
		}
		return s, nil
	})
}

// Decide records one actor's ruling on a request's current step and advances the
// chain when the step's rule is met. It is the heart of the engine: it resolves
// delegation, enforces four-eyes and any wider segregation predicate, dedupes a
// repeated decision, and advances or completes the request.
//
// A repeated decision by the same authority on the same step is a no-op that
// returns nil, so two goroutines recording the same approval count it once. An
// extra approval of an already-approved request is likewise a harmless no-op.
func (e *Engine) Decide(ctx context.Context, reqID string, d Decision) error {
	if d.Actor.IsZero() || d.At.IsZero() {
		return ErrBadDecision
	}
	if d.OnBehalfOf != nil && d.OnBehalfOf.IsZero() {
		return ErrBadDecision
	}
	return e.store.Update(ctx, reqID, func(snap RequestSnapshot) (RequestSnapshot, error) {
		// Four-eyes is absolute: the person who raised the request may never be
		// the person who decides it, in any capacity — not directly, and not as
		// another approver's delegate. This bars the physical actor; the
		// authority check below bars counting a decision under the maker's own
		// identity (a delegation from the maker).
		if d.Actor.ID == snap.Maker.ID {
			return snap, ErrMakerChecker
		}
		// Resolve the authority the decision counts under.
		authority := d.Actor
		if d.OnBehalfOf != nil {
			if !e.deleg.Resolve(*d.OnBehalfOf, d.Actor, snap.Series, d.At) {
				return snap, ErrNoDelegation
			}
			authority = *d.OnBehalfOf
		}

		// Terminal states: an extra approval consistent with an approved
		// request is a no-op; anything else is blocked.
		if snap.State == Approved {
			if d.Approve && snap.eligibleInAny(authority, d.Group) {
				return snap, nil
			}
			return snap, ErrNotPending
		}
		if !e.pending(snap.State) {
			return snap, ErrNotPending
		}

		cur := &snap.Chain[snap.StepIndex]
		if !eligibleAt(cur, authority, d.Group) {
			if snap.eligibleAfter(authority, d.Group) {
				return snap, ErrStepNotActive
			}
			if snap.eligibleInAny(authority, d.Group) {
				return snap, nil // an already-completed earlier step
			}
			return snap, ErrNotApprover
		}

		// Write-once: a repeat by the same authority counts once.
		if findDecision(cur, authority.ID) != nil {
			return snap, nil
		}

		// Segregation of duties: the maker cannot be a checker, plus any wider
		// predicate.
		if authority.ID == snap.Maker.ID {
			return snap, ErrMakerChecker
		}
		if e.sod != nil {
			if err := e.sod(RequestView{snap: &snap}, authority, snap.StepIndex); err != nil {
				return snap, err
			}
		}

		// Record the decision.
		cur.Decisions = append(cur.Decisions, recordedDecision{
			Actor: d.Actor, Authority: authority, Group: d.Group,
			Approve: d.Approve, Reason: d.Reason, At: d.At,
		})
		approve := d.Approve
		var authRef *Actor
		if authority.ID != d.Actor.ID {
			a := authority
			authRef = &a
		}
		if err := e.audit(ctx, &snap, auditArgs{
			typ: EventDecision, step: snap.StepIndex, actor: d.Actor, reason: d.Reason,
			at: d.At, authority: authRef, approve: &approve, group: d.Group,
		}); err != nil {
			return snap, err
		}

		if !d.Approve {
			ns, err := e.fire(ctx, snap.State, evReject)
			if err != nil {
				return snap, err
			}
			snap.State = ns
			if err := e.audit(ctx, &snap, auditArgs{
				typ: EventRejected, step: snap.StepIndex, actor: d.Actor, reason: d.Reason, at: d.At,
			}); err != nil {
				return snap, err
			}
			return snap, nil
		}

		if ruleMet(cur) {
			return e.completeStep(ctx, snap, d.Actor, d.Reason, d.At)
		}
		return snap, nil
	})
}

// completeStep advances the chain after the current step's rule is met: it
// approves the request on the last step, or moves to the next step and starts
// its SLA clock. actor and reason are attributed to the resulting event.
func (e *Engine) completeStep(ctx context.Context, snap RequestSnapshot, actor Actor, reason string, at time.Time) (RequestSnapshot, error) {
	from := snap.StepIndex
	if from == len(snap.Chain)-1 {
		ns, err := e.fire(ctx, snap.State, evApprove)
		if err != nil {
			return snap, err
		}
		snap.State = ns
		err = e.audit(ctx, &snap, auditArgs{typ: EventApproved, step: from, actor: actor, reason: reason, at: at})
		return snap, err
	}
	ns, err := e.fire(ctx, snap.State, evAdvance)
	if err != nil {
		return snap, err
	}
	snap.State = ns
	snap.StepIndex = from + 1
	if err := e.enterStep(&snap, snap.StepIndex, at); err != nil {
		return snap, err
	}
	err = e.audit(ctx, &snap, auditArgs{
		typ: EventAdvanced, step: from, actor: actor, reason: reason, at: at,
		extra: map[string]string{"to": itoa(snap.StepIndex)},
	})
	return snap, err
}

// enterStep starts the step's SLA clock, if it has one, at instant at.
func (e *Engine) enterStep(snap *RequestSnapshot, idx int, at time.Time) error {
	st := &snap.Chain[idx]
	if st.Escalation == nil {
		return nil
	}
	tm, err := sundial.Start(escConfig(st.Escalation), at)
	if err != nil {
		return wrapEscalation(err)
	}
	sn := tm.Snapshot()
	st.Esc = escState{Started: true, Timer: &sn}
	return nil
}

// Tick advances a request's SLA clock to now and applies any escalation that has
// come due: it records each warn level once, and on breach applies the step's
// action once — add a fallback approver, reassign the step, or auto-approve.
// Ticking a request with no active SLA, or a terminal request, is a no-op.
func (e *Engine) Tick(ctx context.Context, reqID string, now time.Time) error {
	if now.IsZero() {
		return ErrBadDecision
	}
	return e.store.Update(ctx, reqID, func(snap RequestSnapshot) (RequestSnapshot, error) {
		if !e.pending(snap.State) {
			return snap, nil
		}
		st := &snap.Chain[snap.StepIndex]
		if st.Escalation == nil || !st.Esc.Started || st.Esc.Timer == nil {
			return snap, nil
		}
		esc := st.Escalation
		tm, err := sundial.Restore(escConfig(esc), *st.Esc.Timer)
		if err != nil {
			return snap, wrapEscalation(err)
		}

		// Record each warn level (a level below the budget) once.
		for _, f := range tm.Fired(now) {
			idx := levelIndex(esc.Levels, f)
			if idx < 0 || containsInt(st.Esc.Handled, idx) {
				continue
			}
			st.Esc.Handled = append(st.Esc.Handled, idx)
			if f.Level.At < esc.Budget {
				if err := e.audit(ctx, &snap, auditArgs{
					typ: EventWarned, step: snap.StepIndex, actor: e.system, at: f.At,
					extra: map[string]string{"level": f.Level.Name},
				}); err != nil {
					return snap, err
				}
			}
		}

		// Re-snapshot the current step's timer before any advance moves off it.
		sn := tm.Snapshot()
		st.Esc.Timer = &sn

		if tm.Breached(now) && !st.Esc.Actioned {
			st.Esc.Actioned = true
			breachAt := now
			if at, ok := tm.BreachedAt(); ok {
				breachAt = at
			}
			return e.applyEscalation(ctx, snap, esc, breachAt)
		}
		return snap, nil
	})
}

// applyEscalation applies the step's breach action once and records it.
func (e *Engine) applyEscalation(ctx context.Context, snap RequestSnapshot, esc *Escalation, at time.Time) (RequestSnapshot, error) {
	step := snap.StepIndex
	st := &snap.Chain[step]
	switch esc.Action {
	case EscalateUp:
		st.Approvers = append(st.Approvers, esc.Fallback)
		err := e.audit(ctx, &snap, auditArgs{
			typ: EventEscalated, step: step, actor: e.system, at: at,
			extra: map[string]string{"action": esc.Action.String(), "fallback": esc.Fallback.User + esc.Fallback.Group},
		})
		return snap, err
	case Reassign:
		st.Approvers = []Assignment{esc.Fallback}
		err := e.audit(ctx, &snap, auditArgs{
			typ: EventEscalated, step: step, actor: e.system, at: at,
			extra: map[string]string{"action": esc.Action.String(), "fallback": esc.Fallback.User + esc.Fallback.Group},
		})
		return snap, err
	case AutoApprove:
		if err := e.audit(ctx, &snap, auditArgs{
			typ: EventAutoApproved, step: step, actor: e.system, at: at,
			extra: map[string]string{"action": esc.Action.String()},
		}); err != nil {
			return snap, err
		}
		return e.completeStep(ctx, snap, e.system, "sla auto-approve", at)
	default:
		return snap, ErrBadEscalation
	}
}

// Withdraw moves a pending request to Withdrawn. Only the maker may withdraw. A
// non-pending request returns a wrapped [ErrNotPending].
func (e *Engine) Withdraw(ctx context.Context, reqID string, by Actor, reason string, at time.Time) error {
	if by.IsZero() || at.IsZero() {
		return ErrBadDecision
	}
	return e.store.Update(ctx, reqID, func(snap RequestSnapshot) (RequestSnapshot, error) {
		if !e.pending(snap.State) {
			return snap, ErrNotPending
		}
		if by.ID != snap.Maker.ID {
			return snap, ErrNotMaker
		}
		ns, err := e.fire(ctx, snap.State, evWithdraw)
		if err != nil {
			return snap, err
		}
		snap.State = ns
		err = e.audit(ctx, &snap, auditArgs{typ: EventWithdrawn, step: snap.StepIndex, actor: by, reason: reason, at: at})
		return snap, err
	})
}

// Reassign moves the current step's assignment from one approver to another and
// records it. When from names an approver of the step, it is replaced; otherwise
// to is added. A non-pending request returns a wrapped [ErrNotPending].
func (e *Engine) Reassign(ctx context.Context, reqID string, from, to Assignment, by Actor, reason string, at time.Time) error {
	if to.IsZero() || by.IsZero() || at.IsZero() {
		return ErrBadDecision
	}
	return e.store.Update(ctx, reqID, func(snap RequestSnapshot) (RequestSnapshot, error) {
		if !e.pending(snap.State) {
			return snap, ErrNotPending
		}
		st := &snap.Chain[snap.StepIndex]
		replaced := false
		if !from.IsZero() {
			for i, a := range st.Approvers {
				if a == from {
					st.Approvers[i] = to
					replaced = true
					break
				}
			}
		}
		if !replaced {
			st.Approvers = append(st.Approvers, to)
		}
		err := e.audit(ctx, &snap, auditArgs{
			typ: EventReassigned, step: snap.StepIndex, actor: by, reason: reason, at: at,
			extra: map[string]string{"from": from.User + from.Group, "to": to.User + to.Group},
		})
		return snap, err
	})
}

// Snapshot returns the request's full serializable state, for persistence and
// crash recovery. It is [Store.Get] under a name that states its purpose.
func (e *Engine) Snapshot(ctx context.Context, reqID string) (RequestSnapshot, error) {
	return e.store.Get(ctx, reqID)
}

// Restore inserts a snapshot into the store, rebuilding a request so a restart
// resumes exactly. The audit log is separate durable storage and is reattached
// by constructing the engine over the same [chronicle.Log]. A malformed snapshot
// returns a wrapped [ErrBadSnapshot]; an existing ID returns [ErrExists].
func (e *Engine) Restore(ctx context.Context, snap RequestSnapshot) error {
	if snap.ID == "" || snap.Maker.IsZero() || len(snap.Chain) == 0 {
		return ErrBadSnapshot
	}
	if snap.StepIndex < 0 || snap.StepIndex >= len(snap.Chain) {
		return ErrBadSnapshot
	}
	switch snap.State {
	case Draft, Pending, Approved, Rejected, Withdrawn:
	default:
		return ErrBadSnapshot
	}
	return e.store.Create(ctx, snap)
}

// State returns a request's current lifecycle state.
func (e *Engine) State(ctx context.Context, reqID string) (State, error) {
	snap, err := e.store.Get(ctx, reqID)
	if err != nil {
		return "", err
	}
	return snap.State, nil
}
