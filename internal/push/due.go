// Package push decides when a reading is worth sending.
package push

import (
	"math"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

const (
	minGap  = 5 * time.Minute
	minMove = 1.0 // percentage points
)

// Due reports whether next should be pushed now (BR7/FR10). nextAllowed is
// State.NextAllowed (server rate limit / Retry-After); pass the zero time when
// there is none. Nothing is due before nextAllowed. Otherwise it is due when
// nothing was pushed yet, or a tool/window appeared or the hooked list changed
// (these skip the 5-minute wait), or at least 5 minutes have passed and a
// window's usedPercent moved by >= 1 point or its resetsAt changed.
func Due(last store.Pushed, next contract.Reading, now, nextAllowed time.Time) bool {
	if now.Before(nextAllowed) {
		return false
	}
	if last.At.IsZero() {
		return true
	}
	if !sameStrings(last.Reading.Hooked, next.Hooked) {
		return true
	}
	moved := false
	for name, tool := range next.Tools {
		old, ok := last.Reading.Tools[name]
		if !ok {
			return true
		}
		for _, w := range tool.Windows {
			ow, found := findWindow(old.Windows, w.ID)
			if !found {
				return true
			}
			if math.Abs(w.UsedPercent-ow.UsedPercent) >= minMove || !w.ResetsAt.Equal(ow.ResetsAt) {
				moved = true
			}
		}
	}
	return moved && now.Sub(last.At) >= minGap
}

func findWindow(ws []contract.Window, id string) (contract.Window, bool) {
	for _, w := range ws {
		if w.ID == id {
			return w, true
		}
	}
	return contract.Window{}, false
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
