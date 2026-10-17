package main

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// isMsysPty reports whether f is the pipe a Cygwin or MSYS terminal (Git
// Bash's mintty) uses as a tty: \{cygwin,msys}-<hex>-pty<n>-{from,to}-master.
func isMsysPty(f *os.File) bool {
	if t, err := windows.GetFileType(windows.Handle(f.Fd())); err != nil || t != windows.FILE_TYPE_PIPE {
		return false
	}
	// FILE_NAME_INFO: a uint32 length in bytes, then the UTF-16 name.
	buf := make([]uint16, 2+windows.MAX_PATH)
	if err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileNameInfo,
		(*byte)(unsafe.Pointer(&buf[0])), uint32(len(buf)*2)); err != nil {
		return false
	}
	n := *(*uint32)(unsafe.Pointer(&buf[0])) / 2
	if int(n) > len(buf)-2 {
		return false
	}
	name := windows.UTF16ToString(buf[2 : 2+n])
	parts := strings.Split(strings.TrimPrefix(name, `\`), "-")
	if len(parts) < 5 || (parts[0] != "cygwin" && parts[0] != "msys") {
		return false
	}
	return strings.HasPrefix(parts[2], "pty") && (parts[3] == "from" || parts[3] == "to") && parts[4] == "master"
}
