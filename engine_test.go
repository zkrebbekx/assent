package assent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zkrebbekx/chronicle"
	"github.com/zkrebbekx/sundial"
)

func TestNew(t *testing.T) {
	log := chronicle.NewLog(chronicle.NewMemStore())
	t.Run("Given a nil store", func(t *testing.T) {
		t.Run("Then New errors", func(t *testing.T) {
			if _, err := New(nil, log); !errors.Is(err, ErrBadRequest) {
				t.Fatalf("err = %v, want ErrBadRequest", err)
			}
		})
	})
	t.Run("Given a nil log", func(t *testing.T) {
		t.Run("Then New errors", func(t *testing.T) {
			if _, err := New(NewMemStore(), nil); !errors.Is(err, ErrBadRequest) {
				t.Fatalf("err = %v, want ErrBadRequest", err)
			}
		})
	})
	t.Run("Given valid arguments and options", func(t *testing.T) {
		e, err := New(NewMemStore(), log,
			WithDelegations(nil), WithSystemActor(Actor{}), WithSystemActor(user("root")))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Run("Then a nil delegation option keeps the default resolver", func(t *testing.T) {
			if _, ok := e.deleg.(noDelegations); !ok {
				t.Fatalf("resolver = %T, want noDelegations", e.deleg)
			}
		})
		t.Run("Then a zero system actor is ignored and a real one is kept", func(t *testing.T) {
			if e.system.ID != "root" {
				t.Fatalf("system actor = %q, want root", e.system.ID)
			}
		})
	})
}

func TestSubmitValidation(t *testing.T) {
	e, _ := newEngine(t)
	bad := []struct {
		name string
		req  Request
		want error
	}{
		{"empty ID", Request{Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone()}}}, ErrBadRequest},
		{"zero maker", Request{ID: "x", Chain: []Step{{Approvers: approvers("a"), Rule: Anyone()}}}, ErrBadRequest},
		{"empty chain", Request{ID: "x", Maker: user("m")}, ErrEmptyChain},
		{"empty step", Request{ID: "x", Maker: user("m"), Chain: []Step{{Rule: Anyone()}}}, ErrEmptyStep},
		{"assignment both", Request{ID: "x", Maker: user("m"), Chain: []Step{{Approvers: []Assignment{{User: "a", Group: "g"}}, Rule: Anyone()}}}, ErrBadAssignment},
		{"assignment neither", Request{ID: "x", Maker: user("m"), Chain: []Step{{Approvers: []Assignment{{}}, Rule: Anyone()}}}, ErrBadAssignment},
		{"quorum below one", Request{ID: "x", Maker: user("m"), Chain: []Step{{Approvers: approvers("a", "b"), Rule: Quorum(0)}}}, ErrBadRule},
		{"quorum above users", Request{ID: "x", Maker: user("m"), Chain: []Step{{Approvers: approvers("a", "b"), Rule: Quorum(3)}}}, ErrBadRule},
		{"bad rule kind", Request{ID: "x", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Rule{Kind: RuleKind(9)}}}}, ErrBadRule},
	}
	for _, tc := range bad {
		t.Run("Given "+tc.name, func(t *testing.T) {
			err := e.Submit(ctx(), tc.req, mon)
			t.Run("Then submit errors", func(t *testing.T) {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			})
		})
	}

	t.Run("Given a zero submit time", func(t *testing.T) {
		req := Request{ID: "z", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone()}}}
		if err := e.Submit(ctx(), req, time.Time{}); !errors.Is(err, ErrBadDecision) {
			t.Fatalf("err = %v, want ErrBadDecision", err)
		}
	})

	t.Run("Given a quorum step that also has a group", func(t *testing.T) {
		req := Request{ID: "grpq", Maker: user("m"), Chain: []Step{{
			Approvers: []Assignment{{User: "a"}, {Group: "team"}}, Rule: Quorum(2),
		}}}
		t.Run("Then a quorum above the named users is allowed (the group is unbounded)", func(t *testing.T) {
			if err := e.Submit(ctx(), req, mon); err != nil {
				t.Fatalf("Submit: %v", err)
			}
		})
	})

	t.Run("Given a duplicate ID", func(t *testing.T) {
		req := Request{ID: "dup", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone()}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit 1: %v", err)
		}
		if err := e.Submit(ctx(), req, mon); !errors.Is(err, ErrExists) {
			t.Fatalf("Submit 2 err = %v, want ErrExists", err)
		}
	})
}

func TestSubmitEscalationValidation(t *testing.T) {
	e, _ := newEngine(t)
	t.Run("Given an escalation with an invalid schedule", func(t *testing.T) {
		esc := &Escalation{Schedule: sundial.Schedule{}, Budget: time.Hour, Action: AutoApprove}
		req := Request{ID: "be", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone(), Escalation: esc}}}
		err := e.Submit(ctx(), req, mon)
		t.Run("Then submit errors, wrapping the sundial fault", func(t *testing.T) {
			if !errors.Is(err, ErrBadEscalation) {
				t.Fatalf("err = %v, want ErrBadEscalation", err)
			}
			if !errors.Is(err, sundial.ErrBadSchedule) {
				t.Fatalf("err = %v, want to wrap sundial.ErrBadSchedule", err)
			}
		})
	})
	t.Run("Given an escalate-up action with no fallback", func(t *testing.T) {
		esc := &Escalation{Schedule: stdSchedule(), Budget: time.Hour, Action: EscalateUp}
		req := Request{ID: "nf", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone(), Escalation: esc}}}
		if err := e.Submit(ctx(), req, mon); !errors.Is(err, ErrBadEscalation) {
			t.Fatalf("err = %v, want ErrBadEscalation", err)
		}
	})
	t.Run("Given an unknown escalation action", func(t *testing.T) {
		esc := &Escalation{Schedule: stdSchedule(), Budget: time.Hour, Action: EscalationAction(9)}
		req := Request{ID: "ua", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone(), Escalation: esc}}}
		if err := e.Submit(ctx(), req, mon); !errors.Is(err, ErrBadEscalation) {
			t.Fatalf("err = %v, want ErrBadEscalation", err)
		}
	})
}

func TestDecideBadInput(t *testing.T) {
	e, _ := newEngine(t)
	mustSubmit(t, e, "d1", user("m"), Anyone(), "a", "b")
	cases := []struct {
		name string
		d    Decision
	}{
		{"zero actor", Decision{Approve: true, At: mon}},
		{"zero time", Decision{Actor: user("a"), Approve: true}},
		{"zero on-behalf-of", Decision{Actor: user("a"), OnBehalfOf: &Actor{}, Approve: true, At: mon}},
	}
	for _, tc := range cases {
		t.Run("Given a decision with a "+tc.name, func(t *testing.T) {
			if err := e.Decide(ctx(), "d1", tc.d); !errors.Is(err, ErrBadDecision) {
				t.Fatalf("err = %v, want ErrBadDecision", err)
			}
		})
	}
	t.Run("Given a decision on a missing request", func(t *testing.T) {
		err := e.Decide(ctx(), "missing", Decision{Actor: user("a"), Approve: true, At: mon})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("Given a decision by a non-approver", func(t *testing.T) {
		err := e.Decide(ctx(), "d1", Decision{Actor: user("stranger"), Approve: true, At: mon})
		if !errors.Is(err, ErrNotApprover) {
			t.Fatalf("err = %v, want ErrNotApprover", err)
		}
	})
}

func TestDecideTerminalStates(t *testing.T) {
	t.Run("Given an approved single-step request", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "t1", user("m"), Anyone(), "a", "b")
		mustApprove(t, e, "t1", "a", "ok", mon)
		t.Run("When an in-chain approver adds a late approval", func(t *testing.T) {
			err := e.Decide(ctx(), "t1", Decision{Actor: user("b"), Approve: true, At: mon})
			t.Run("Then it is a harmless no-op", func(t *testing.T) {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			})
		})
		t.Run("When an in-chain approver tries to reject", func(t *testing.T) {
			err := e.Decide(ctx(), "t1", Decision{Actor: user("b"), Approve: false, At: mon})
			t.Run("Then it errors: not pending", func(t *testing.T) {
				if !errors.Is(err, ErrNotPending) {
					t.Fatalf("err = %v, want ErrNotPending", err)
				}
			})
		})
		t.Run("When a stranger approves the approved request", func(t *testing.T) {
			err := e.Decide(ctx(), "t1", Decision{Actor: user("zzz"), Approve: true, At: mon})
			t.Run("Then it errors: not pending", func(t *testing.T) {
				if !errors.Is(err, ErrNotPending) {
					t.Fatalf("err = %v, want ErrNotPending", err)
				}
			})
		})
	})
	t.Run("Given a rejected request", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "t2", user("m"), Anyone(), "a", "b")
		if err := e.Decide(ctx(), "t2", Decision{Actor: user("a"), Approve: false, At: mon}); err != nil {
			t.Fatalf("reject: %v", err)
		}
		t.Run("When another approver approves", func(t *testing.T) {
			err := e.Decide(ctx(), "t2", Decision{Actor: user("b"), Approve: true, At: mon})
			t.Run("Then it errors: not pending", func(t *testing.T) {
				if !errors.Is(err, ErrNotPending) {
					t.Fatalf("err = %v, want ErrNotPending", err)
				}
			})
		})
	})
}

func TestDecideGroupClaim(t *testing.T) {
	t.Run("Given a step assigned to a candidate group", func(t *testing.T) {
		e, _ := newEngine(t)
		req := Request{ID: "grp", Maker: user("m"), Chain: []Step{{
			Approvers: []Assignment{{Group: "finance"}}, Rule: Unanimous(),
		}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When a claimed member of the group approves", func(t *testing.T) {
			err := e.Decide(ctx(), "grp", Decision{Actor: user("dana"), Group: "finance", Approve: true, Reason: "ok", At: mon})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			t.Run("Then the group slot is satisfied and the request is Approved", func(t *testing.T) {
				mustState(t, e, "grp", Approved)
			})
		})
	})
	t.Run("Given a group step and an actor claiming the wrong group", func(t *testing.T) {
		e, _ := newEngine(t)
		req := Request{ID: "grp2", Maker: user("m"), Chain: []Step{{
			Approvers: []Assignment{{Group: "finance"}}, Rule: Anyone(),
		}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("Then a claim for another group is not eligible", func(t *testing.T) {
			err := e.Decide(ctx(), "grp2", Decision{Actor: user("dana"), Group: "ops", Approve: true, At: mon})
			if !errors.Is(err, ErrNotApprover) {
				t.Fatalf("err = %v, want ErrNotApprover", err)
			}
		})
	})
}

func TestSeparationOfDuties(t *testing.T) {
	t.Run("Given a two-step chain and the SeparateSteps predicate", func(t *testing.T) {
		e, _ := newEngine(t, WithSoD(SeparateSteps))
		req := Request{ID: "sod", Maker: user("m"), Chain: []Step{
			{Approvers: approvers("x", "y"), Rule: Anyone()},
			{Approvers: approvers("x", "z"), Rule: Anyone()},
		}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When x approves step 0", func(t *testing.T) {
			mustApprove(t, e, "sod", "x", "ok", mon)
			t.Run("Then x cannot also approve step 1", func(t *testing.T) {
				err := e.Decide(ctx(), "sod", Decision{Actor: user("x"), Approve: true, At: mon})
				if !errors.Is(err, ErrSeparation) {
					t.Fatalf("err = %v, want ErrSeparation", err)
				}
			})
			t.Run("Then a different actor z can approve step 1", func(t *testing.T) {
				mustApprove(t, e, "sod", "z", "ok", mon)
				mustState(t, e, "sod", Approved)
			})
		})
	})
}

func TestWithdraw(t *testing.T) {
	t.Run("Given a pending request", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "w1", user("alice"), Anyone(), "a")
		t.Run("Then a non-maker cannot withdraw", func(t *testing.T) {
			if err := e.Withdraw(ctx(), "w1", user("bob"), "no", mon); !errors.Is(err, ErrNotMaker) {
				t.Fatalf("err = %v, want ErrNotMaker", err)
			}
		})
		t.Run("Then bad arguments error", func(t *testing.T) {
			if err := e.Withdraw(ctx(), "w1", Actor{}, "no", mon); !errors.Is(err, ErrBadDecision) {
				t.Fatalf("err = %v, want ErrBadDecision", err)
			}
			if err := e.Withdraw(ctx(), "w1", user("alice"), "no", time.Time{}); !errors.Is(err, ErrBadDecision) {
				t.Fatalf("err = %v, want ErrBadDecision", err)
			}
		})
		t.Run("Then the maker can withdraw", func(t *testing.T) {
			if err := e.Withdraw(ctx(), "w1", user("alice"), "changed mind", mon); err != nil {
				t.Fatalf("Withdraw: %v", err)
			}
			mustState(t, e, "w1", Withdrawn)
		})
		t.Run("Then withdrawing again errors: not pending", func(t *testing.T) {
			if err := e.Withdraw(ctx(), "w1", user("alice"), "again", mon); !errors.Is(err, ErrNotPending) {
				t.Fatalf("err = %v, want ErrNotPending", err)
			}
		})
	})
}

func TestReassign(t *testing.T) {
	t.Run("Given a pending step {a,b}", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "r1", user("m"), Anyone(), "a", "b")
		t.Run("Then bad arguments error", func(t *testing.T) {
			if err := e.Reassign(ctx(), "r1", approver("a"), Assignment{}, user("m"), "x", mon); !errors.Is(err, ErrBadDecision) {
				t.Fatalf("err = %v, want ErrBadDecision", err)
			}
		})
		t.Run("When a is reassigned to c", func(t *testing.T) {
			if err := e.Reassign(ctx(), "r1", approver("a"), approver("c"), user("m"), "cover", mon); err != nil {
				t.Fatalf("Reassign: %v", err)
			}
			t.Run("Then c replaces a on the step", func(t *testing.T) {
				appr := snap(t, e, "r1").Chain[0].Approvers
				if appr[0] != approver("c") || appr[1] != approver("b") {
					t.Fatalf("approvers = %v, want [c b]", appr)
				}
			})
			t.Run("Then c can now approve", func(t *testing.T) {
				mustApprove(t, e, "r1", "c", "ok", mon)
				mustState(t, e, "r1", Approved)
			})
		})
	})
	t.Run("Given a step where the from-approver is absent", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "r2", user("m"), Anyone(), "a")
		t.Run("When reassigning from an unknown approver", func(t *testing.T) {
			if err := e.Reassign(ctx(), "r2", approver("ghost"), approver("d"), user("m"), "add", mon); err != nil {
				t.Fatalf("Reassign: %v", err)
			}
			t.Run("Then the target is appended", func(t *testing.T) {
				appr := snap(t, e, "r2").Chain[0].Approvers
				if len(appr) != 2 || appr[1] != approver("d") {
					t.Fatalf("approvers = %v, want [a d]", appr)
				}
			})
		})
	})
	t.Run("Given a terminal request", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "r3", user("m"), Anyone(), "a")
		mustApprove(t, e, "r3", "a", "ok", mon)
		t.Run("Then reassignment errors: not pending", func(t *testing.T) {
			if err := e.Reassign(ctx(), "r3", approver("a"), approver("b"), user("m"), "x", mon); !errors.Is(err, ErrNotPending) {
				t.Fatalf("err = %v, want ErrNotPending", err)
			}
		})
	})
}

func TestTickEdges(t *testing.T) {
	t.Run("Given a request with no escalation", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "tk1", user("m"), Anyone(), "a")
		t.Run("Then ticking is a no-op", func(t *testing.T) {
			if err := e.Tick(ctx(), "tk1", mon); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if snap(t, e, "tk1").Seq != 1 { // only the submit event
				t.Fatalf("Seq = %d, want 1", snap(t, e, "tk1").Seq)
			}
		})
	})
	t.Run("Given a terminal request", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "tk2", user("m"), Anyone(), "a")
		mustApprove(t, e, "tk2", "a", "ok", mon)
		t.Run("Then ticking is a no-op", func(t *testing.T) {
			if err := e.Tick(ctx(), "tk2", tue); err != nil {
				t.Fatalf("Tick: %v", err)
			}
		})
	})
	t.Run("Given a zero tick time", func(t *testing.T) {
		e, _ := newEngine(t)
		mustSubmit(t, e, "tk3", user("m"), Anyone(), "a")
		if err := e.Tick(ctx(), "tk3", time.Time{}); !errors.Is(err, ErrBadDecision) {
			t.Fatalf("err = %v, want ErrBadDecision", err)
		}
	})
	t.Run("Given a missing request", func(t *testing.T) {
		e, _ := newEngine(t)
		if err := e.Tick(ctx(), "nope", mon); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("Given a running SLA polled before any level fires", func(t *testing.T) {
		e, _ := newEngine(t)
		esc := &Escalation{Schedule: stdSchedule(), Budget: 8 * time.Hour, Levels: []sundial.Level{{Name: "warn", At: 4 * time.Hour}}, Action: EscalateUp, Fallback: approver("boss")}
		req := Request{ID: "tk4", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone(), Escalation: esc}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("Then an early tick records nothing", func(t *testing.T) {
			if err := e.Tick(ctx(), "tk4", mon.Add(time.Hour)); err != nil { // Monday 10:00, 1 working hour
				t.Fatalf("Tick: %v", err)
			}
			if countType(trail(t, e, "tk4"), EventWarned) != 0 {
				t.Fatal("warn fired too early")
			}
		})
	})
}

func TestEscalationReassignAndAutoApprove(t *testing.T) {
	breach := time.Date(2026, 1, 6, 10, 0, 0, 0, time.UTC) // 9 working hours after Monday 09:00
	t.Run("Given a reassign-on-breach step", func(t *testing.T) {
		e, _ := newEngine(t)
		esc := &Escalation{Schedule: stdSchedule(), Budget: 8 * time.Hour, Action: Reassign, Fallback: approver("oncall")}
		req := Request{ID: "es1", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone(), Escalation: esc}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When ticked past the breach", func(t *testing.T) {
			if err := e.Tick(ctx(), "es1", breach); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			t.Run("Then the step is reassigned to the fallback alone", func(t *testing.T) {
				appr := snap(t, e, "es1").Chain[0].Approvers
				if len(appr) != 1 || appr[0] != approver("oncall") {
					t.Fatalf("approvers = %v, want [oncall]", appr)
				}
			})
		})
	})
	t.Run("Given an auto-approve-on-breach single step", func(t *testing.T) {
		e, _ := newEngine(t)
		esc := &Escalation{Schedule: stdSchedule(), Budget: 8 * time.Hour, Action: AutoApprove}
		req := Request{ID: "es2", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Unanimous(), Escalation: esc}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When ticked past the breach", func(t *testing.T) {
			if err := e.Tick(ctx(), "es2", breach); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			t.Run("Then the request is auto-approved and the trail records it", func(t *testing.T) {
				mustState(t, e, "es2", Approved)
				tr := trail(t, e, "es2")
				if countType(tr, EventAutoApproved) != 1 || countType(tr, EventApproved) != 1 {
					t.Fatalf("auto_approved/approved counts wrong: %v", tr)
				}
			})
		})
	})
	t.Run("Given an auto-approve on a non-last step", func(t *testing.T) {
		e, _ := newEngine(t)
		esc := &Escalation{Schedule: stdSchedule(), Budget: 8 * time.Hour, Action: AutoApprove}
		req := Request{ID: "es3", Maker: user("m"), Chain: []Step{
			{Approvers: approvers("a"), Rule: Unanimous(), Escalation: esc},
			{Approvers: approvers("b"), Rule: Unanimous()},
		}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When ticked past the breach", func(t *testing.T) {
			if err := e.Tick(ctx(), "es3", breach); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			t.Run("Then the chain advances to the next step", func(t *testing.T) {
				s := snap(t, e, "es3")
				if s.State != Pending || s.StepIndex != 1 {
					t.Fatalf("state/step = %q/%d, want pending/1", s.State, s.StepIndex)
				}
			})
		})
	})
	t.Run("Given a custom system actor", func(t *testing.T) {
		e, _ := newEngine(t, WithSystemActor(user("sla-bot")))
		esc := &Escalation{Schedule: stdSchedule(), Budget: 8 * time.Hour, Action: EscalateUp, Fallback: approver("boss")}
		req := Request{ID: "es4", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone(), Escalation: esc}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		if err := e.Tick(ctx(), "es4", breach); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		t.Run("Then the escalation is attributed to the system actor", func(t *testing.T) {
			for _, en := range trail(t, e, "es4") {
				if en.Type == EventEscalated && en.Actor.ID != "sla-bot" {
					t.Fatalf("escalation actor = %q, want sla-bot", en.Actor.ID)
				}
			}
		})
	})
}

func TestStateAndTrailNotFound(t *testing.T) {
	e, _ := newEngine(t)
	t.Run("Given a missing request", func(t *testing.T) {
		if _, err := e.State(ctx(), "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("State err = %v, want ErrNotFound", err)
		}
		if _, err := e.Trail(ctx(), "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Trail err = %v, want ErrNotFound", err)
		}
	})
}

func TestContextCancellation(t *testing.T) {
	e, _ := newEngine(t)
	mustSubmit(t, e, "cc", user("m"), Anyone(), "a")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	t.Run("Given a cancelled context", func(t *testing.T) {
		if err := e.Decide(cancelled, "cc", Decision{Actor: user("a"), Approve: true, At: mon}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Decide err = %v, want context.Canceled", err)
		}
		if _, err := e.State(cancelled, "cc"); !errors.Is(err, context.Canceled) {
			t.Fatalf("State err = %v, want context.Canceled", err)
		}
		if err := e.Submit(cancelled, Request{ID: "cc2", Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone()}}}, mon); !errors.Is(err, context.Canceled) {
			t.Fatalf("Submit err = %v, want context.Canceled", err)
		}
	})
}

func TestStringers(t *testing.T) {
	t.Run("Given the rule kinds", func(t *testing.T) {
		cases := map[RuleKind]string{KindAnyone: "anyone", KindQuorum: "quorum", KindUnanimous: "unanimous", RuleKind(9): "unknown"}
		for k, want := range cases {
			if got := k.String(); got != want {
				t.Fatalf("RuleKind(%d).String() = %q, want %q", k, got, want)
			}
		}
	})
	t.Run("Given the escalation actions", func(t *testing.T) {
		cases := map[EscalationAction]string{EscalateUp: "escalate_up", Reassign: "reassign", AutoApprove: "auto_approve", EscalationAction(9): "unknown"}
		for a, want := range cases {
			if got := a.String(); got != want {
				t.Fatalf("EscalationAction(%d).String() = %q, want %q", a, got, want)
			}
		}
	})
}

func TestInternalHelpers(t *testing.T) {
	t.Run("Given an unknown rule kind", func(t *testing.T) {
		st := &StepState{Approvers: approvers("a"), Rule: Rule{Kind: RuleKind(9)}}
		if ruleMet(st) {
			t.Fatal("ruleMet should be false for an unknown kind")
		}
	})
	t.Run("Given a request view over a snapshot", func(t *testing.T) {
		s := &RequestSnapshot{Maker: user("m"), State: Pending, StepIndex: 1, Chain: make([]StepState, 3)}
		v := RequestView{snap: s}
		if v.Maker().ID != "m" || v.State() != Pending || v.StepIndex() != 1 || v.StepCount() != 3 {
			t.Fatal("view accessors wrong")
		}
		if v.ApprovedBy(-1) != nil || v.ApprovedBy(9) != nil {
			t.Fatal("ApprovedBy out of range should be nil")
		}
	})
	t.Run("Given the wrap helper", func(t *testing.T) {
		err := wrap(ErrNotFound, "abc")
		if !errors.Is(err, ErrNotFound) {
			t.Fatal("wrap should keep the sentinel matchable")
		}
	})
}
