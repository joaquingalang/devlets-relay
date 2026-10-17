package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
