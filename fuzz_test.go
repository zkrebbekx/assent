package assent

import (
	"testing"
	"time"
)

// FuzzQuorum drives a single quorum step with an arbitrary approver count,
// threshold, and sequence of approvals, and checks the quorum invariants hold:
// the step completes exactly when the distinct approvals reach the threshold,
// duplicates never over-count, and the recorded decisions never exceed the
// threshold.
func FuzzQuorum(f *testing.F) {
	f.Add([]byte{3, 2, 0, 1, 1, 2})
	f.Add([]byte{5, 3, 0, 0, 0})
	f.Add([]byte{1, 1, 0})
	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) < 2 {
			return
		}
		m := int(in[0]%8) + 1 // 1..8 approvers
		n := int(in[1]%uint8(m)) + 1
		ids := make([]string, m)
		for i := range ids {
			ids[i] = string(rune('a' + i))
		}

		e, _ := newEngine(t)
		mustSubmit(t, e, "fz", user("maker"), Quorum(n), ids...)

		distinct := map[int]bool{}
		for _, b := range in[2:] {
			idx := int(b) % m
			distinct[idx] = true
			// Approvals after completion are harmless no-ops; ignore their error
			// only for the not-pending case, which is the intended terminal.
			err := e.Decide(ctx(), "fz", Decision{Actor: user(ids[idx]), Approve: true, Reason: "ok", At: mon})
			if err != nil {
				t.Fatalf("Decide %s: %v", ids[idx], err)
			}
		}

		s := snap(t, e, "fz")
		reached := len(distinct) >= n
		if reached && s.State != Approved {
			t.Fatalf("state = %q, want approved (distinct=%d, n=%d)", s.State, len(distinct), n)
		}
		if !reached && s.State != Pending {
			t.Fatalf("state = %q, want pending (distinct=%d, n=%d)", s.State, len(distinct), n)
		}
		if got := len(s.Chain[0].Decisions); got > n {
			t.Fatalf("recorded decisions = %d, exceeds quorum %d", got, n)
		}
		want := len(distinct)
		if want > n {
			want = n
		}
		if got := len(s.Chain[0].Decisions); got != want {
			t.Fatalf("recorded decisions = %d, want min(distinct,n) = %d", got, want)
		}
	})
}

// FuzzDelegationWindow checks that delegation resolution honours the half-open
// window and the series scope for any instants, independently recomputing the
// expected answer.
func FuzzDelegationWindow(f *testing.F) {
	f.Add(int64(0), int64(100), int64(50), true)
	f.Add(int64(0), int64(100), int64(100), false)
	f.Add(int64(10), int64(10), int64(10), true)
	f.Fuzz(func(t *testing.T, startNs, endNs, atNs int64, inScope bool) {
		start := time.Unix(0, startNs).UTC()
		end := time.Unix(0, endNs).UTC()
		at := time.Unix(0, atNs).UTC()

		scope := []string{"expense"}
		series := "expense"
		if !inScope {
			series = "hr"
		}
		d := NewDelegations(Delegation{From: user("bob"), To: user("carol"), Window: Interval{Start: start, End: end}, Series: scope})

		got := d.Resolve(user("bob"), user("carol"), series, at)
		want := !at.Before(start) && at.Before(end) && inScope
		if got != want {
			t.Fatalf("Resolve(start=%v end=%v at=%v inScope=%v) = %v, want %v", start, end, at, inScope, got, want)
		}
		// A different authority never resolves.
		if d.Resolve(user("dave"), user("carol"), series, at) {
			t.Fatal("resolution leaked across authorities")
		}
	})
}
