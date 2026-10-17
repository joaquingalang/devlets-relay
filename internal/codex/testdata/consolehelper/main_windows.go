// Command consolehelper reports whether it has a console window
// (GetConsoleWindow != 0) by writing "1" or "0" to the file named by HELPER_OUT.
package main

import (
	"os"
	"syscall"
)

func main() {
	p := os.Getenv("HELPER_OUT")
	if p == "" {
		return
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	h, _, _ := proc.Call()
	out := "0"
	if h != 0 {
		out = "1"
	}
	_ = os.WriteFile(p, []byte(out), 0o644)
}
