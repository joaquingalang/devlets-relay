package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const statusLineKey = "statusLine"

// settingsPath is $CLAUDE_CONFIG_DIR/settings.json, else ~/.claude/settings.json.
func settingsPath() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude", "settings.json"), nil
}

// readSettings returns the file's bytes; a missing or blank file is "{}".
func readSettings(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{}"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Claude settings: %w", errors.Unwrap(err))
	}
	if strings.TrimSpace(string(raw)) == "" {
		return []byte("{}"), nil
	}
	if !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject() {
		return nil, errors.New("Claude settings.json is not a valid JSON object; leaving it alone")
	}
	return raw, nil
}

func writeSettings(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// hookCommand is the status-line command: the absolute binary path (quoted
// when it holds anything the shell would split or expand) and the subcommand.
// Forward slashes keep Git Bash, which Claude Code uses on Windows, happy.
func hookCommand(devletsPath string) string {
	p := filepath.ToSlash(devletsPath)
	if strings.ContainsAny(p, " \t\"'$`\\&;|<>()*?[]{}!#~") {
		p = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(p) + `"`
	}
	return p + " claude-statusline"
}

// Hook points Claude Code's statusLine at `devlets claude-statusline` and
// returns the previous statusLine value as raw JSON (nil when there was none).
// Only the statusLine value is edited; the rest of the file keeps its bytes,
// and other fields of a previous statusLine object (padding) are kept.
func Hook(devletsPath string) (previous json.RawMessage, err error) {
	abs, err := filepath.Abs(devletsPath)
	if err != nil {
		return nil, err
	}
	path, err := settingsPath()
	if err != nil {
		return nil, err
	}
	raw, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	cmd := hookCommand(abs)
	cur := gjson.GetBytes(raw, statusLineKey)
	var out []byte
	if cur.Exists() {
		previous = json.RawMessage(cur.Raw)
	}
	if cur.IsObject() {
		if out, err = sjson.SetBytes(raw, statusLineKey+".type", "command"); err == nil {
			out, err = sjson.SetBytes(out, statusLineKey+".command", cmd)
		}
	} else {
		obj, _ := json.Marshal(map[string]string{"type": "command", "command": cmd})
		out, err = sjson.SetRawBytes(raw, statusLineKey, obj)
	}
	if err != nil {
		return nil, errors.New("could not edit Claude settings.json")
	}
	if err := writeSettings(path, out); err != nil {
		return nil, err
	}
	return previous, nil
}

// Unhook writes the saved statusLine value back exactly as it was, or removes
// the key when there was none (previous == nil).
func Unhook(previous json.RawMessage) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	raw, err := readSettings(path)
	if err != nil {
		return err
	}
	var out []byte
	if previous == nil {
		out, err = sjson.DeleteBytes(raw, statusLineKey)
	} else {
		out, err = sjson.SetRawBytes(raw, statusLineKey, previous)
	}
	if err != nil {
		return errors.New("could not edit Claude settings.json")
	}
	return writeSettings(path, out)
}
