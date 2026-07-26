// Command examples drives the assent core through two scenarios a real approval
// service would run: a two-step expense chain with a quorum and a delegated
// approval, and a step whose SLA breaches and escalates. It shows what assent
// owns (the chain, the quorum math, delegation, the audit trail) and what it
// composes (fsmx for the lifecycle, sundial for the SLA, chronicle for the
// trail). Everything is deterministic — the caller supplies every instant — so
// the program prints the same story on every run.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/zkrebbekx/assent"
	"github.com/zkrebbekx/chronicle"
	"github.com/zkrebbekx/sundial"
)

func main() {
	ctx := context.Background()

	// A delegation: the finance lead is away, so a deputy may act for them on
	// expense requests for the working week.
	mon := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	fri := time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
	delegations := assent.NewDelegations(assent.Delegation{
		From:   assent.Actor{ID: "lead", Name: "Finance Lead"},
		To:     assent.Actor{ID: "deputy", Name: "Finance Deputy"},
		Window: assent.Interval{Start: mon, End: fri},
		Series: []string{"expense"},
	})

	eng, err := assent.New(
		assent.NewMemStore(),
		chronicle.NewLog(chronicle.NewMemStore()),
		assent.WithDelegations(delegations),
		assent.WithSoD(assent.SeparateSteps),
	)
	if err != nil {
		panic(err)
	}

	expenseChain(ctx, eng)
	fmt.Println()
	escalationScenario(ctx)
}

// expenseChain runs a two-step expense approval: a manager gate, then a two-of-
// three finance quorum, one of whose approvals is delegated.
func expenseChain(ctx context.Context, eng *assent.Engine) {
	at := time.Date(2026, 1, 6, 9, 0, 0, 0, time.UTC) // Tuesday morning
	req := assent.Request{
		ID:      "EXP-1001",
		Maker:   assent.Actor{ID: "alice", Name: "Alice"},
		Series:  "expense",
		Subject: "expense-report-1001",
		Chain: []assent.Step{
			{Name: "manager", Approvers: []assent.Assignment{{User: "mgr"}}, Rule: assent.Unanimous()},
			{Name: "finance", Approvers: []assent.Assignment{{User: "lead"}, {User: "cfo"}, {User: "controller"}}, Rule: assent.Quorum(2)},
		},
	}
	if err := eng.Submit(ctx, req, at); err != nil {
		panic(err)
	}
	fmt.Printf("submitted %s by %s (subject %s)\n", req.ID, req.Maker.Name, req.Subject)

	// Step 1: the manager approves and the chain advances to finance.
	decide(ctx, eng, "EXP-1001", assent.Decision{Actor: assent.Actor{ID: "mgr"}, Approve: true, Reason: "within policy", At: at})
	printState(ctx, eng, "EXP-1001")

	// Step 2: the CFO approves directly; the deputy approves for the absent lead.
	lead := assent.Actor{ID: "lead"}
	decide(ctx, eng, "EXP-1001", assent.Decision{Actor: assent.Actor{ID: "cfo"}, Approve: true, Reason: "budget ok", At: at})
	decide(ctx, eng, "EXP-1001", assent.Decision{Actor: assent.Actor{ID: "deputy"}, OnBehalfOf: &lead, Approve: true, Reason: "covering for lead", At: at})
	printState(ctx, eng, "EXP-1001")

	fmt.Println("audit trail:")
	trail, err := eng.Trail(ctx, "EXP-1001")
	if err != nil {
		panic(err)
	}
	for _, en := range trail {
		line := fmt.Sprintf("  %02d %-10s step %d  by %-8s", en.Seq, en.Type, en.Step, en.Actor.ID)
		if en.Authority != nil {
			line += fmt.Sprintf(" for %s", en.Authority.ID)
		}
		if en.Reason != "" {
			line += fmt.Sprintf("  (%s)", en.Reason)
		}
		fmt.Println(line)
	}
}

// escalationScenario shows a step whose SLA breaches: a background poll adds a
// fallback approver. assent owns no timer, so the caller drives the clock — here
// a compressed loop stands in for a cron poll.
func escalationScenario(ctx context.Context) {
	eng, err := assent.New(assent.NewMemStore(), chronicle.NewLog(chronicle.NewMemStore()))
	if err != nil {
		panic(err)
	}
	sched := sundial.Schedule{
		Loc:  time.UTC,
		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
	}
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC) // Monday 09:00
	req := assent.Request{
		ID:    "REQ-SLA",
		Maker: assent.Actor{ID: "alice"},
		Chain: []assent.Step{{
			Name:      "review",
			Approvers: []assent.Assignment{{User: "reviewer"}},
			Rule:      assent.Unanimous(),
			Escalation: &assent.Escalation{
				Schedule: sched,
				Budget:   8 * time.Hour,
				Levels:   []sundial.Level{{Name: "warn", At: 4 * time.Hour}},
				Action:   assent.EscalateUp,
				Fallback: assent.Assignment{User: "senior-reviewer"},
			},
		}},
	}
	if err := eng.Submit(ctx, req, start); err != nil {
		panic(err)
	}
	fmt.Printf("submitted %s with an 8 working-hour SLA (due %s)\n", req.ID,
		sched.Add(start, 8*time.Hour).Format("Mon 15:04"))

	// Poll the SLA clock as a cron would: step the simulated now forward and
	// tick. The warn fires at 4h and the breach escalates at 8h.
	warned, escalated := false, false
	for sim := start; sim.Before(start.Add(72 * time.Hour)); sim = sim.Add(30 * time.Minute) {
		if err := eng.Tick(ctx, "REQ-SLA", sim); err != nil {
			panic(err)
		}
		trail, err := eng.Trail(ctx, "REQ-SLA")
		if err != nil {
			panic(err)
		}
		for _, en := range trail {
			if en.Type == assent.EventWarned && !warned {
				warned = true
				fmt.Printf("%s  SLA warn fired\n", sim.Format("Mon 15:04"))
			}
			if en.Type == assent.EventEscalated && !escalated {
				escalated = true
				fmt.Printf("%s  SLA breached: fallback approver added\n", sim.Format("Mon 15:04"))
			}
		}
		if escalated {
			break
		}
	}
	snap, err := eng.Snapshot(ctx, "REQ-SLA")
	if err != nil {
		panic(err)
	}
	fmt.Printf("step now has %d approvers: %v\n", len(snap.Chain[0].Approvers), approverIDs(snap.Chain[0].Approvers))
}

func decide(ctx context.Context, eng *assent.Engine, id string, d assent.Decision) {
	if err := eng.Decide(ctx, id, d); err != nil {
		panic(err)
	}
}

func printState(ctx context.Context, eng *assent.Engine, id string) {
	st, err := eng.State(ctx, id)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  %s is now %s\n", id, st)
}

func approverIDs(as []assent.Assignment) []string {
	out := make([]string, len(as))
	for i, a := range as {
		if a.User != "" {
			out[i] = a.User
		} else {
			out[i] = "group:" + a.Group
		}
	}
	return out
}
