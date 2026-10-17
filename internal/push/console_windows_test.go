package push_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/push"
)

// A push child started through StartDetached must have no console window.
func TestStartDetachedChildHasNoConsoleWindow(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "consolehelper.exe")
	build := exec.Command("go", "build", "-o", bin, "../codex/testdata/consolehelper")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out := filepath.Join(dir, "out")
	t.Setenv("HELPER_OUT", out)
	push.SetExecutable(t, func() (string, error) { return bin, nil })
	if err := push.StartDetached(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			if string(b) != "0" {
				t.Fatalf("GetConsoleWindow != 0 (%s)", b)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("helper wrote nothing")
}
