package assent

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zkrebbekx/chronicle"
	"github.com/zkrebbekx/sundial"
)

func TestSnapshotRoundTrip(t *testing.T) {
	warnAt := time.Date(2026, 1, 5, 14, 0, 0, 0, time.UTC)   // Monday 14:00, 5 working hours
	breachAt := time.Date(2026, 1, 6, 10, 0, 0, 0, time.UTC) // Tuesday 10:00, 9 working hours

	t.Run("Given a two-step request with a warned-but-unbreached SLA on step 0", func(t *testing.T) {
		log := chronicle.NewLog(chronicle.NewMemStore())
		e1, err := New(NewMemStore(), log)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		esc := &Escalation{
			Schedule: stdSchedule(), Budget: 8 * time.Hour,
			Levels: []sundial.Level{{Name: "warn", At: 4 * time.Hour}, {Name: "breach", At: 8 * time.Hour}},
			Action: EscalateUp, Fallback: approver("senior"),
		}
		req := Request{ID: "snap", Maker: user("alice"), Chain: []Step{
			{Approvers: approvers("mgr"), Rule: Anyone(), Escalation: esc},
			{Approvers: approvers("finance"), Rule: Unanimous()},
		}}
		if err := e1.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		if err := e1.Tick(ctx(), "snap", warnAt); err != nil {
			t.Fatalf("Tick warn: %v", err)
		}
		if countType(trail(t, e1, "snap"), EventWarned) != 1 {
			t.Fatal("warn should have fired once before the snapshot")
		}

		t.Run("When the snapshot is JSON round-tripped and restored into a fresh engine", func(t *testing.T) {
			s := snap(t, e1, "snap")
			blob, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var restored RequestSnapshot
			if err := json.Unmarshal(blob, &restored); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			t.Run("Then the running SLA timer survives serialization", func(t *testing.T) {
				if restored.Chain[0].Esc.Timer == nil {
					t.Fatal("timer snapshot lost across JSON")
				}
				if !containsInt(restored.Chain[0].Esc.Handled, 0) {
					t.Fatal("the fired warn level was not persisted")
				}
			})

			// A fresh engine over a fresh store but the same audit log.
			e2, err := New(NewMemStore(), log)
			if err != nil {
				t.Fatalf("New e2: %v", err)
			}
			if err := e2.Restore(ctx(), restored); err != nil {
				t.Fatalf("Restore: %v", err)
			}

			t.Run("Then ticking past the breach escalates exactly once, without re-warning", func(t *testing.T) {
				if err := e2.Tick(ctx(), "snap", breachAt); err != nil {
					t.Fatalf("Tick breach: %v", err)
				}
				tr := trail(t, e2, "snap")
				if countType(tr, EventWarned) != 1 {
					t.Fatalf("warned events = %d, want 1 (not re-emitted after restore)", countType(tr, EventWarned))
				}
				if countType(tr, EventEscalated) != 1 {
					t.Fatalf("escalated events = %d, want 1", countType(tr, EventEscalated))
				}
				appr := snap(t, e2, "snap").Chain[0].Approvers
				if len(appr) != 2 || appr[1] != approver("senior") {
					t.Fatalf("approvers = %v, want [mgr senior]", appr)
				}
			})

			t.Run("Then the restored request completes normally", func(t *testing.T) {
				mustApprove(t, e2, "snap", "mgr", "ok", breachAt)
				mustState(t, e2, "snap", Pending) // advanced to step 1
				mustApprove(t, e2, "snap", "finance", "ok", breachAt)
				mustState(t, e2, "snap", Approved)
			})
		})
	})
}

func TestRestoreValidation(t *testing.T) {
	e, _ := newEngine(t)
	good := RequestSnapshot{ID: "ok", Maker: user("m"), State: Pending, Chain: []StepState{{Approvers: approvers("a"), Rule: Anyone()}}}
	bad := []struct {
		name string
		snap RequestSnapshot
	}{
		{"empty ID", func() RequestSnapshot { s := good; s.ID = ""; return s }()},
		{"zero maker", func() RequestSnapshot { s := good; s.Maker = Actor{}; return s }()},
		{"empty chain", func() RequestSnapshot { s := good; s.Chain = nil; return s }()},
		{"negative step index", func() RequestSnapshot { s := good; s.StepIndex = -1; return s }()},
		{"step index past the chain", func() RequestSnapshot { s := good; s.StepIndex = 5; return s }()},
		{"unknown state", func() RequestSnapshot { s := good; s.State = State("weird"); return s }()},
	}
	for _, tc := range bad {
		t.Run("Given a snapshot with a "+tc.name, func(t *testing.T) {
			if err := e.Restore(ctx(), tc.snap); !errors.Is(err, ErrBadSnapshot) {
				t.Fatalf("err = %v, want ErrBadSnapshot", err)
			}
		})
	}
	t.Run("Given a valid snapshot", func(t *testing.T) {
		if err := e.Restore(ctx(), good); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		t.Run("Then restoring the same ID again is ErrExists", func(t *testing.T) {
			if err := e.Restore(ctx(), good); !errors.Is(err, ErrExists) {
				t.Fatalf("err = %v, want ErrExists", err)
			}
		})
	})
}
