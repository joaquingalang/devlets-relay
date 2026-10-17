//go:build !windows

package codex

import (
	"os/exec"
	"syscall"
)

// prepare puts the child in its own process group.
func prepare(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// contain returns a function that kills the child's whole process group.
func contain(cmd *exec.Cmd) func() {
	return func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}
