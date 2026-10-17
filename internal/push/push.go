package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/codex"
	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/crypto"
	"github.com/joaquingalang/devlets-relay/internal/mailbox"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

// Log receives push diagnostics (the detached child's stderr is push.log).
// Messages never carry tokens, keys, readings or prompt text.
var Log io.Writer = io.Discard

// UnhookClaude restores Claude Code's statusLine from the saved previous
// value (nil = there was none). The claude package imports this one, so main
// sets this variable instead of push importing claude.
var UnhookClaude func(previous *string) error

// refreshCodex reads Codex's rate limits into st; tests replace it.
var refreshCodex = func(ctx context.Context, st *store.State) error {
	return codex.Refresh(ctx, "codex", st)
}

const (
	minBackoff = 60 * time.Second
	maxBackoff = 15 * time.Minute
)

func logf(format string, a ...any) { fmt.Fprintf(Log, format+"\n", a...) }

// backoff is 60 s for the first failure, doubling, capped at 15 min.
func backoff(failures int) time.Duration {
	d := minBackoff
	for i := 1; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
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

// Every failure Run returns has already been written to Log; callers only
// turn it into an exit code.
//
// Run reads Codex (when readCodex), encrypts and sends the current reading.
// It holds push.lock for the whole run and does nothing when another push is
// running or the server asked us to wait. A 404 means the box is gone: both
// tools are unhooked and the local files removed (BR12).
func Run(ctx context.Context, cfg *store.Config, st *store.State, client *mailbox.Client, readCodex bool, now time.Time) error {
	now = now.UTC()
	release, err := acquireLock(now)
	if errors.Is(err, ErrLocked) {
		return nil
	}
	if err != nil {
		logf("push: cannot take the lock")
		return err
	}
	defer release()

	var codexTool *contract.Tool
	if readCodex {
		if err := refreshCodex(ctx, st); err != nil {
			logf("codex read failed")
		} else {
			t := st.Reading.Tools["codex"]
			codexTool = &t
		}
	}
	// The status line may have written state meanwhile: reload it, fold in the
	// fresh Codex read, and honour the newest NextAllowed. If it cannot be
	// loaded, nothing is saved (never overwrite with an empty state).
	cur, lerr := store.LoadState()
	canSave := lerr == nil && cur != nil
	if !canSave {
		cur = st
	}
	if codexTool != nil {
		if cur.Reading.Tools == nil {
			cur.Reading.Tools = map[string]contract.Tool{}
		}
		cur.Reading.Tools["codex"] = *codexTool
		if canSave {
			_ = store.SaveState(cur)
		}
	}
	if now.Before(cur.NextAllowed) {
		return nil
	}
	if len(cur.Reading.Tools) == 0 {
		logf("nothing to send yet")
		return nil
	}
	reading := contract.Reading{V: 1, Computer: cfg.Computer, SentAt: now, Hooked: hookedList(cfg), Tools: cur.Reading.Tools}
	// The status line starts a push only when one is due; a Codex read starts
	// one on a timer, so the due rule (FR10) is applied here.
	if readCodex && !Due(cur.LastPush, reading, now, cur.NextAllowed) {
		return nil
	}

	key, err := crypto.UnB64(cfg.Key)
	if err != nil {
		logf("push: bad key in config")
		return errors.New("push: bad key in config")
	}
	plain, err := json.Marshal(reading)
	if err != nil {
		logf("push: encode failed")
		return errors.New("push: encode failed")
	}
	env, err := crypto.Seal(key, cfg.BoxID, crypto.Version, plain)
	if err != nil {
		logf("push: encrypt failed")
		return errors.New("push: encrypt failed")
	}

	perr := client.Put(ctx, cfg.BoxID, cfg.WriteToken, env)
	var rl *mailbox.RateLimited
	switch {
	case perr == nil:
		update(canSave, func(s *store.State) {
			s.LastPush = store.Pushed{At: now, Reading: reading}
			s.NextAllowed = time.Time{}
			s.PushFailures = 0
		})
		return nil
	case errors.Is(perr, mailbox.ErrGone):
		unlink(cfg, now, canSave)
		return nil
	case errors.As(perr, &rl):
		wait := rl.RetryAfter
		if wait <= 0 {
			wait = minBackoff
		}
		update(canSave, func(s *store.State) { s.NextAllowed = now.Add(wait) })
		logf("rate limited, waiting %s", wait)
		return perr
	default:
		update(canSave, func(s *store.State) {
			s.PushFailures++
			s.NextAllowed = now.Add(backoff(s.PushFailures))
		})
		logf("push failed: %v", perr)
		return perr
	}
}

// update reloads state right before saving so other writers' changes survive.
// It writes nothing when the state could not be loaded.
func update(canSave bool, mut func(*store.State)) {
	if !canSave {
		return
	}
	s, err := store.LoadState()
	if err != nil || s == nil {
		return
	}
	mut(s)
	_ = store.SaveState(s)
}

// unlink handles a 404: restore both tools' settings, then delete config.json
// and state.json. When an unhook fails the config is kept (it holds the saved
// previous values), the failure is logged and the next try is backed off.
// It acts only while config.json still holds the box this push sent to: a
// `devlets link` that replaced the link meanwhile must not be undone.
func unlink(started *store.Config, now time.Time, canSave bool) {
	cfg, err := store.LoadConfig()
	if err != nil || cfg.BoxID != started.BoxID {
		logf("link changed during the push; nothing removed")
		return
	}
	failed := false
	if cfg.Claude.Hooked {
		if UnhookClaude == nil || UnhookClaude(cfg.Claude.Previous) != nil {
			failed = true
			logf("could not restore Claude Code settings")
		} else {
			// remember it now, so a retry never re-writes the restored settings
			cfg.Claude = store.Hook{}
			_ = store.SaveConfig(cfg)
		}
	}
	if cfg.Codex.Hooked {
		prev := ""
		if cfg.Codex.Previous != nil {
			prev = *cfg.Codex.Previous
		}
		if codex.Unhook(prev) != nil {
			failed = true
			logf("could not restore Codex config")
		} else {
			cfg.Codex = store.Hook{}
			_ = store.SaveConfig(cfg)
		}
	}
	if failed {
		update(canSave, func(s *store.State) {
			s.PushFailures++
			s.NextAllowed = now.Add(backoff(s.PushFailures))
		})
		return
	}
	dir, err := store.Dir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, "config.json"))
	_ = os.Remove(filepath.Join(dir, "state.json"))
}
