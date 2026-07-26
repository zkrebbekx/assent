package assent

import (
	"context"
	"testing"
	"time"

	"github.com/zkrebbekx/chronicle"
	"github.com/zkrebbekx/sundial"
)

// newEngine builds an engine over fresh in-memory stores, plus the chronicle log
// so tests can inspect the append-only trail directly.
func newEngine(t *testing.T, opts ...Option) (*Engine, *chronicle.Log) {
	t.Helper()
	log := chronicle.NewLog(chronicle.NewMemStore())
	eng, err := New(NewMemStore(), log, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng, log
}

// ctx is a shared background context for the tests.
func ctx() context.Context { return context.Background() }

// newBenchLog returns a fresh in-memory chronicle log for the benchmarks.
func newBenchLog() *chronicle.Log { return chronicle.NewLog(chronicle.NewMemStore()) }

// A fixed base week: 2026-01-05 is a Monday, 2026-01-09 a Friday.
var (
	mon = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	tue = time.Date(2026, 1, 6, 9, 0, 0, 0, time.UTC)
	wed = time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)
	fri = time.Date(2026, 1, 9, 9, 0, 0, 0, time.UTC)
	sat = time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
)

// user builds an actor from an ID.
func user(id string) Actor { return Actor{ID: id} }

// approver builds a named-user assignment.
func approver(id string) Assignment { return Assignment{User: id} }

// approvers builds a slice of named-user assignments.
func approvers(ids ...string) []Assignment {
	out := make([]Assignment, len(ids))
	for i, id := range ids {
		out[i] = approver(id)
	}
	return out
}

// approve records an approving decision by id at instant at, failing on error.
func mustApprove(t *testing.T, e *Engine, reqID, id, reason string, at time.Time) {
	t.Helper()
	if err := e.Decide(ctx(), reqID, Decision{Actor: user(id), Approve: true, Reason: reason, At: at}); err != nil {
		t.Fatalf("Decide approve %s: %v", id, err)
	}
}

// mustState asserts the request's lifecycle state.
func mustState(t *testing.T, e *Engine, reqID string, want State) {
	t.Helper()
	got, err := e.State(ctx(), reqID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
}

// mustSubmit submits a single-step request with the given rule and approvers.
func mustSubmit(t *testing.T, e *Engine, reqID string, maker Actor, rule Rule, ids ...string) {
	t.Helper()
	req := Request{
		ID:    reqID,
		Maker: maker,
		Chain: []Step{{Approvers: approvers(ids...), Rule: rule}},
	}
	if err := e.Submit(ctx(), req, mon); err != nil {
		t.Fatalf("Submit: %v", err)
	}
}

// stdSchedule is the Mon-Fri 09:00-17:00 UTC business schedule for SLA tests.
func stdSchedule() sundial.Schedule {
	return sundial.Schedule{
		Loc:  time.UTC,
		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
	}
}

// snap loads a request snapshot, failing on error.
func snap(t *testing.T, e *Engine, reqID string) RequestSnapshot {
	t.Helper()
	s, err := e.Snapshot(ctx(), reqID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return s
}

// trail loads a request's audit trail, failing on error.
func trail(t *testing.T, e *Engine, reqID string) []AuditEntry {
	t.Helper()
	tr, err := e.Trail(ctx(), reqID)
	if err != nil {
		t.Fatalf("Trail: %v", err)
	}
	return tr
}

// countType counts trail entries of a given event type.
func countType(tr []AuditEntry, typ string) int {
	n := 0
	for _, en := range tr {
		if en.Type == typ {
			n++
		}
	}
	return n
}
