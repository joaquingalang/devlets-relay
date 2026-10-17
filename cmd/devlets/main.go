// Command devlets links a computer to the Devlets phone app.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/claude"
	"github.com/joaquingalang/devlets-relay/internal/codex"
	"github.com/joaquingalang/devlets-relay/internal/link"
	"github.com/joaquingalang/devlets-relay/internal/mailbox"
	"github.com/joaquingalang/devlets-relay/internal/push"
	"github.com/joaquingalang/devlets-relay/internal/store"
	"github.com/joaquingalang/devlets-relay/internal/update"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: devlets <command>

Commands:
  link          Link this computer to your phone
  status        Show what is linked and the last reading
  unlink        Remove the link and the hooks
  update        Install the latest version
  push          Read usage and send it now
  claude-statusline   (used by Claude Code)
  codex-notify        (used by Codex)

Flags:
  --version     Print the version
`

func main() {
	update.Current = version
	update.CleanupOld() // a Windows update leaves the old binary as .old
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
	case "link":
		return runLink(args[1:], stdin, stdout, stderr)
	case "update":
		return runUpdate(stdout, stderr)
	case "status":
		link.Version = version
		update.Current = version
		link.AfterStatus = func(out io.Writer) { update.Notice(out, time.Now()) }
		if err := link.Status(stdout, time.Now()); err != nil {
			fmt.Fprintf(stderr, "devlets status: %v\n", err)
			return 1
		}
		return 0
	case "unlink":
		link.Version = version
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := link.Unlink(ctx, stdout, link.Options{API: os.Getenv("DEVLETS_API")}); err != nil {
			fmt.Fprintf(stderr, "devlets unlink: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "devlets: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// runUpdate is `devlets update`.
func runUpdate(stdout, stderr io.Writer) int {
	update.Current = version
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	latest, newer, err := update.Check(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "devlets update: %v\n", err)
		return 1
	}
	if !newer {
		fmt.Fprintf(stdout, "devlets %s is up to date.\n", version)
		return 0
	}
	if err := update.Apply(ctx); err != nil {
		fmt.Fprintf(stderr, "devlets update: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Updated devlets %s to %s.\n", version, latest)
	return 0
}

// runLink is`devlets link [--yes] [--no-claude] [--no-codex]`.
func runLink(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts := link.Options{API: os.Getenv("DEVLETS_API")}
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			opts.Yes = true
		case "--no-claude":
			opts.NoClaude = true
		case "--no-codex":
			opts.NoCodex = true
		default:
			fmt.Fprintf(stderr, "devlets link: unknown flag %q\n", a)
			return 2
		}
	}
	link.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := link.Run(ctx, stdin, stdout, opts); err != nil {
		if errors.Is(err, link.ErrReported) {
			return 1
		}
		fmt.Fprintf(stderr, "devlets link: %v\n", err)
		return 1
	}
	return 0
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
