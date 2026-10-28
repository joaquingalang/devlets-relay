//go:build !windows

package codex

// chainable is always true here: arguments reach the program as they are.
func chainable(string) bool { return true }
