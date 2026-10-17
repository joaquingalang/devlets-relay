package push

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

// StartDetached runs `devlets <args>` (this binary) detached, with stdout and
// stderr going to push.log. It does nothing when a push is running (push.lock)
// or one was started less than 30 s ago, so the status line (~every 300 ms)
// and a failing push cannot cause a storm.
func StartDetached(args ...string) error {
	dir, err := store.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if recent(filepath.Join(dir, lockName), lockStale) || recent(filepath.Join(dir, startedName), startGap) {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, startedName), nil, 0o600)
	logPath := filepath.Join(dir, logName)
	trimLog(logPath)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = lf, lf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// trimLog keeps the last 100 lines of the log.
func trimLog(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	if len(lines) <= logKeepLines {
		return
	}
	_ = os.WriteFile(path, bytes.Join(lines[len(lines)-logKeepLines:], nil), 0o600)
}
