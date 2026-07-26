//go:build js && wasm

package main

import (
	"time"

	"github.com/zkrebbekx/assent"
	"github.com/zkrebbekx/chronicle"
)

// chronicleLog builds a fresh append-only audit log over an in-memory store —
// the compliance boundary assent writes every event to. Mirrors the README's
// chronicle.NewLog(chronicle.NewMemStore()).
func chronicleLog() *chronicle.Log {
	return chronicle.NewLog(chronicle.NewMemStore())
}

// --- state DTOs the page renders from ---------------------------------------

type decisionDTO struct {
	Actor     actorDTO  `json:"actor"`
	Authority *actorDTO `json:"authority,omitempty"`
	Group     string    `json:"group,omitempty"`
	Approve   bool      `json:"approve"`
	Reason    string    `json:"reason,omitempty"`
	At        string    `json:"at"`
}

type escStateDTO struct {
	BudgetHours float64       `json:"budgetHours"`
	Levels      []levelDTO    `json:"levels"`
	Action      string        `json:"action"`
	Fallback    assignmentDTO `json:"fallback"`
	Started     bool          `json:"started"`
	Fired       []string      `json:"fired"`
	Breached    bool          `json:"breached"`
}

type stepStateDTO struct {
	Index        int             `json:"index"`
	Name         string          `json:"name,omitempty"`
	Rule         ruleDTO         `json:"rule"`
	Approvers    []assignmentDTO `json:"approvers"`
	Escalation   *escStateDTO    `json:"escalation,omitempty"`
	Decisions    []decisionDTO   `json:"decisions"`
	ApprovedBy   []string        `json:"approvedBy"`
	QuorumNeeded int             `json:"quorumNeeded"`
	QuorumHave   int             `json:"quorumHave"`
	Status       string          `json:"status"`
}

type delegationViewDTO struct {
	From   actorDTO `json:"from"`
	To     actorDTO `json:"to"`
	Series []string `json:"series"`
	Start  string   `json:"start"`
	End    string   `json:"end"`
}

type trailDTO struct {
	Seq       int               `json:"seq"`
	Type      string            `json:"type"`
	Step      int               `json:"step"`
	Actor     actorDTO          `json:"actor"`
	Authority *actorDTO         `json:"authority,omitempty"`
	Approve   *bool             `json:"approve,omitempty"`
	Group     string            `json:"group,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	At        string            `json:"at"`
	Extra     map[string]string `json:"extra,omitempty"`
}

type stateDTO struct {
	SessionID   string              `json:"sessionId"`
	ReqID       string              `json:"reqId"`
	State       string              `json:"state"`
	Maker       actorDTO            `json:"maker"`
	Series      string              `json:"series,omitempty"`
	Subject     string              `json:"subject,omitempty"`
	StepIndex   int                 `json:"stepIndex"`
	Clock       string              `json:"clock"`
	SoD         string              `json:"sod"`
	Steps       []stepStateDTO      `json:"steps"`
	Delegations []delegationViewDTO `json:"delegations"`
	Trail       []trailDTO          `json:"trail"`
	Error       string              `json:"error,omitempty"`
	Code        string              `json:"code,omitempty"`
}

// stateOf reads the live engine's snapshot and audit trail and assembles the one
// value the page renders from. The static configuration — each step's approvers,
// rule and escalation spec — comes from the snapshot; everything dynamic — the
// decisions, the distinct approving authorities, the warn and breach firings,
// and the per-step status — is reconstructed from the append-only trail, so the
// bridge reads only assent's exported surface (Snapshot and Trail).
//
// opErr is the outcome of the operation that produced this render. A nil opErr
// is a clean call. A non-nil opErr is an expected engine rejection, embedded as
// {error, code} beside the unchanged state so the page can show it.
func stateOf(s *session, opErr error) (any, error) {
	snap, err := s.eng.Snapshot(ctx, s.reqID)
	if err != nil {
		return nil, err
	}
	trail, err := s.eng.Trail(ctx, s.reqID)
	if err != nil {
		return nil, err
	}

	// One pass over the trail to reconstruct per-step dynamic state.
	type stepDyn struct {
		decisions  []decisionDTO
		approvedBy []string
		approvedID map[string]bool
		warned     []string
		breached   bool
	}
	dyn := make([]stepDyn, len(snap.Chain))
	for i := range dyn {
		dyn[i].approvedID = map[string]bool{}
	}
	advancedFrom := map[int]bool{}
	approvedAt := -1
	rejectedAt := -1

	for _, en := range trail {
		if en.Step < 0 || en.Step >= len(dyn) {
			continue
		}
		d := &dyn[en.Step]
		switch en.Type {
		case assent.EventDecision:
			dec := decisionDTO{
				Actor:  actorDTO{ID: en.Actor.ID, Name: en.Actor.Name},
				Group:  en.Group,
				Reason: en.Reason,
				At:     en.At.Format(time.RFC3339),
			}
			if en.Approve != nil {
				dec.Approve = *en.Approve
			}
			authID := en.Actor.ID
			if en.Authority != nil {
				dec.Authority = &actorDTO{ID: en.Authority.ID, Name: en.Authority.Name}
				authID = en.Authority.ID
			}
			d.decisions = append(d.decisions, dec)
			if dec.Approve && !d.approvedID[authID] {
				d.approvedID[authID] = true
				d.approvedBy = append(d.approvedBy, authID)
			}
		case assent.EventAdvanced:
			advancedFrom[en.Step] = true
		case assent.EventApproved:
			approvedAt = en.Step
		case assent.EventRejected:
			rejectedAt = en.Step
		case assent.EventWarned:
			if en.Extra != nil {
				d.warned = append(d.warned, en.Extra["level"])
			}
		case assent.EventEscalated, assent.EventAutoApproved:
			d.breached = true
		}
	}

	steps := make([]stepStateDTO, 0, len(snap.Chain))
	for i := range snap.Chain {
		cs := &snap.Chain[i]
		approvers := make([]assignmentDTO, 0, len(cs.Approvers))
		for _, a := range cs.Approvers {
			approvers = append(approvers, assignmentDTO{User: a.User, Group: a.Group})
		}
		rk := cs.Rule.Kind.String()
		sd := stepStateDTO{
			Index:      i,
			Name:       cs.Name,
			Rule:       ruleDTO{Kind: rk, N: cs.Rule.N},
			Approvers:  approvers,
			Decisions:  dyn[i].decisions,
			ApprovedBy: dyn[i].approvedBy,
			QuorumHave: len(dyn[i].approvedBy),
		}
		if sd.Decisions == nil {
			sd.Decisions = []decisionDTO{}
		}
		if sd.ApprovedBy == nil {
			sd.ApprovedBy = []string{}
		}
		switch cs.Rule.Kind {
		case assent.KindAnyone:
			sd.QuorumNeeded = 1
		case assent.KindQuorum:
			sd.QuorumNeeded = cs.Rule.N
		case assent.KindUnanimous:
			sd.QuorumNeeded = len(cs.Approvers)
		}
		if cs.Escalation != nil {
			esc := cs.Escalation
			levels := make([]levelDTO, 0, len(esc.Levels))
			for _, l := range esc.Levels {
				levels = append(levels, levelDTO{Name: l.Name, AtHours: l.At.Hours()})
			}
			warned := dyn[i].warned
			if warned == nil {
				warned = []string{}
			}
			sd.Escalation = &escStateDTO{
				BudgetHours: esc.Budget.Hours(),
				Levels:      levels,
				Action:      esc.Action.String(),
				Fallback:    assignmentDTO{User: esc.Fallback.User, Group: esc.Fallback.Group},
				Started:     i <= snap.StepIndex,
				Fired:       warned,
				Breached:    dyn[i].breached,
			}
		}
		sd.Status = stepStatus(i, snap, advancedFrom, approvedAt, rejectedAt)
		steps = append(steps, sd)
	}

	dels := make([]delegationViewDTO, 0, len(s.grants))
	for _, g := range s.grants {
		dels = append(dels, delegationViewDTO{
			From:   actorDTO{ID: g.From.ID, Name: g.From.Name},
			To:     actorDTO{ID: g.To.ID, Name: g.To.Name},
			Series: g.Series,
			Start:  g.Window.Start.Format(time.RFC3339),
			End:    g.Window.End.Format(time.RFC3339),
		})
	}

	tr := make([]trailDTO, 0, len(trail))
	for _, en := range trail {
		t := trailDTO{
			Seq:    en.Seq,
			Type:   en.Type,
			Step:   en.Step,
			Actor:  actorDTO{ID: en.Actor.ID, Name: en.Actor.Name},
			Group:  en.Group,
			Reason: en.Reason,
			At:     en.At.Format(time.RFC3339),
			Extra:  en.Extra,
		}
		if en.Authority != nil {
			t.Authority = &actorDTO{ID: en.Authority.ID, Name: en.Authority.Name}
		}
		if en.Approve != nil {
			b := *en.Approve
			t.Approve = &b
		}
		tr = append(tr, t)
	}

	out := stateDTO{
		SessionID:   s.id,
		ReqID:       s.reqID,
		State:       string(snap.State),
		Maker:       actorDTO{ID: snap.Maker.ID, Name: snap.Maker.Name},
		Series:      snap.Series,
		Subject:     snap.Subject,
		StepIndex:   snap.StepIndex,
		Clock:       s.clock.Format(time.RFC3339),
		SoD:         s.sod,
		Steps:       steps,
		Delegations: dels,
		Trail:       tr,
	}
	if opErr != nil {
		out.Error = opErr.Error()
		out.Code = errCode(opErr)
	}
	return out, nil
}

// stepStatus classifies a step for the chain visualization: a step that
// completed and advanced or approved is "complete"; the step a rejection landed
// on is "rejected"; the current step of a pending request is "active"; a step
// past the active index is "complete"; anything else is "pending".
func stepStatus(i int, snap assent.RequestSnapshot, advancedFrom map[int]bool, approvedAt, rejectedAt int) string {
	if rejectedAt == i {
		return "rejected"
	}
	if advancedFrom[i] || approvedAt == i {
		return "complete"
	}
	if i < snap.StepIndex {
		return "complete"
	}
	if snap.State == assent.Pending && i == snap.StepIndex {
		return "active"
	}
	return "pending"
}
