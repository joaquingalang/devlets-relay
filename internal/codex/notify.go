package codex

import (
	"os/exec"
	"time"

	"github.com/tidwall/gjson"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

// StartPush launches `devlets <args>` detached from this process. The CLI sets
// it to the real detacher; tests set it to a recorder. Nil means do nothing.
var StartPush func(args ...string) error

// StartChain launches the user's previous notify argv detached. The CLI sets
// it to StartDetached; tests set it to a recorder. Nil means do nothing.
var StartChain func(argv []string) error

// readGap is the least time between two Codex rate-limit reads.
const readGap = 5 * time.Minute

// StartDetached runs argv without waiting for it, with no stdio attached.
func StartDetached(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// RunNotify is the body of `devlets codex-notify <json>`. Codex appends the
// event JSON as the last argument; only its "type" is read, and nothing from
// it is logged, stored or sent. The previous notify, if any, gets the same
// arguments, then a Codex read is started when one is due (at most one per
// five minutes). It never fails and prints nothing: errors are swallowed.
func RunNotify(args []string, cfg *store.Config, now time.Time) error {
	// The previous notify argv is saved only in config.json. When the config
	// cannot be loaded (cfg == nil) there is nothing to chain, so do nothing:
	// no chain, no read, and no write to Codex's config or to state.
	if cfg == nil {
		return nil
	}
	if cfg.Codex.Previous != nil && StartChain != nil {
		if prev := parseNotifyArgv(*cfg.Codex.Previous); len(prev) > 0 {
			argv := append(append([]string(nil), prev...), args...)
			_ = StartChain(argv)
		}
	}
	if len(args) == 0 || gjson.Get(args[len(args)-1], "type").String() != "agent-turn-complete" {
		return nil
	}
	st, err := store.LoadState()
	if err != nil || st == nil {
		// Unreadable state: never write an empty one over it, and skip the read
		// (the attempt cannot be recorded, so it could repeat every turn).
		return nil
	}
	last := st.CodexAttemptAt
	if r := st.Reading.Tools["codex"].ReadAt; r.After(last) {
		last = r
	}
	if !last.IsZero() && now.Sub(last) < readGap {
		return nil
	}
	st.CodexAttemptAt = now.UTC()
	if store.SaveState(st) != nil {
		return nil // can't record the attempt, so don't risk a read on every turn
	}
	if StartPush != nil {
		_ = StartPush("push", "--read-codex")
	}
	return nil
}
