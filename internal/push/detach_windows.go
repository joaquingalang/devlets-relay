package push

import (
	"os/exec"
	"syscall"
)

// detach: DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP, no console window.
func detach(cmd *exec.Cmd) {
	const createNewProcessGroup, detachedProcess = 0x00000200, 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup | detachedProcess}
}
