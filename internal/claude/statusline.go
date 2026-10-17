// Package claude connects Claude Code's status line to the relay: it parses
// the rate limits Claude Code passes on stdin, chains the user's previous
// status line, and hooks and unhooks Claude Code's settings.
package claude

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"time"

	"github.com/tidwall/gjson"

	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/push"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

// StartPush launches `devlets <args>` detached from this process. The CLI sets
// it to the real detacher; tests set it to a recorder. Nil means do nothing.
var StartPush func(args ...string) error

// chainedEnv is set in the chained command's environment (see RunStatusLine).
const chainedEnv = "DEVLETS_CHAINED"

// chainTimeout bounds the user's previous status-line command.
var chainTimeout = time.Second

var windowNames = []string{"five_hour", "seven_day"}

// ParseStatusLine reads rate_limits from Claude Code's status-line JSON. It
// returns false when no window is present. used_percentage is clamped to 0-100
// and resets_at (Unix seconds) becomes UTC. ReadAt is left for the caller.
func ParseStatusLine(stdin []byte) (contract.Tool, bool) {
	var tool contract.Tool
	limits := gjson.GetBytes(stdin, "rate_limits")
	for _, name := range windowNames {
		w := limits.Get(name)
		used, reset := w.Get("used_percentage"), w.Get("resets_at")
		if !used.Exists() || used.Type != gjson.Number || !reset.Exists() || reset.Type != gjson.Number {
			continue
		}
		p := used.Float()
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		tool.Windows = append(tool.Windows, contract.Window{
			ID:          name,
			UsedPercent: p,
			ResetsAt:    time.Unix(reset.Int(), 0).UTC(),
		})
	}
	return tool, len(tool.Windows) > 0
}

// RunStatusLine is the body of `devlets claude-statusline`. It never fails the
// status line: errors are swallowed and the previous command's output is
// printed whatever happens.
func RunStatusLine(stdin io.Reader, stdout io.Writer, cfg *store.Config, now time.Time) error {
	input, _ := io.ReadAll(stdin)
	if cfg == nil {
		return nil
	}
	// A devlets started by another devlets' chain never chains again, so a
	// status line that somehow points back at us cannot loop.
	if cmd := previousCommand(cfg); cmd != "" && os.Getenv(chainedEnv) == "" {
		if out := runChained(cmd, input); len(out) > 0 {
			_, _ = stdout.Write(out)
		}
	}
	tool, ok := ParseStatusLine(input)
	if !ok {
		return nil
	}
	tool.ReadAt = now.UTC()

	st, err := store.LoadState()
	if err != nil || st == nil {
		st = &store.State{}
	}
	old, had := st.Reading.Tools["claude"]
	changed := !had || !reflect.DeepEqual(old.Windows, tool.Windows)
	hooked := hookedList(cfg)
	if !reflect.DeepEqual(st.Reading.Hooked, hooked) || st.Reading.Computer != cfg.Computer {
		changed = true
	}
	if changed {
		if st.Reading.Tools == nil {
			st.Reading.Tools = map[string]contract.Tool{}
		}
		tool.Plan = old.Plan
		st.Reading.V = 1
		st.Reading.Computer = cfg.Computer
		st.Reading.Hooked = hooked
		st.Reading.SentAt = now.UTC()
		st.Reading.Tools["claude"] = tool
		_ = store.SaveState(st)
	}
	if StartPush != nil && push.Due(st.LastPush, st.Reading, now, st.NextAllowed) {
		_ = StartPush("push")
	}
	return nil
}

func hookedList(cfg *store.Config) []string {
	list := []string{}
	if cfg.Claude.Hooked {
		list = append(list, "claude")
	}
	if cfg.Codex.Hooked {
		list = append(list, "codex")
	}
	return list
}

// previousCommand extracts the command of the saved previous statusLine.
func previousCommand(cfg *store.Config) string {
	if cfg.Claude.Previous == nil {
		return ""
	}
	prev := gjson.Parse(*cfg.Claude.Previous)
	if !prev.IsObject() {
		return ""
	}
	if t := prev.Get("type"); t.Exists() && t.String() != "command" {
		return ""
	}
	return prev.Get("command").String()
}

// chainShell is the shell Claude Code uses for status lines: $SHELL when it
// points at a real file (Git Bash on Windows), else sh. On Windows the
// fallback is Git for Windows' bash; plain "bash" is avoided because
// System32\bash.exe is WSL, which mangles quotes and sees other paths.
func chainShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		if fi, err := os.Stat(s); err == nil && !fi.IsDir() {
			return s
		}
	}
	if runtime.GOOS != "windows" {
		return "sh"
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("LOCALAPPDATA") + `\Programs`} {
		if root == "" {
			continue
		}
		p := filepath.Join(root, "Git", "bin", "bash.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "bash"
}

// runChained runs the previous command with the same stdin and a timeout and
// returns its stdout. Stderr is discarded; failures and timeouts yield whatever
// was printed before them.
func runChained(command string, input []byte) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), chainTimeout)
	defer cancel()
	if runtime.GOOS == "windows" {
		// Go escapes a space-free argument as bare \"...\", which the MSYS runtime
		// under Git Bash mis-parses; a trailing space makes Go quote the whole
		// argument, which it reads correctly. A trailing space is harmless to sh.
		command += " "
	}
	cmd := exec.CommandContext(ctx, chainShell(), "-c", command)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), chainedEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = 100 * time.Millisecond // don't wait on grandchildren holding the pipe
	_ = cmd.Run()
	return out.Bytes()
}
