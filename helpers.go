package assent

import (
	"fmt"
	"strconv"

	"github.com/zkrebbekx/sundial"
)

// escConfig builds the sundial config for a step's escalation.
func escConfig(esc *Escalation) sundial.Config {
	return sundial.Config{
		Schedule: esc.Schedule,
		Budget:   esc.Budget,
		Levels:   esc.Levels,
	}
}

// wrapEscalation reports a sundial config or snapshot failure as an assent
// escalation error, keeping both [ErrBadEscalation] and the underlying sundial
// sentinel matchable with errors.Is.
func wrapEscalation(err error) error {
	return fmt.Errorf("%w: %w", ErrBadEscalation, err)
}

// levelIndex returns the position of the fired level within levels, matched by
// its threshold and name, or -1.
func levelIndex(levels []sundial.Level, f sundial.Firing) int {
	for i, l := range levels {
		if l.At == f.Level.At && l.Name == f.Level.Name {
			return i
		}
	}
	return -1
}

// containsInt reports whether xs holds x.
func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// itoa renders an int for an audit payload's extra map.
func itoa(n int) string { return strconv.Itoa(n) }
