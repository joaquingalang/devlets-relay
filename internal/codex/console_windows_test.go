package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func consoleBin(t *testing.T) string {
	t.Helper()
	return buildGo(t.TempDir(), "consolehelper", "./testdata/consolehelper")
}

func waitFile(t *testing.T, p string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("helper wrote nothing")
	return ""
}

// The child must have no console window: prepare is what the Codex read uses.
func TestPrepareChildHasNoConsoleWindow(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	t.Setenv("HELPER_OUT", out)
	cmd := exec.Command(consoleBin(t))
	prepare(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatalf("prepare must set HideWindow and CREATE_NO_WINDOW: %+v", cmd.SysProcAttr)
	}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := waitFile(t, out); got != "0" {
		t.Fatalf("GetConsoleWindow != 0 (%s)", got)
	}
}

// The chained previous notify is started windowless too.
func TestChainChildHasNoConsoleWindow(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	t.Setenv("HELPER_OUT", out)
	if err := StartDetached([]string{consoleBin(t)}, nil); err != nil {
		t.Fatal(err)
	}
	if got := waitFile(t, out); got != "0" {
		t.Fatalf("GetConsoleWindow != 0 (%s)", got)
	}
}
