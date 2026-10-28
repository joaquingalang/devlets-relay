package codex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// Refusal is why the saved previous notify is not chained to; ChainOK means it is.
type Refusal string

const (
	ChainOK       Refusal = ""
	RefuseBatch   Refusal = "batch file"
	RefuseShell   Refusal = "command shell"
	RefuseMissing Refusal = "not found"
	RefuseSelf    Refusal = "devlets"
)

// isSelf reports whether argv0 resolves to this very executable: a second
// loop guard for a wrapper that clears the environment (RH13).
func isSelf(argv0 string) bool {
	p, err := exec.LookPath(argv0)
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	a, err1 := os.Stat(p)
	b, err2 := os.Stat(exe)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// checkArgv0 combines the platform check with the self check.
func checkArgv0(argv0 string) (Refusal, string) {
	if r, name := chainCheck(argv0); r != ChainOK {
		return r, name
	}
	if isSelf(argv0) {
		return RefuseSelf, filepath.Base(argv0)
	}
	return ChainOK, filepath.Base(argv0)
}

func chainOK(argv0 string) bool {
	r, _ := checkArgv0(argv0)
	return r == ChainOK
}

// PreviousNote tells the user, in one sentence, why the saved previous notify
// will not run, or returns "" when it will. link and status print it.
func PreviousNote(previous string) string {
	prev := parseNotifyArgv(previous)
	if len(prev) == 0 {
		return ""
	}
	r, name := checkArgv0(prev[0])
	switch r {
	case RefuseBatch:
		return fmt.Sprintf("Your previous Codex notify is a batch file (%s). Devlets won't pass Codex's messages to it, because Windows can't do that safely, so it won't run while Devlets is linked.", name)
	case RefuseShell:
		return fmt.Sprintf("Your previous Codex notify runs through %s, which would treat Codex's messages as commands. Devlets won't pass them on, so it won't run while Devlets is linked.", name)
	case RefuseMissing:
		return fmt.Sprintf("Your previous Codex notify (%s) wasn't found, so Devlets won't run it.", name)
	case RefuseSelf:
		return "Your previous Codex notify is Devlets itself, so Devlets won't run it."
	}
	return ""
}

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
		if prev := parseNotifyArgv(*cfg.Codex.Previous); len(prev) > 0 && chainOK(prev[0]) {
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
