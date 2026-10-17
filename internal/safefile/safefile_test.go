package safefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteNewAndReplace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "settings.json")
	if err := Write(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("%q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

// symlink makes link -> target, or skips when the OS needs a privilege for it.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func TestWriteKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "settings.json")
	_ = os.MkdirAll(filepath.Dir(real), 0o755)
	if err := os.WriteFile(real, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	symlink(t, real, link)
	if err := Write(link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a file: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(real); string(b) != "new" {
		t.Fatalf("target not written: %q", b)
	}
}

func TestWriteRefusesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "settings.json")
	symlink(t, filepath.Join(dir, "missing", "settings.json"), link)
	if err := Write(link, []byte("x"), 0o644); err == nil {
		t.Fatal("want an error for a dangling link")
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("dangling link replaced")
	}
}

func TestWriteKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes")
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	q := filepath.Join(t.TempDir(), "new.toml")
	if err := Write(q, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(q); fi.Mode().Perm() != 0o644 {
		t.Fatalf("new file mode %v", fi.Mode().Perm())
	}
}
