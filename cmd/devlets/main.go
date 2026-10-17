// Command devlets links a computer to the Devlets phone app.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: devlets <command>

Commands:
  link          Link this computer to your phone
  status        Show what is linked and the last reading
  unlink        Remove the link and the hooks
  push          Read usage and send it now
  claude-statusline   (used by Claude Code)
  codex-notify        (used by Codex)

Flags:
  --version     Print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "--version", "-version", "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "link", "status", "unlink", "push", "claude-statusline", "codex-notify":
		fmt.Fprintf(stderr, "devlets %s: not implemented yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "devlets: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
