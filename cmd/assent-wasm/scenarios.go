//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
)

// A scenario is a named story: a complete chain the page submits, plus which
// panel to focus and a one-line note on what to try. Because assent is stateful,
// a scenario is not just inputs — it is a whole request the page drives live.
// The page reads Spec, calls newSession with it, then focuses Focus and shows
// Note. The specs are pinned to the library's own examples, so a scenario
// reproduces a figure from the README exactly.
type scenario struct {
	Name  string      `json:"name"`
	Title string      `json:"title"`
	Blurb string      `json:"blurb"`
	Focus string      `json:"focus"`
	Note  string      `json:"note"`
	Spec  sessionSpec `json:"spec"`
}

func step(name string, rule ruleDTO, esc *escalationDTO, approvers ...assignmentDTO) stepDTO {
	return stepDTO{Name: name, Approvers: approvers, Rule: rule, Escalation: esc}
}

func user(id string) assignmentDTO  { return assignmentDTO{User: id} }
func group(id string) assignmentDTO { return assignmentDTO{Group: id} }

var (
	anyone    = ruleDTO{Kind: "anyone"}
	unanimous = ruleDTO{Kind: "unanimous"}
)

func quorum(n int) ruleDTO { return ruleDTO{Kind: "quorum", N: n} }

// scenarios are the loadable stories, keyed by name.
var scenarios = map[string]*scenario{
	"expense": {
		Name:  "expense",
		Title: "Expense: manager → finance",
		Blurb: "A two-desk sequential chain. Approve as the manager, watch it advance, then approve as finance to close it.",
		Focus: "drive",
		Note: "A $4,000 expense raised by alice. Step 1 needs the manager; step 2 needs finance. " +
			"Approve as mgr — the chain advances to finance and stays Pending — then approve as finance to reach Approved. " +
			"The badge is the real fsmx lifecycle machine.",
		Spec: sessionSpec{
			Maker:   actorDTO{ID: "alice", Name: "Alice (maker)"},
			Series:  "expense",
			Subject: "expense #4021 — $4,000",
			Steps: []stepDTO{
				step("Manager review", anyone, nil, user("mgr")),
				step("Finance sign-off", anyone, nil, user("finance")),
			},
		},
	},

	"board-vote": {
		Name:  "board-vote",
		Title: "Board vote: 3-of-5 quorum",
		Blurb: "One step, five directors, a quorum of three. Watch the counter fill 1/3 → 2/3 → 3/3 and approve.",
		Focus: "drive",
		Note: "A board resolution needing three of five directors. Approve as any three distinct directors " +
			"and the step completes; a fourth approval of the already-approved request is a harmless no-op. " +
			"assent counts distinct approving actors — the same director never counts twice.",
		Spec: sessionSpec{
			Maker:   actorDTO{ID: "chair", Name: "Chair (maker)"},
			Series:  "board",
			Subject: "resolution 2026-04 — new credit line",
			Steps: []stepDTO{
				step("Board vote", quorum(3), nil,
					user("dir-aldo"), user("dir-bea"), user("dir-cyd"), user("dir-dot"), user("dir-eve")),
			},
		},
	},

	"four-eyes": {
		Name:  "four-eyes",
		Title: "Four-eyes blocked",
		Blurb: "The maker cannot approve their own request — not directly, and not by borrowing an approver's authority.",
		Focus: "foureyes",
		Note: "alice raised this request, so she is barred from approving it. Click \"approve as the maker\" — " +
			"blocked with ErrMakerChecker. There is a delegation granting alice bob's authority; try approving as " +
			"alice on behalf of bob — still blocked. Four-eyes bars the physical person, not just the counted authority.",
		Spec: sessionSpec{
			Maker:   actorDTO{ID: "alice", Name: "Alice (maker)"},
			Series:  "expense",
			Subject: "expense #7788 — $12,500",
			Steps: []stepDTO{
				step("Approval", unanimous, nil, user("bob")),
			},
			Delegations: []delegationDTO{
				{From: actorDTO{ID: "bob", Name: "Bob"}, To: actorDTO{ID: "alice", Name: "Alice"}, Series: []string{"expense"}},
			},
		},
	},

	"delegation": {
		Name:  "delegation",
		Title: "Delegation while on vacation",
		Blurb: "Bob is away and has delegated to Carol for the week. Carol approves as Bob — the trail keeps both.",
		Focus: "delegation",
		Note: "Bob is on vacation Mon–Fri and delegated his expense authority to Carol. Approve on behalf of bob as " +
			"carol inside the window — it counts under bob, and the trail records \"carol, as bob\". Then try a decision " +
			"timed outside the window — rejected with ErrNoDelegation. The window is half-open [Mon, Fri).",
		Spec: sessionSpec{
			Maker:   actorDTO{ID: "alice", Name: "Alice (maker)"},
			Series:  "expense",
			Subject: "expense #5150 — $2,300",
			Steps: []stepDTO{
				step("Bob's approval", unanimous, nil, user("bob")),
			},
			Delegations: []delegationDTO{
				{
					From:   actorDTO{ID: "bob", Name: "Bob"},
					To:     actorDTO{ID: "carol", Name: "Carol"},
					Series: []string{"expense"},
					Start:  "2026-01-05T00:00:00Z",
					End:    "2026-01-09T00:00:00Z",
				},
			},
		},
	},

	"sla-breach": {
		Name:  "sla-breach",
		Title: "SLA breach escalates",
		Blurb: "Nobody acts. Tick the clock nine working hours: a warn fires at four, the budget breaches at eight, and a director is added.",
		Focus: "sla",
		Note: "The manager has an 8 working-hour SLA with a warn at 4h, over a Mon–Fri 09:00–17:00 calendar. Nobody acts. " +
			"Tick +9 working hours: sundial fires the warn at 4h, then breaches at 8h, and assent adds director as a " +
			"fallback approver (EscalateUp). Tick again — nothing more happens; the escalation is write-once. The " +
			"director can then approve to unblock the step.",
		Spec: sessionSpec{
			Maker:   actorDTO{ID: "alice", Name: "Alice (maker)"},
			Series:  "expense",
			Subject: "expense #9001 — $850",
			Steps: []stepDTO{
				step("Manager review", anyone, &escalationDTO{
					BudgetHours: 8,
					Levels:      []levelDTO{{Name: "warn", AtHours: 4}},
					Action:      "escalate_up",
					Fallback:    user("director"),
				}, user("mgr")),
			},
		},
	},
}

// order fixes the presentation order in the UI: the two simplest drives first,
// then the three that showcase one composed sibling each.
var scenarioOrder = []string{"expense", "board-vote", "four-eyes", "delegation", "sla-breach"}

func scenarioList() []map[string]any {
	out := make([]map[string]any, 0, len(scenarioOrder))
	for _, name := range scenarioOrder {
		sc, ok := scenarios[name]
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"name":  sc.Name,
			"title": sc.Title,
			"blurb": sc.Blurb,
			"focus": sc.Focus,
		})
	}
	return out
}

// handleLoadScenario returns a scenario's full story by name. The name may arrive
// as a bare string or as {"name": ...}.
func handleLoadScenario(in []byte) (any, error) {
	name := scenarioName(in)
	sc, ok := scenarios[name]
	if !ok {
		return nil, badArg("unknown_scenario", fmt.Sprintf("unknown scenario %q", name))
	}
	return sc, nil
}

// scenarioName extracts the requested name from a bare string or a {name}
// object, so loadScenario("expense") and loadScenario({name:"..."}) both work.
func scenarioName(in []byte) string {
	s := string(in)
	if s == "" || s == "{}" {
		return ""
	}
	if s[0] != '{' {
		return s
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(in, &obj); err == nil {
		return obj.Name
	}
	return ""
}
