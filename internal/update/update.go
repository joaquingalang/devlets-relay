// Package update checks GitHub Releases for a newer devlets and replaces the
// running binary (BR13, BR15). Downloads are verified against checksums.txt
// before the installed binary is touched.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

const releasePath = "/repos/joaquingalang/devlets-relay/releases/latest"

// Package variables so tests can point at httptest and a temp binary.
var (
	// BaseURL is the GitHub API root.
	BaseURL = "https://api.github.com"
	// Current is the running version (main.version); "dev" never updates.
	Current = "dev"
	// ExePath returns the binary to replace.
	ExePath = os.Executable
)

const (
	maxDownload   = 100 << 20
	noticeEvery   = 7 * 24 * time.Hour
	noticeTimeout = 2 * time.Second
)

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// AssetName is the archive name for a version (without the leading v), OS and
// arch. These names are a contract with the install scripts (BR14).
func AssetName(version, goos, goarch string) string {
	switch goos {
	case "windows":
		return fmt.Sprintf("devlets_%s_windows_%s.zip", version, goarch)
	case "darwin":
		return fmt.Sprintf("devlets_%s_darwin_all.tar.gz", version)
	}
	return fmt.Sprintf("devlets_%s_%s_%s.tar.gz", version, goos, goarch)
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "devlets.exe"
	}
	return "devlets"
}

// checkHost allows https URLs on github.com or *.githubusercontent.com, and
// any URL on the host of BaseURL (the API, or httptest in tests).
func checkHost(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if b, err := url.Parse(BaseURL); err == nil && b.Host != "" && u.Host == b.Host {
		return nil
	}
	h := strings.ToLower(u.Hostname())
	if u.Scheme == "https" && (h == "github.com" || strings.HasSuffix(h, ".githubusercontent.com")) {
		return nil
	}
	return fmt.Errorf("refusing to download from %s", u.Host)
}

var client = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	return checkHost(req.URL.String())
}}

func get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if err := checkHost(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "devlets/"+Current)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download too large")
	}
	return data, nil
}

func latestRelease(ctx context.Context) (*release, error) {
	data, err := get(ctx, strings.TrimRight(BaseURL, "/")+releasePath, 4<<20)
	if err != nil {
		return nil, err
	}
	var r release
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Tag == "" {
		return nil, errors.New("release has no tag")
	}
	return &r, nil
}

// Check returns the latest release version (no leading v) and whether it is
// newer than the running one. A "dev" build is never older.
func Check(ctx context.Context) (latest string, newer bool, err error) {
	r, err := latestRelease(ctx)
	if err != nil {
		return "", false, err
	}
	latest = strings.TrimPrefix(r.Tag, "v")
	return latest, Newer(Current, latest), nil
}

// Newer reports whether latest is a higher semver than current.
func Newer(current, latest string) bool {
	if current == "dev" || current == "" {
		return false
	}
	c, cok := parse(current)
	l, lok := parse(latest)
	if !cok || !lok {
		return false
	}
	for i := 0; i < 3; i++ {
		if l.n[i] != c.n[i] {
			return l.n[i] > c.n[i]
		}
	}
	return c.pre != "" && l.pre == "" // 1.0.0 is newer than 1.0.0-rc1
}

type semver struct {
	n   [3]int
	pre string
}

func parse(v string) (semver, bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var s semver
	if i := strings.IndexByte(v, '-'); i >= 0 {
		s.pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return s, false
		}
		s.n[i] = n
	}
	return s, true
}

// Apply installs the latest release over the running binary if it is newer.
// The archive's SHA-256 must match checksums.txt before anything is replaced.
func Apply(ctx context.Context) error {
	r, err := latestRelease(ctx)
	if err != nil {
		return err
	}
	version := strings.TrimPrefix(r.Tag, "v")
	if Current == "dev" {
		return errors.New("this is a development build; install a release to update")
	}
	if !Newer(Current, version) {
		return nil
	}
	name := AssetName(version, runtime.GOOS, runtime.GOARCH)
	var archiveURL, sumsURL string
	for _, a := range r.Assets {
		switch a.Name {
		case name:
			archiveURL = a.URL
		case "checksums.txt":
			sumsURL = a.URL
		}
	}
	if archiveURL == "" || sumsURL == "" {
		return fmt.Errorf("release %s has no %s or checksums.txt", r.Tag, name)
	}
	sums, err := get(ctx, sumsURL, 1<<20)
	if err != nil {
		return err
	}
	archive, err := get(ctx, archiveURL, maxDownload)
	if err != nil {
		return err
	}
	if err := verify(sums, name, archive); err != nil {
		return err
	}
	bin, err := extract(name, archive, binaryName())
	if err != nil {
		return err
	}
	exe, err := ExePath()
	if err != nil {
		return err
	}
	return replace(exe, bin)
}

func verify(sums []byte, name string, archive []byte) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s; nothing was changed", name)
	}
	return nil
}

// readCapped reads r fully, failing instead of truncating past maxDownload.
func readCapped(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownload {
		return nil, errors.New("archive entry too large")
	}
	return data, nil
}

func extract(archiveName string, archive []byte, binary string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == binary && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return readCapped(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in archive", binary)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in archive", binary)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == binary {
			return readCapped(tr)
		}
	}
}

// replace swaps exe for bin. A running .exe cannot be overwritten on Windows
// but can be renamed, so the old one moves to .old and CleanupOld removes it
// on the next run. Elsewhere a rename over the target is atomic.
func replace(exe string, bin []byte) error {
	next := exe + ".new"
	if err := os.WriteFile(next, bin, 0o755); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Rename(next, exe); err != nil {
			os.Remove(next)
			return err
		}
		return nil
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(next)
		return err
	}
	if err := os.Rename(next, exe); err != nil {
		os.Rename(old, exe) // put the original back
		os.Remove(next)
		return err
	}
	return nil
}

// CleanupOld deletes the .old binary a previous Windows update left behind.
func CleanupOld() {
	if exe, err := ExePath(); err == nil {
		os.Remove(exe + ".old")
	}
}

// Notice prints the "newer version" line at most once a week. It checks the
// network for at most noticeTimeout and stays silent on any error.
func Notice(out io.Writer, now time.Time) {
	if Current == "dev" {
		return
	}
	st, err := store.LoadState()
	if err != nil || now.Sub(st.UpdateCheckedAt) < noticeEvery {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), noticeTimeout)
	defer cancel()
	_, newer, err := Check(ctx)
	// Reload: a push may have saved state while we waited on the network.
	// Change only our field, and do not save if state cannot be read.
	if fresh, lerr := store.LoadState(); lerr == nil {
		fresh.UpdateCheckedAt = now // also after a failure, so offline status stays fast
		_ = store.SaveState(fresh)
	}
	if err == nil && newer {
		fmt.Fprintln(out, "A newer version is available: devlets update")
	}
}
