package main

import (
	"bytes"
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
