# assent — design

The human **approval + escalation** layer that durable-workflow engines leave
out. Declarative approval chains — sequential, parallel, quorum M-of-N, and
unanimous — with delegation, escalation on SLA breach, four-eyes / maker-checker,
and an auditable "who approved what, when, and as whom" trail. An embeddable,
storage-agnostic core, not a BPM platform.

Status: design. This document is the requirements sketch, fed from an in-depth
market evaluation (see "Evidence base"). Phase 1 builds the core below.

## Why this exists

A durable-execution engine runs your workflow to completion across restarts. It
does not decide **who** may approve a step, **how many** of them must, what
happens **when nobody acts in time**, or whether the person who raised the
request is barred from approving it. Those are human-approval concerns, and the
engines omit them by design.

The market evaluation confirmed the gap. No maintained, embeddable Go library
models human approval chains, maker-checker, or escalation. A live GitHub search
for `approval chain language:Go` returned eight repositories, all blockchain,
crypto, or agent-gateway projects at zero or one star, and the one repository
that called itself a "Maker Checker library" was a mislabeled, abandoned
integration for large language models. The durable-execution engines that assent
would sit beside — `cschleiden/go-workflows` and Temporal's Go SDK — ship durable
**execution** only. Temporal exposes three generic message-passing primitives
(Query, Signal, Update), and its only approval example is one a developer writes
by hand. The human-approval layer is absent on purpose. assent is that layer.

## What assent owns, and what it composes

assent does not reimplement machinery it already ships as focused siblings. It
**owns** the approval logic and **composes** three zero-dependency libraries for
the rest.

assent **owns**:
- **Chain advancement.** Does this decision complete the current step? Does step
  completion approve the request or move to the next step? Does a rejection fail
  the request?
- **The quorum math.** Count the distinct approving actors against the step's
  rule (anyone, M-of-N, unanimous). The same actor counts once.
- **Delegation resolution.** Given a decision that an actor makes on behalf of
  another, is a valid delegation in force for this step at this time?
- **Segregation of duties.** The maker cannot be a checker. A pluggable
  predicate enforces four-eyes and wider separation rules.
- **Idempotency.** A decision is write-once per (request, step, actor), so two
  approvers who act at the same instant never double-count.

assent **composes**:

| Concern | Library | Boundary |
|---|---|---|
| Request lifecycle | `fsmx` | assent builds a typed state machine over the request states and events. Every state change goes through `Fire`, so an illegal transition (approve a withdrawn request) is impossible. |
| Per-step SLA + escalation | `sundial` | Each active step starts a `sundial.Timer` with the step's business schedule, budget, and levels. assent polls `Fired(now)`, learns which levels crossed, and applies the escalation action. sundial owns the working-time math and the write-once firing; assent owns the action. |
| The audit trail | `chronicle` | Every decision, delegation use, reassignment, escalation, and state change is a `Put` on a bitemporal log — append-only, actor recorded, never rewritten. This is the compliance boundary. |

assent depends only on these three first-party siblings. Each is
zero-dependency, so assent's third-party dependency set is empty. assent is
low-dependency by composition, not zero-dependency.

## The model

An approval **request** moves through a **chain** of ordered **steps**. Steps
run one after another. Within a step, the approvers act at the same time, and the
step completes by its **rule**. When the last step approves, the request is
approved. A rejection fails the request (the safe v0.1 default).

```go
type Request struct {
    ID      string
    Maker   Actor       // who raised it; barred from approving (four-eyes)
    Subject any         // an opaque reference the caller owns
    Chain   []Step
    // current step index and state live inside; fsmx guards the state
}

type Step struct {
    Approvers  []Assignment  // named users and/or candidate groups
    Rule       Rule          // Anyone | Quorum(n) | Unanimous
    Escalation *Escalation    // optional: a sundial budget + levels + action
}

type Rule struct { /* Kind: Anyone, Quorum; N int; (Unanimous = all named) */ }

type Assignment struct { User string; Group string }  // one of the two

type Decision struct {
    Actor      Actor       // who really acted
    OnBehalfOf *Actor      // the authority, when the actor is a delegate
    Approve    bool        // true = approve, false = reject
    Reason     string      // recorded; compliance-load-bearing
    At         time.Time
}

type Delegation struct {
    From, To   Actor        // authority From delegates to actor To
    Window     Interval     // [start, end): time-bounded
    Series     []string     // scope-limited; empty = all
}
```

### The request lifecycle (fsmx)

The states are `Draft`, `Pending`, `Approved`, `Rejected`, and `Withdrawn`. The
events are `Submit`, `Approve`, `Reject`, `Withdraw`, and `Escalate`. Step
advancement is an internal counter, not a state, because the step count is
dynamic.

- `Draft` — `Submit` → `Pending`
- `Pending` — `Approve` and the rule is met on the last step → `Approved`
- `Pending` — `Approve` and the rule is met on a non-last step → `Pending`
  (advance to the next step)
- `Pending` — `Reject` → `Rejected`
- `Pending` — `Withdraw` → `Withdrawn`
- `Pending` — `Escalate` → `Pending` (reassign or escalate up), or `Approved`
  (auto-approve on breach)

fsmx validates the table once at build and blocks every illegal transition. You
cannot approve a withdrawn request, and you cannot withdraw an approved one.

### Chain advancement and the quorum math (assent owns)

A step completes by its rule over the **distinct** approving actors, after
delegation resolution:

- **Anyone** — one approval completes the step (1 of N).
- **Quorum(n)** — n distinct approvals complete the step (M of N). This is the
  incumbent "complete at 60%" rule: three of five approvers.
- **Unanimous** — every named approver must approve. assent models this
  **explicitly**, as "all named approvers returned approve". It does not infer
  unanimity from "all instances finished" — an instance finishing is not an
  approval. (See "Evidence base": that inference was refuted.)

A rejection by any approver fails the request in v0.1. A reject that only fails
the step and re-routes is a later option.

### Delegation resolution (assent owns)

Delegation is the feature that in-house builds get wrong — the "approver on
vacation" gap. assent resolves it precisely:

- **Time-bounded.** The delegation holds only inside its window `[start, end)`.
- **Scope-limited.** The delegation applies only to the listed series, or to all
  when the list is empty.
- **Provenance-preserving.** The trail records the actor who acted **and** the
  authority they acted for. A quorum counts the decision once, under the
  authority.

A decision made on behalf of another, with no delegation in force for this step
at this time, is rejected.

### Segregation of duties (assent owns)

The maker cannot be a checker. By default, the actor who raised the request is
barred from every step. A pluggable predicate widens this — for example, no
actor may approve two distinct steps of the same request. This is the four-eyes
control that compliance requires (see "Evidence base").

### Escalation (sundial)

Each active step may carry an SLA. assent starts a `sundial.Timer` at step
entry with the step's business schedule, its working-time budget, and its
levels (warn at 50%, breach at 100%, and beyond). assent polls `Fired(now)` and
acts on each new firing exactly once:

- **Escalate up** — add a fallback approver to the step.
- **Reassign** — move the pending assignment to another approver.
- **Auto-approve** — complete the step on breach (for low-risk flows only).

sundial owns the working-time math and the write-once firing. assent owns the
action and records it on the trail.

### The audit trail (chronicle)

Every event is a `Put` on a bitemporal log: submit, each decision (with actor,
authority, and reason), each reassignment, each escalation firing, and each
state change. The log is append-only and never rewrites a prior belief. This
gives assent the compliance-load-bearing trail without owning storage.

### Storage

assent defines a `Store` boundary, as sundial, sequent, and chronicle do. The
caller binds it to their database and, for durability, to the same transaction
that persists the subject. A `Snapshot` / `Restore` pair serializes a request so
a restart resumes exactly.

## Golden test vectors (become the suite)

1. **Sequential chain.** Chain [A {assignee mgr, Unanimous}, B {assignee
   finance, Unanimous}]. Approve A → still `Pending`, current step B. Approve B →
   `Approved`. Approving B before A → an error: the step is not active.
2. **Parallel unanimous step.** One step, approvers {a, b, c}, Unanimous.
   Approve a, b → `Pending`. Approve c → the step completes. A reject by any →
   `Rejected`.
3. **Quorum M-of-N.** One step, approvers {a, b, c, d, e}, Quorum(3). Three
   distinct approvals complete the step. The fourth approval is a no-op. The step
   completes at exactly three, not before.
4. **Anyone.** approvers {a, b, c}, Anyone. The first approval completes the
   step.
5. **Four-eyes.** maker = alice. A step lists alice as an approver. alice's
   decision returns an error: the maker cannot be a checker.
6. **Delegation.** Delegation bob → carol, window [Mon, Fri), series
   ["expense"]. carol approves on behalf of bob on Wednesday for an expense
   request → the decision counts as bob's, and the trail records actor carol,
   authority bob. carol on behalf of bob on Saturday → an error: no delegation is
   in force.
7. **Idempotent decision.** Two goroutines record bob's approve for one
   (request, step). Exactly one decision is stored, the step advances once, and
   the quorum count rises by one. No double-count under `-race`.
8. **Escalation on breach.** A step with budget 8 working hours, warn at 4h,
   breach at 8h, action escalate-up. At 9 working hours, the fallback approver is
   added and the trail records the escalation once (sundial fires write-once).
9. **Withdraw mid-flight.** A `Pending` request at step B. The maker withdraws →
   `Withdrawn`. A later approve on B → an error: the request is not pending
   (fsmx blocks the transition).
10. **Audit completeness.** After a submit, approve, approve, approved run, the
    chronicle log holds, in order: submit(alice), approve(mgr, reason),
    approve(finance, reason), approved. Each entry has the actor identity, the
    reason, and the timestamp. None is rewritten.

## Scope

**In scope — Phase 1:**
- A sequential chain of steps.
- Per-step rules: Anyone, Quorum(M), Unanimous.
- Assignment to named users and candidate groups, with claim-to-own.
- A decision with recorded actor identity and reason.
- Four-eyes / segregation of duties, with a pluggable predicate.
- Idempotent decisions under concurrency.
- Delegation resolution: time-bounded, scope-limited, provenance-preserving.
- Recall / withdraw, and reassignment.
- Per-step escalation through sundial: escalate up, reassign, or auto-approve.
- The audit trail through chronicle.
- `Snapshot` / `Restore` for crash safety.

**Explicitly NOT in Phase 1 (later):**
- **Conditional and branching steps + a routing DSL** ("if the amount is over
  10k, add a step"). This is the highest-complexity feature and needs an
  expression evaluator.
- **Parallel chains** — several independent branches that join. Phase 1 is
  parallel within a step and sequential between steps.
- **A human-task inbox** ("what awaits me"). This is a read model, and it belongs
  to a later phase or a service tier.
- **Notification.** assent reports that a step is pending or escalated. Delivery
  is the caller's, or a sibling's (digestr).
- **A REST service tier** (`assentd`) and persistence adapters, as chronicled
  and tariffd add to their cores.

## Difficulty ranking (for the build order)

1. **Idempotent decisions under concurrency** — hardest. Two approvers act at
   the same instant; the decision must be write-once per (request, step, actor).
2. **Delegation resolution** — hard. Time-bounded, scope-limited, and
   provenance-preserving, and it must compose with the quorum count.
3. **The quorum math** — medium. Count distinct actors against the rule; get
   unanimous right as an explicit rule.
4. **Segregation of duties** — medium. The default maker-check plus a pluggable
   wider predicate.
5. **Escalation through sundial** — medium. Driven by sundial's firing; assent
   applies the action once.
6. **Snapshot / Restore** — medium. Serialize the request and the per-step
   timers.
7. **Chain structure, assignment, withdraw, reassignment** — easier, structural.

## Open questions

1. **Reject semantics.** Does a reject fail the whole request, or fail only the
   step and re-route? Phase 1 fails the request. Pin this in the build.
2. **Delegation scope.** The delegation model here follows the ServiceNow
   `sys_user_delegate` shape (a date-range delegation with delegatee-and-delegator
   provenance) from domain knowledge. The market evaluation did not pin the
   ServiceNow, Jira, SAP, or Pega delegation and escalation rules to primary
   quotes (that angle was starved). Validate the scope semantics against a
   primary source before you lock them.
3. **The Store transaction boundary.** How does a caller bind a decision write to
   the same transaction that persists the subject, and does assent expose the
   chronicle `Put` inside that transaction? Decide and document, as sequent does
   for its counter.
4. **Group assignment resolution.** When a step assigns to a group, does assent
   resolve the group's members, or does the caller? Phase 1 leans on the caller
   to resolve membership; assent holds the candidate group and records the actor
   who claims.

## Evidence base

The market evaluation verified these facts (each 3-0 on adversarial review
unless noted):

- **The gap is real.** No maintained, embeddable Go approval-chain library
  exists. Durable-execution engines omit the human layer by design.
- **The chain models come from the incumbents.** Camunda and Flowable model
  assignment with three primitives (single assignee, candidate users, candidate
  groups), parallel-versus-sequential execution with the `isSequential`
  attribute, and quorum / M-of-N early completion with a `completionCondition`
  re-evaluated after each instance ends (for example, complete at 60%).
- **Unanimous is explicit, not default.** The claim that a multi-instance task
  "completes when all instances finish = unanimous" was **refuted** 0-3. An
  instance finishing is not an approval. Model unanimous as "all named approvers
  approved".
- **The compliance features have a regulatory basis.**
  - **Recorded approver identity + reason.** 21 CFR §11.50 requires a signed
    electronic record to carry the signer's printed name, the timestamp, and the
    meaning of the signature (review, approval, responsibility, or authorship).
  - **Tamper-evident trail.** 21 CFR §11.10(e) requires a secure, time-stamped
    audit trail whose changes do not obscure prior records. SOX §404 requires
    retained evidential matter. Both map to chronicle's append-only log.
  - **Segregation of duties.** Basel Principle 6 requires appropriate segregation
    of duties so no individual holds conflicting responsibilities. Note: SOX
    §404 anchors the authorization objective, but four-eyes and SoD are
    framework-derived through COSO, not spelled out in the SEC rule.
- **Don't-build check.** No existing Go library was missed. A YC-backed SaaS
  sells human-in-the-loop approval as a hosted API — which shows the demand but
  is a service, not a Go library. The narrow-it warning is honored: assent is not
  a BPM platform. It is the chain core plus delegation, escalation, and audit,
  storage-agnostic.

## Phase 1 — as built

This section records the decisions the implementation made where this design
left a choice open. It is the authoritative note of what the code actually does.

### Open questions, resolved

1. **Reject semantics.** A reject fails the whole request. The first reject on an
   active step fires the `Reject` event and the request becomes `Rejected`. A
   reject that fails only the step and re-routes is deferred to a later phase.

2. **Delegation scope.** The delegation follows the ServiceNow `sys_user_delegate`
   shape: a half-open time window `[start, end)`, a series scope (empty means all
   series), and provenance that keeps both the delegate and the authority.
   `assent` resolves a delegation through a `DelegationResolver` interface;
   `Delegations` is the in-memory reference. The counted identity is the
   authority, so a delegated approval counts once, under the authority.

3. **The Store transaction boundary.** `Store.Update` carries the whole
   read-modify-write as a closure the store runs under its own lock. `assent`
   writes its `chronicle` audit entries inside that closure, so a decision and
   its audit entry are produced together. The in-memory store serializes the two
   under one mutex. A durable store runs the closure inside one database
   transaction and binds it to the transaction that persists the subject; the
   `chronicle` Put enrols in the same transaction. `assent` opens no transaction
   itself — durability stays in the Store, as it does in sequent.

4. **Group assignment resolution.** The caller resolves group membership.
   `assent` holds the candidate group as one approver slot and records the actor
   who claims it, through the optional `Decision.Group` field. `assent` does not
   verify membership; it trusts the caller to route only eligible actors. The
   "what awaits me" read model stays outside the core.

### Further decisions

5. **Unanimous, explicitly.** A unanimous step is complete when every named user
   slot has an approving decision by that user, and every candidate group slot has
   at least one approving claim naming it. `assent` never infers unanimity from a
   count of finished instances.

6. **The event split.** `fsmx` permits one target per (state, event) pair. A
   completing approval therefore fires one of two internal events: a mid-chain
   step fires `advance` and stays `Pending` on the next step, and the last step
   fires `approve` and becomes `Approved`. The design's single "approve" is that
   split. The external vocabulary — Submit, Approve, Reject, Withdraw, Escalate —
   is unchanged.

7. **Idempotency semantics.** The write-once key is (request, step, authority). A
   repeated decision by the same authority on the same step is a no-op that
   returns nil. An extra approval of an already-approved request, by an actor in
   the chain, is a harmless no-op. A decision on a rejected or withdrawn request
   returns `ErrNotPending` — the lifecycle machine has no such transition.

8. **Escalation trigger.** `assent` drives the escalation action off the timer's
   breach, which `sundial` reports write-once, and it applies the action once
   through its own `actioned` flag. Warn levels below the budget are recorded once
   each. So warn at 4h and breach at 8h produce one warn entry and one escalation
   entry, not two of either.

9. **Subject reference.** `Request.Subject` is an opaque string reference the
   caller owns, not an arbitrary object. `assent` stores the reference so the
   snapshot serializes cleanly; the caller keeps the object.

10. **Reason.** A decision reason is recorded but not enforced non-empty, matching
    `chronicle`, which makes the reason optional. It is compliance-load-bearing,
    so callers should supply it.

11. **Four-eyes holds under delegation.** The maker is barred as the physical
    actor of a decision, not only as the counted authority. `assent` rejects a
    decision when the acting person is the maker, before it resolves delegation.
    So the maker cannot approve their own request by acting as another approver's
    delegate, and no delegation can count a decision under the maker's own
    identity. Both directions return `ErrMakerChecker`. This closes a
    segregation-of-duties bypass: without the actor check, a maker who held a
    delegation from an approver could sign off their own request.

### Go version floor

`assent` composes `fsmx`, whose `go.mod` declares `go 1.24`. That makes 1.24
`assent`'s minimum: a `go 1.23` build cannot compile a `go 1.24` dependency
without a silent toolchain switch. The CI matrix is therefore 1.24 and 1.25.

### Not built in Phase 1

The scope list above holds. Conditional and branching steps with a routing DSL,
parallel chains that fork and join, a human-task inbox, notification delivery,
and a REST service tier are all deferred.
