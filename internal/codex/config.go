package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// configPath is $CODEX_HOME/config.toml, else ~/.codex/config.toml.
func configPath() (string, error) {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "config.toml"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".codex", "config.toml"), nil
}

func readConfig(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Codex config: %w", errors.Unwrap(err))
	}
	return string(b), nil
}

// writeConfig writes atomically: a temp file in the same directory, then a
// rename. An existing file keeps its mode.
func writeConfig(path, text string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, "config.toml.*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(text)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(name)
		return errors.New("write Codex config failed")
	}
	_ = os.Chmod(name, mode)
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ---- a small TOML line scanner: just enough to know where statements start ----

type scanState struct {
	ml    string // open multi-line string delimiter, or ""
	depth int    // open [ and { outside strings
}

func (s *scanState) open() bool { return s.ml != "" || s.depth > 0 }

// feed advances the state over one line.
func (s *scanState) feed(line string) {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if s.ml != "" {
			if s.ml == `"""` && c == '\\' {
				i++
			} else if strings.HasPrefix(line[i:], s.ml) {
				i += 2
				s.ml = ""
			}
			continue
		}
		switch c {
		case '#':
			return
		case '"', '\'':
			if d := string([]byte{c, c, c}); strings.HasPrefix(line[i:], d) {
				s.ml = d
				i += 2
				continue
			}
			for i++; i < len(line); i++ {
				if c == '"' && line[i] == '\\' {
					i++
				} else if line[i] == c {
					break
				}
			}
		case '[', '{':
			s.depth++
		case ']', '}':
			if s.depth > 0 {
				s.depth--
			}
		}
	}
}

var notifyKey = regexp.MustCompile(`^notify[ \t]*=`)

// locate finds the top-level notify statement. It returns the line range
// [start,end) of the whole span, or start == -1 when absent, plus the index of
// the first table header (len(lines) when there is none).
func locate(lines []string) (start, end, header int) {
	start, end, header = -1, -1, len(lines)
	var st scanState
	for i := 0; i < len(lines); i++ {
		if !st.open() {
			t := strings.TrimLeft(lines[i], " \t")
			if strings.HasPrefix(t, "[") {
				return start, end, i
			}
			if start < 0 && notifyKey.MatchString(t) {
				start = i
				st.feed(lines[i])
				j := i + 1
				for ; st.open() && j < len(lines); j++ {
					st.feed(lines[j])
				}
				end = j
				i = j - 1
				continue
			}
		}
		st.feed(lines[i])
	}
	return start, end, header
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.SplitAfter(strings.TrimSuffix(text, "\x00"), "\n")
}

func cleanLines(lines []string) []string {
	if n := len(lines); n > 0 && lines[n-1] == "" {
		return lines[:n-1]
	}
	return lines
}

func lineEnding(s string) string {
	for _, e := range []string{"\r\n", "\n"} {
		if strings.HasSuffix(s, e) {
			return e
		}
	}
	return ""
}

// tomlQuote returns s as a TOML basic string.
func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Hook points Codex's top-level `notify` at `devlets codex-notify` and returns
// the previous statement verbatim, line endings included ("" when there was
// none). Only that statement changes; every other byte of the file stays. A
// notify that is already ours (any path, e.g. after config.json was lost) is
// adopted: it is re-pointed at this binary and previous is "", so unlink
// removes it and Codex never chains devlets to itself.
func Hook(devletsPath string) (previous string, err error) {
	abs, err := filepath.Abs(devletsPath)
	if err != nil {
		return "", err
	}
	path, err := configPath()
	if err != nil {
		return "", err
	}
	text, err := readConfig(path)
	if err != nil {
		return "", err
	}
	lines := cleanLines(splitLines(text))
	start, end, header := locate(lines)
	ours := "notify = [" + tomlQuote(abs) + `, "codex-notify"]`

	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	var out string
	if start >= 0 {
		span := strings.Join(lines[start:end], "")
		if !isOurs(span) {
			previous = span
		}
		out = strings.Join(lines[:start], "") + ours + lineEnding(span) + strings.Join(lines[end:], "")
	} else if header == len(lines) && len(lines) > 0 && lineEnding(lines[len(lines)-1]) == "" {
		// the file does not end in a newline: add ours after one, with none after it
		out = text + eol + ours
	} else {
		out = strings.Join(lines[:header], "") + ours + eol + strings.Join(lines[header:], "")
	}
	if err := writeConfig(path, out); err != nil {
		return "", err
	}
	return previous, nil
}

// Unhook puts the saved statement back exactly as it was, or removes ours when
// there was none (previous == ""). A notify the user changed since is left
// alone.
func Unhook(previous string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	text, err := readConfig(path)
	if err != nil {
		return err
	}
	lines := cleanLines(splitLines(text))
	start, end, _ := locate(lines)
	if start < 0 {
		return nil
	}
	span := strings.Join(lines[start:end], "")
	if !isOurs(span) {
		return nil
	}
	var out string
	if previous != "" {
		out = strings.Join(lines[:start], "") + previous + strings.Join(lines[end:], "")
	} else {
		before := lines[:start]
		if end == len(lines) && lineEnding(span) == "" && len(before) > 0 {
			// ours was appended after a newline we added; take that back too
			last := before[len(before)-1]
			before = append(append([]string(nil), before[:len(before)-1]...), strings.TrimSuffix(strings.TrimSuffix(last, "\n"), "\r"))
		}
		out = strings.Join(before, "") + strings.Join(lines[end:], "")
	}
	return writeConfig(path, out)
}

// isOurs reports whether a notify statement is `[<something>, "codex-notify"]`.
func isOurs(span string) bool {
	argv := parseNotifyArgv(span)
	return len(argv) == 2 && argv[1] == "codex-notify"
}

// parseNotifyArgv reads the string array of a `notify = [...]` statement. It
// returns nil when the text is not an array of strings.
func parseNotifyArgv(span string) []string {
	i := strings.Index(span, "=")
	if i < 0 {
		return nil
	}
	s := span[i+1:]
	p := 0
	skip := func() {
		for p < len(s) {
			switch s[p] {
			case ' ', '\t', '\r', '\n':
				p++
			case '#':
				for p < len(s) && s[p] != '\n' {
					p++
				}
			default:
				return
			}
		}
	}
	skip()
	if p >= len(s) || s[p] != '[' {
		return nil
	}
	p++
	argv := []string{}
	for {
		skip()
		if p >= len(s) {
			return nil
		}
		switch s[p] {
		case ']':
			return argv
		case ',':
			p++
		case '\'':
			e := strings.IndexByte(s[p+1:], '\'')
			if e < 0 {
				return nil
			}
			argv = append(argv, s[p+1:p+1+e])
			p += e + 2
		case '"':
			var b strings.Builder
			p++
			closed := false
			for p < len(s) && !closed {
				c := s[p]
				switch {
				case c == '"':
					closed = true
					p++
				case c == '\\' && p+1 < len(s):
					p++
					switch s[p] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					case 'b':
						b.WriteByte('\b')
					case 'f':
						b.WriteByte('\f')
					case 'u', 'U':
						n := 4
						if s[p] == 'U' {
							n = 8
						}
						if p+n >= len(s) {
							return nil
						}
						v, err := strconv.ParseUint(s[p+1:p+1+n], 16, 32)
						if err != nil || !utf8.ValidRune(rune(v)) {
							return nil
						}
						b.WriteRune(rune(v))
						p += n
					default:
						b.WriteByte(s[p])
					}
					p++
				case c == '\n':
					return nil
				default:
					b.WriteByte(c)
					p++
				}
			}
			if !closed {
				return nil
			}
			argv = append(argv, b.String())
		default:
			return nil
		}
	}
}
