package assent

import (
	"context"
	"errors"
	"testing"
)

func TestMemStore(t *testing.T) {
	base := RequestSnapshot{
		ID: "s1", Maker: user("m"), State: Pending,
		Chain: []StepState{{Approvers: approvers("a"), Rule: Anyone()}},
	}
	t.Run("Given an empty store", func(t *testing.T) {
		s := NewMemStore()
		t.Run("Then Get of a missing ID is ErrNotFound", func(t *testing.T) {
			if _, err := s.Get(ctx(), "s1"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
		t.Run("Then Update of a missing ID is ErrNotFound", func(t *testing.T) {
			err := s.Update(ctx(), "s1", func(r RequestSnapshot) (RequestSnapshot, error) { return r, nil })
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
		t.Run("When the snapshot is created", func(t *testing.T) {
			if err := s.Create(ctx(), base); err != nil {
				t.Fatalf("Create: %v", err)
			}
			t.Run("Then a duplicate Create is ErrExists", func(t *testing.T) {
				if err := s.Create(ctx(), base); !errors.Is(err, ErrExists) {
					t.Fatalf("err = %v, want ErrExists", err)
				}
			})
		})
	})

	t.Run("Given a stored snapshot", func(t *testing.T) {
		s := NewMemStore()
		if err := s.Create(ctx(), base); err != nil {
			t.Fatalf("Create: %v", err)
		}
		t.Run("When a caller mutates a snapshot it read", func(t *testing.T) {
			got, _ := s.Get(ctx(), "s1")
			got.Chain[0].Approvers[0] = approver("evil")
			got.Chain[0].Decisions = append(got.Chain[0].Decisions, recordedDecision{Actor: user("evil")})
			t.Run("Then the store's own state is untouched", func(t *testing.T) {
				fresh, _ := s.Get(ctx(), "s1")
				if fresh.Chain[0].Approvers[0] != approver("a") {
					t.Fatalf("store approver leaked: %v", fresh.Chain[0].Approvers[0])
				}
				if len(fresh.Chain[0].Decisions) != 0 {
					t.Fatalf("store decisions leaked: %v", fresh.Chain[0].Decisions)
				}
			})
		})
		t.Run("When an update returns an error", func(t *testing.T) {
			sentinel := errors.New("abort")
			err := s.Update(ctx(), "s1", func(r RequestSnapshot) (RequestSnapshot, error) {
				r.State = Approved
				return r, sentinel
			})
			t.Run("Then the error propagates and nothing is persisted", func(t *testing.T) {
				if !errors.Is(err, sentinel) {
					t.Fatalf("err = %v, want abort", err)
				}
				fresh, _ := s.Get(ctx(), "s1")
				if fresh.State != Pending {
					t.Fatalf("state = %q, want pending (update rolled back)", fresh.State)
				}
			})
		})
	})

	t.Run("Given a cancelled context", func(t *testing.T) {
		s := NewMemStore()
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Create(cancelled, base); !errors.Is(err, context.Canceled) {
			t.Fatalf("Create err = %v, want context.Canceled", err)
		}
		if _, err := s.Get(cancelled, "s1"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Get err = %v, want context.Canceled", err)
		}
		if err := s.Update(cancelled, "s1", func(r RequestSnapshot) (RequestSnapshot, error) { return r, nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("Update err = %v, want context.Canceled", err)
		}
	})
}
