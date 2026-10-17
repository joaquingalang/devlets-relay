package codex

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

func prepare(cmd *exec.Cmd) {}

// contain puts the started child in a Job Object that kills everything in it
// when the handle closes, so the npm .cmd shim's node/codex children cannot
// outlive us even if the shim exits first. The returned function closes the
// handle and must run on every exit path. Best effort: on failure it only
// kills the child.
func contain(cmd *exec.Cmd) func() {
	fallback := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fallback
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return fallback
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return fallback
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return fallback
	}
	return func() { windows.CloseHandle(job) }
}
