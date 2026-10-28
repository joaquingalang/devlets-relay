package link

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/crypto"
	"github.com/joaquingalang/devlets-relay/internal/mailbox"
	"github.com/joaquingalang/devlets-relay/internal/mailbox/mailboxtest"
	"github.com/joaquingalang/devlets-relay/internal/push"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

type env struct {
	srv    *mailboxtest.Server
	claude string // settings.json
	codex  string // config.toml
}

// phoneUA is what mailboxtest sends for the requests the test plays as the phone.
const phoneUA = "mailboxtest-phone"

const claudeBefore = `{"statusLine":{"type":"command","command":"old-line"},"theme":"dark"}`
const codexBefore = "notify = [\"old\"]\nmodel = \"x\"\n"

func setup(t *testing.T) *env {
	t.Helper()
	cfgDir, claudeDir, codexDir := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("DEVLETS_CONFIG_DIR", cfgDir)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CODEX_HOME", codexDir)
	e := &env{srv: mailboxtest.New(), claude: filepath.Join(claudeDir, "settings.json"), codex: filepath.Join(codexDir, "config.toml")}
	t.Cleanup(e.srv.Close)
	if err := os.WriteFile(e.claude, []byte(claudeBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.codex, []byte(codexBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	oldExe, oldCodex, oldPoll := executable, readCodex, pollEvery
	exe := filepath.Join(t.TempDir(), "devlets")
	executable = func() (string, error) { return exe, nil }
	readCodex = func(ctx context.Context) (contract.Tool, error) {
		return contract.Tool{ReadAt: time.Now().UTC(), Windows: []contract.Window{{ID: "5h", UsedPercent: 7, ResetsAt: time.Now().Add(time.Hour).UTC()}}}, nil
	}
	pollEvery = 10 * time.Millisecond
	t.Cleanup(func() { executable, readCodex, pollEvery = oldExe, oldCodex, oldPoll })
	return e
}

func (e *env) opts(yes bool) Options {
	return Options{Yes: yes, Terminal: true, API: e.srv.URL, HelloTimeout: 5 * time.Second,
		Detect: func() (bool, bool) { return true, true }}
}

// phone waits until the CLI has pushed (so its config is final and no longer
// being rewritten; reading it earlier would block Windows renames), then posts
// a hello as the phone would.
func (e *env) phone(name string) {
	n0 := len(e.puts())
	go func() {
		for i := 0; i < 1000; i++ {
			if len(e.puts()) > n0 {
				if cfg, err := store.LoadConfig(); err == nil {
					e.hello(cfg, name)
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func (e *env) hello(cfg *store.Config, name string) {
	key, _ := crypto.UnB64(cfg.Key)
	pt, _ := json.Marshal(map[string]string{"phone": name})
	envl, _ := crypto.Seal(key, cfg.BoxID, crypto.Version, pt)
	e.srv.PhoneHello(cfg.BoxID, crypto.ReadToken(key), envl)
}

// lateHello posts another hello after the CLI has started waiting.
func (e *env) lateHello(name string) {
	go func() {
		time.Sleep(150 * time.Millisecond)
		if cfg, err := store.LoadConfig(); err == nil {
			e.hello(cfg, name)
		}
	}()
}

func (e *env) puts() []mailboxtest.RecordedRequest {
	var out []mailboxtest.RecordedRequest
	for _, r := range e.srv.Requests() {
		if r.Method == "PUT" {
			out = append(out, r)
		}
	}
	return out
}

func (e *env) deleted(boxID string) bool {
	for _, r := range e.srv.Requests() {
		if r.Method == "DELETE" && strings.HasSuffix(r.Path, boxID) {
			return true
		}
	}
	return false
}

func decryptPut(t *testing.T, r mailboxtest.RecordedRequest) contract.Reading {
	t.Helper()
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	var envl crypto.Envelope
	if err := json.Unmarshal(r.Body, &envl); err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.UnB64(cfg.Key)
	pt, err := crypto.Open(key, cfg.BoxID, envl)
	if err != nil {
		t.Fatal(err)
	}
	var rd contract.Reading
	if err := json.Unmarshal(pt, &rd); err != nil {
		t.Fatal(err)
	}
	return rd
}

func linkOnce(t *testing.T, e *env) *store.Config {
	t.Helper()
	e.phone("Pixel 8")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, e.opts(false)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLinkFlow(t *testing.T) {
	e := setup(t)
	e.phone("Pixel 8")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, e.opts(false)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	s := out.String()
	t.Log("\n" + s)
	if !strings.Contains(s, "Send Claude usage? This adds Devlets to your Claude Code status line. Your current status line keeps working. [Y/n]") ||
		!strings.Contains(s, "Send Codex usage? This runs Devlets after each Codex reply. [Y/n]") {
		t.Errorf("questions missing:\n%s", s)
	}
	if !strings.Contains(s, "or open it with your phone's camera\n") || strings.Contains(s, "camera:") {
		t.Errorf("camera line missing")
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "✓ Linked to Pixel 8") {
		t.Errorf("output must end with the success line:\n%s", s)
	}
	puts := e.puts()
	if len(puts) != 1 {
		t.Fatalf("want exactly one push, got %d", len(puts))
	}
	rd := decryptPut(t, puts[0])
	if strings.Join(rd.Hooked, ",") != "claude,codex" {
		t.Errorf("hooked = %v", rd.Hooked)
	}
	if _, ok := rd.Tools["codex"]; !ok || rd.Computer == "" {
		t.Errorf("reading lacks codex tool or computer: %+v", rd)
	}
	// the one push came before the first hello poll, so before the QR
	reqs := e.srv.Requests()
	putAt, pollAt := -1, -1
	for i, r := range reqs {
		if r.Method == "PUT" && putAt < 0 {
			putAt = i
		}
		if r.Method == "GET" && strings.HasSuffix(r.Path, "/hello") && pollAt < 0 {
			pollAt = i
		}
	}
	if putAt < 0 || pollAt < putAt {
		t.Errorf("push must come before waiting for the hello: put=%d poll=%d", putAt, pollAt)
	}
	if strings.Index(s, "Linked") < strings.Index(s, "camera") {
		t.Errorf("QR must be printed before the success line")
	}
	cfg, err := store.LoadConfig()
	if err != nil || !cfg.Claude.Hooked || !cfg.Codex.Hooked || cfg.Claude.Previous == nil || cfg.Codex.Previous == nil {
		t.Errorf("config not saved with hooks: %+v %v", cfg, err)
	}
	if b, _ := os.ReadFile(e.claude); !strings.Contains(string(b), "claude-statusline") {
		t.Errorf("claude not hooked: %s", b)
	}
	if st, _ := store.LoadState(); st.LastPush.At.IsZero() || st.Reading.Tools["codex"].Windows == nil {
		t.Errorf("state not saved after the push: %+v", st)
	}
}

func TestLinkAnswers(t *testing.T) {
	e := setup(t)
	e.phone("P")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("n\n\n"), &out, e.opts(false)); err != nil {
		t.Fatal(err)
	}
	cfg, _ := store.LoadConfig()
	if cfg.Claude.Hooked || !cfg.Codex.Hooked {
		t.Errorf("n then empty: want claude off, codex on: %+v", cfg)
	}
	if b, _ := os.ReadFile(e.claude); string(b) != claudeBefore {
		t.Errorf("claude settings changed on a no")
	}
}

func TestLinkFlags(t *testing.T) {
	e := setup(t)
	e.phone("P")
	o := e.opts(true)
	o.NoCodex = true
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader(""), &out, o); err != nil {
		t.Fatal(err)
	}
	cfg, _ := store.LoadConfig()
	if !cfg.Claude.Hooked || cfg.Codex.Hooked {
		t.Errorf("--yes --no-codex: %+v", cfg)
	}
	if strings.Contains(out.String(), "Send Codex usage?") {
		t.Errorf("must not ask about a skipped tool")
	}
	if b, _ := os.ReadFile(e.codex); string(b) != codexBefore {
		t.Errorf("codex config touched")
	}
}

func TestLinkURL(t *testing.T) {
	key := bytes.Repeat([]byte{0xfb, 0x01, 0x7f}, 11)[:32]
	u := URL("b_abc123", key)
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Host != "getdevlets.com" || p.Path != "/l" {
		t.Fatalf("bad url %q", u)
	}
	frag, err := url.ParseQuery(p.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	if frag.Get("b") != "b_abc123" {
		t.Errorf("box id: %q", frag.Get("b"))
	}
	got, err := crypto.UnB64(frag.Get("k"))
	if err != nil || !bytes.Equal(got, key) {
		t.Errorf("key did not round-trip")
	}
	if strings.HasSuffix(frag.Get("k"), "=") {
		t.Errorf("key must be unpadded")
	}
}

func TestRelinkShowCodeAgain(t *testing.T) {
	e := setup(t)
	first := linkOnce(t, e)
	e.lateHello("Galaxy S24")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("1\n"), &out, e.opts(false)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	second, _ := store.LoadConfig()
	if second.BoxID != first.BoxID || second.Key != first.Key {
		t.Errorf("show again must keep the box and key")
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "✓ Linked to Galaxy S24") {
		t.Errorf("should wait for the NEW hello:\n%s", out.String())
	}
	if len(e.puts()) != 1 {
		t.Errorf("show again must not push, got %d", len(e.puts()))
	}
}

func TestRelinkYesShowsCodeAgain(t *testing.T) {
	e := setup(t)
	first := linkOnce(t, e)
	e.lateHello("Tablet")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader(""), &out, e.opts(true)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if second, _ := store.LoadConfig(); second.BoxID != first.BoxID {
		t.Errorf("--yes must show the code again")
	}
}

func TestRelinkCancel(t *testing.T) {
	e := setup(t)
	first := linkOnce(t, e)
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("3\n"), &out, e.opts(false)); err != nil {
		t.Fatal(err)
	}
	if second, _ := store.LoadConfig(); second.BoxID != first.BoxID || e.deleted(first.BoxID) {
		t.Errorf("cancel must change nothing")
	}
}

func TestRelinkStartOver(t *testing.T) {
	e := setup(t)
	first := linkOnce(t, e)
	var out bytes.Buffer
	// "2" = start over, then the two tool questions. The phone helper reads the
	// config, so it must see the NEW key: post the hello once the box id changes.
	e.phone("New Phone")
	if err := Run(context.Background(), strings.NewReader("2\ny\ny\n"), &out, e.opts(false)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	second, err := store.LoadConfig()
	if err != nil || second.BoxID == first.BoxID || second.Key == first.Key {
		t.Fatalf("want a new box and key, got %v / %v", second, err)
	}
	if !e.deleted(first.BoxID) {
		t.Errorf("old box was not DELETEd")
	}
	if second.Claude.Previous == nil || !strings.Contains(*second.Claude.Previous, "old-line") {
		t.Errorf("never hook twice: previous must stay the user's own line: %v", second.Claude.Previous)
	}
	if second.Codex.Previous == nil || !strings.Contains(*second.Codex.Previous, "old") {
		t.Errorf("codex previous lost: %v", second.Codex.Previous)
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "✓ Linked to New Phone") {
		t.Errorf("output: %s", out.String())
	}
}

func TestUnlinkRestoresAndDeletes(t *testing.T) {
	e := setup(t)
	cfg := linkOnce(t, e)
	var out bytes.Buffer
	if err := Unlink(context.Background(), &out, Options{API: e.srv.URL}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Unlinked. Your Claude Code and Codex settings are back as they were.") {
		t.Errorf("output: %s", out.String())
	}
	if b, _ := os.ReadFile(e.claude); string(b) != claudeBefore {
		t.Errorf("claude not restored: %s", b)
	}
	if b, _ := os.ReadFile(e.codex); string(b) != codexBefore {
		t.Errorf("codex not restored: %s", b)
	}
	if _, err := store.LoadConfig(); err != store.ErrNotLinked {
		t.Errorf("config should be gone: %v", err)
	}
	if !e.deleted(cfg.BoxID) {
		t.Errorf("box not deleted")
	}
}

func TestUnlinkKeepsConfigWhenRestoreFails(t *testing.T) {
	e := setup(t)
	linkOnce(t, e)
	if err := os.WriteFile(e.claude, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Unlink(context.Background(), &out, Options{API: e.srv.URL}); err == nil {
		t.Fatal("want an error when settings cannot be restored")
	}
	if !strings.Contains(out.String(), e.claude) {
		t.Errorf("message must name the file: %s", out.String())
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatalf("config must be kept: %v", err)
	}
	if !cfg.Claude.Hooked {
		t.Errorf("claude is still hooked and must say so")
	}
	if cfg.Codex.Hooked {
		t.Errorf("codex was restored; hooked must be false")
	}
	if strings.Contains(out.String(), "Unlinked.") {
		t.Errorf("must not claim success")
	}
}

func TestStatusNotLinked(t *testing.T) {
	setup(t)
	var out bytes.Buffer
	if err := Status(&out, time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "Not linked. Run devlets link." {
		t.Errorf("got %q", out.String())
	}
}

func TestStatusLinked(t *testing.T) {
	e := setup(t)
	cfg := linkOnce(t, e)
	st, _ := store.LoadState()
	now := time.Now().UTC()
	st.Reading.Tools = map[string]contract.Tool{"codex": {ReadAt: now.Add(-5 * time.Minute)}}
	st.LastPush = store.Pushed{At: now.Add(-2 * time.Minute)}
	if err := store.SaveState(st); err != nil {
		t.Fatal(err)
	}
	called := false
	oldHook := AfterStatus
	AfterStatus = func(io.Writer) { called = true }
	defer func() { AfterStatus = oldHook }()
	var out bytes.Buffer
	if err := Status(&out, now); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	t.Log("\n" + s)
	for _, want := range []string{cfg.Computer, cfg.BoxID[len(cfg.BoxID)-4:], "Claude Code", "Codex", "hooked", "5 min ago", "2 min ago"} {
		if !strings.Contains(s, want) {
			t.Errorf("status lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, cfg.BoxID) || strings.Contains(s, cfg.Key) || strings.Contains(s, cfg.WriteToken) {
		t.Errorf("status leaks secrets:\n%s", s)
	}
	if !called {
		t.Errorf("AfterStatus hook not called")
	}
}

func TestKeyNeverSent(t *testing.T) {
	e := setup(t)
	cfg := linkOnce(t, e)
	key, _ := crypto.UnB64(cfg.Key)
	readToken := crypto.ReadToken(key)
	rtRaw, _ := crypto.UnB64(readToken)

	st, _ := store.LoadState()
	client := &mailbox.Client{Base: e.srv.URL, Version: "test"}
	push.Log = io.Discard
	if err := push.Run(context.Background(), cfg, st, client, false, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := Unlink(context.Background(), io.Discard, Options{API: e.srv.URL}); err != nil {
		t.Fatal(err)
	}
	needles := map[string][]byte{
		"key raw": key, "key b64url": []byte(crypto.B64(key)), "key b64": []byte(base64.StdEncoding.EncodeToString(key)),
		"key hex":    []byte(hex.EncodeToString(key)),
		"read token": []byte(readToken), "read token raw": rtRaw,
		"read token b64": []byte(base64.StdEncoding.EncodeToString(rtRaw)),
		"read token hex": []byte(hex.EncodeToString(rtRaw)),
	}
	n := 0
	for _, r := range e.srv.Requests() {
		if r.Header.Get("User-Agent") == phoneUA {
			continue // the test phone's own calls, marked explicitly by mailboxtest
		}
		n++
		var all bytes.Buffer
		all.WriteString(r.Method + " " + r.Path + "\n")
		for k, vs := range r.Header {
			for _, v := range vs {
				all.WriteString(k + ": " + v + "\n")
			}
		}
		all.Write(r.Body)
		for name, nd := range needles {
			if bytes.Contains(all.Bytes(), nd) {
				t.Errorf("%s %s leaks %s", r.Method, r.Path, name)
			}
		}
	}
	if n < 5 { // create, put, hello poll, put (push), delete
		t.Errorf("only %d CLI requests recorded", n)
	}
}

func TestLinkPrintsNoPlainKey(t *testing.T) {
	e := setup(t)
	e.phone("P")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, e.opts(false)); err != nil {
		t.Fatal(err)
	}
	cfg, _ := store.LoadConfig()
	key, _ := crypto.UnB64(cfg.Key)
	for name, secret := range map[string]string{"key": crypto.B64(key), "read token": crypto.ReadToken(key), "write token": cfg.WriteToken, "box id": cfg.BoxID} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("link output contains the %s", name)
		}
	}
}

func posts(e *env) int {
	n := 0
	for _, r := range e.srv.Requests() {
		if r.Method == "POST" {
			n++
		}
	}
	return n
}

func TestLinkNothingToLink(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    func(*env) Options
		in   string
	}{
		{"none detected", func(e *env) Options {
			o := e.opts(false)
			o.Detect = func() (bool, bool) { return false, false }
			return o
		}, ""},
		{"both declined", func(e *env) Options { return e.opts(false) }, "n\nn\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			codexRead := false
			readCodex = func(ctx context.Context) (contract.Tool, error) { codexRead = true; return contract.Tool{}, nil }
			var out bytes.Buffer
			err := Run(context.Background(), strings.NewReader(tc.in), &out, tc.o(e))
			if !errors.Is(err, ErrReported) {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(out.String(), "Neither Claude Code nor Codex is set up to send usage. Nothing to link.") {
				t.Errorf("output: %s", out.String())
			}
			if posts(e) != 0 || codexRead {
				t.Errorf("must not read Codex or create a box (posts=%d, read=%v)", posts(e), codexRead)
			}
			if _, err := store.LoadConfig(); err != store.ErrNotLinked {
				t.Errorf("no config expected")
			}
		})
	}
}

func TestLinkBothHooksFailDeletesBox(t *testing.T) {
	e := setup(t)
	if err := os.WriteFile(e.claude, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.codex); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(e.codex, 0o700); err != nil { // reading a directory as config.toml fails
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, e.opts(false))
	if !errors.Is(err, ErrReported) || !strings.Contains(out.String(), "Neither Claude Code nor Codex is set up to send usage. Nothing to link.") {
		t.Fatalf("err=%v out=%s", err, out.String())
	}
	if posts(e) != 1 {
		t.Fatalf("want one box created, got %d", posts(e))
	}
	deleted := false
	for _, r := range e.srv.Requests() {
		if r.Method == "DELETE" {
			deleted = true
		}
	}
	if !deleted || len(e.puts()) != 0 {
		t.Errorf("box must be deleted and nothing pushed (deleted=%v puts=%d)", deleted, len(e.puts()))
	}
	if _, err := store.LoadConfig(); err != store.ErrNotLinked {
		t.Errorf("config must be gone")
	}
}

func TestLinkHelloTimeoutKeepsLink(t *testing.T) {
	e := setup(t)
	o := e.opts(false)
	o.HelloTimeout = 150 * time.Millisecond
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, o)
	if !errors.Is(err, ErrReported) {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "Run devlets link again within 7 days to show the code.") {
		t.Errorf("output: %s", out.String())
	}
	if _, err := store.LoadConfig(); err != nil {
		t.Errorf("link must stay: %v", err)
	}
}

func TestLinkCtrlCSaysSameLine(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	waitingHook = cancel
	defer func() { waitingHook = nil }()
	var out bytes.Buffer
	err := Run(ctx, strings.NewReader("y\ny\n"), &out, e.opts(false))
	if !errors.Is(err, ErrReported) || !strings.Contains(out.String(), "Run devlets link again within 7 days to show the code.") || strings.Contains(out.String(), "canceled") {
		t.Errorf("err=%v out=%s", err, out.String())
	}
}

func TestShowAgainOnRemovedBox(t *testing.T) {
	e := setup(t)
	first := linkOnce(t, e)
	e.srv.DeleteBox(first.BoxID)
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("1\n"), &out, e.opts(false))
	if !errors.Is(err, ErrReported) || !strings.Contains(out.String(), "This link was removed from the phone. Run devlets link to set it up again.") {
		t.Fatalf("err=%v out=%s", err, out.String())
	}
	if _, err := store.LoadConfig(); err != store.ErrNotLinked {
		t.Errorf("files must be removed (BR12)")
	}
	if b, _ := os.ReadFile(e.claude); string(b) != claudeBefore {
		t.Errorf("claude not restored: %s", b)
	}
	if b, _ := os.ReadFile(e.codex); string(b) != codexBefore {
		t.Errorf("codex not restored: %s", b)
	}
}

func TestUnlinkKeepsConfigWhenCodexRestoreFails(t *testing.T) {
	e := setup(t)
	linkOnce(t, e)
	if err := os.Remove(e.codex); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(e.codex, 0o700); err != nil { // a directory cannot be read as config.toml
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Unlink(context.Background(), &out, Options{API: e.srv.URL}); err == nil {
		t.Fatal("want an error when the Codex config cannot be restored")
	}
	if !strings.Contains(out.String(), e.codex) {
		t.Errorf("message must name the full path %s: %s", e.codex, out.String())
	}
	cfg, err := store.LoadConfig()
	if err != nil || !cfg.Codex.Hooked || cfg.Claude.Hooked {
		t.Errorf("config must be kept with only codex still hooked: %+v %v", cfg, err)
	}
}

func TestUnlinkLeavesChangedStatusLine(t *testing.T) {
	e := setup(t)
	linkOnce(t, e)
	theirs := `{"statusLine":{"type":"command","command":"their-new-line"},"theme":"dark"}`
	if err := os.WriteFile(e.claude, []byte(theirs), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Unlink(context.Background(), &out, Options{}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(e.claude); string(b) != theirs {
		t.Errorf("user's status line overwritten: %s", b)
	}
	if !strings.Contains(out.String(), "Your Claude Code status line was changed after linking, so Devlets left it as it is.") {
		t.Errorf("must say so: %s", out.String())
	}
	if b, _ := os.ReadFile(e.codex); string(b) != codexBefore {
		t.Errorf("codex not restored: %s", b)
	}
	if _, err := store.LoadConfig(); err != store.ErrNotLinked {
		t.Errorf("config should be gone: %v", err)
	}
}

func TestUnlinkUsesSavedAPIBase(t *testing.T) {
	e := setup(t)
	cfg := linkOnce(t, e)
	var out bytes.Buffer
	if err := Unlink(context.Background(), &out, Options{API: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !e.deleted(cfg.BoxID) {
		t.Errorf("box not deleted at the saved API base")
	}
}

func TestLinkNeedsATerminal(t *testing.T) {
	e := setup(t)
	o := e.opts(false)
	o.Terminal = false
	var out bytes.Buffer
	if err := Run(context.Background(), nil, &out, o); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("err = %v", err)
	}
	if ErrNoTerminal.Error() != "devlets link needs a terminal to ask before changing your settings. Run it in a terminal, or pass --yes." {
		t.Errorf("message: %q", ErrNoTerminal.Error())
	}
	if len(e.srv.Requests()) != 0 {
		t.Errorf("contacted the server without consent")
	}
	if _, err := store.LoadConfig(); err != store.ErrNotLinked {
		t.Errorf("config written")
	}
	if b, _ := os.ReadFile(e.claude); string(b) != claudeBefore {
		t.Errorf("claude settings touched")
	}
}

func TestLinkYesWithoutTerminal(t *testing.T) {
	e := setup(t)
	e.phone("P")
	o := e.opts(true)
	o.Terminal = false
	var out bytes.Buffer
	if err := Run(context.Background(), nil, &out, o); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if cfg, _ := store.LoadConfig(); cfg == nil || !cfg.Claude.Hooked || !cfg.Codex.Hooked {
		t.Errorf("--yes must link both: %+v", cfg)
	}
}

func TestEmptyAnswerIsYesOnlyFromATerminal(t *testing.T) {
	e := setup(t)
	o := e.opts(false)
	o.Terminal = false
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("\n\n"), &out, o)
	if !errors.Is(err, ErrReported) || !strings.Contains(out.String(), msgNothingToLink) {
		t.Fatalf("empty answers from a non-terminal must be no: %v\n%s", err, out.String())
	}
	if posts(e) != 0 {
		t.Errorf("box created")
	}
}

func TestUnlinkDespiteHeldPushLog(t *testing.T) {
	e := setup(t)
	linkOnce(t, e)
	lf, err := os.OpenFile(filepath.Join(os.Getenv("DEVLETS_CONFIG_DIR"), "push.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	var out bytes.Buffer
	if err := Unlink(context.Background(), &out, Options{}); err != nil {
		t.Fatalf("unlink failed over a held push.log: %v\n%s", err, out.String())
	}
	if _, err := store.LoadConfig(); err != store.ErrNotLinked {
		t.Errorf("config should be gone: %v", err)
	}
}

// afterFirstPut runs fn in the background once the CLI has pushed its reading.
func (e *env) afterFirstPut(fn func(cfg *store.Config, key []byte, put mailboxtest.RecordedRequest)) {
	n0 := len(e.puts())
	go func() {
		for i := 0; i < 1000; i++ {
			if p := e.puts(); len(p) > n0 {
				if cfg, err := store.LoadConfig(); err == nil {
					key, _ := crypto.UnB64(cfg.Key)
					fn(cfg, key, p[0])
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func TestReflectedReadingIsNotAHello(t *testing.T) {
	e := setup(t)
	// a malicious server answers GET .../hello with the box's own first reading
	e.afterFirstPut(func(cfg *store.Config, key []byte, put mailboxtest.RecordedRequest) {
		var envl crypto.Envelope
		if json.Unmarshal(put.Body, &envl) == nil {
			e.srv.PhoneHello(cfg.BoxID, crypto.ReadToken(key), envl)
		}
	})
	o := e.opts(false)
	o.HelloTimeout = 2 * time.Second
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, o)
	if !errors.Is(err, ErrReported) {
		t.Fatalf("err = %v, want ErrReported\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "✓ Linked") {
		t.Errorf("a reflected reading confirmed the link:\n%s", out.String())
	}
}

func TestEmptyPhoneNameIsIgnored(t *testing.T) {
	e := setup(t)
	e.afterFirstPut(func(cfg *store.Config, key []byte, _ mailboxtest.RecordedRequest) { e.hello(cfg, "") })
	o := e.opts(false)
	o.HelloTimeout = 500 * time.Millisecond
	var out bytes.Buffer
	err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, o)
	if !errors.Is(err, ErrReported) || strings.Contains(out.String(), "✓ Linked") {
		t.Errorf("err=%v out=%s", err, out.String())
	}
}

func TestTodaysHelloStillLinks(t *testing.T) {
	e := setup(t)
	// sealed exactly as Android 0.2.0 does it: crypto.Seal, today's AAD, {"phone":"SM-S931B"}
	e.phone("SM-S931B")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, e.opts(false)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✓ Linked to SM-S931B") {
		t.Errorf("output: %s", out.String())
	}
}

func TestLinkPrintsConfirmCode(t *testing.T) {
	e := setup(t)
	e.phone("Pixel 8")
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("y\ny\n"), &out, e.opts(false)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.UnB64(cfg.Key)
	c := crypto.ConfirmCode(key)
	want := "Code: " + c[:3] + " " + c[3:] + " (if your phone shows a code, it must match)"
	s := out.String()
	if !strings.Contains(s, want) {
		t.Errorf("missing %q in:\n%s", want, s)
	}
	// The warning is about the QR code (it holds the key), so it sits with the QR, above the confirm code.
	warning := "Don't share or stream the QR code or link: they open your readings."
	if !strings.Contains(s, warning) || strings.Index(s, warning) > strings.Index(s, want) {
		t.Errorf("share warning missing or below the confirm code:\n%s", s)
	}
}
