package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/contract"
)

func useTemp(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("DEVLETS_CONFIG_DIR", d)
	return d
}

func TestConfigRoundTrip(t *testing.T) {
	useTemp(t)
	if _, err := LoadConfig(); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("want ErrNotLinked, got %v", err)
	}
	prev := `{"a":1}`
	c := &Config{APIBase: "https://x", BoxID: "b", WriteToken: "w", Key: "k", Computer: "pc",
		Claude: Hook{Hooked: true, Previous: &prev}, Codex: Hook{Hooked: true}}
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.BoxID != "b" || got.Key != "k" || got.Claude.Previous == nil || *got.Claude.Previous != prev || got.Codex.Previous != nil || !got.Codex.Hooked {
		t.Fatalf("round trip differs: %+v", got)
	}
}

func TestStateRoundTripAndRemove(t *testing.T) {
	dir := useTemp(t)
	s, err := LoadState()
	if err != nil || s == nil {
		t.Fatalf("missing state should be empty, got %v %v", s, err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	s.Reading = contract.Reading{V: 1, Computer: "pc"}
	s.LastPush = Pushed{At: now, Reading: s.Reading}
	s.NextAllowed = now.Add(time.Minute)
	if err := SaveState(s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastPush.At.Equal(now) || got.LastPush.At.Location() != time.UTC || got.NextAllowed.Location() != time.UTC {
		t.Fatalf("times not UTC/equal: %+v", got)
	}
	_ = SaveConfig(&Config{BoxID: "b"})
	if err := Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir should be gone: %v", err)
	}
	if err := Remove(); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}

func TestConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not enforced on Windows; the user profile ACL applies")
	}
	dir := useTemp(t)
	if err := SaveConfig(&Config{BoxID: "b"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestDirPerOS(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("APPDATA", app)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	var want string
	switch runtime.GOOS {
	case "windows":
		want = filepath.Join(app, "Devlets")
	case "darwin":
		want = filepath.Join(home, "Library", "Application Support", "Devlets")
	default:
		want = filepath.Join(xdg, "devlets")
	}
	got, err := Dir()
	if err != nil || got != want {
		t.Fatalf("Dir() = %q, %v; want %q", got, err, want)
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Setenv("XDG_CONFIG_HOME", "")
		got, _ := Dir()
		if got != filepath.Join(home, ".config", "devlets") {
			t.Fatalf("fallback = %q", got)
		}
	}
	t.Setenv("DEVLETS_CONFIG_DIR", "/override")
	if got, _ := Dir(); got != "/override" {
		t.Fatalf("override = %q", got)
	}
}
