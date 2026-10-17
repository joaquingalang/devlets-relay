//go:build !windows

package codex

import (
	"os/exec"
	"syscall"
)

// detach starts the child in its own session so Codex exiting doesn't stop it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
