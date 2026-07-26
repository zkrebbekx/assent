package assent

import (
	"sync"
	"time"
)

// DelegationResolver answers one question: may the delegate To act on behalf of
// the authority From, for a request in series, at instant at? assent consults it
// during delegation resolution. It is an interface so a caller can back
// delegations with their own store; [Delegations] is the in-memory reference.
type DelegationResolver interface {
	// Resolve reports whether a delegation From -> To is in force for series at
	// at.
	Resolve(from, to Actor, series string, at time.Time) bool
}

// Delegations is an in-memory [DelegationResolver]: a set of grants a caller
// registers and assent scans. It is safe for concurrent use.
type Delegations struct {
	mu    sync.RWMutex
	items []Delegation
}

// NewDelegations returns a resolver seeded with the given grants.
func NewDelegations(grants ...Delegation) *Delegations {
	d := &Delegations{}
	d.items = append(d.items, grants...)
	return d
}

// Add registers a delegation grant.
func (d *Delegations) Add(g Delegation) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.items = append(d.items, g)
}

// Resolve reports whether a registered grant lets to act for from, for series,
// at at. A grant matches when the parties agree, at lies inside the grant's
// half-open window, and the grant's series list is empty (all series) or
// contains series.
func (d *Delegations) Resolve(from, to Actor, series string, at time.Time) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, g := range d.items {
		if g.From.ID != from.ID || g.To.ID != to.ID {
			continue
		}
		if !g.Window.Contains(at) {
			continue
		}
		if seriesInScope(g.Series, series) {
			return true
		}
	}
	return false
}

// seriesInScope reports whether series is covered by scope. An empty scope
// covers every series.
func seriesInScope(scope []string, series string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if s == series {
			return true
		}
	}
	return false
}

// noDelegations is the default resolver: it grants nothing, so a decision made
// on behalf of another is rejected unless the caller configures delegations.
type noDelegations struct{}

func (noDelegations) Resolve(_, _ Actor, _ string, _ time.Time) bool { return false }
