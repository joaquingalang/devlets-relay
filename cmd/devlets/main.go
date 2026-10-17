// Command devlets links a computer to the Devlets phone app.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/claude"
	"github.com/joaquingalang/devlets-relay/internal/codex"
	"github.com/joaquingalang/devlets-relay/internal/mailbox"
	"github.com/joaquingalang/devlets-relay/internal/push"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: devlets <command>

Commands:
  link          Link this computer to your phone
  status        Show what is linked and the last reading
  unlink        Remove the link and the hooks
  push          Read usage and send it now
  claude-statusline   (used by Claude Code)
  codex-notify        (used by Codex)

Flags:
  --version     Print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, os.Stdin, stdout, stderr)
}

func runWith(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "--version", "-version", "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "claude-statusline":
		// Never fails: the status line must keep working whatever happens here.
		cfg, _ := store.LoadConfig()
		claude.StartPush = push.StartDetached
		_ = claude.RunStatusLine(stdin, stdout, cfg, time.Now())
		return 0
	case "codex-notify":
		// Never fails and prints nothing: Codex runs this after every turn.
		cfg, _ := store.LoadConfig()
		codex.StartChain = codex.StartDetached
		codex.StartPush = push.StartDetached
		codex.ClientVersion = version
		_ = codex.RunNotify(args[1:], cfg, time.Now())
		return 0
	case "push":
		return runPush(args[1:], stderr)
	case "link", "status", "unlink":
		fmt.Fprintf(stderr, "devlets %s: not implemented yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "devlets: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// pushTimeout must stay below the 60 s push.lock staleness limit, so a run
// never outlives its lock.
const pushTimeout = 45 * time.Second

const defaultAPI = "https://api.getdevlets.com"

// apiBase is DEVLETS_API, else the base saved at link time, else production (BR10).
func apiBase(cfg *store.Config) string {
	if v := os.Getenv("DEVLETS_API"); v != "" {
		return v
	}
	if cfg.APIBase != "" {
		return cfg.APIBase
	}
	return defaultAPI
}

// runPush is `devlets push [--read-codex]`. It prints nothing to stdout; its
// stderr is push.log when it was started detached.
func runPush(args []string, stderr io.Writer) int {
	readCodex := false
	for _, a := range args {
		if a != "--read-codex" {
			fmt.Fprintf(stderr, "devlets push: unknown flag %q\n", a)
			return 2
		}
		readCodex = true
	}
	cfg, err := store.LoadConfig()
	if errors.Is(err, store.ErrNotLinked) {
		fmt.Fprintln(stderr, "Not linked. Run devlets link.")
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "devlets push: cannot read config")
		return 1
	}
	st, err := store.LoadState()
	if err != nil {
		fmt.Fprintln(stderr, "devlets push: cannot read state")
		return 1
	}
	push.Log = stderr
	push.UnhookClaude = func(previous *string) error {
		if previous == nil {
			return claude.Unhook(nil)
		}
		return claude.Unhook(json.RawMessage(*previous))
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	client := &mailbox.Client{Base: apiBase(cfg), Version: version}
	if err := push.Run(ctx, cfg, st, client, readCodex, time.Now()); err != nil {
		return 1 // already logged by push.Run
	}
	return 0
}
