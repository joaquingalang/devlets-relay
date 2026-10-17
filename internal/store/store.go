// Package store keeps the CLI's two local files: config.json (box id, write
// token, key, hooked settings) and state.json (last reading and push). Both are
// written atomically and readable by the user only. Contents are never put in
// error messages.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/contract"
)

// ErrNotLinked means there is no config.json: `devlets link` has not run.
var ErrNotLinked = errors.New("not linked: run `devlets link`")

// Hook records whether the relay hooked a tool's settings and what was there
// before. Previous is nil when the setting was absent (an empty string is a
// real previous value). Claude stores raw JSON, Codex the saved line or span.
type Hook struct {
	Hooked   bool    `json:"hooked"`
	Previous *string `json:"previous,omitempty"`
}

// Config is config.json. WriteToken and Key are secrets (Key is base64url).
type Config struct {
	APIBase    string `json:"apiBase"`
	BoxID      string `json:"boxId"`
	WriteToken string `json:"writeToken"`
	Key        string `json:"key"`
	Computer   string `json:"computer"`
	Claude     Hook   `json:"claude"`
	Codex      Hook   `json:"codex"`
}

// Pushed is what the last successful push sent, and when. The zero value means
// nothing was pushed yet.
type Pushed struct {
	At      time.Time        `json:"at"`
	Reading contract.Reading `json:"reading"`
}

// State is state.json. All times are stored in UTC.
type State struct {
	Reading     contract.Reading `json:"reading"`
	LastPush    Pushed           `json:"lastPush"`
	NextAllowed time.Time        `json:"nextAllowed"`
	// CodexAttemptAt is when a Codex rate-limit read was last started, so a
	// failing read is not retried on every turn.
	CodexAttemptAt time.Time `json:"codexAttemptAt"`
	// PushFailures counts failed pushes in a row, for the retry backoff.
	PushFailures int `json:"pushFailures,omitempty"`
	// UpdateCheckedAt is when status last asked GitHub for a newer version.
	UpdateCheckedAt time.Time `json:"updateCheckedAt"`
}

const (
	configFile = "config.json"
	stateFile  = "state.json"
)

// Dir returns the config directory. DEVLETS_CONFIG_DIR overrides everything
// (for tests). Otherwise: %APPDATA%\Devlets on Windows,
// ~/Library/Application Support/Devlets on macOS, $XDG_CONFIG_HOME/devlets
// (else ~/.config/devlets) elsewhere.
func Dir() (string, error) {
	if d := os.Getenv("DEVLETS_CONFIG_DIR"); d != "" {
		return d, nil
	}
	switch runtime.GOOS {
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "Devlets"), nil
		}
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, "AppData", "Roaming", "Devlets"), nil
	case "darwin":
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, "Library", "Application Support", "Devlets"), nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "devlets"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "devlets"), nil
}

func read(name string, v any) (bool, error) {
	dir, err := Dir()
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", name, errors.Unwrap(err))
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, fmt.Errorf("%s is not valid JSON", name) // never echo contents
	}
	return true, nil
}

// write saves v atomically (temp file in the same dir, then rename), mode 0600.
func write(name string, v any) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s", name)
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp") // created 0600
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(raw)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write %s failed", name)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpName, 0o600); err != nil {
			os.Remove(tmpName)
			return err
		}
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// LoadConfig reads config.json, or returns ErrNotLinked when it is absent.
func LoadConfig() (*Config, error) {
	var c Config
	ok, err := read(configFile, &c)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotLinked
	}
	return &c, nil
}

// SaveConfig writes config.json.
func SaveConfig(c *Config) error { return write(configFile, c) }

// LoadState reads state.json; a missing file yields an empty State.
func LoadState() (*State, error) {
	var s State
	if _, err := read(stateFile, &s); err != nil {
		return nil, err
	}
	s.LastPush.At = s.LastPush.At.UTC()
	s.NextAllowed = s.NextAllowed.UTC()
	s.CodexAttemptAt = s.CodexAttemptAt.UTC()
	s.UpdateCheckedAt = s.UpdateCheckedAt.UTC()
	return &s, nil
}

// SaveState writes state.json with times in UTC.
func SaveState(s *State) error {
	c := *s
	c.LastPush.At = c.LastPush.At.UTC()
	c.NextAllowed = c.NextAllowed.UTC()
	c.CodexAttemptAt = c.CodexAttemptAt.UTC()
	c.UpdateCheckedAt = c.UpdateCheckedAt.UTC()
	return write(stateFile, &c)
}

// Remove deletes the config directory (both files). Missing is not an error.
func Remove() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
