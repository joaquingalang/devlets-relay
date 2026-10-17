// Command helper is a stand-in for a user's previous notify program: it writes
// its arguments, one per line, to the file named by HELPER_OUT.
package main

import (
	"os"
	"strings"
)

func main() {
	if p := os.Getenv("HELPER_OUT"); p != "" {
		_ = os.WriteFile(p, []byte(strings.Join(os.Args[1:], "\n")), 0o644)
	}
}
