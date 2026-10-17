package codex

import (
	"os/exec"
	"syscall"
)

// detach starts the child in its own process group with no console window.
// CREATE_NO_WINDOW rather than DETACHED_PROCESS: a console program such as the
// user's previous notify would otherwise get a new visible console.
func detach(cmd *exec.Cmd) {
	const createNewProcessGroup, createNoWindow = 0x00000200, 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup | createNoWindow}
}
