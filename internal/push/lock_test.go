package push

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReleaseOnlyRemovesOwnLock(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	release, err := acquireLock(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// the lock went stale and a successor took it over
	path := filepath.Join(os.Getenv("DEVLETS_CONFIG_DIR"), lockName)
	if err := os.WriteFile(path, []byte("someone-else"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("released a lock it does not own")
	}
}
