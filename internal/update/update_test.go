package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/store"
)

func binName() string {
	if runtime.GOOS == "windows" {
		return "devlets.exe"
	}
	return "devlets"
}

// archiveFor builds the archive for this OS/arch holding one binary.
func archiveFor(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(binName())
		w.Write([]byte(content))
		zw.Close()
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: binName(), Mode: 0o755, Size: int64(len(content))})
		tw.Write([]byte(content))
		tw.Close()
		gz.Close()
	}
	return buf.Bytes()
}

// fakeRelease serves a release with the given tag. checksumOf is the archive
// bytes the checksum line is computed from (differs from archive to simulate
// tampering).
func fakeRelease(t *testing.T, tag string, archive, checksumOf []byte) *httptest.Server {
	t.Helper()
	name := AssetName(strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(checksumOf)
	checks := hex.EncodeToString(sum[:]) + "  " + name + "\n" +
		strings.Repeat("0", 64) + "  devlets_other.tar.gz\n"
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/joaquingalang/devlets-relay/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
			tag, name, srv.URL+"/dl/"+name, srv.URL+"/dl/checksums.txt")
	})
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(checks)) })
	mux.HandleFunc("/dl/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func setup(t *testing.T, srv *httptest.Server, current string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), binName())
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldBase, oldCur, oldExe := BaseURL, Current, ExePath
	BaseURL, Current, ExePath = srv.URL, current, func() (string, error) { return exe, nil }
	t.Cleanup(func() { BaseURL, Current, ExePath = oldBase, oldCur, oldExe })
	return exe
}

func TestUpdateVerifiesChecksum(t *testing.T) {
	good := archiveFor(t, "NEW")
	bad := archiveFor(t, "TAMPERED")
	srv := fakeRelease(t, "v1.2.0", bad, good) // checksums.txt describes another archive
	exe := setup(t, srv, "1.1.0")

	err := Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want checksum error, got %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "OLD" {
		t.Errorf("binary changed to %q despite bad checksum", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Errorf("leftover files after failed update: %v", entries)
	}
}

func TestUpdateApplies(t *testing.T) {
	a := archiveFor(t, "NEW")
	srv := fakeRelease(t, "v1.10.0", a, a)
	exe := setup(t, srv, "v1.9.0")

	if err := Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "NEW" {
		t.Errorf("binary = %q, want NEW", got)
	}
	CleanupOld() // the .old left on Windows goes away on the next run
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestUpdateNotNewer(t *testing.T) {
	a := archiveFor(t, "NEW")
	for _, c := range []struct {
		cur, latest string
		newer       bool
	}{
		{"1.2.0", "v1.2.0", false},
		{"v1.3.0", "v1.2.0", false},
		{"0.9.0", "v0.10.0", true},
		{"dev", "v9.0.0", false},
	} {
		srv := fakeRelease(t, c.latest, a, a)
		exe := setup(t, srv, c.cur)
		latest, newer, err := Check(context.Background())
		if err != nil || newer != c.newer || latest != strings.TrimPrefix(c.latest, "v") {
			t.Errorf("Check(%s vs %s) = %q,%v,%v", c.cur, c.latest, latest, newer, err)
		}
		if !c.newer && c.cur != "dev" {
			if err := Apply(context.Background()); err != nil {
				t.Errorf("Apply when not newer: %v", err)
			}
			if got, _ := os.ReadFile(exe); string(got) != "OLD" {
				t.Errorf("%s vs %s: binary replaced although not newer", c.cur, c.latest)
			}
		}
	}
}

func TestAssetNames(t *testing.T) {
	for _, c := range []struct{ os, arch, want string }{
		{"windows", "amd64", "devlets_1.0.0_windows_amd64.zip"},
		{"windows", "arm64", "devlets_1.0.0_windows_arm64.zip"},
		{"darwin", "arm64", "devlets_1.0.0_darwin_all.tar.gz"},
		{"darwin", "amd64", "devlets_1.0.0_darwin_all.tar.gz"},
		{"linux", "amd64", "devlets_1.0.0_linux_amd64.tar.gz"},
		{"linux", "arm64", "devlets_1.0.0_linux_arm64.tar.gz"},
	} {
		if got := AssetName("1.0.0", c.os, c.arch); got != c.want {
			t.Errorf("AssetName(%s,%s) = %s, want %s", c.os, c.arch, got, c.want)
		}
	}
}

func TestNoticeOncePerWeek(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, `{"tag_name":"v2.0.0","assets":[]}`)
	}))
	defer srv.Close()
	setup(t, srv, "1.0.0")
	now := time.Now()

	var out bytes.Buffer
	Notice(&out, now)
	if !strings.Contains(out.String(), "A newer version is available: devlets update") {
		t.Fatalf("no notice: %q", out.String())
	}
	out.Reset()
	Notice(&out, now.Add(24*time.Hour))
	if out.Len() != 0 || hits != 1 {
		t.Errorf("checked again within a week: out=%q hits=%d", out.String(), hits)
	}
	Notice(&out, now.Add(8*24*time.Hour))
	if hits != 2 || out.Len() == 0 {
		t.Errorf("no re-check after a week: hits=%d", hits)
	}
}

func TestNoticeSilentOnError(t *testing.T) {
	t.Setenv("DEVLETS_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	setup(t, srv, "1.0.0")
	var out bytes.Buffer
	Notice(&out, time.Now())
	if out.Len() != 0 {
		t.Errorf("printed on error: %q", out.String())
	}
	_ = store.Dir
}
