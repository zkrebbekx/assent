package assent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zkrebbekx/chronicle"
)

// Audit event type constants recorded on the trail.
const (
	// EventSubmitted marks a request entering the chain (Draft to Pending).
	EventSubmitted = "submitted"
	// EventDecision marks a recorded ruling (approve or reject) by an actor.
	EventDecision = "decision"
	// EventAdvanced marks a step completing with more steps to run.
	EventAdvanced = "advanced"
	// EventApproved marks the last step approving the request.
	EventApproved = "approved"
	// EventRejected marks a rejection failing the request.
	EventRejected = "rejected"
	// EventWithdrawn marks the maker withdrawing the request.
	EventWithdrawn = "withdrawn"
	// EventWarned marks an SLA warn level firing.
	EventWarned = "warned"
	// EventEscalated marks an escalate-up or reassign action on SLA breach.
	EventEscalated = "escalated"
	// EventAutoApproved marks an auto-approve action on SLA breach.
	EventAutoApproved = "auto_approved"
	// EventReassigned marks a manual reassignment of the current step.
	EventReassigned = "reassigned"
)

// auditPayload is the assent-specific part of an event, stored as the chronicle
// record's data. The actor, reason and timestamp are the record's own fields and
// are not duplicated here.
type auditPayload struct {
	Type      string            `json:"type"`
	Step      int               `json:"step"`
	Authority *Actor            `json:"authority,omitempty"`
	Approve   *bool             `json:"approve,omitempty"`
	Group     string            `json:"group,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// AuditEntry is one event on a request's trail, decoded for the caller. It joins
// the chronicle record's actor, reason and timestamp with assent's event type
// and detail.
type AuditEntry struct {
	// Seq is the event's position on the trail, from zero.
	Seq int
	// Type is the event type, one of the Event* constants.
	Type string
	// Step is the step the event concerns.
	Step int
	// Actor is who caused the event.
	Actor Actor
	// Authority is the authority a delegated decision counted under; nil
	// otherwise.
	Authority *Actor
	// Approve is the ruling for a decision event; nil otherwise.
	Approve *bool
	// Group is the claimed candidate group for a group decision; empty
	// otherwise.
	Group string
	// Reason is the recorded justification.
	Reason string
	// At is the instant the event occurred.
	At time.Time
	// Extra carries event-specific detail, such as an escalation action.
	Extra map[string]string
}

// auditArgs bundles the parts of one audit write.
type auditArgs struct {
	typ       string
	step      int
	actor     Actor
	reason    string
	at        time.Time
	authority *Actor
	approve   *bool
	group     string
	extra     map[string]string
}

// audit appends one event to the request's trail and advances the snapshot's
// event counter. It runs inside the store update, so an event and the state
// change that produced it commit together. Each event is its own chronicle
// entity, written once and never superseded — an append-only log.
func (e *Engine) audit(ctx context.Context, snap *RequestSnapshot, a auditArgs) error {
	payload := auditPayload{
		Type:      a.typ,
		Step:      a.step,
		Authority: a.authority,
		Approve:   a.approve,
		Group:     a.group,
		Extra:     a.extra,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	entityID := fmt.Sprintf("%s#%06d", snap.ID, snap.Seq)
	_, err = e.log.Put(ctx, e.kind, entityID, data, a.at, time.Time{},
		chronicle.Actor{ID: a.actor.ID, Name: a.actor.Name},
		chronicle.WithReason(a.reason))
	if err != nil {
		return err
	}
	snap.Seq++
	return nil
}

// Trail returns the ordered audit trail for a request: every event, from submit
// onward, each with its actor, reason and timestamp. It reads the append-only
// chronicle log the engine wrote. A missing request returns a wrapped
// [ErrNotFound].
func (e *Engine) Trail(ctx context.Context, reqID string) ([]AuditEntry, error) {
	snap, err := e.store.Get(ctx, reqID)
	if err != nil {
		return nil, err
	}
	out := make([]AuditEntry, 0, snap.Seq)
	for i := 0; i < snap.Seq; i++ {
		entityID := fmt.Sprintf("%s#%06d", reqID, i)
		rec, err := e.log.Get(ctx, e.kind, entityID, chronicle.Now())
		if err != nil {
			return nil, err
		}
		var p auditPayload
		if err := json.Unmarshal(rec.Data, &p); err != nil {
			return nil, err
		}
		out = append(out, AuditEntry{
			Seq:       i,
			Type:      p.Type,
			Step:      p.Step,
			Actor:     Actor{ID: rec.Actor.ID, Name: rec.Actor.Name},
			Authority: p.Authority,
			Approve:   p.Approve,
			Group:     p.Group,
			Reason:    rec.Reason,
			At:        rec.ValidFrom,
			Extra:     p.Extra,
		})
	}
	return out, nil
}
