package assent

import (
	"testing"
)

// BenchmarkDecideAdvance measures one approval that completes a step and
// advances the chain, the engine's hot path.
func BenchmarkDecideAdvance(b *testing.B) {
	e, err := New(NewMemStore(), newBenchLog())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; b.Loop(); i++ {
		id := "b" + itoa(i)
		req := Request{ID: id, Maker: user("m"), Chain: []Step{{Approvers: approvers("a"), Rule: Anyone()}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			b.Fatal(err)
		}
		if err := e.Decide(ctx(), id, Decision{Actor: user("a"), Approve: true, Reason: "ok", At: mon}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQuorum measures a three-of-five quorum reaching completion.
func BenchmarkQuorum(b *testing.B) {
	e, err := New(NewMemStore(), newBenchLog())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; b.Loop(); i++ {
		id := "q" + itoa(i)
		req := Request{ID: id, Maker: user("m"), Chain: []Step{{Approvers: approvers("a", "b", "c", "d", "e"), Rule: Quorum(3)}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			b.Fatal(err)
		}
		for _, who := range []string{"a", "b", "c"} {
			if err := e.Decide(ctx(), id, Decision{Actor: user(who), Approve: true, Reason: "ok", At: mon}); err != nil {
				b.Fatal(err)
			}
		}
	}
}
