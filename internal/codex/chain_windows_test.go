//go:build windows

package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

// S1: Codex's message goes to a batch file as an argument, and cmd.exe would
// run what is inside it.
func TestNotifyNeverChainsBatchFile(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	t.Setenv("DEVLETS_CHAINED", "")
	dir := t.TempDir()
	bat := filepath.Join(dir, "notify.cmd")
	body := "@echo off\r\necho ran> \"%~dp0ran.txt\"\r\n"
	if err := os.WriteFile(bat, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := "notify = [" + tomlQuote(bat) + "]\n"
	cfg := &store.Config{Codex: store.Hook{Hooked: true, Previous: &prev}}

	oldC, oldP := StartChain, StartPush
	StartChain = StartDetached
	StartPush = func(args ...string) error { return nil }
	defer func() { StartChain, StartPush = oldC, oldP }()

	payload := `{"type":"agent-turn-complete","last-assistant-message":"done & echo INJECTED> pwned.txt"}`
	if err := RunNotify([]string{payload}, cfg, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	wd, _ := os.Getwd()
	for _, f := range []string{filepath.Join(dir, "ran.txt"), filepath.Join(dir, "pwned.txt"), filepath.Join(wd, "pwned.txt"), "pwned.txt"} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s exists: the batch file ran", f)
			os.Remove(f)
		}
	}
}
