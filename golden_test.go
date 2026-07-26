package assent

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zkrebbekx/sundial"
)

// The ten golden vectors from docs/DESIGN.md, each a Given/When/Then case
// asserting exact outcomes. The wider suites in engine_test.go,
// delegation_test.go and snapshot_test.go exercise the edges around them.

func TestGolden1SequentialChain(t *testing.T) {
	t.Run("Given a two-step chain [A{mgr,Unanimous}, B{finance,Unanimous}]", func(t *testing.T) {
		e, _ := newEngine(t)
		req := Request{
			ID:    "g1",
			Maker: user("alice"),
			Chain: []Step{
				{Name: "A", Approvers: approvers("mgr"), Rule: Unanimous()},
				{Name: "B", Approvers: approvers("finance"), Rule: Unanimous()},
			},
		}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When mgr approves step A", func(t *testing.T) {
			mustApprove(t, e, "g1", "mgr", "ok", mon)
			t.Run("Then the request stays Pending on step B", func(t *testing.T) {
				mustState(t, e, "g1", Pending)
				if idx := snap(t, e, "g1").StepIndex; idx != 1 {
					t.Fatalf("StepIndex = %d, want 1", idx)
				}
			})
			t.Run("When finance approves step B", func(t *testing.T) {
				mustApprove(t, e, "g1", "finance", "ok", mon)
				t.Run("Then the request is Approved", func(t *testing.T) {
					mustState(t, e, "g1", Approved)
				})
			})
		})
	})

	t.Run("Given a fresh two-step chain", func(t *testing.T) {
		e, _ := newEngine(t)
		req := Request{
			ID:    "g1b",
			Maker: user("alice"),
			Chain: []Step{
				{Approvers: approvers("mgr"), Rule: Unanimous()},
				{Approvers: approvers("finance"), Rule: Unanimous()},
			},
		}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When finance approves step B before step A is active", func(t *testing.T) {
			err := e.Decide(ctx(), "g1b", Decision{Actor: user("finance"), Approve: true, Reason: "x", At: mon})
			t.Run("Then it errors: the step is not active", func(t *testing.T) {
				if !errors.Is(err, ErrStepNotActive) {
					t.Fatalf("err = %v, want ErrStepNotActive", err)
				}
				mustState(t, e, "g1b", Pending)
			})
		})
	})
}

func TestGolden2ParallelUnanimous(t *testing.T) {
	t.Run("Given one step {a,b,c} Unanimous", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "g2", user("maker"), Unanimous(), "a", "b", "c")
		t.Run("When a and b approve", func(t *testing.T) {
			mustApprove(t, e, "g2", "a", "ok", mon)
			mustApprove(t, e, "g2", "b", "ok", mon)
			t.Run("Then the request stays Pending", func(t *testing.T) {
				mustState(t, e, "g2", Pending)
			})
			t.Run("When c approves", func(t *testing.T) {
				mustApprove(t, e, "g2", "c", "ok", mon)
				t.Run("Then the step completes and the request is Approved", func(t *testing.T) {
					mustState(t, e, "g2", Approved)
				})
			})
		})
	})

	t.Run("Given a fresh unanimous step {a,b,c}", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "g2b", user("maker"), Unanimous(), "a", "b", "c")
		t.Run("When b rejects", func(t *testing.T) {
			err := e.Decide(ctx(), "g2b", Decision{Actor: user("b"), Approve: false, Reason: "no", At: mon})
			if err != nil {
				t.Fatalf("Decide reject: %v", err)
			}
			t.Run("Then the request is Rejected", func(t *testing.T) {
				mustState(t, e, "g2b", Rejected)
			})
		})
	})
}

func TestGolden3QuorumMofN(t *testing.T) {
	t.Run("Given one step {a,b,c,d,e} Quorum(3)", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "g3", user("maker"), Quorum(3), "a", "b", "c", "d", "e")
		t.Run("When two approve", func(t *testing.T) {
			mustApprove(t, e, "g3", "a", "ok", mon)
			mustApprove(t, e, "g3", "b", "ok", mon)
			t.Run("Then the step is not yet complete (2 of 3)", func(t *testing.T) {
				mustState(t, e, "g3", Pending)
			})
			t.Run("When a third distinct actor approves", func(t *testing.T) {
				mustApprove(t, e, "g3", "c", "ok", mon)
				t.Run("Then the step completes at exactly three", func(t *testing.T) {
					mustState(t, e, "g3", Approved)
				})
				t.Run("Then a fourth approval is a no-op", func(t *testing.T) {
					if err := e.Decide(ctx(), "g3", Decision{Actor: user("d"), Approve: true, Reason: "late", At: mon}); err != nil {
						t.Fatalf("fourth Decide = %v, want nil no-op", err)
					}
					got := len(snap(t, e, "g3").Chain[0].Decisions)
					if got != 3 {
						t.Fatalf("recorded decisions = %d, want 3", got)
					}
				})
			})
		})
	})
}

func TestGolden4Anyone(t *testing.T) {
	t.Run("Given one step {a,b,c} Anyone", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "g4", user("maker"), Anyone(), "a", "b", "c")
		t.Run("When the first approval arrives", func(t *testing.T) {
			mustApprove(t, e, "g4", "a", "ok", mon)
			t.Run("Then the step completes and the request is Approved", func(t *testing.T) {
				mustState(t, e, "g4", Approved)
				if got := len(snap(t, e, "g4").Chain[0].Decisions); got != 1 {
					t.Fatalf("decisions = %d, want 1", got)
				}
			})
		})
	})
}

func TestGolden5FourEyes(t *testing.T) {
	t.Run("Given maker alice is also listed as an approver", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "g5", user("alice"), Anyone(), "alice", "bob")
		t.Run("When alice tries to approve her own request", func(t *testing.T) {
			err := e.Decide(ctx(), "g5", Decision{Actor: user("alice"), Approve: true, Reason: "self", At: mon})
			t.Run("Then it errors: the maker cannot be a checker", func(t *testing.T) {
				if !errors.Is(err, ErrMakerChecker) {
					t.Fatalf("err = %v, want ErrMakerChecker", err)
				}
				mustState(t, e, "g5", Pending)
			})
			t.Run("Then bob can still approve", func(t *testing.T) {
				mustApprove(t, e, "g5", "bob", "ok", mon)
				mustState(t, e, "g5", Approved)
			})
		})
	})
}

func TestGolden6Delegation(t *testing.T) {
	t.Run("Given a delegation bob->carol, window [Mon,Fri), series [expense]", func(t *testing.T) {
		del := NewDelegations(Delegation{
			From:   user("bob"),
			To:     user("carol"),
			Window: Interval{Start: mon.Add(-9 * time.Hour), End: fri.Add(-9 * time.Hour)}, // [Mon 00:00, Fri 00:00)
			Series: []string{"expense"},
		})
		e, _ := newEngine(t, WithDelegations(del))
		req := Request{
			ID:     "g6",
			Maker:  user("alice"),
			Series: "expense",
			Chain:  []Step{{Approvers: approvers("bob"), Rule: Unanimous()}},
		}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		bob := user("bob")
		t.Run("When carol approves on behalf of bob on Wednesday", func(t *testing.T) {
			err := e.Decide(ctx(), "g6", Decision{Actor: user("carol"), OnBehalfOf: &bob, Approve: true, Reason: "expense ok", At: wed})
			if err != nil {
				t.Fatalf("Decide OBO: %v", err)
			}
			t.Run("Then the decision counts as bob's and the request is Approved", func(t *testing.T) {
				mustState(t, e, "g6", Approved)
				dec := snap(t, e, "g6").Chain[0].Decisions
				if len(dec) != 1 {
					t.Fatalf("decisions = %d, want 1", len(dec))
				}
				if dec[0].Actor.ID != "carol" || dec[0].Authority.ID != "bob" {
					t.Fatalf("actor/authority = %q/%q, want carol/bob", dec[0].Actor.ID, dec[0].Authority.ID)
				}
			})
			t.Run("Then the trail records actor carol and authority bob", func(t *testing.T) {
				tr := trail(t, e, "g6")
				var found bool
				for _, en := range tr {
					if en.Type == EventDecision {
						found = true
						if en.Actor.ID != "carol" {
							t.Fatalf("trail actor = %q, want carol", en.Actor.ID)
						}
						if en.Authority == nil || en.Authority.ID != "bob" {
							t.Fatalf("trail authority = %v, want bob", en.Authority)
						}
					}
				}
				if !found {
					t.Fatal("no decision entry on the trail")
				}
			})
		})
	})

	t.Run("Given the same delegation and a fresh request on Saturday", func(t *testing.T) {
		del := NewDelegations(Delegation{
			From:   user("bob"),
			To:     user("carol"),
			Window: Interval{Start: mon.Add(-9 * time.Hour), End: fri.Add(-9 * time.Hour)},
			Series: []string{"expense"},
		})
		e, _ := newEngine(t, WithDelegations(del))
		req := Request{ID: "g6b", Maker: user("alice"), Series: "expense", Chain: []Step{{Approvers: approvers("bob"), Rule: Unanimous()}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		bob := user("bob")
		t.Run("When carol approves on behalf of bob on Saturday", func(t *testing.T) {
			err := e.Decide(ctx(), "g6b", Decision{Actor: user("carol"), OnBehalfOf: &bob, Approve: true, Reason: "late", At: sat})
			t.Run("Then it errors: no delegation is in force", func(t *testing.T) {
				if !errors.Is(err, ErrNoDelegation) {
					t.Fatalf("err = %v, want ErrNoDelegation", err)
				}
				mustState(t, e, "g6b", Pending)
			})
		})
	})
}

func TestGolden7IdempotentDecision(t *testing.T) {
	t.Run("Given a two-step chain whose first step bob alone completes", func(t *testing.T) {
		e, log := newEngine(t)
		req := Request{
			ID:    "g7",
			Maker: user("alice"),
			Chain: []Step{
				{Approvers: approvers("bob", "extra"), Rule: Anyone()},
				{Approvers: approvers("finance"), Rule: Anyone()},
			},
		}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When two goroutines record bob's approve at the same instant", func(t *testing.T) {
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					errs[i] = e.Decide(ctx(), "g7", Decision{Actor: user("bob"), Approve: true, Reason: "ok", At: mon})
				}(i)
			}
			wg.Wait()
			t.Run("Then neither call errors", func(t *testing.T) {
				for i, err := range errs {
					if err != nil {
						t.Fatalf("goroutine %d: %v", i, err)
					}
				}
			})
			t.Run("Then exactly one decision is stored on step 0", func(t *testing.T) {
				if got := len(snap(t, e, "g7").Chain[0].Decisions); got != 1 {
					t.Fatalf("decisions on step 0 = %d, want 1", got)
				}
			})
			t.Run("Then the chain advanced exactly once, to step 1", func(t *testing.T) {
				s := snap(t, e, "g7")
				if s.State != Pending || s.StepIndex != 1 {
					t.Fatalf("state/step = %q/%d, want pending/1", s.State, s.StepIndex)
				}
				if n := countType(trail(t, e, "g7"), EventAdvanced); n != 1 {
					t.Fatalf("advanced events = %d, want 1", n)
				}
			})
			t.Run("Then the audit trail holds exactly one bob decision", func(t *testing.T) {
				n := 0
				for _, en := range trail(t, e, "g7") {
					if en.Type == EventDecision {
						n++
					}
				}
				if n != 1 {
					t.Fatalf("decision events = %d, want 1", n)
				}
				_ = log
			})
		})
	})

	t.Run("Given a single Quorum(2) step {bob,carol,dave}", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "g7b", user("alice"), Quorum(2), "bob", "carol", "dave")
		t.Run("When bob and carol approve concurrently", func(t *testing.T) {
			var wg sync.WaitGroup
			for _, id := range []string{"bob", "carol"} {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					_ = e.Decide(ctx(), "g7b", Decision{Actor: user(id), Approve: true, Reason: "ok", At: mon})
				}(id)
			}
			wg.Wait()
			t.Run("Then both distinct approvals count and the step completes once", func(t *testing.T) {
				s := snap(t, e, "g7b")
				if len(s.Chain[0].Decisions) != 2 {
					t.Fatalf("decisions = %d, want 2", len(s.Chain[0].Decisions))
				}
				mustState(t, e, "g7b", Approved)
				if n := countType(trail(t, e, "g7b"), EventApproved); n != 1 {
					t.Fatalf("approved events = %d, want 1", n)
				}
			})
		})
	})
}

func TestGolden8EscalationOnBreach(t *testing.T) {
	t.Run("Given a step with budget 8h, warn@4h, breach@8h, action escalate-up", func(t *testing.T) {
		e, _ := newEngine(t)
		esc := &Escalation{
			Schedule: stdSchedule(),
			Budget:   8 * time.Hour,
			Levels:   []sundial.Level{{Name: "warn", At: 4 * time.Hour}, {Name: "breach", At: 8 * time.Hour}},
			Action:   EscalateUp,
			Fallback: approver("senior"),
		}
		req := Request{
			ID:    "g8",
			Maker: user("alice"),
			Chain: []Step{{Approvers: approvers("mgr"), Rule: Unanimous(), Escalation: esc}},
		}
		if err := e.Submit(ctx(), req, mon); err != nil { // Monday 09:00
			t.Fatalf("Submit: %v", err)
		}
		nineWorkingHours := time.Date(2026, 1, 6, 10, 0, 0, 0, time.UTC) // Tuesday 10:00
		t.Run("When ticked at 9 working hours", func(t *testing.T) {
			if err := e.Tick(ctx(), "g8", nineWorkingHours); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			t.Run("Then the fallback approver is added to the step", func(t *testing.T) {
				appr := snap(t, e, "g8").Chain[0].Approvers
				if len(appr) != 2 || appr[1] != approver("senior") {
					t.Fatalf("approvers = %v, want [mgr senior]", appr)
				}
			})
			t.Run("Then the trail records exactly one escalation and one warn", func(t *testing.T) {
				tr := trail(t, e, "g8")
				if n := countType(tr, EventEscalated); n != 1 {
					t.Fatalf("escalated events = %d, want 1", n)
				}
				if n := countType(tr, EventWarned); n != 1 {
					t.Fatalf("warned events = %d, want 1", n)
				}
			})
			t.Run("When ticked again at the same instant", func(t *testing.T) {
				if err := e.Tick(ctx(), "g8", nineWorkingHours); err != nil {
					t.Fatalf("Tick 2: %v", err)
				}
				t.Run("Then the escalation is not applied twice", func(t *testing.T) {
					if got := len(snap(t, e, "g8").Chain[0].Approvers); got != 2 {
						t.Fatalf("approvers after re-tick = %d, want 2", got)
					}
					if n := countType(trail(t, e, "g8"), EventEscalated); n != 1 {
						t.Fatalf("escalated events after re-tick = %d, want 1", n)
					}
				})
			})
		})
	})
}

func TestGolden9WithdrawMidFlight(t *testing.T) {
	t.Run("Given a Pending request advanced to step B", func(t *testing.T) {
		e, _ := newEngine(t)
		req := Request{
			ID:    "g9",
			Maker: user("alice"),
			Chain: []Step{
				{Approvers: approvers("mgr"), Rule: Unanimous()},
				{Approvers: approvers("finance"), Rule: Unanimous()},
			},
		}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		mustApprove(t, e, "g9", "mgr", "ok", mon)
		t.Run("When the maker withdraws", func(t *testing.T) {
			if err := e.Withdraw(ctx(), "g9", user("alice"), "changed mind", mon); err != nil {
				t.Fatalf("Withdraw: %v", err)
			}
			t.Run("Then the request is Withdrawn", func(t *testing.T) {
				mustState(t, e, "g9", Withdrawn)
			})
			t.Run("When finance later approves step B", func(t *testing.T) {
				err := e.Decide(ctx(), "g9", Decision{Actor: user("finance"), Approve: true, Reason: "ok", At: tue})
				t.Run("Then it errors: the request is not pending", func(t *testing.T) {
					if !errors.Is(err, ErrNotPending) {
						t.Fatalf("err = %v, want ErrNotPending", err)
					}
					mustState(t, e, "g9", Withdrawn)
				})
			})
		})
	})
}

func TestGolden10AuditCompleteness(t *testing.T) {
	t.Run("Given a submit/approve/approve/approved run over one unanimous step", func(t *testing.T) {
		e, log := newEngine(t)
		req := Request{
			ID:    "g10",
			Maker: user("alice"),
			Chain: []Step{{Approvers: approvers("mgr", "finance"), Rule: Unanimous()}},
		}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		mustApprove(t, e, "g10", "mgr", "reviewed", mon)
		mustApprove(t, e, "g10", "finance", "funded", tue)
		mustState(t, e, "g10", Approved)

		tr := trail(t, e, "g10")
		t.Run("Then the trail holds four entries in order", func(t *testing.T) {
			if len(tr) != 4 {
				t.Fatalf("trail length = %d, want 4", len(tr))
			}
			wantTypes := []string{EventSubmitted, EventDecision, EventDecision, EventApproved}
			for i, want := range wantTypes {
				if tr[i].Type != want {
					t.Fatalf("entry %d type = %q, want %q", i, tr[i].Type, want)
				}
			}
		})
		t.Run("Then each entry carries the right actor, reason and timestamp", func(t *testing.T) {
			if tr[0].Actor.ID != "alice" || !tr[0].At.Equal(mon) {
				t.Fatalf("submit entry = %+v, want alice at Monday", tr[0])
			}
			if tr[1].Actor.ID != "mgr" || tr[1].Reason != "reviewed" || !tr[1].At.Equal(mon) {
				t.Fatalf("mgr entry = %+v", tr[1])
			}
			if tr[2].Actor.ID != "finance" || tr[2].Reason != "funded" || !tr[2].At.Equal(tue) {
				t.Fatalf("finance entry = %+v", tr[2])
			}
			if tr[3].Type != EventApproved || tr[3].Actor.ID != "finance" {
				t.Fatalf("approved entry = %+v", tr[3])
			}
		})
		t.Run("Then no entry has been rewritten", func(t *testing.T) {
			for i := 0; i < len(tr); i++ {
				id := "g10#" + pad6(i)
				hist, err := log.History(ctx(), "approval.event", id)
				if err != nil {
					t.Fatalf("History %s: %v", id, err)
				}
				if len(hist) != 1 {
					t.Fatalf("history for %s = %d records, want 1 (never rewritten)", id, len(hist))
				}
				if !hist[0].TxTo.IsZero() {
					t.Fatalf("entry %s is superseded; an audit entry must not be rewritten", id)
				}
			}
		})
	})
}

// pad6 renders i as a six-digit, zero-padded string, matching the audit entity
// ID scheme.
func pad6(i int) string {
	s := itoa(i)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}
