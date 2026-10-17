// Package safefile writes a user's settings file (Claude Code's settings.json,
// Codex's config.toml) atomically without breaking a symlink: the link is
// resolved, a temp file is written next to the real file with its mode, and
// the temp file is renamed over the real file.
package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Write replaces the file at path with data. When path is a symlink the file
// it points to is replaced and the link stays. An existing file keeps its
// mode; a new one gets mode. Missing directories are created.
func Write(path string, data []byte, mode os.FileMode) error {
	target, err := resolve(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if werr != nil || serr != nil || cerr != nil {
		os.Remove(name)
		return errors.New("writing " + filepath.Base(target) + " failed")
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := rename(name, target); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// resolve follows symlinks. A path that does not exist yet is used as is; a
// symlink whose target is missing is an error rather than being replaced by a
// plain file.
func resolve(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return real, nil
	}
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.New(filepath.Base(path) + " is a link to a file that cannot be reached; leaving it alone")
	}
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	return "", err
}

// rename retries briefly on Windows, where a reader holding the file open can
// make the rename fail for a moment.
func rename(from, to string) error {
	err := os.Rename(from, to)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 5; i++ {
		time.Sleep(20 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}
