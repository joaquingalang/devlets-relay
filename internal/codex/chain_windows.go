package codex

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// chainCheck says whether Codex's message may be passed to argv0, and if not,
// why and under which name. On Windows a .bat or .cmd file is run by cmd.exe,
// and cmd.exe, powershell.exe and pwsh.exe re-parse their extra arguments as
// command text, so an & or | in the message would run a command (S1, RH3).
func chainCheck(argv0 string) (Refusal, string) {
	p, err := exec.LookPath(argv0)
	if err != nil {
		return RefuseMissing, filepath.Base(argv0)
	}
	// Windows ignores trailing spaces and dots in a file name, so "a.cmd " is a batch file.
	p = strings.TrimRight(p, ". ")
	name := filepath.Base(p)
	switch strings.ToLower(filepath.Ext(p)) {
	case ".bat", ".cmd":
		return RefuseBatch, name
	}
	switch strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))) {
	case "cmd", "powershell", "pwsh":
		return RefuseShell, name
	}
	return ChainOK, name
}
