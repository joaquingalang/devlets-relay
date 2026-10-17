//go:build !windows

package push

import (
	"os/exec"
	"syscall"
)

// detach starts the child in its own session.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
