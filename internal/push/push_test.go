package push_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/claude"
	"github.com/joaquingalang/devlets-relay/internal/codex"
	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/crypto"
	"github.com/joaquingalang/devlets-relay/internal/mailbox"
	"github.com/joaquingalang/devlets-relay/internal/mailbox/mailboxtest"
	"github.com/joaquingalang/devlets-relay/internal/push"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

var now = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

type rig struct {
	srv    *mailboxtest.Server
	cfg    *store.Config
	key    []byte
	client *mailbox.Client
}

func setup(t *testing.T) *rig {
	t.Helper()
	t.Setenv("DEVLETS_CONFIG_DIR", filepath.Join(t.TempDir(), "devlets"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	srv := mailboxtest.New()
	t.Cleanup(srv.Close)
	key, _ := crypto.NewKey()
	client := &mailbox.Client{Base: srv.URL, Version: "test"}
	id, wt, err := client.Create(context.Background(), crypto.TokenHash(crypto.ReadToken(key)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &store.Config{APIBase: srv.URL, BoxID: id, WriteToken: wt, Key: crypto.B64(key), Computer: "PC"}
	return &rig{srv, cfg, key, client}
}

func (r *rig) state(t *testing.T) *store.State {
	t.Helper()
	st := &store.State{Reading: contract.Reading{V: 1, Tools: map[string]contract.Tool{
		"claude": {ReadAt: now, Windows: []contract.Window{{ID: "5h", UsedPercent: 12, ResetsAt: now.Add(time.Hour)}}},
	}}}
	if err := store.SaveConfig(r.cfg); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPushSealsAndPuts(t *testing.T) {
	r := setup(t)
	r.cfg.Claude.Hooked, r.cfg.Codex.Hooked = true, true
	st := r.state(t)
	st.Reading.Tools["codex"] = contract.Tool{ReadAt: now, Windows: []contract.Window{{ID: "5h", UsedPercent: 3, ResetsAt: now.Add(time.Hour)}}}
	_ = store.SaveState(st)
	if err := store.SaveConfig(r.cfg); err != nil {
		t.Fatal(err)
	}

	if err := push.Run(context.Background(), r.cfg, st, r.client, false, now); err != nil {
		t.Fatal(err)
	}
	env, code := r.srv.PhoneGet(r.cfg.BoxID, crypto.ReadToken(r.key))
	if code != 200 {
		t.Fatalf("phone get %d", code)
	}
	pt, err := crypto.Open(r.key, r.cfg.BoxID, *env)
	if err != nil {
		t.Fatal(err)
	}
	var got contract.Reading
	if err := json.Unmarshal(pt, &got); err != nil {
		t.Fatal(err)
	}
	if got.Computer != "PC" || !got.SentAt.Equal(now) || len(got.Tools) != 2 || len(got.Hooked) != 2 {
		t.Fatalf("bad reading %+v", got)
	}
	saved, _ := store.LoadState()
	if !saved.LastPush.At.Equal(now) || !saved.NextAllowed.IsZero() {
		t.Fatalf("state not updated: %+v", saved)
	}
}

func TestPush404Unlinks(t *testing.T) {
	r := setup(t)
	claudeFile := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	codexFile := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
	claudeBefore := []byte("{\n  \"model\": \"x\",\n  \"statusLine\": {\"type\": \"command\", \"command\": \"old\"}\n}\n")
	codexBefore := []byte("model = \"gpt\"\nnotify = [\"old\"]\n")
	if err := os.WriteFile(claudeFile, claudeBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexFile, codexBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	cp, err := claude.Hook("/opt/devlets")
	if err != nil {
		t.Fatal(err)
	}
	xp, err := codex.Hook("/opt/devlets")
	if err != nil {
		t.Fatal(err)
	}
	cps := string(cp)
	r.cfg.Claude = store.Hook{Hooked: true, Previous: &cps}
	r.cfg.Codex = store.Hook{Hooked: true, Previous: &xp}
	st := r.state(t)
	push.UnhookClaude = func(p *string) error {
		if p == nil {
			return claude.Unhook(nil)
		}
		return claude.Unhook(json.RawMessage(*p))
	}
	t.Cleanup(func() { push.UnhookClaude = nil })

	r.srv.DeleteBox(r.cfg.BoxID)
	if err := push.Run(context.Background(), r.cfg, st, r.client, false, now); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(claudeFile); string(b) != string(claudeBefore) {
		t.Fatalf("claude settings not restored: %q", b)
	}
	if b, _ := os.ReadFile(codexFile); string(b) != string(codexBefore) {
		t.Fatalf("codex config not restored: %q", b)
	}
	if _, err := store.LoadConfig(); !errors.Is(err, store.ErrNotLinked) {
		t.Fatalf("want ErrNotLinked, got %v", err)
	}
}

func TestPush404KeepsConfigWhenUnhookFails(t *testing.T) {
	r := setup(t)
	r.cfg.Claude = store.Hook{Hooked: true}
	st := r.state(t)
	push.UnhookClaude = func(*string) error { return errors.New("boom") }
	t.Cleanup(func() { push.UnhookClaude = nil })
	r.srv.DeleteBox(r.cfg.BoxID)
	if err := push.Run(context.Background(), r.cfg, st, r.client, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadConfig(); err != nil {
		t.Fatalf("config must stay: %v", err)
	}
}

func TestPush429SetsNextAllowed(t *testing.T) {
	r := setup(t)
	st := r.state(t)
	r.srv.ForceRateLimit(90)
	_ = push.Run(context.Background(), r.cfg, st, r.client, false, now)
	saved, _ := store.LoadState()
	if want := now.Add(90 * time.Second); !saved.NextAllowed.Equal(want) {
		t.Fatalf("NextAllowed %v want %v", saved.NextAllowed, want)
	}
	if !saved.LastPush.At.IsZero() {
		t.Fatal("LastPush must stay unset")
	}
}

func TestPushFailureBacksOffAndDoubles(t *testing.T) {
	r := setup(t)
	st := r.state(t)
	r.srv.ForceStatus(500)
	at := now
	for _, want := range []time.Duration{60 * time.Second, 120 * time.Second} {
		_ = push.Run(context.Background(), r.cfg, st, r.client, false, at)
		saved, _ := store.LoadState()
		if !saved.NextAllowed.Equal(at.Add(want)) {
			t.Fatalf("NextAllowed %v want +%v", saved.NextAllowed, want)
		}
		at = saved.NextAllowed
	}
}

func TestPushKeepsStatusLineWrites(t *testing.T) {
	r := setup(t)
	st := r.state(t)
	// the status line writes a newer reading while the push is running
	newer := *st
	newer.Reading.Tools = map[string]contract.Tool{"claude": {ReadAt: now, Windows: []contract.Window{{ID: "5h", UsedPercent: 50}}}}
	_ = store.SaveState(&newer)
	if err := push.Run(context.Background(), r.cfg, st, r.client, false, now); err != nil {
		t.Fatal(err)
	}
	saved, _ := store.LoadState()
	if saved.Reading.Tools["claude"].Windows[0].UsedPercent != 50 {
		t.Fatal("status line write lost")
	}
}

func TestLockPreventsConcurrentPush(t *testing.T) {
	r := setup(t)
	st := r.state(t)
	// another process holds a fresh lock: Run does nothing
	lock := filepath.Join(os.Getenv("DEVLETS_CONFIG_DIR"), "push.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := push.Run(context.Background(), r.cfg, st, r.client, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.srv.Requests()); n != 1 { // only the box creation in setup
		t.Fatalf("pushed while locked: %d requests", n)
	}
	// a stale lock is taken over
	old := time.Now().Add(-2 * time.Minute)
	_ = os.Chtimes(lock, old, old)
	if err := push.Run(context.Background(), r.cfg, st, r.client, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.srv.Requests()); n != 2 {
		t.Fatalf("stale lock blocked: %d requests", n)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("lock not released")
	}
}

func TestStartDetachedSkipsWhenRecentOrLocked(t *testing.T) {
	setup(t)
	dir := os.Getenv("DEVLETS_CONFIG_DIR")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "push.lock"), nil, 0o600)
	if err := push.StartDetached("push"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "push.started")); !os.IsNotExist(err) {
		t.Fatal("spawned despite lock")
	}
	_ = os.Remove(filepath.Join(dir, "push.lock"))
	_ = os.WriteFile(filepath.Join(dir, "push.started"), nil, 0o600)
	if err := push.StartDetached("push"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "push.log")); !os.IsNotExist(err) {
		t.Fatal("spawned despite recent start")
	}
}
