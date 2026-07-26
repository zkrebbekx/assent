package assent

import "fmt"

// eligibleAt reports whether an authority may decide this step: either it is a
// named user of the step, or the decision claims a candidate group the step
// lists. assent does not verify group membership — the caller routes only
// eligible actors, and the claim is recorded (see the package doc).
func eligibleAt(st *StepState, authority Actor, group string) bool {
	for _, a := range st.Approvers {
		if a.User != "" && a.User == authority.ID {
			return true
		}
		if group != "" && a.Group != "" && a.Group == group {
			return true
		}
	}
	return false
}

// eligibleInAny reports whether the authority is an approver of some step in the
// chain — used to tell a harmless extra approval of an already-approved request
// from a stranger's.
func (snap *RequestSnapshot) eligibleInAny(authority Actor, group string) bool {
	for i := range snap.Chain {
		if eligibleAt(&snap.Chain[i], authority, group) {
			return true
		}
	}
	return false
}

// eligibleAfter reports whether the authority is an approver only of a step
// later than the current one — the "approving a step before its turn" case.
func (snap *RequestSnapshot) eligibleAfter(authority Actor, group string) bool {
	for i := snap.StepIndex + 1; i < len(snap.Chain); i++ {
		if eligibleAt(&snap.Chain[i], authority, group) {
			return true
		}
	}
	return false
}

// distinctApprovers returns the set of authority IDs that approved the step. A
// reject never lingers on an active step (it terminates the request), so every
// counted decision is an approval.
func distinctApprovers(st *StepState) map[string]bool {
	set := make(map[string]bool)
	for _, d := range st.Decisions {
		if d.Approve {
			set[d.Authority.ID] = true
		}
	}
	return set
}

// findDecision returns the decision the given authority already recorded on the
// step, or nil. It is the write-once key: at most one decision per (step,
// authority).
func findDecision(st *StepState, authorityID string) *recordedDecision {
	for i := range st.Decisions {
		if st.Decisions[i].Authority.ID == authorityID {
			return &st.Decisions[i]
		}
	}
	return nil
}

// ruleMet reports whether the step's rule is satisfied by its recorded
// approvals.
func ruleMet(st *StepState) bool {
	approvers := distinctApprovers(st)
	switch st.Rule.Kind {
	case KindAnyone:
		return len(approvers) >= 1
	case KindQuorum:
		return len(approvers) >= st.Rule.N
	case KindUnanimous:
		return unanimousMet(st, approvers)
	default:
		return false
	}
}

// unanimousMet reports whether every named approver slot of the step is
// satisfied: each named user approved, and each candidate group has at least one
// approving claim.
func unanimousMet(st *StepState, approvers map[string]bool) bool {
	claimedGroups := make(map[string]bool)
	for _, d := range st.Decisions {
		if d.Approve && d.Group != "" {
			claimedGroups[d.Group] = true
		}
	}
	for _, a := range st.Approvers {
		if a.User != "" && !approvers[a.User] {
			return false
		}
		if a.Group != "" && !claimedGroups[a.Group] {
			return false
		}
	}
	return true
}

// SoD is a segregation-of-duties predicate. assent always bars the maker from
// approving; a predicate widens that. It receives a read-only view of the
// request, the deciding authority, and the active step, and returns an error to
// block the decision. Compose your own, or use [SeparateSteps].
type SoD func(view RequestView, authority Actor, step int) error

// RequestView is a read-only window onto a request for a [SoD] predicate.
type RequestView struct {
	snap *RequestSnapshot
}

// Maker returns who raised the request.
func (v RequestView) Maker() Actor { return v.snap.Maker }

// State returns the request's lifecycle state.
func (v RequestView) State() State { return v.snap.State }

// StepIndex returns the active step.
func (v RequestView) StepIndex() int { return v.snap.StepIndex }

// StepCount returns the number of steps in the chain.
func (v RequestView) StepCount() int { return len(v.snap.Chain) }

// ApprovedBy returns the distinct authority IDs that have approved the given
// step, or nil when the index is out of range.
func (v RequestView) ApprovedBy(step int) []string {
	if step < 0 || step >= len(v.snap.Chain) {
		return nil
	}
	set := distinctApprovers(&v.snap.Chain[step])
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

// SeparateSteps is a [SoD] predicate that bars an authority from approving more
// than one step of the same request. It enforces separation across a chain: a
// person who signed off one stage cannot also sign off another. It wraps
// [ErrSeparation].
func SeparateSteps(view RequestView, authority Actor, step int) error {
	for i := 0; i < view.StepCount(); i++ {
		if i == step {
			continue
		}
		for _, id := range view.ApprovedBy(i) {
			if id == authority.ID {
				return fmt.Errorf("%w: %q already approved step %d", ErrSeparation, authority.ID, i)
			}
		}
	}
	return nil
}
