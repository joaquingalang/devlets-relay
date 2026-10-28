package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

var fakeBin, helperBin string

func buildGo(dir, name, pkg string) string {
	out := filepath.Join(dir, name)
	if os.PathSeparator == '\\' {
		out += ".exe"
	}
	if b, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil {
		panic(string(b))
	}
	_ = exec.Command(out, "warm").Run() // first run of a fresh exe can be slow under antivirus
	return out
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "devlets-codex")
	if err != nil {
		panic(err)
	}
	fakeBin = buildGo(dir, "fakeappserver", "./fakeappserver")
	helperBin = buildGo(dir, "helper", "./testdata/helper")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func golden(t *testing.T, name string) string {
	t.Helper()
	p, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "codex", name))
	return p
}

func useFake(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("FAKE_CODEX_MODE", mode)
	old := readTimeout
	readTimeout = 5 * time.Second
	t.Cleanup(func() { readTimeout = old })
}

func TestClampsUsedPercent(t *testing.T) {
	in := `{"rateLimits":{"primary":{"usedPercent":104.2,"windowDurationMins":300,"resetsAt":1794852360},"secondary":{"usedPercent":-1,"windowDurationMins":10080,"resetsAt":1794852360}}}`
	tool, err := mapRateLimits([]byte(in))
	if err != nil || len(tool.Windows) != 2 || tool.Windows[0].UsedPercent != 100 || tool.Windows[1].UsedPercent != 0 {
		t.Fatalf("%v %+v", err, tool)
	}
}

func TestReadRateLimitsFake(t *testing.T) {
	useFake(t, "ok")
	tool, err := ReadRateLimits(context.Background(), fakeBin)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Plan != "plus" || len(tool.Windows) != 2 {
		t.Fatalf("%+v", tool)
	}
	p, s := tool.Windows[0], tool.Windows[1]
	if p.ID != "primary" || p.UsedPercent != 12.5 || p.Minutes != 300 || p.ResetsAt.Unix() != 1794852360 || p.ResetsAt.Location() != time.UTC {
		t.Fatalf("primary %+v", p)
	}
	if s.ID != "secondary" || s.UsedPercent != 100 || s.Minutes != 10080 {
		t.Fatalf("secondary %+v", s)
	}
	if time.Since(tool.ReadAt) > time.Minute {
		t.Fatalf("readAt %v", tool.ReadAt)
	}
}

func TestReadRateLimitsGolden(t *testing.T) {
	useFake(t, "ok")
	t.Setenv("FAKE_CODEX_RATELIMITS", golden(t, "codex-appserver-ratelimits-response.json"))
	tool, err := ReadRateLimits(context.Background(), fakeBin)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Plan != "free" || len(tool.Windows) != 1 {
		t.Fatalf("%+v", tool)
	}
	w := tool.Windows[0]
	if w.ID != "primary" || w.UsedPercent != 0 || w.Minutes != 43200 || w.ResetsAt.Unix() != 1794852360 {
		t.Fatalf("%+v", w)
	}
}

func TestAppServerTimeoutKeepsPrevious(t *testing.T) {
	useFake(t, "hang")
	readTimeout = 200 * time.Millisecond
	st := keptState()
	start := time.Now()
	err := Refresh(context.Background(), fakeBin, st)
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	assertKept(t, st)
}

func TestAppServerErrorKeepsPrevious(t *testing.T) {
	for _, mode := range []string{"error", "newshape"} {
		useFake(t, mode)
		st := keptState()
		if err := Refresh(context.Background(), fakeBin, st); err == nil {
			t.Fatalf("%s: want an error", mode)
		}
		assertKept(t, st)
	}
	st := keptState()
	if err := Refresh(context.Background(), filepath.Join(t.TempDir(), "missing"), st); err == nil {
		t.Fatal("missing binary: want an error")
	}
	assertKept(t, st)
}

func TestRefreshStoresReading(t *testing.T) {
	useFake(t, "ok")
	st := keptState()
	if err := Refresh(context.Background(), fakeBin, st); err != nil {
		t.Fatal(err)
	}
	if got := st.Reading.Tools["codex"]; got.Plan != "plus" || len(got.Windows) != 2 {
		t.Fatalf("%+v", got)
	}
}

// ---- notify hook ----

func codexHome(t *testing.T, content *string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	p := filepath.Join(dir, "config.toml")
	if content != nil {
		if err := os.WriteFile(p, []byte(*content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestNotifyHookUnhookRestoresBytes(t *testing.T) {
	cases := map[string]string{
		"one line":                          "model = \"gpt-5\"\nnotify = [\"/bin/x\", \"turn-ended\"]\n\n[tui]\nnotifications = true\n",
		"multi line":                        "model = \"gpt-5\"\nnotify = [\n  \"/bin/x\", # helper\n  \"turn-ended\",\n]\nsandbox = \"read-only\"\n\n[tui]\nx = 1\n",
		"table first":                       "[tui]\nnotifications = true\n",
		"notify only in a table":            "model = \"gpt-5\"\n\n[tui]\nnotify = [\"not\", \"top-level\"]\n",
		"no notify no table":                "model = \"gpt-5\"\n",
		"no final newline":                  "model = \"gpt-5\"",
		"notify at eof no newline":          "model = 1\nnotify = [\"a\"]",
		"crlf":                              "model = 1\r\nnotify = [\"a\",\r\n \"b\"]\r\n[tui]\r\n",
		"empty":                             "",
		"commented notify":                  "# notify = [\"a\", \"b\"]\nmodel = 1\n[tui]\n",
		"notify text in a multiline string": "note = \"\"\"\nnotify = [\"x\"]\n\"\"\"\n[tui]\n",
		"notify text in a literal multiline string": "note = '''\nnotify = [\"x\"]\n[other]\n'''\nmodel = 1\n",
		"multiline string before table":             "note = \"\"\"\n[not a table]\n\"\"\"\n[tui]\n",
		"quoted key":                                "model = 1\n\"notify\" = [\"/bin/x\", \"turn-ended\"]\n[tui]\n",
		"literal quoted key":                        "'notify'= [\"/bin/x\", \"turn-ended\"]\nmodel = 1\n",
	}
	for name, orig := range cases {
		t.Run(name, func(t *testing.T) {
			orig := orig
			p := codexHome(t, &orig)
			prev, err := Hook(`C:\bin\devlets.exe`)
			if err != nil {
				t.Fatal(err)
			}
			hooked, _ := os.ReadFile(p)
			if strings.Count(string(hooked), "codex-notify") != 1 || !strings.Contains(string(hooked), "devlets.exe") {
				t.Fatalf("hooked file:\n%s", hooked)
			}
			if strings.Contains(orig, "turn-ended") && strings.Contains(string(hooked), "turn-ended") {
				t.Fatalf("old notify still there:\n%s", hooked)
			}
			// our line must come before the first table header
			if i, j := strings.Index(string(hooked), "codex-notify"), strings.Index(string(hooked), "[tui]"); j >= 0 && i > j {
				t.Fatalf("notify after table:\n%s", hooked)
			}
			if err := Unhook(prev); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if string(got) != orig {
				t.Fatalf("not restored.\nwant %q\ngot  %q", orig, got)
			}
		})
	}
}

func TestHookMissingFileAndAlreadyHooked(t *testing.T) {
	p := codexHome(t, nil)
	prev, err := Hook("/usr/bin/devlets")
	if err != nil || prev != "" {
		t.Fatalf("%q %v", prev, err)
	}
	// a notify that is already ours (e.g. config.json was lost) is adopted:
	// hooked, no previous, re-pointed at this binary, never doubled
	prev, err = Hook("/new/place/devlets")
	if err != nil || prev != "" {
		t.Fatalf("adopt: %q %v", prev, err)
	}
	if b, _ := os.ReadFile(p); strings.Count(string(b), "codex-notify") != 1 || !strings.Contains(string(b), "place") {
		t.Fatalf("adopted file: %s", b)
	}
	if err := Unhook(""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "codex-notify") {
		t.Fatalf("still hooked: %s", b)
	}
	// Unhook with no file is a no-op
	codexHome(t, nil)
	if err := Unhook(""); err != nil {
		t.Fatal(err)
	}
}

func TestUnhookLeavesUserEdit(t *testing.T) {
	c := "notify = [\"theirs\"]\n"
	p := codexHome(t, &c)
	if err := Unhook("notify = [\"old\"]\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != c {
		t.Fatalf("%q", b)
	}
}

func TestNotifyChainsPrevious(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	payload := `{"type":"agent-turn-complete","last-assistant-message":"hi"}`
	prev := "notify = [" + tomlQuote(helperBin) + ", \"turn-ended\"]\n"
	cfg := &store.Config{Codex: store.Hook{Hooked: true, Previous: &prev}}

	var chained [][]string
	var pushes [][]string
	oldC, oldP := StartChain, StartPush
	StartChain = func(argv, env []string) error { chained = append(chained, argv); return nil }
	StartPush = func(args ...string) error { pushes = append(pushes, args); return nil }
	defer func() { StartChain, StartPush = oldC, oldP }()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if err := RunNotify([]string{payload}, cfg, now); err != nil {
		t.Fatal(err)
	}
	if len(chained) != 1 || len(chained[0]) != 3 || chained[0][0] != helperBin || chained[0][1] != "turn-ended" || chained[0][2] != payload {
		t.Fatalf("chained %q", chained)
	}
	if len(pushes) != 1 || strings.Join(pushes[0], " ") != "push --read-codex" {
		t.Fatalf("pushes %q", pushes)
	}
	// throttle: a second notify within 5 minutes starts no read
	if err := RunNotify([]string{payload}, cfg, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(pushes) != 1 || len(chained) != 2 {
		t.Fatalf("pushes %q chained %d", pushes, len(chained))
	}
	if err := RunNotify([]string{payload}, cfg, now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(pushes) != 2 {
		t.Fatalf("pushes %q", pushes)
	}
	// other event types: chained, no read
	if err := RunNotify([]string{`{"type":"other"}`}, cfg, now.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(pushes) != 2 || len(chained) != 4 {
		t.Fatalf("pushes %q chained %d", pushes, len(chained))
	}
}

func TestChainRealProcess(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("HELPER_OUT", out)
	if err := StartDetached([]string{helperBin, "turn-ended", `{"type":"x"}`}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(out); len(b) > 0 {
			if string(b) != "turn-ended\n{\"type\":\"x\"}" {
				t.Fatalf("%q", b)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("previous notify never ran")
}

func TestNotifyNilConfig(t *testing.T) {
	if err := RunNotify([]string{"{}"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestParseNotifyArgv(t *testing.T) {
	got := parseNotifyArgv("notify = [\n \"a b\", 'c\\d', # x\n \"e\\\\f\\\"\",\n]\n")
	want := []string{"a b", `c\d`, `e\f"`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
	if parseNotifyArgv("notify = nope") != nil {
		t.Fatal("garbage should give nil")
	}
}

func TestLiveAppServer(t *testing.T) {
	if os.Getenv("DEVLETS_LIVE_CODEX") != "1" {
		t.Skip("set DEVLETS_LIVE_CODEX=1 to read the real Codex rate limits")
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed")
	}
	tool, err := ReadRateLimits(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range tool.Windows {
		t.Logf("window %s used=%v minutes=%d resets=%s", w.ID, w.UsedPercent, w.Minutes, w.ResetsAt)
	}
	t.Logf("plan=%q windows=%d", tool.Plan, len(tool.Windows))
}

func keptState() *store.State {
	st := &store.State{}
	st.Reading.V = 1
	st.Reading.Tools = map[string]contract.Tool{"codex": {Plan: "kept"}}
	return st
}

func assertKept(t *testing.T, st *store.State) {
	t.Helper()
	if st.Reading.Tools["codex"].Plan != "kept" || len(st.Reading.Tools) != 1 {
		t.Fatalf("previous reading lost: %+v", st.Reading.Tools)
	}
}

func alive(pid string) bool {
	if runtime.GOOS == "windows" {
		out, _ := exec.Command("tasklist", "/FI", "PID eq "+pid, "/NH").Output()
		return strings.Contains(string(out), " "+pid+" ")
	}
	return exec.Command("kill", "-0", pid).Run() == nil
}

// A shim-style server: the process we start exits on its own, leaving a
// long-lived child holding the pipes. The child must not outlive the read.
func TestShimChildIsKilled(t *testing.T) {
	useFake(t, "shim")
	readTimeout = 500 * time.Millisecond
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("FAKE_CODEX_CHILDPID", pidFile)
	if _, err := ReadRateLimits(context.Background(), fakeBin); err == nil {
		t.Fatal("want an error")
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child never started: %v", err)
	}
	pid := strings.TrimSpace(string(b))
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		_ = exec.Command("taskkill", "/F", "/PID", pid).Run()
		t.Fatalf("child %s still running", pid)
	}
}

// A corrupt state.json is read as empty: the read is started (so Codex-only
// users are not stuck) but the corrupt file is not replaced just to record the
// attempt; the push replaces it once it has a real reading.
func TestRunNotifyUnreadableStateWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVLETS_CONFIG_DIR", dir)
	bad := filepath.Join(dir, "state.json")
	if err := os.WriteFile(bad, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	pushes := 0
	oldP := StartPush
	StartPush = func(...string) error { pushes++; return nil }
	defer func() { StartPush = oldP }()
	cfg := &store.Config{}
	if err := RunNotify([]string{`{"type":"agent-turn-complete"}`}, cfg, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bad); string(b) != "{broken" || pushes != 1 {
		t.Fatalf("state touched or read not started: %q pushes=%d", b, pushes)
	}
	// nil config: nothing at all
	codexHome(t, nil)
	if err := RunNotify([]string{`{"type":"agent-turn-complete"}`}, nil, time.Now()); err != nil || pushes != 1 {
		t.Fatalf("%v %d", err, pushes)
	}
}

func TestHookUnhookThroughSymlinkKeepsMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	real := filepath.Join(t.TempDir(), "config.toml")
	orig := "model = 1\nnotify = [\"a\"]\n"
	if err := os.WriteFile(real, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	prev, err := Hook("/opt/devlets")
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config.toml is no longer a symlink")
	}
	if err := Unhook(prev); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(real); string(b) != orig {
		t.Fatalf("not restored: %q", b)
	}
	if fi, _ := os.Stat(real); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestPreviousNoteChainableIsEmpty(t *testing.T) {
	prev := "notify = [" + tomlQuote(helperBin) + "]\n"
	if n := PreviousNote(prev); n != "" {
		t.Errorf("note = %q, want none", n)
	}
	if n := PreviousNote("notify = nope"); n != "" {
		t.Errorf("note for garbage = %q", n)
	}
}

// A second loop guard (RH13): the saved previous notify is devlets itself.
func TestNotifyDoesNotChainToItself(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	t.Setenv("DEVLETS_CHAINED", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	prev := "notify = [" + tomlQuote(self) + ", \"codex-notify\"]\n"
	cfg := &store.Config{Codex: store.Hook{Hooked: true, Previous: &prev}}
	called := 0
	oldC, oldP := StartChain, StartPush
	StartChain = func(argv, env []string) error { called++; return nil }
	StartPush = func(args ...string) error { return nil }
	defer func() { StartChain, StartPush = oldC, oldP }()
	if err := RunNotify([]string{`{"type":"other"}`}, cfg, time.Now()); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatal("chained to itself")
	}
	if n := PreviousNote(prev); !strings.Contains(n, "Devlets itself") {
		t.Errorf("note = %q", n)
	}
}

// RH13: Codex's computer-use wrapper calls devlets, which chains back to the wrapper.
func TestNotifyChainCannotLoop(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	t.Setenv("DEVLETS_CHAINED", "")
	payload := `{"type":"other"}`
	prev := "notify = [" + tomlQuote(helperBin) + "]\n"
	cfg := &store.Config{Codex: store.Hook{Hooked: true, Previous: &prev}}

	var calls [][]string
	var envs [][]string
	oldC, oldP := StartChain, StartPush
	StartChain = func(argv, env []string) error { calls = append(calls, argv); envs = append(envs, env); return nil }
	StartPush = func(args ...string) error { return nil }
	defer func() { StartChain, StartPush = oldC, oldP }()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if err := RunNotify([]string{payload}, cfg, now); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Join(calls[0], "|") != helperBin+"|"+payload {
		t.Fatalf("chained %q", calls)
	}
	found := false
	for _, e := range envs[0] {
		if e == "DEVLETS_CHAINED=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env lacks DEVLETS_CHAINED=1: %q", envs[0])
	}

	t.Setenv("DEVLETS_CHAINED", "1")
	if err := RunNotify([]string{payload}, cfg, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("a chained process chained again: %q", calls)
	}
}
