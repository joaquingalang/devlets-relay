package internal_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCredentialPaths enforces BR16: no Go source may mention a credential
// file, OAuth or a provider usage endpoint. It scans .go files only and skips
// this file, whose job is to name the forbidden words.
func TestNoCredentialPaths(t *testing.T) {
	forbidden := []string{".credentials", "auth.json", "oauth", "wham"}
	root := ".."
	self, _ := filepath.Abs("policy_test.go")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "dist", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if abs, _ := filepath.Abs(path); abs == self {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		low := strings.ToLower(string(data))
		for _, word := range forbidden {
			if strings.Contains(low, word) {
				t.Errorf("%s mentions %q (BR16: never touch credentials)", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 10 {
		t.Fatalf("scanned only %d .go files; wrong root?", scanned)
	}
}
