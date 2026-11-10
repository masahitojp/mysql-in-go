package serverbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func archive(t *testing.T, name string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	entries := []string{"bin/mysqlmem", "share/charsets/Index.xml", "share/english/errmsg.sys", "lib/private/icudt77l/unames.icu"}
	if name != "" {
		entries = []string{name}
	}
	for _, name := range entries {
		data := []byte("test")
		h := &tar.Header{Name: name, Mode: 0700, Size: int64(len(data)), Typeflag: kind}
		if kind == tar.TypeSymlink {
			h.Linkname = "/tmp/outside"
			h.Size = 0
			data = nil
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestVerifiedDownloadConcurrentAndOfflineCache(t *testing.T) {
	data := archive(t, "", tar.TypeReg)
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(data) }))
	defer server.Close()
	a := asset{server.URL, sum}
	dir := filepath.Join(t.TempDir(), "bundle")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if _, err := resolve(t.Context(), server.Client(), a, dir); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if requests.Load() == 0 {
		t.Fatal("not downloaded")
	}
	server.Close()
	p, err := resolve(t.Context(), server.Client(), a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Binary != filepath.Join(dir, "bin/mysqlmem") {
		t.Fatal(p)
	}
	if err := validate(dir); err != nil {
		t.Fatal(err)
	}
}
func TestFailedDownloadsDoNotPublish(t *testing.T) {
	valid := archive(t, "", tar.TypeReg)
	for _, tc := range []struct {
		name    string
		data    []byte
		badHash bool
		status  int
	}{
		{"checksum", valid, true, 200},
		{"traversal", archive(t, "../escape", tar.TypeReg), false, 200},
		{"absolute", archive(t, "/escape", tar.TypeReg), false, 200},
		{"symlink", archive(t, "link", tar.TypeSymlink), false, 200},
		{"missing-assets", archive(t, "other", tar.TypeReg), false, 200},
		{"http", valid, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write(tc.data) }))
			defer server.Close()
			sum := fmt.Sprintf("%x", sha256.Sum256(tc.data))
			if tc.badHash {
				sum = fmt.Sprintf("%064d", 0)
			}
			dir := filepath.Join(t.TempDir(), "bundle")
			if _, err := resolve(t.Context(), server.Client(), asset{server.URL, sum}, dir); err == nil {
				t.Fatal("accepted invalid archive")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("published failed bundle: %v", err)
			}
		})
	}
}
func TestCancelledDownload(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := resolve(ctx, http.DefaultClient, asset{"https://example.invalid", fmt.Sprintf("%064d", 0)}, filepath.Join(t.TempDir(), "bundle"))
	if err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestRealReleaseArchiveIntegration(t *testing.T) {
	filename := os.Getenv("MYSQL_IN_GO_BUNDLE_TEST_ARCHIVE")
	if filename == "" {
		t.Skip("set MYSQL_IN_GO_BUNDLE_TEST_ARCHIVE to test the assembled release")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "release")
	p, err := resolve(t.Context(), server.Client(), asset{server.URL, sum}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseDir != dir {
		t.Fatal(p)
	}
	if _, err := os.Stat(filepath.Join(dir, "licenses/MySQL-LICENSE.txt")); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if _, err = resolve(t.Context(), server.Client(), asset{server.URL, sum}, dir); err != nil {
		t.Fatalf("offline reuse: %v", err)
	}
}
