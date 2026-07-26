package assent_test

import (
	"context"
	"fmt"
	"time"

	"github.com/zkrebbekx/assent"
	"github.com/zkrebbekx/chronicle"
	"github.com/zkrebbekx/sundial"
)

// Example runs a request through a single unanimous step and prints its ordered
// audit trail.
func Example() {
	ctx := context.Background()
	eng, _ := assent.New(assent.NewMemStore(), chronicle.NewLog(chronicle.NewMemStore()))
	at := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

	_ = eng.Submit(ctx, assent.Request{
		ID:    "exp-42",
		Maker: assent.Actor{ID: "alice"},
		Chain: []assent.Step{{
			Approvers: []assent.Assignment{{User: "mgr"}, {User: "finance"}},
			Rule:      assent.Unanimous(),
		}},
	}, at)

	_ = eng.Decide(ctx, "exp-42", assent.Decision{Actor: assent.Actor{ID: "mgr"}, Approve: true, Reason: "reviewed", At: at})
	_ = eng.Decide(ctx, "exp-42", assent.Decision{Actor: assent.Actor{ID: "finance"}, Approve: true, Reason: "funded", At: at})

	state, _ := eng.State(ctx, "exp-42")
	fmt.Println("state:", state)
	trail, _ := eng.Trail(ctx, "exp-42")
	for _, en := range trail {
		fmt.Printf("%s by %s\n", en.Type, en.Actor.ID)
	}
	// Output:
	// state: approved
	// submitted by alice
	// decision by mgr
	// decision by finance
	// approved by finance
}

// Example_delegation shows a delegate approving on behalf of an authority who is
// away, with the trail preserving both identities.
func Example_delegation() {
	ctx := context.Background()
	mon := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	fri := time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
	del := assent.NewDelegations(assent.Delegation{
		From:   assent.Actor{ID: "bob"},
		To:     assent.Actor{ID: "carol"},
		Window: assent.Interval{Start: mon, End: fri},
		Series: []string{"expense"},
	})
	eng, _ := assent.New(assent.NewMemStore(), chronicle.NewLog(chronicle.NewMemStore()), assent.WithDelegations(del))

	_ = eng.Submit(ctx, assent.Request{
		ID: "exp-99", Maker: assent.Actor{ID: "alice"}, Series: "expense",
		Chain: []assent.Step{{Approvers: []assent.Assignment{{User: "bob"}}, Rule: assent.Unanimous()}},
	}, mon)

	bob := assent.Actor{ID: "bob"}
	wed := time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)
	_ = eng.Decide(ctx, "exp-99", assent.Decision{Actor: assent.Actor{ID: "carol"}, OnBehalfOf: &bob, Approve: true, Reason: "covering", At: wed})

	state, _ := eng.State(ctx, "exp-99")
	fmt.Println("state:", state)
	trail, _ := eng.Trail(ctx, "exp-99")
	for _, en := range trail {
		if en.Type == assent.EventDecision {
			fmt.Printf("decision by %s on behalf of %s\n", en.Actor.ID, en.Authority.ID)
		}
	}
	// Output:
	// state: approved
	// decision by carol on behalf of bob
}

// Example_escalation shows a step's SLA breaching and its escalate-up action
// adding a fallback approver.
func Example_escalation() {
	ctx := context.Background()
	sched := sundial.Schedule{
		Loc:  time.UTC,
		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
	}
	eng, _ := assent.New(assent.NewMemStore(), chronicle.NewLog(chronicle.NewMemStore()))

	mon := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	_ = eng.Submit(ctx, assent.Request{
		ID: "req-sla", Maker: assent.Actor{ID: "alice"},
		Chain: []assent.Step{{
			Approvers: []assent.Assignment{{User: "mgr"}},
			Rule:      assent.Unanimous(),
			Escalation: &assent.Escalation{
				Schedule: sched, Budget: 8 * time.Hour,
				Action: assent.EscalateUp, Fallback: assent.Assignment{User: "director"},
			},
		}},
	}, mon)

	// Poll the SLA clock at nine working hours: the budget has breached.
	_ = eng.Tick(ctx, "req-sla", time.Date(2026, 1, 6, 10, 0, 0, 0, time.UTC))

	snap, _ := eng.Snapshot(ctx, "req-sla")
	fmt.Println("approvers now:", len(snap.Chain[0].Approvers))
	// Output:
	// approvers now: 2
}
