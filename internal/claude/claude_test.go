package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

var helperBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "devlets-helper")
	if err != nil {
		panic(err)
	}
	helperBin = filepath.Join(dir, "helper")
	if os.PathSeparator == '\\' {
		helperBin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", helperBin, "./testdata/helper").CombinedOutput(); err != nil {
		panic(string(out))
	}
	// First run of a fresh exe can take over a second under antivirus scans.
	_ = exec.Command(helperBin, "warm").Run()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "claude", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseGolden(t *testing.T) {
	tool, ok := ParseStatusLine(golden(t, "claude-statusline-with-rate-limits.json"))
	if !ok || len(tool.Windows) != 2 {
		t.Fatalf("ok=%v tool=%+v", ok, tool)
	}
	w := tool.Windows
	if w[0].ID != "five_hour" || w[0].UsedPercent != 4 || !w[0].ResetsAt.Equal(time.Unix(1792273200, 0)) || w[0].ResetsAt.Location() != time.UTC {
		t.Fatalf("five_hour %+v", w[0])
	}
	if w[1].ID != "seven_day" || w[1].UsedPercent != 53 || !w[1].ResetsAt.Equal(time.Unix(1792378800, 0)) {
		t.Fatalf("seven_day %+v", w[1])
	}
}

func TestOnlyFiveHour(t *testing.T) {
	tool, ok := ParseStatusLine(golden(t, "claude-statusline-only-five-hour.json"))
	if !ok || len(tool.Windows) != 1 || tool.Windows[0].ID != "five_hour" {
		t.Fatalf("ok=%v tool=%+v", ok, tool)
	}
}

func TestClampsUsedPercent(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":104.2,"resets_at":1792273200},"seven_day":{"used_percentage":-1,"resets_at":1792378800}}}`
	tool, ok := ParseStatusLine([]byte(in))
	if !ok || tool.Windows[0].UsedPercent != 100 || tool.Windows[1].UsedPercent != 0 {
		t.Fatalf("%v %+v", ok, tool)
	}
}

type env struct {
	cfg    *store.Config
	pushes [][]string
}

func setup(t *testing.T, previous *string) *env {
	t.Helper()
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	e := &env{cfg: &store.Config{Computer: "PC", Claude: store.Hook{Hooked: true, Previous: previous}}}
	StartPush = func(args ...string) error { e.pushes = append(e.pushes, args); return nil }
	t.Cleanup(func() { StartPush = nil })
	return e
}

var now = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

func prevCmd(args string) *string {
	raw, _ := json.Marshal(map[string]any{"type": "command", "command": `"` + filepath.ToSlash(helperBin) + `"` + args, "padding": 0})
	s := string(raw)
	return &s
}

func TestStatusLineWithoutRateLimits(t *testing.T) {
	e := setup(t, prevCmd(""))
	var out bytes.Buffer
	if err := RunStatusLine(bytes.NewReader(golden(t, "claude-statusline-without-rate-limits.json")), &out, e.cfg, now); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "PREV[") {
		t.Fatalf("chained output missing: %q", out.String())
	}
	st, _ := store.LoadState()
	if len(st.Reading.Tools) != 0 || len(e.pushes) != 0 {
		t.Fatalf("wrote reading or pushed: %+v %v", st, e.pushes)
	}
}

func TestChainPassesStdinAndPrintsOutput(t *testing.T) {
	e := setup(t, prevCmd(""))
	var out bytes.Buffer
	if err := RunStatusLine(bytes.NewReader(golden(t, "claude-statusline-with-rate-limits.json")), &out, e.cfg, now); err != nil {
		t.Fatal(err)
	}
	if out.String() != "PREV[Opus 5.5]" {
		t.Fatalf("got %q", out.String())
	}
	st, _ := store.LoadState()
	c, ok := st.Reading.Tools["claude"]
	if !ok || len(c.Windows) != 2 || !c.ReadAt.Equal(now) || st.Reading.Computer != "PC" || !reflect.DeepEqual(st.Reading.Hooked, []string{"claude"}) {
		t.Fatalf("state %+v", st.Reading)
	}
	if len(e.pushes) != 1 || e.pushes[0][0] != "push" {
		t.Fatalf("pushes %v", e.pushes)
	}
}

func TestNoPreviousPrintsNothing(t *testing.T) {
	e := setup(t, nil)
	var out bytes.Buffer
	_ = RunStatusLine(bytes.NewReader(golden(t, "claude-statusline-with-rate-limits.json")), &out, e.cfg, now)
	if out.Len() != 0 {
		t.Fatalf("got %q", out.String())
	}
}

func TestNotDueDoesNotPush(t *testing.T) {
	e := setup(t, nil)
	in := golden(t, "claude-statusline-with-rate-limits.json")
	_ = RunStatusLine(bytes.NewReader(in), &bytes.Buffer{}, e.cfg, now)
	st, _ := store.LoadState()
	st.LastPush = store.Pushed{At: now, Reading: st.Reading}
	_ = store.SaveState(st)
	e.pushes = nil
	_ = RunStatusLine(bytes.NewReader(in), &bytes.Buffer{}, e.cfg, now.Add(time.Minute))
	if len(e.pushes) != 0 {
		t.Fatalf("pushed %v", e.pushes)
	}
}

func TestChainTimeout(t *testing.T) {
	e := setup(t, prevCmd(" sleep"))
	var out bytes.Buffer
	start := time.Now()
	if err := RunStatusLine(bytes.NewReader(golden(t, "claude-statusline-with-rate-limits.json")), &out, e.cfg, now); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	if strings.Contains(out.String(), "late") {
		t.Fatalf("got %q", out.String())
	}
	if st, _ := store.LoadState(); len(st.Reading.Tools) != 1 {
		t.Fatal("reading not recorded after timeout")
	}
}

func tempSettings(t *testing.T) string {
	d := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", d)
	return filepath.Join(d, "settings.json")
}

func TestHookUnhookRestoresBytes(t *testing.T) {
	cases := map[string]string{
		"previous with padding": "{\r\n\t\"model\":   \"opus\",\r\n\t\"statusLine\" :  {\"type\": \"command\",   \"command\":\"node x.js\", \"padding\":0},\r\n  \"env\": {\"A\":\"1\"}\r\n}\r\n",
		"previous last key":     "{\n  \"a\": [1,2,   3],\n  \"statusLine\": {\"type\":\"command\",\"command\":\"echo hi\"}\n}",
		"previous string":       "{ \"statusLine\":   \"weird\"  , \"b\":2 }\n",
	}
	for name, orig := range cases {
		t.Run(name, func(t *testing.T) {
			p := tempSettings(t)
			os.WriteFile(p, []byte(orig), 0o600)
			prev, err := Hook("/usr/local/bin/devlets")
			if err != nil || prev == nil {
				t.Fatalf("prev=%s err=%v", prev, err)
			}
			hooked, _ := os.ReadFile(p)
			if !strings.Contains(string(hooked), "/usr/local/bin/devlets claude-statusline") {
				t.Fatalf("not hooked: %s", hooked)
			}
			if err := Unhook(prev); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if string(got) != orig {
				t.Fatalf("bytes differ:\n%q\n%q", got, orig)
			}
		})
	}
}

func TestHookUnhookAbsent(t *testing.T) {
	for name, orig := range map[string]string{
		"keys":    "{\n  \"model\": \"opus\",\n  \"env\": {\"A\": \"1\"}\n}\n",
		"empty":   "{}",
		"missing": "",
	} {
		t.Run(name, func(t *testing.T) {
			p := tempSettings(t)
			if orig != "" {
				os.WriteFile(p, []byte(orig), 0o600)
			}
			prev, err := Hook("/opt/devlets")
			if err != nil || prev != nil {
				t.Fatalf("prev=%s err=%v", prev, err)
			}
			if err := Unhook(prev); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if orig == "" {
				orig = "{}"
			}
			var a, b any
			if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(orig), &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatalf("not JSON-equal: %s vs %s", got, orig)
			}
		})
	}
}

func TestHookKeepsPadding(t *testing.T) {
	p := tempSettings(t)
	os.WriteFile(p, []byte(`{"statusLine":{"type":"command","command":"old","padding":2}}`), 0o600)
	abs, _ := filepath.Abs("/opt/my tools/devlets")
	wantCmd := `"` + filepath.ToSlash(abs) + `" claude-statusline`
	if _, err := Hook("/opt/my tools/devlets"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	var s struct {
		StatusLine struct {
			Type, Command string
			Padding       int
		}
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.StatusLine.Padding != 2 || s.StatusLine.Type != "command" || s.StatusLine.Command != wantCmd {
		t.Fatalf("%+v", s)
	}
}
