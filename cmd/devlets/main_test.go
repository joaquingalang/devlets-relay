package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb); code != 0 {
		t.Fatalf("code %d", code)
	}
	if out.String() != "dev\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"nope"}, &out, &errb); code != 2 {
		t.Fatalf("code %d", code)
	}
}

func TestWiringSetsStartersAndUnhook(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", filepath.Join(t.TempDir(), "d"))
	claude.StartPush, codex.StartPush, push.UnhookClaude = nil, nil, nil
	var out, errb bytes.Buffer
	runWith([]string{"claude-statusline"}, strings.NewReader("{}"), &out, &errb)
	runWith([]string{"codex-notify", `{"type":"x"}`}, strings.NewReader(""), &out, &errb)
	runWith([]string{"push"}, strings.NewReader(""), &out, &errb) // not linked: exits early
	if claude.StartPush == nil || codex.StartPush == nil {
		t.Fatal("StartPush not wired")
	}
	// push.UnhookClaude is wired by the push command once it is linked (below).
}

func TestPush404RestoresAndUnlinks(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", filepath.Join(t.TempDir(), "d"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("DEVLETS_API", "")
	push.UnhookClaude = nil
	srv := mailboxtest.New()
	defer srv.Close()
	key, _ := crypto.NewKey()
	client := &mailbox.Client{Base: srv.URL, Version: "test"}
	id, wt, err := client.Create(context.Background(), crypto.TokenHash(crypto.ReadToken(key)))
	if err != nil {
		t.Fatal(err)
	}

	settings := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	before := []byte("{\n  \"model\": \"x\",\n  \"statusLine\": {\"type\": \"command\", \"command\": \"old\"}\n}\n")
	if err := os.WriteFile(settings, before, 0o600); err != nil {
		t.Fatal(err)
	}
	prev, err := claude.Hook("/opt/devlets")
	if err != nil {
		t.Fatal(err)
	}
	var prevStr *string
	if prev != nil {
		s := string(prev)
		prevStr = &s
	}
	cfg := &store.Config{APIBase: srv.URL, BoxID: id, WriteToken: wt, Key: crypto.B64(key), Computer: "PC",
		Claude: store.Hook{Hooked: true, Previous: prevStr}}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	st := &store.State{Reading: contract.Reading{V: 1, Tools: map[string]contract.Tool{"claude": {Windows: []contract.Window{{ID: "5h"}}}}}}
	if err := store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	srv.DeleteBox(id)

	var out, errb bytes.Buffer
	if code := run([]string{"push"}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if push.UnhookClaude == nil {
		t.Fatal("push.UnhookClaude not wired")
	}
	if b, _ := os.ReadFile(settings); !bytes.Equal(b, before) {
		t.Fatalf("settings not restored: %q", b)
	}
	if _, err := store.LoadConfig(); !errors.Is(err, store.ErrNotLinked) {
		t.Fatalf("want ErrNotLinked, got %v", err)
	}
}

func TestPushIgnoresDevletsAPI(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", filepath.Join(t.TempDir(), "d"))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("DEVLETS_API", "http://127.0.0.1:1") // unreachable: must not be used
	srv := mailboxtest.New()
	defer srv.Close()
	key, _ := crypto.NewKey()
	client := &mailbox.Client{Base: srv.URL, Version: "test"}
	id, wt, err := client.Create(context.Background(), crypto.TokenHash(crypto.ReadToken(key)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &store.Config{APIBase: srv.URL, BoxID: id, WriteToken: wt, Key: crypto.B64(key), Computer: "PC"}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	st := &store.State{Reading: contract.Reading{V: 1, Tools: map[string]contract.Tool{"claude": {Windows: []contract.Window{{ID: "5h"}}}}}}
	if err := store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"push"}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if _, code := srv.PhoneGet(id, crypto.ReadToken(key)); code != 200 {
		t.Fatalf("reading did not reach the saved API base: %d", code)
	}
}

func TestHookEntryPointsSurvivePanics(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", filepath.Join(t.TempDir(), "d"))
	oldS, oldN := runStatusLine, runNotify
	runStatusLine = func(io.Reader, io.Writer, *store.Config, time.Time) error { panic("boom") }
	runNotify = func([]string, *store.Config, time.Time) error { panic("boom") }
	defer func() { runStatusLine, runNotify = oldS, oldN }()
	for _, args := range [][]string{{"claude-statusline"}, {"codex-notify", "{}"}} {
		var out, errb bytes.Buffer
		if code := runWith(args, strings.NewReader("{}"), &out, &errb); code != 0 || out.Len() != 0 {
			t.Errorf("%s: code %d out %q", args[0], code, out.String())
		}
	}
}

func TestPushWithCorruptStateRuns(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", filepath.Join(t.TempDir(), "d"))
	if err := store.SaveConfig(&store.Config{APIBase: "http://127.0.0.1:1", BoxID: "b", Key: crypto.B64(make([]byte, 32))}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(os.Getenv("DEVLETS_CONFIG_DIR"), "state.json"), []byte("{broken"), 0o600)
	var out, errb bytes.Buffer
	if code := run([]string{"push"}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
}

func TestUsageHidesInternalPush(t *testing.T) {
	var out, errb bytes.Buffer
	run([]string{"help"}, &out, &errb)
	if strings.Contains(out.String(), "push") {
		t.Fatalf("usage lists the internal push command:\n%s", out.String())
	}
}
