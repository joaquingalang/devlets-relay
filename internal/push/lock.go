package push

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

const (
	lockName     = "push.lock"
	startedName  = "push.started"
	logName      = "push.log"
	lockStale    = 60 * time.Second
	startGap     = 30 * time.Second
	logKeepLines = 100
)

// ErrLocked means another push is running.
var ErrLocked = errors.New("push: another push is running")

// acquireLock creates push.lock exclusively (O_EXCL, so it works across
// processes). A lock older than 60 s is taken over.
func acquireLock(now time.Time) (release func(), err error) {
	dir, err := store.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, lockName)
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		fi, serr := os.Stat(path)
		if serr != nil {
			continue // vanished: try again
		}
		if age := now.Sub(fi.ModTime()); age < lockStale && age > -lockStale {
			return nil, ErrLocked
		}
		_ = os.Remove(path) // stale
	}
	return nil, ErrLocked
}

// recent reports whether path was modified within d.
func recent(path string, d time.Duration) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	age := time.Since(fi.ModTime())
	return age < d && age > -d
}
