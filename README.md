# assent

The human **approval and escalation** layer that durable-workflow engines leave
out. Declarative approval chains — sequential steps, each gated by a rule
(anyone, a quorum of M, or unanimous) — with delegation, SLA escalation,
four-eyes, and an auditable "who approved what, when, and as whom" trail.

A durable-execution engine runs your workflow to completion across restarts. It
does not decide **who** may approve a step, **how many** of them must, what
happens **when nobody acts in time**, or whether the person who raised the
request is barred from approving it. Those are human-approval concerns, and the
engines omit them by design. `assent` is that layer. It is an embeddable,
storage-agnostic core, not a BPM platform.

`assent` **owns** the approval logic and **composes** three first-party siblings
for the rest, so it carries no third-party dependencies:

- **`fsmx`** builds the request lifecycle machine. Every state change goes
  through the machine, so an illegal transition — approving a withdrawn request
  — is impossible.
- **`sundial`** runs each step's SLA. `assent` starts a timer at step entry,
  polls it, and applies the escalation action once when the budget breaches.
- **`chronicle`** holds the audit trail. Every event is an append-only entry
  that records the actor, the authority and the reason, and is never rewritten.

```go
import "github.com/zkrebbekx/assent"
```

## What assent owns

- **Chain advancement.** Does this decision complete the current step? Does the
  step completing approve the request or move to the next step?
- **The quorum math.** Count the distinct approving actors against the rule. The
  same actor counts once. Unanimous is explicit — every named approver must
  approve, not "all instances finished".
- **Delegation resolution.** A decision made on behalf of another holds only when
  a delegation is in force for this series at this time.
- **Segregation of duties.** The maker cannot be a checker. A pluggable predicate
  widens the rule.
- **Idempotency.** A decision is write-once per request, step and actor, so two
  approvers who act at the same instant never double-count.

## Example

```go
eng, _ := assent.New(assent.NewMemStore(), chronicle.NewLog(chronicle.NewMemStore()))

eng.Submit(ctx, assent.Request{
    ID:    "exp-42",
    Maker: assent.Actor{ID: "alice"},
    Chain: []assent.Step{{
        Approvers: []assent.Assignment{{User: "mgr"}, {User: "finance"}},
        Rule:      assent.Unanimous(),
    }},
}, now)

eng.Decide(ctx, "exp-42", assent.Decision{Actor: assent.Actor{ID: "mgr"}, Approve: true, Reason: "reviewed", At: now})
eng.Decide(ctx, "exp-42", assent.Decision{Actor: assent.Actor{ID: "finance"}, Approve: true, Reason: "funded", At: now})

state, _ := eng.State(ctx, "exp-42") // Approved
```

## The model

A **request** moves through a **chain** of ordered **steps**. The steps run one
after another. Within a step, the approvers act at the same time, and the step
completes by its **rule**. When the last step approves, the request is approved.
A rejection fails the request.

### Rules

```go
assent.Anyone()     // the first approval completes the step (1 of N)
assent.Quorum(3)    // three distinct approvals complete the step (M of N)
assent.Unanimous()  // every named approver must approve
```

`assent` counts the **distinct** approving actors after delegation resolution.
Unanimous is modelled explicitly, as "all named approvers approved" — an approver
who does nothing never completes a unanimous step.

### The lifecycle

The states are `Draft`, `Pending`, `Approved`, `Rejected` and `Withdrawn`. The
transition table is declared once, in an `fsmx` machine, and validated at
construction. You cannot approve a withdrawn request, and you cannot withdraw an
approved one — the machine blocks it, rather than a runtime check.

### Delegation

Delegation is the "approver on vacation" gap. `assent` resolves it precisely:

- **Time-bounded.** The delegation holds only inside its half-open window
  `[start, end)`.
- **Scope-limited.** It applies to the listed request series, or to all when the
  list is empty.
- **Provenance-preserving.** The trail records the delegate who acted **and** the
  authority they acted for. A quorum counts the decision once, under the
  authority.

```go
del := assent.NewDelegations(assent.Delegation{
    From: assent.Actor{ID: "lead"}, To: assent.Actor{ID: "deputy"},
    Window: assent.Interval{Start: mon, End: fri}, Series: []string{"expense"},
})
eng, _ := assent.New(store, log, assent.WithDelegations(del))
// deputy acts for lead on an expense request inside the window; the trail keeps both.
```

### Segregation of duties

By default the maker of a request is barred from approving any step. A pluggable
predicate widens this — `SeparateSteps` bars an actor from approving two steps of
the same request:

```go
eng, _ := assent.New(store, log, assent.WithSoD(assent.SeparateSteps))
```

### Escalation

Each step may carry an SLA. `assent` starts a `sundial` timer at step entry.
Poll `Tick` as a cron would; on breach, `assent` applies the step's action once —
add a fallback approver, reassign the step, or auto-approve a low-risk step:

```go
Escalation: &assent.Escalation{
    Schedule: sched, Budget: 8 * time.Hour,
    Levels: []sundial.Level{{Name: "warn", At: 4 * time.Hour}},
    Action: assent.EscalateUp, Fallback: assent.Assignment{User: "director"},
}
```

`assent` owns no clock and no goroutine. The caller supplies every instant, so
the engine is deterministic and testable.

### The audit trail

Every event is an append-only entry on a `chronicle` log: the submit, each
decision (with actor, authority and reason), each escalation, and each state
change. `Trail` returns the ordered history:

```go
for _, en := range eng.Trail(ctx, "exp-42") {
    fmt.Println(en.Seq, en.Type, en.Actor.ID, en.Reason)
}
```

No entry is ever rewritten.

## Storage

`assent` defines a `Store` boundary and ships `MemStore` as the reference. The
read-modify-write of a decision is indivisible: `Store.Update` carries the whole
change as a closure the store runs under its own lock, and `assent` writes the
audit entries inside that closure, so a decision and its trail commit together. A
durable store binds that closure to the transaction that persists the subject.

`Snapshot` and `Restore` serialize a request — including its per-step SLA timers
— so a restart resumes exactly, with escalation still write-once.

## Compliance features and their basis

`assent` records the facts a regulated approval needs, and claims nothing more.

- **Recorded approver identity and reason.** Each decision records who acted, the
  authority, and the reason. **21 CFR §11.50** requires a signed electronic
  record to carry the signer's name, the timestamp, and the meaning of the
  signature.
- **Tamper-evident trail.** The `chronicle` log is append-only and never obscures
  a prior record. **21 CFR §11.10(e)** requires a secure, time-stamped audit
  trail whose changes do not obscure prior records; **SOX §404** requires
  retained evidential matter.
- **Segregation of duties.** The maker cannot be a checker, and a predicate
  widens the rule. **Basel Principle 6** requires appropriate segregation of
  duties so no individual holds conflicting responsibilities. (Four-eyes and SoD
  are framework-derived through COSO, not spelled out in the SEC rule.)

## Not in v0.1

- **Conditional and branching steps, and a routing DSL** ("if the amount is over
  10k, add a step"). This needs an expression evaluator.
- **Parallel chains** — several branches that fork and join. v0.1 is parallel
  within a step and sequential between steps.
- **A human-task inbox** ("what awaits me"). This is a read model.
- **Notification.** `assent` reports that a step is pending or escalated;
  delivery is the caller's.
- **A REST service tier and persistence adapters.**

## License

MIT
