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

func fakeProgram(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPreviousChainable(t *testing.T) {
	dir := t.TempDir()
	cmdFile := filepath.Join(dir, "notify.cmd")
	exeFile := filepath.Join(dir, "notify.exe")
	for _, f := range []string{cmdFile, exeFile} {
		if err := os.WriteFile(f, []byte("@echo off\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		argv []string
		want Refusal
	}{
		{"cmd file", []string{cmdFile}, RefuseBatch},
		{"cmd file with trailing space", []string{cmdFile + " "}, RefuseBatch},
		{"cmd file with trailing dot", []string{cmdFile + "."}, RefuseBatch},
		{"exe", []string{exeFile}, ChainOK},
		{"missing", []string{filepath.Join(dir, "nope-missing")}, RefuseMissing},
		{"cmd /c", []string{"cmd", "/c", "echo"}, RefuseShell},
		{"cmd.exe full path", []string{fakeProgram(t, "CMD.EXE"), "/c"}, RefuseShell},
		{"powershell -c", []string{fakeProgram(t, "powershell.exe"), "-c", "x"}, RefuseShell},
		{"pwsh -c", []string{fakeProgram(t, "pwsh.exe"), "-c", "x"}, RefuseShell},
	}
	for _, c := range cases {
		prev := "notify = [" + tomlQuote(c.argv[0]) + "]\n"
		if got, _ := checkArgv0(c.argv[0]); got != c.want {
			t.Errorf("%s: refusal = %q, want %q", c.name, got, c.want)
		}
		if (c.want == ChainOK) != (PreviousNote(prev) == "") {
			t.Errorf("%s: note %q does not match", c.name, PreviousNote(prev))
		}
	}
}

func TestPreviousNoteWording(t *testing.T) {
	dir := t.TempDir()
	cmdFile := filepath.Join(dir, "notify.cmd")
	if err := os.WriteFile(cmdFile, []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	note := func(p string) string { return PreviousNote("notify = [" + tomlQuote(p) + "]\n") }
	want := map[string]string{
		cmdFile:                    "Your previous Codex notify is a batch file (notify.cmd). Devlets won't pass Codex's messages to it, because Windows can't do that safely, so it won't run while Devlets is linked.",
		fakeProgram(t, "pwsh.exe"): "Your previous Codex notify runs through pwsh.exe, which would treat Codex's messages as commands. Devlets won't pass them on, so it won't run while Devlets is linked.",
		filepath.Join(dir, "codex-computer-use.exe"): "Your previous Codex notify (codex-computer-use.exe) wasn't found, so Devlets won't run it.",
	}
	for p, w := range want {
		if got := note(p); got != w {
			t.Errorf("note(%s) = %q, want %q", filepath.Base(p), got, w)
		}
	}
}

// cmd /c with a target that is not a batch file still lets cmd.exe read & in
// the message as a command separator.
func TestNotifyNeverChainsCmdShell(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	t.Setenv("DEVLETS_CHAINED", "")
	wd := t.TempDir()
	t.Chdir(wd)
	prev := "notify = [" + tomlQuote("cmd") + ", " + tomlQuote("/c") + ", " + tomlQuote("echo") + "]\n"
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
	if _, err := os.Stat(filepath.Join(wd, "pwned.txt")); err == nil {
		t.Error("pwned.txt exists: cmd /c ran the injected command")
	}
}
