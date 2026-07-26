package assent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zkrebbekx/fsmx"
	"github.com/zkrebbekx/sundial"
)

func TestFireRejectsIllegalTransition(t *testing.T) {
	e, _ := newEngine(t)
	t.Run("Given a terminal Approved state", func(t *testing.T) {
		t.Run("Then firing approve again returns no-transition", func(t *testing.T) {
			_, err := e.fire(context.Background(), Approved, evApprove)
			if !errors.Is(err, fsmx.ErrNoTransition) {
				t.Fatalf("err = %v, want fsmx.ErrNoTransition", err)
			}
		})
	})
}

func TestLevelIndex(t *testing.T) {
	levels := []sundial.Level{{Name: "warn", At: 4 * time.Hour}, {Name: "breach", At: 8 * time.Hour}}
	t.Run("Given a firing that matches a level", func(t *testing.T) {
		f := sundial.Firing{Level: sundial.Level{Name: "breach", At: 8 * time.Hour}}
		if got := levelIndex(levels, f); got != 1 {
			t.Fatalf("levelIndex = %d, want 1", got)
		}
	})
	t.Run("Given a firing that matches no level", func(t *testing.T) {
		f := sundial.Firing{Level: sundial.Level{Name: "page", At: 12 * time.Hour}}
		if got := levelIndex(levels, f); got != -1 {
			t.Fatalf("levelIndex = %d, want -1", got)
		}
	})
}

func TestUnanimousWithGroup(t *testing.T) {
	t.Run("Given a unanimous step with one user and one group", func(t *testing.T) {
		e, _ := newEngine(t)
		req := Request{ID: "ug", Maker: user("m"), Chain: []Step{{
			Approvers: []Assignment{{User: "mgr"}, {Group: "audit"}}, Rule: Unanimous(),
		}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When only the named user approves", func(t *testing.T) {
			mustApprove(t, e, "ug", "mgr", "ok", mon)
			t.Run("Then the group slot is still unsatisfied", func(t *testing.T) {
				mustState(t, e, "ug", Pending)
			})
			t.Run("When a member claims the group", func(t *testing.T) {
				if err := e.Decide(ctx(), "ug", Decision{Actor: user("gina"), Group: "audit", Approve: true, Reason: "ok", At: mon}); err != nil {
					t.Fatalf("Decide group: %v", err)
				}
				t.Run("Then unanimity is met and the request is Approved", func(t *testing.T) {
					mustState(t, e, "ug", Approved)
				})
			})
		})
	})
}

func TestEnterStepBadEscalationOnRestore(t *testing.T) {
	t.Run("Given a restored snapshot whose second step has an invalid escalation", func(t *testing.T) {
		e, _ := newEngine(t)
		snap := RequestSnapshot{
			ID: "corrupt", Maker: user("m"), State: Pending, StepIndex: 0,
			Chain: []StepState{
				{Approvers: approvers("a"), Rule: Anyone()},
				{Approvers: approvers("b"), Rule: Anyone(), Escalation: &Escalation{
					Schedule: sundial.Schedule{}, Budget: time.Hour, Action: AutoApprove,
				}},
			},
			Seq: 1,
		}
		if err := e.Restore(ctx(), snap); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		t.Run("When approving step 0 tries to advance into the bad step", func(t *testing.T) {
			err := e.Decide(ctx(), "corrupt", Decision{Actor: user("a"), Approve: true, Reason: "ok", At: mon})
			t.Run("Then it errors: the escalation cannot start", func(t *testing.T) {
				if !errors.Is(err, ErrBadEscalation) {
					t.Fatalf("err = %v, want ErrBadEscalation", err)
				}
			})
		})
	})
}
