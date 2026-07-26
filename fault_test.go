package assent

import (
	"testing"

	"github.com/zkrebbekx/chronicle"
)

// newFaultyEngine builds an engine whose audit log rejects assent's event kind,
// so every audit write fails. It lets the tests exercise the error paths that
// wire an audit failure back out of an operation.
func newFaultyEngine(t *testing.T) *Engine {
	t.Helper()
	log := chronicle.NewLog(chronicle.NewMemStore(), chronicle.WithKinds("something-else"))
	e, err := New(NewMemStore(), log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestAuditFailurePropagates(t *testing.T) {
	t.Run("Given an engine whose audit log rejects writes", func(t *testing.T) {
		t.Run("Then Submit surfaces the audit failure", func(t *testing.T) {
			e := newFaultyEngine(t)
			req := Request{ID: "af1", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone()}}}
			if err := e.Submit(ctx(), req, mon); err == nil {
				t.Fatal("Submit should fail when the audit write fails")
			}
		})
		t.Run("Then a decision surfaces the audit failure", func(t *testing.T) {
			e := newFaultyEngine(t)
			// Restore bypasses the audit log, so we can set up pending state.
			s := RequestSnapshot{ID: "af2", Maker: user("m"), State: Pending, Chain: []StepState{{Approvers: approvers("a"), Rule: Anyone()}}}
			if err := e.Restore(ctx(), s); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if err := e.Decide(ctx(), "af2", Decision{Actor: user("a"), Approve: true, Reason: "ok", At: mon}); err == nil {
				t.Fatal("Decide should fail when the audit write fails")
			}
		})
		t.Run("Then a rejection surfaces the audit failure", func(t *testing.T) {
			e := newFaultyEngine(t)
			s := RequestSnapshot{ID: "af3", Maker: user("m"), State: Pending, Chain: []StepState{{Approvers: approvers("a"), Rule: Anyone()}}}
			if err := e.Restore(ctx(), s); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if err := e.Decide(ctx(), "af3", Decision{Actor: user("a"), Approve: false, Reason: "no", At: mon}); err == nil {
				t.Fatal("Decide reject should fail when the audit write fails")
			}
		})
		t.Run("Then a withdrawal surfaces the audit failure", func(t *testing.T) {
			e := newFaultyEngine(t)
			s := RequestSnapshot{ID: "af4", Maker: user("alice"), State: Pending, Chain: []StepState{{Approvers: approvers("a"), Rule: Anyone()}}}
			if err := e.Restore(ctx(), s); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if err := e.Withdraw(ctx(), "af4", user("alice"), "stop", mon); err == nil {
				t.Fatal("Withdraw should fail when the audit write fails")
			}
		})
		t.Run("Then a reassignment surfaces the audit failure", func(t *testing.T) {
			e := newFaultyEngine(t)
			s := RequestSnapshot{ID: "af5", Maker: user("m"), State: Pending, Chain: []StepState{{Approvers: approvers("a"), Rule: Anyone()}}}
			if err := e.Restore(ctx(), s); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if err := e.Reassign(ctx(), "af5", approver("a"), approver("b"), user("m"), "x", mon); err == nil {
				t.Fatal("Reassign should fail when the audit write fails")
			}
		})
	})
}

func TestTrailMissingEvent(t *testing.T) {
	t.Run("Given a restored snapshot claiming more events than the log holds", func(t *testing.T) {
		e, _ := newEngine(t)
		s := RequestSnapshot{ID: "tm", Maker: user("m"), State: Pending, Seq: 3,
			Chain: []StepState{{Approvers: approvers("a"), Rule: Anyone()}}}
		if err := e.Restore(ctx(), s); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		t.Run("Then Trail surfaces the missing-event lookup failure", func(t *testing.T) {
			if _, err := e.Trail(ctx(), "tm"); err == nil {
				t.Fatal("Trail should fail when an event record is absent")
			}
		})
	})
}
