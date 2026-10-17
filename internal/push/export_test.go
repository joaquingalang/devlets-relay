package push

import (
	"context"
	"testing"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

// SetExecutable swaps the program StartDetached spawns for one test.
func SetExecutable(t *testing.T, f func() (string, error)) {
	old := executable
	executable = f
	t.Cleanup(func() { executable = old })
}

// SetRefreshCodex swaps the Codex read for one test, so no real codex runs.
func SetRefreshCodex(t *testing.T, f func(ctx context.Context, st *store.State) error) {
	old := refreshCodex
	refreshCodex = f
	t.Cleanup(func() { refreshCodex = old })
}
