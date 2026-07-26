package assent

import (
	"time"

	"github.com/zkrebbekx/sundial"
)

// validateRequest rejects a structurally unusable request before it is stored.
// It checks the identity, the chain, every step's approvers and rule, and every
// escalation's sundial config, so a later step's bad config surfaces at submit
// rather than when its turn comes. at is a probe instant for the sundial check.
func validateRequest(r Request, at time.Time) error {
	if r.ID == "" || r.Maker.IsZero() {
		return ErrBadRequest
	}
	if len(r.Chain) == 0 {
		return ErrEmptyChain
	}
	for i := range r.Chain {
		if err := validateStep(r.Chain[i], at); err != nil {
			return err
		}
	}
	return nil
}

// validateStep checks one step's approvers, rule and escalation.
func validateStep(st Step, at time.Time) error {
	if len(st.Approvers) == 0 {
		return ErrEmptyStep
	}
	users, groups := 0, 0
	for _, a := range st.Approvers {
		// Exactly one of user or group.
		if (a.User == "") == (a.Group == "") {
			return ErrBadAssignment
		}
		if a.User != "" {
			users++
		} else {
			groups++
		}
	}
	if err := validateRule(st.Rule, users, groups); err != nil {
		return err
	}
	if st.Escalation != nil {
		if err := validateEscalation(st.Escalation, at); err != nil {
			return err
		}
	}
	return nil
}

// validateRule rejects an unsatisfiable rule. A quorum must be at least one, and
// when a step has only named users it cannot exceed their number.
func validateRule(rule Rule, users, groups int) error {
	switch rule.Kind {
	case KindAnyone, KindUnanimous:
		return nil
	case KindQuorum:
		if rule.N < 1 {
			return ErrBadRule
		}
		if groups == 0 && rule.N > users {
			return ErrBadRule
		}
		return nil
	default:
		return ErrBadRule
	}
}

// validateEscalation rejects an escalation whose sundial config is unusable, or
// whose action needs a fallback approver it does not have.
func validateEscalation(esc *Escalation, at time.Time) error {
	if _, err := sundial.Start(escConfig(esc), at); err != nil {
		return wrapEscalation(err)
	}
	switch esc.Action {
	case EscalateUp, Reassign:
		if esc.Fallback.IsZero() {
			return ErrBadEscalation
		}
	case AutoApprove:
		// No fallback needed.
	default:
		return ErrBadEscalation
	}
	return nil
}
