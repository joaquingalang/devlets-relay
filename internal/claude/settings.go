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

	"github.com/joaquingalang/devlets-relay/internal/safefile"
)

const statusLineKey = "statusLine"

// ErrUserChanged means the statusLine is no longer ours: the user changed it
// after linking, so Unhook left it as it is. Callers treat it as restored.
var ErrUserChanged = errors.New("Claude Code's status line was changed after linking; left as it is")

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

// writeSettings writes atomically through a symlink, keeping the file's mode.
func writeSettings(path string, raw []byte) error {
	return safefile.Write(path, raw, 0o644)
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

// isOurCommand reports whether a status-line command is `<path to devlets>
// claude-statusline` as hookCommand writes it, whatever the path.
func isOurCommand(command string) bool {
	c := strings.TrimSpace(command)
	const sub = " claude-statusline"
	if !strings.HasSuffix(c, sub) {
		return false
	}
	prog := strings.TrimSpace(strings.TrimSuffix(c, sub))
	if len(prog) >= 2 && prog[0] == '"' && prog[len(prog)-1] == '"' {
		prog = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\$`, "$", "\\`", "`").Replace(prog[1 : len(prog)-1])
	}
	base := prog[strings.LastIndexAny(prog, `/\`)+1:]
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	return base == "devlets"
}

// Hook points Claude Code's statusLine at `devlets claude-statusline` and
// returns the previous statusLine value as raw JSON (nil when there was none).
// Only the statusLine value is edited; the rest of the file keeps its bytes,
// and other fields of a previous statusLine object (padding) are kept. A
// statusLine that is already ours (any path, e.g. after config.json was lost)
// is adopted: it is re-pointed at this binary and previous is nil, so the
// status line never chains to itself.
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
	if cur.Exists() && !(cur.IsObject() && isOurCommand(cur.Get("command").String())) {
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
// the key when there was none (previous == nil). It only does so while the
// statusLine is still ours; one the user changed since is left alone and
// ErrUserChanged is returned.
func Unhook(previous json.RawMessage) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	raw, err := readSettings(path)
	if err != nil {
		return err
	}
	if cur := gjson.GetBytes(raw, statusLineKey); !(cur.IsObject() && isOurCommand(cur.Get("command").String())) {
		if !cur.Exists() && previous == nil {
			return nil // already as it was
		}
		return ErrUserChanged
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
