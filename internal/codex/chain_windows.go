package codex

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// chainable says whether Codex's message may be passed to argv0. On Windows a
// .bat or .cmd file is run by cmd.exe, which would read & and | in the message
// as commands (S1, RH3). An unresolvable command is not chained either.
func chainable(argv0 string) bool {
	p, err := exec.LookPath(argv0)
	if err != nil {
		return false
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".bat", ".cmd":
		return false
	}
	return true
}
