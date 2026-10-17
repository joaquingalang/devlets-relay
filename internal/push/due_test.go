package push

import (
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

var t0 = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
var reset = t0.Add(3 * time.Hour)

func reading(hooked []string, tools map[string][]contract.Window) contract.Reading {
	r := contract.Reading{V: 1, Hooked: hooked, Tools: map[string]contract.Tool{}}
	for k, w := range tools {
		r.Tools[k] = contract.Tool{Windows: w}
	}
	return r
}

func win(id string, pct float64, resets time.Time) contract.Window {
	return contract.Window{ID: id, UsedPercent: pct, ResetsAt: resets}
}

func TestDue(t *testing.T) {
	base := reading([]string{"claude"}, map[string][]contract.Window{"claude": {win("5h", 10, reset)}})
	last := store.Pushed{At: t0, Reading: base}
	mk := func(pct float64, resets time.Time) contract.Reading {
		return reading([]string{"claude"}, map[string][]contract.Window{"claude": {win("5h", pct, resets)}})
	}
	cases := []struct {
		name  string
		last  store.Pushed
		next  contract.Reading
		now   time.Time
		allow time.Time
		want  bool
	}{
		{"first push", store.Pushed{}, base, t0, time.Time{}, true},
		{"4 min, 5 point move", last, mk(15, reset), t0.Add(4 * time.Minute), time.Time{}, false},
		{"6 min, 0.5 point move", last, mk(10.5, reset), t0.Add(6 * time.Minute), time.Time{}, false},
		{"6 min, 1 point move", last, mk(11, reset), t0.Add(6 * time.Minute), time.Time{}, true},
		{"6 min, resetsAt changed", last, mk(10, reset.Add(time.Hour)), t0.Add(6 * time.Minute), time.Time{}, true},
		{"6 min, unchanged", last, mk(10, reset), t0.Add(6 * time.Minute), time.Time{}, false},
		{"6 min, new tool", last, reading([]string{"claude", "codex"}, map[string][]contract.Window{
			"claude": {win("5h", 10, reset)}, "codex": {win("5h", 1, reset)}}), t0.Add(6 * time.Minute), time.Time{}, true},
		{"2 min, new tool bypasses wait", last, reading([]string{"claude"}, map[string][]contract.Window{
			"claude": {win("5h", 10, reset)}, "codex": {win("5h", 1, reset)}}), t0.Add(2 * time.Minute), time.Time{}, true},
		{"2 min, new window bypasses wait", last, reading([]string{"claude"}, map[string][]contract.Window{
			"claude": {win("5h", 10, reset), win("7d", 1, reset)}}), t0.Add(2 * time.Minute), time.Time{}, true},
		{"2 min, hooked changed bypasses wait", last, reading([]string{"claude", "codex"}, map[string][]contract.Window{
			"claude": {win("5h", 10, reset)}}), t0.Add(2 * time.Minute), time.Time{}, true},
		{"within nextAllowed", last, reading([]string{"claude", "codex"}, map[string][]contract.Window{
			"claude": {win("5h", 10, reset)}, "codex": {win("5h", 1, reset)}}), t0.Add(2 * time.Minute), t0.Add(5 * time.Minute), false},
		{"first push within nextAllowed", store.Pushed{}, base, t0, t0.Add(time.Minute), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Due(c.last, c.next, c.now, c.allow); got != c.want {
				t.Fatalf("Due = %v, want %v", got, c.want)
			}
		})
	}
}
