package assent

import (
	"errors"
	"testing"
	"time"
)

func TestDelegationResolve(t *testing.T) {
	start := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
	base := Delegation{From: user("bob"), To: user("carol"), Window: Interval{Start: start, End: end}, Series: []string{"expense", "travel"}}

	t.Run("Given a delegation bob->carol for [expense,travel] over the week", func(t *testing.T) {
		d := NewDelegations(base)
		cases := []struct {
			name           string
			from, to       Actor
			series         string
			at             time.Time
			wantResolvable bool
		}{
			{"matching party, series and window", user("bob"), user("carol"), "expense", start.Add(48 * time.Hour), true},
			{"start bound is inclusive", user("bob"), user("carol"), "expense", start, true},
			{"end bound is exclusive", user("bob"), user("carol"), "expense", end, false},
			{"before the window", user("bob"), user("carol"), "expense", start.Add(-time.Hour), false},
			{"series out of scope", user("bob"), user("carol"), "hr", start.Add(time.Hour), false},
			{"wrong authority", user("dave"), user("carol"), "expense", start.Add(time.Hour), false},
			{"wrong delegate", user("bob"), user("erin"), "expense", start.Add(time.Hour), false},
		}
		for _, tc := range cases {
			t.Run("Then "+tc.name, func(t *testing.T) {
				if got := d.Resolve(tc.from, tc.to, tc.series, tc.at); got != tc.wantResolvable {
					t.Fatalf("Resolve = %v, want %v", got, tc.wantResolvable)
				}
			})
		}
	})

	t.Run("Given a delegation with an empty series scope", func(t *testing.T) {
		d := NewDelegations(Delegation{From: user("bob"), To: user("carol"), Window: Interval{Start: start, End: end}})
		t.Run("Then it covers every series", func(t *testing.T) {
			if !d.Resolve(user("bob"), user("carol"), "anything", start.Add(time.Hour)) {
				t.Fatal("empty scope should match all series")
			}
		})
	})

	t.Run("Given a grant added after construction", func(t *testing.T) {
		d := NewDelegations()
		if d.Resolve(user("bob"), user("carol"), "expense", start.Add(time.Hour)) {
			t.Fatal("empty resolver should grant nothing")
		}
		d.Add(base)
		t.Run("Then the added grant resolves", func(t *testing.T) {
			if !d.Resolve(user("bob"), user("carol"), "expense", start.Add(time.Hour)) {
				t.Fatal("added grant should resolve")
			}
		})
	})

	t.Run("Given the default no-op resolver", func(t *testing.T) {
		t.Run("Then it grants nothing", func(t *testing.T) {
			if (noDelegations{}).Resolve(user("bob"), user("carol"), "expense", start) {
				t.Fatal("noDelegations should grant nothing")
			}
		})
	})
}

// TestDelegationFourEyesHolds proves the maker cannot approve their own request
// through delegation, in either direction. Four-eyes must bar the physical
// actor, not only the counted authority.
func TestDelegationFourEyesHolds(t *testing.T) {
	window := Interval{Start: mon.Add(-9 * time.Hour), End: fri.Add(-9 * time.Hour)} // [Mon 00:00, Fri 00:00)

	t.Run("Given a delegation TO the maker (bob->alice) and alice is the maker", func(t *testing.T) {
		bob := user("bob")
		del := NewDelegations(Delegation{From: bob, To: user("alice"), Window: window})
		e, _ := newEngine(t, WithDelegations(del))
		req := Request{ID: "fe1", Maker: user("alice"), Chain: []Step{{Approvers: approvers("bob"), Rule: Unanimous()}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When the maker approves on behalf of bob", func(t *testing.T) {
			err := e.Decide(ctx(), "fe1", Decision{Actor: user("alice"), OnBehalfOf: &bob, Approve: true, Reason: "self via bob", At: wed})
			t.Run("Then it errors: the maker cannot be a checker", func(t *testing.T) {
				if !errors.Is(err, ErrMakerChecker) {
					t.Fatalf("err = %v, want ErrMakerChecker", err)
				}
				mustState(t, e, "fe1", Pending)
			})
		})
	})

	t.Run("Given a delegation FROM the maker (alice->bob) and alice is the maker", func(t *testing.T) {
		alice := user("alice")
		del := NewDelegations(Delegation{From: alice, To: user("bob"), Window: window})
		e, _ := newEngine(t, WithDelegations(del))
		req := Request{ID: "fe2", Maker: user("alice"), Chain: []Step{{Approvers: approvers("alice"), Rule: Unanimous()}}}
		if err := e.Submit(ctx(), req, mon); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t.Run("When bob approves on behalf of the maker", func(t *testing.T) {
			err := e.Decide(ctx(), "fe2", Decision{Actor: user("bob"), OnBehalfOf: &alice, Approve: true, Reason: "as alice", At: wed})
			t.Run("Then it errors: nobody may decide as the maker", func(t *testing.T) {
				if !errors.Is(err, ErrMakerChecker) {
					t.Fatalf("err = %v, want ErrMakerChecker", err)
				}
				mustState(t, e, "fe2", Pending)
			})
		})
	})
}
