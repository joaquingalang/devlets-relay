package codex

import (
	"os"
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
// env is extra environment for the child (DEVLETS_CHAINED=1, see RunNotify).
var StartChain func(argv []string, env []string) error

// chainedEnv is set in the chained process's environment. A process that has
// it never chains again, so two notifiers that wrap each other cannot loop
// (RH13). The Claude side uses the same name.
const chainedEnv = "DEVLETS_CHAINED"

// readGap is the least time between two Codex rate-limit reads.
const readGap = 5 * time.Minute

// PreviousChainable says whether the saved previous notify may be chained to.
// link and status use it to tell the user when it will not run.
func PreviousChainable(previous string) bool {
	prev := parseNotifyArgv(previous)
	return len(prev) > 0 && chainable(prev[0])
}

// PreviousNotifyArgv is the saved previous notify as an argv (nil if none).
func PreviousNotifyArgv(previous string) []string { return parseNotifyArgv(previous) }

// StartDetached runs argv without waiting for it, with no stdio attached. env
// is added to this process's environment.
func StartDetached(argv []string, env []string) error {
	if len(argv) == 0 {
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
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
	// Chain only when we were not started by a chain (RH13), and never to a
	// batch file (S1, RH3). The Codex read below still runs in a chained call;
	// readGap already stops repeats.
	if cfg.Codex.Previous != nil && StartChain != nil && os.Getenv(chainedEnv) == "" {
		if prev := parseNotifyArgv(*cfg.Codex.Previous); len(prev) > 0 && chainable(prev[0]) {
			argv := append(append([]string(nil), prev...), args...)
			_ = StartChain(argv, []string{chainedEnv + "=1"})
		}
	}
	if len(args) == 0 || gjson.Get(args[len(args)-1], "type").String() != "agent-turn-complete" {
		return nil
	}
	st, err := store.LoadState()
	corrupt := err != nil || st == nil
	if corrupt {
		// Unreadable state is read as empty and the read is started, so a
		// Codex-only user is never stuck; the push replaces the file once it
		// has a real reading. The attempt is not saved over the corrupt file,
		// so until then StartDetached's 30 s gap and push.lock bound the reads.
		st = &store.State{}
	}
	last := st.CodexAttemptAt
	if r := st.Reading.Tools["codex"].ReadAt; r.After(last) {
		last = r
	}
	if !last.IsZero() && now.Sub(last) < readGap {
		return nil
	}
	st.CodexAttemptAt = now.UTC()
	if !corrupt && store.SaveState(st) != nil {
		return nil // can't record the attempt, so don't risk a read on every turn
	}
	if StartPush != nil {
		_ = StartPush("push", "--read-codex")
	}
	return nil
}
