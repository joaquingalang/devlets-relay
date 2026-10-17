package push

import "testing"

// SetExecutable swaps the program StartDetached spawns for one test.
func SetExecutable(t *testing.T, f func() (string, error)) {
	old := executable
	executable = f
	t.Cleanup(func() { executable = old })
}
