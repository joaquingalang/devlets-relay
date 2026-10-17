package push

import (
	"crypto/rand"
	"encoding/hex"
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
			token := newToken()
			_, _ = f.WriteString(token)
			_ = f.Close()
			return func() { releaseLock(path, token) }, nil
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

func newToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// releaseLock removes the lock only if it is still ours, so a run that
// outlived the staleness limit cannot delete its successor's lock.
func releaseLock(path, token string) {
	if b, err := os.ReadFile(path); err == nil && string(b) == token {
		_ = os.Remove(path)
	}
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
