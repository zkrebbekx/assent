// Package assent is the human approval and escalation layer that durable-workflow
// engines leave out. An approval request moves through a chain of sequential
// steps; each step gates on a rule — anyone, a quorum of M, or unanimous — over a
// set of approvers. assent owns the approval logic and composes three siblings
// for the rest.
//
// # The mental model
//
// A request is a document that needs sign-off. Its chain is the route the
// sign-off takes. The steps run one after another, like desks a form crosses.
// Within one step the approvers act at the same time, and the step completes by
// its rule. When the last step approves, the request is approved. A rejection
// fails the request. That is the whole shape: a request, an ordered chain, a
// rule per step, a decision per approver.
//
// assent answers the four questions a durable-execution engine does not: who may
// approve a step, how many of them must, what happens when nobody acts in time,
// and whether the person who raised the request is barred from approving it.
//
//	eng, _ := assent.New(assent.NewMemStore(), chronicle.NewLog(chronicle.NewMemStore()))
//	eng.Submit(ctx, assent.Request{
//		ID:    "req-1",
//		Maker: assent.Actor{ID: "alice"},
//		Chain: []assent.Step{{
//			Approvers: []assent.Assignment{{User: "mgr"}, {User: "finance"}},
//			Rule:      assent.Unanimous(),
//		}},
//	}, now)
//	eng.Decide(ctx, "req-1", assent.Decision{Actor: assent.Actor{ID: "mgr"}, Approve: true, Reason: "ok", At: now})
//	eng.Decide(ctx, "req-1", assent.Decision{Actor: assent.Actor{ID: "finance"}, Approve: true, Reason: "ok", At: now})
//	st, _ := eng.State(ctx, "req-1") // Approved
//
// # What assent owns
//
// assent owns the parts an in-house build gets wrong:
//
//   - Chain advancement. Does this decision complete the current step? Does the
//     step completing approve the request or move to the next step?
//   - The quorum math. Count the distinct approving actors against the rule. The
//     same actor counts once. Unanimous is explicit — every named approver must
//     approve, not "all instances finished".
//   - Delegation resolution. A decision made on behalf of another holds only
//     when a delegation is in force for this series at this time.
//   - Segregation of duties. The maker cannot be a checker. A pluggable
//     predicate widens the rule.
//   - Idempotency. A decision is write-once per request, step and actor, so two
//     approvers who act at the same instant never double-count.
//
// # What assent composes
//
// assent depends only on three first-party siblings, each zero-dependency, so
// its own third-party dependency set is empty:
//
//   - github.com/zkrebbekx/fsmx builds the request lifecycle machine. The states
//     are Draft, Pending, Approved, Rejected and Withdrawn. Every state change
//     goes through the machine, so an illegal transition — approving a withdrawn
//     request — is impossible.
//   - github.com/zkrebbekx/sundial runs each step's SLA. assent starts a timer at
//     step entry, polls it on [Engine.Tick], and applies the escalation action
//     once when the budget breaches. sundial owns the working-time math and the
//     write-once firing; assent owns the action.
//   - github.com/zkrebbekx/chronicle holds the audit trail. Every submit,
//     decision, delegation use, escalation and state change is an append-only
//     entry that records the actor, the authority and the reason, and is never
//     rewritten. This is the compliance boundary.
//
// # Chain advancement and the quorum math
//
// A step completes by its rule over the distinct approving actors, after
// delegation resolution. [Anyone] completes on the first approval. [Quorum]
// completes on N distinct approvals. [Unanimous] completes when every named
// approver has approved. A rejection by any approver fails the request. Because
// [fsmx] permits one target per state and event, assent splits a completing
// approval into two internal events: a mid-chain step fires "advance" and stays
// Pending on the next step, and the last step fires "approve" and becomes
// Approved.
//
// # Delegation
//
// Delegation is the "approver on vacation" gap. assent resolves it precisely. A
// delegation is time-bounded — it holds only inside its half-open window. It is
// scope-limited — it applies to the listed request series, or to all when the
// list is empty. It is provenance-preserving — the trail records the delegate
// who acted and the authority they acted for, and a quorum counts the decision
// once, under the authority. A decision made on behalf of another with no
// delegation in force is rejected.
//
// # Segregation of duties
//
// By default the actor who raised the request is barred from approving any step.
// A pluggable [SoD] predicate widens this — for example [SeparateSteps] bars an
// actor from approving two steps of the same request. This is the four-eyes
// control that compliance requires.
//
// # Group assignment
//
// A step assigns to named users and to candidate groups. assent holds the
// candidate group and records the actor who claims it; it does not resolve group
// membership. The caller resolves membership and routes only eligible actors,
// and assent records the claim on the trail. This keeps the read model — "what
// awaits me" — outside the core.
//
// # Storage and the transaction boundary
//
// assent defines a [Store] boundary and ships [MemStore] as the reference. The
// read-modify-write of a decision is indivisible: [Store.Update] carries the
// whole change as a closure the store runs under its own lock, and assent writes
// the audit entries inside that closure, so a decision and its audit trail
// commit together. A durable store binds that closure to the transaction that
// persists the subject. [Engine.Snapshot] and [Engine.Restore] serialize a
// request — including its per-step SLA timers — so a restart resumes exactly,
// with escalation still write-once.
//
// # Not in scope
//
// Phase 1 is the chain core. It does not include conditional or branching steps
// and a routing DSL, parallel chains that fork and join, a human-task inbox,
// notification delivery, or a REST service tier. assent reports that a step is
// pending or escalated; delivering that is the caller's. See docs/DESIGN.md for
// the design record and the decisions this implementation made where the design
// left a choice open.
package assent
