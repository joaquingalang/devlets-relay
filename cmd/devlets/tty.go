package main

import (
	"io"
	"os"
	"runtime"

	"golang.org/x/term"
)

// promptInput is where `devlets link` reads answers: stdin when it is a
// terminal, else the terminal itself (/dev/tty, or CONIN$ on Windows), so a
// pipe or a closed stdin never answers for the user. It returns nil when there
// is no terminal. Tests replace it.
var promptInput = terminalInput

func terminalInput(stdin io.Reader) (in io.Reader, closeFn func()) {
	if f, ok := stdin.(*os.File); ok && isTerminal(f) {
		return f, func() {}
	}
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return nil, func() {}
	}
	if !term.IsTerminal(int(f.Fd())) {
		f.Close()
		return nil, func() {}
	}
	return f, func() { f.Close() }
}

// isTerminal is a console or tty, or (on Windows) a Git Bash/MSYS terminal,
// which hands programs a named pipe instead of a console.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) || isMsysPty(f)
}
