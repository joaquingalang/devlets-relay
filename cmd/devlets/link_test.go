package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusAndUnlinkWhenNotLinked(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	for _, cmd := range []string{"status", "unlink"} {
		var out, errb bytes.Buffer
		if code := runWith([]string{cmd}, strings.NewReader(""), &out, &errb); code != 0 {
			t.Errorf("%s: exit %d, stderr %q", cmd, code, errb.String())
		}
		if strings.TrimSpace(out.String()) != "Not linked. Run devlets link." {
			t.Errorf("%s: got %q", cmd, out.String())
		}
	}
}

func TestLinkRejectsUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runWith([]string{"link", "--bogus"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("exit %d", code)
	}
}

func TestLinkWithoutTerminalAborts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	t.Setenv("DEVLETS_CONFIG_DIR", dir)
	t.Setenv("DEVLETS_API", "http://127.0.0.1:1")
	old := promptInput
	promptInput = func(io.Reader) (io.Reader, func()) { return nil, func() {} }
	defer func() { promptInput = old }()
	var out, errb bytes.Buffer
	if code := runWith([]string{"link"}, strings.NewReader("y\ny\n"), &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	want := "devlets link needs a terminal to ask before changing your settings. Run it in a terminal, or pass --yes.\n"
	if errb.String() != want {
		t.Fatalf("stderr %q", errb.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("config dir created")
	}
}

func TestPromptInputUsesNoPipe(t *testing.T) {
	// a non-file reader (a pipe in spirit) is never used for answers
	in, done := terminalInput(strings.NewReader("y\n"))
	defer done()
	if r, ok := in.(*strings.Reader); ok && r != nil {
		t.Fatal("piped stdin used as the answer source")
	}
}
