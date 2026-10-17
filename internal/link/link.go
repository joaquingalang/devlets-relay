// Package link implements the user-facing commands: `devlets link`,
// `devlets status` and `devlets unlink` (BR11 to BR13, FR21).
//
// Nothing here prints the key, the write token or the read token, except the
// key inside the QR code and link the user is meant to scan.
package link

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/joaquingalang/devlets-relay/internal/claude"
	"github.com/joaquingalang/devlets-relay/internal/codex"
	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/crypto"
	"github.com/joaquingalang/devlets-relay/internal/mailbox"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

// Options are the flags of `devlets link`, plus test seams.
type Options struct {
	Yes, NoClaude, NoCodex bool
	// Terminal says the answers come from a real terminal. Only then does an
	// empty answer mean yes.
	Terminal     bool
	API          string        // relay API base; empty means production
	HelloTimeout time.Duration // how long to wait for the phone; 0 means 10 minutes
	Detect       func() (claude, codex bool)
}

// Version is the CLI version, for the User-Agent. main sets it.
var Version = "dev"

// ErrReported means the command already told the user what happened; main
// exits 1 without printing anything more.
var ErrReported = errors.New("already reported")

// ErrNoTerminal means there is nobody to ask: no terminal and no --yes.
var ErrNoTerminal = errors.New("devlets link needs a terminal to ask before changing your settings. Run it in a terminal, or pass --yes.")

const (
	msgNothingToLink = "Neither Claude Code nor Codex is set up to send usage. Nothing to link."
	msgShowAgain     = "Run devlets link again to show the code."
	msgRemoved       = "This link was removed from the phone. Run devlets link to set it up again."
	msgClaudeLeft    = "Your Claude Code status line was changed after linking, so Devlets left it as it is."
)

// AfterStatus runs at the end of `devlets status`. main hooks the weekly
// "newer version" line here.
var AfterStatus func(out io.Writer)

const (
	defaultAPI   = "https://api.getdevlets.com"
	defaultWait  = 10 * time.Minute
	codexTimeout = 25 * time.Second
	maxName      = 64
)

// Seams for tests.
var (
	pollEvery   = 2 * time.Second
	waitingHook func() // called when polling for the hello starts
	executable  = os.Executable
	readCodex   = func(ctx context.Context) (contract.Tool, error) {
		return codex.ReadRateLimits(ctx, "codex")
	}
)

// URL is the link the QR code carries. The key sits in the fragment, so a web
// server never receives it.
func URL(boxID string, key []byte) string {
	return "https://getdevlets.com/l#b=" + boxID + "&k=" + crypto.B64(key)
}

func (o Options) api() string {
	if o.API != "" {
		return o.API
	}
	return defaultAPI
}

func defaultDetect() (bool, bool) {
	_, cerr := exec.LookPath("claude")
	claudeFound := cerr == nil
	if !claudeFound {
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if dir == "" {
			if h, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(h, ".claude")
			}
		}
		if dir != "" {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				claudeFound = true
			}
		}
	}
	_, xerr := exec.LookPath("codex")
	return claudeFound, xerr == nil
}

func computerName() string {
	h, _ := os.Hostname()
	h = strings.TrimSpace(h)
	if h == "" {
		h = "My computer"
	}
	if r := []rune(h); len(r) > maxName {
		h = string(r[:maxName])
	}
	return h
}

// prompt prints question and returns the trimmed answer line ("" at EOF).
func prompt(r *bufio.Reader, out io.Writer, question string) string {
	fmt.Fprint(out, question+" ")
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// yesNo asks a [Y/n] question: y and yes are yes, and so is an empty answer
// typed at a real terminal (not one from a closed or redirected input).
func yesNo(r *bufio.Reader, out io.Writer, opts Options, question string) bool {
	if opts.Yes {
		fmt.Fprintln(out, question+" Y")
		return true
	}
	a := strings.ToLower(prompt(r, out, question))
	return (a == "" && opts.Terminal) || a == "y" || a == "yes"
}

// apiFor is where an existing box lives: the base saved at link time. Only a
// new box uses opts.API (DEVLETS_API), which is then saved with it.
func apiFor(opts Options, cfg *store.Config) string {
	if cfg != nil && cfg.APIBase != "" {
		return cfg.APIBase
	}
	return opts.api()
}

// Run is `devlets link`. in is where answers come from; nil means there is no
// terminal, and then only --yes may go ahead.
func Run(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	if in == nil {
		if !opts.Yes {
			return ErrNoTerminal
		}
		in = strings.NewReader("")
	}
	r := bufio.NewReader(in)
	cfg, err := store.LoadConfig()
	switch {
	case err == nil:
		fmt.Fprintln(out, "This computer is already linked.")
		fmt.Fprintln(out, "  1) Show the code again")
		fmt.Fprintln(out, "  2) Start over")
		fmt.Fprintln(out, "  3) Cancel")
		choice := "1"
		if !opts.Yes {
			choice = strings.ToLower(prompt(r, out, "Choose [1]:"))
		}
		switch choice {
		case "", "1", "show", "show the code again":
			return showAgain(ctx, out, opts, cfg)
		case "2", "start", "start over":
			client := &mailbox.Client{Base: apiFor(opts, cfg), Version: Version}
			if err := teardown(ctx, out, cfg, client, true); err != nil {
				return err
			}
			return fresh(ctx, r, out, opts)
		default:
			fmt.Fprintln(out, "Cancelled. Nothing changed.")
			return nil
		}
	case errors.Is(err, store.ErrNotLinked):
		return fresh(ctx, r, out, opts)
	default:
		return fmt.Errorf("cannot read the saved link: %w", err)
	}
}

func showAgain(ctx context.Context, out io.Writer, opts Options, cfg *store.Config) error {
	key, err := crypto.UnB64(cfg.Key)
	if err != nil || len(key) != 32 {
		return errors.New("the saved key is damaged; choose Start over")
	}
	client := &mailbox.Client{Base: apiFor(opts, cfg), Version: Version}
	// a hello already on the server is an older phone: wait for a new one
	baseline, herr := client.Hello(ctx, cfg.BoxID, cfg.WriteToken)
	if errors.Is(herr, mailbox.ErrGone) {
		return removedFromPhone(out, cfg)
	}
	printCode(out, URL(cfg.BoxID, key))
	err = waitHello(ctx, out, client, cfg.BoxID, cfg.WriteToken, key, baseline, opts.HelloTimeout)
	if errors.Is(err, mailbox.ErrGone) {
		return removedFromPhone(out, cfg)
	}
	return err
}

// removedFromPhone treats a 404 like BR12: restore both tools and delete the
// local files, so the next `devlets link` starts fresh.
func removedFromPhone(out io.Writer, cfg *store.Config) error {
	if err := restoreTools(out, cfg); err != nil {
		return ErrReported
	}
	_ = store.Remove()
	fmt.Fprintln(out, msgRemoved)
	return ErrReported
}

func printCode(out io.Writer, link string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Scan this code in the Devlets app:")
	fmt.Fprintln(out)
	printQR(out, link)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "or open it with your phone's camera")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Waiting for your phone...")
}

func fresh(ctx context.Context, r *bufio.Reader, out io.Writer, opts Options) error {
	detect := opts.Detect
	if detect == nil {
		detect = defaultDetect
	}
	hasClaude, hasCodex := detect()
	wantClaude, wantCodex := false, false
	if hasClaude && !opts.NoClaude {
		wantClaude = yesNo(r, out, opts, "Send Claude usage? This adds Devlets to your Claude Code status line. Your current status line keeps working. [Y/n]")
	}
	if hasCodex && !opts.NoCodex {
		wantCodex = yesNo(r, out, opts, "Send Codex usage? This runs Devlets after each Codex reply. [Y/n]")
	}
	if !wantClaude && !wantCodex {
		fmt.Fprintln(out, msgNothingToLink)
		return ErrReported
	}

	// 1. read Codex first, before anything changes
	var codexTool *contract.Tool
	if wantCodex {
		cctx, cancel := context.WithTimeout(ctx, codexTimeout)
		t, err := readCodex(cctx)
		cancel()
		if err != nil {
			fmt.Fprintln(out, "Could not read Codex usage yet. The next Codex reply will add it.")
		} else {
			codexTool = &t
		}
	}

	// 2. key and box
	key, err := crypto.NewKey()
	if err != nil {
		return errors.New("could not create a key")
	}
	client := &mailbox.Client{Base: opts.api(), Version: Version}
	boxID, writeToken, err := client.Create(ctx, crypto.TokenHash(crypto.ReadToken(key)))
	if err != nil {
		return fmt.Errorf("could not reach Devlets to create the link: %w", err)
	}
	cfg := &store.Config{APIBase: opts.api(), BoxID: boxID, WriteToken: writeToken, Key: crypto.B64(key), Computer: computerName()}
	if err := store.SaveConfig(cfg); err != nil {
		_ = client.Delete(ctx, boxID, writeToken)
		return errors.New("could not save the link on this computer")
	}

	// 3. hook the chosen tools (cfg is new, so nothing is hooked yet)
	exe, exeErr := executable()
	if exeErr != nil {
		exe = "devlets"
	}
	if wantClaude && !cfg.Claude.Hooked {
		prev, err := claude.Hook(exe)
		if err != nil {
			fmt.Fprintf(out, "Could not change the Claude Code settings: %v\nSkipping Claude Code.\n", err)
		} else {
			cfg.Claude = store.Hook{Hooked: true}
			if prev != nil {
				s := string(prev)
				cfg.Claude.Previous = &s
			}
			if err := store.SaveConfig(cfg); err != nil {
				// the saved previous would be lost: put the settings back now
				_ = claude.Unhook(prev)
				cfg.Claude = store.Hook{}
				fmt.Fprintln(out, "Could not save the Claude Code hook record. Skipping Claude Code.")
			}
		}
	}
	if wantCodex && !cfg.Codex.Hooked {
		prev, err := codex.Hook(exe)
		if err != nil {
			fmt.Fprintf(out, "Could not change the Codex config: %v\nSkipping Codex.\n", err)
		} else {
			cfg.Codex = store.Hook{Hooked: true}
			if prev != "" {
				cfg.Codex.Previous = &prev
			}
			if err := store.SaveConfig(cfg); err != nil {
				_ = codex.Unhook(prev)
				cfg.Codex = store.Hook{}
				fmt.Fprintln(out, "Could not save the Codex hook record. Skipping Codex.")
			}
		}
	}

	if !cfg.Claude.Hooked && !cfg.Codex.Hooked {
		// nothing ended up hooked: undo the box and stop
		_ = client.Delete(ctx, boxID, writeToken)
		_ = store.Remove()
		fmt.Fprintln(out, msgNothingToLink)
		return ErrReported
	}

	// 4. ONE reading: hooked list and the Codex tool when read
	now := time.Now().UTC()
	hooked := []string{}
	if cfg.Claude.Hooked {
		hooked = append(hooked, "claude")
	}
	if cfg.Codex.Hooked {
		hooked = append(hooked, "codex")
	}
	reading := contract.Reading{V: 1, Computer: cfg.Computer, SentAt: now, Hooked: hooked, Tools: map[string]contract.Tool{}}
	if codexTool != nil && cfg.Codex.Hooked {
		reading.Tools["codex"] = *codexTool
	}
	st := &store.State{Reading: reading}
	if plain, err := json.Marshal(reading); err == nil {
		if env, err := crypto.Seal(key, boxID, crypto.Version, plain); err == nil {
			if err := client.Put(ctx, boxID, writeToken, env); err != nil {
				fmt.Fprintln(out, "Could not send the first reading. It goes out with the next one.")
			} else {
				st.LastPush = store.Pushed{At: now, Reading: reading}
			}
		}
	}
	_ = store.SaveState(st)

	// 5. the code, then wait for the phone
	printCode(out, URL(boxID, key))
	err = waitHello(ctx, out, client, boxID, writeToken, key, nil, opts.HelloTimeout)
	if errors.Is(err, mailbox.ErrGone) {
		return errors.New("this link was deleted. Run devlets link to start again")
	}
	return err
}

// waitHello polls for the phone's hello. A hello equal to baseline is an old
// one and is ignored.
func waitHello(ctx context.Context, out io.Writer, client *mailbox.Client, boxID, writeToken string, key []byte, baseline *crypto.Envelope, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultWait
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if waitingHook != nil {
		waitingHook()
	}
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		env, err := client.Hello(wctx, boxID, writeToken)
		switch {
		case errors.Is(err, mailbox.ErrGone):
			return mailbox.ErrGone
		case err == nil && env != nil && !(baseline != nil && *env == *baseline):
			if pt, err := crypto.Open(key, boxID, *env); err == nil {
				var h struct {
					Phone string `json:"phone"`
				}
				_ = json.Unmarshal(pt, &h)
				fmt.Fprintf(out, "✓ Linked to %s\n", cleanName(h.Phone))
				return nil
			}
		}
		select {
		case <-wctx.Done():
			fmt.Fprintln(out, msgShowAgain) // timeout or Ctrl-C: the link stays
			return ErrReported
		case <-tick.C:
		}
	}
}

// cleanName drops control characters from a name that came from another device.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxName {
		s = string(r[:maxName])
	}
	if s == "" {
		return "your phone"
	}
	return s
}

// teardown deletes the box and restores both tools, then removes the local
// files. With strictDelete a failed DELETE (other than 404) stops everything
// before any setting changes.
func teardown(ctx context.Context, out io.Writer, cfg *store.Config, client *mailbox.Client, strictDelete bool) error {
	if err := client.Delete(ctx, cfg.BoxID, cfg.WriteToken); err != nil && !errors.Is(err, mailbox.ErrGone) {
		if strictDelete {
			return errors.New("could not reach Devlets to delete the old link; nothing was changed")
		}
		fmt.Fprintln(out, "Could not reach Devlets to delete the link there. It expires on its own after 90 days.")
	}
	if err := restoreTools(out, cfg); err != nil {
		return err
	}
	return store.Remove()
}

// restoreTools unhooks each hooked tool and saves config.json after each one.
// On a failure it names the file, keeps config.json (it holds the saved
// previous values) and returns an error.
func restoreTools(out io.Writer, cfg *store.Config) error {
	failed := false
	if cfg.Claude.Hooked {
		var prev json.RawMessage
		if cfg.Claude.Previous != nil {
			prev = json.RawMessage(*cfg.Claude.Previous)
		}
		err := claude.Unhook(prev)
		if errors.Is(err, claude.ErrUserChanged) {
			fmt.Fprintln(out, msgClaudeLeft)
			err = nil
		}
		if err != nil {
			failed = true
			fmt.Fprintf(out, "Could not restore Claude Code: %v\nFix %s by hand, then run devlets unlink again.\n", err, claudeSettingsPath())
		} else {
			cfg.Claude = store.Hook{}
			_ = store.SaveConfig(cfg)
		}
	}
	if cfg.Codex.Hooked {
		prev := ""
		if cfg.Codex.Previous != nil {
			prev = *cfg.Codex.Previous
		}
		if err := codex.Unhook(prev); err != nil {
			failed = true
			fmt.Fprintf(out, "Could not restore Codex: %v\nFix %s by hand, then run devlets unlink again.\n", err, codexConfigPath())
		} else {
			cfg.Codex = store.Hook{}
			_ = store.SaveConfig(cfg)
		}
	}
	if failed {
		return errors.New("some settings could not be restored")
	}
	return nil
}

// Unlink is `devlets unlink` (BR13).
func Unlink(ctx context.Context, out io.Writer, opts Options) error {
	cfg, err := store.LoadConfig()
	if errors.Is(err, store.ErrNotLinked) {
		fmt.Fprintln(out, "Not linked. Run devlets link.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read the saved link: %w", err)
	}
	client := &mailbox.Client{Base: apiFor(opts, cfg), Version: Version}
	if err := teardown(ctx, out, cfg, client, false); err != nil {
		return err
	}
	fmt.Fprintln(out, "Unlinked. Your Claude Code and Codex settings are back as they were.")
	return nil
}

// Status is `devlets status` (BR13).
func Status(out io.Writer, now time.Time) error {
	cfg, err := store.LoadConfig()
	if errors.Is(err, store.ErrNotLinked) {
		fmt.Fprintln(out, "Not linked. Run devlets link.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read the saved link: %w", err)
	}
	st, err := store.LoadState()
	if err != nil {
		st = &store.State{}
	}
	id := cfg.BoxID
	if len(id) > 4 {
		id = id[len(id)-4:]
	}
	fmt.Fprintf(out, "Computer:    %s\n", cfg.Computer)
	fmt.Fprintf(out, "Link:        ...%s\n", id)
	tool := func(label, key string, h store.Hook) {
		if !h.Hooked {
			fmt.Fprintf(out, "%-12s not hooked\n", label+":")
			return
		}
		last := "no reading yet"
		if t, ok := st.Reading.Tools[key]; ok && !t.ReadAt.IsZero() {
			last = "last reading " + age(now, t.ReadAt)
		}
		fmt.Fprintf(out, "%-12s hooked, %s\n", label+":", last)
	}
	tool("Claude Code", "claude", cfg.Claude)
	tool("Codex", "codex", cfg.Codex)
	if st.LastPush.At.IsZero() {
		fmt.Fprintf(out, "Last push:   never\n")
	} else {
		fmt.Fprintf(out, "Last push:   %s (%s)\n", age(now, st.LastPush.At), st.LastPush.At.Local().Format("2006-01-02 15:04"))
	}
	if AfterStatus != nil {
		AfterStatus(out)
	}
	return nil
}

// age says how long ago t was, in whole units.
func age(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%d d ago", int(d/(24*time.Hour)))
}

// claudeSettingsPath mirrors where the claude package reads settings.json.
func claudeSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "settings.json")
}

// codexConfigPath mirrors where the codex package reads config.toml.
func codexConfigPath() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "config.toml")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex", "config.toml")
}
