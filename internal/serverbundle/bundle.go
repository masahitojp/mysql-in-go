// Package serverbundle downloads immutable, checksummed server releases.
package serverbundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed manifest.json
var manifestData []byte

type asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Version string           `json:"version"`
	MySQL   string           `json:"mysql"`
	Assets  map[string]asset `json:"assets"`
}
type Paths struct{ Binary, BaseDir, CharacterSetsDir string }

func paths(dir string) Paths {
	return Paths{filepath.Join(dir, "bin/mysqlmem"), dir, filepath.Join(dir, "share/charsets")}
}

// Resolve uses a versioned user cache. It never invokes sudo or installs OS packages.
func Resolve(ctx context.Context) (Paths, error) {
	var m manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return Paths{}, err
	}
	key := runtime.GOOS + "-" + runtime.GOARCH
	a, ok := m.Assets[key]
	if !ok {
		return Paths{}, fmt.Errorf("mysql: no prebuilt server for %s in %s; supply Binary, BaseDir and CharacterSetsDir", key, m.Version)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, err
	}
	dir := filepath.Join(cache, "mysql-in-go", m.Version, key, a.SHA256)
	return resolve(ctx, http.DefaultClient, a, dir)
}

func resolve(ctx context.Context, client *http.Client, a asset, dir string) (Paths, error) {
	if len(a.SHA256) != 64 {
		return Paths{}, errors.New("mysql: invalid bundle checksum")
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil {
		return Paths{}, err
	}
	// Cache contents are private to the current user. The marker is published only
	// after checksum verification, complete extraction and validation.
	if marker, err := os.ReadFile(filepath.Join(dir, ".complete")); err == nil && string(marker) == a.SHA256 {
		if err := validate(dir); err == nil {
			return paths(dir), nil
		}
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return Paths{}, err
	}
	stage, err := os.MkdirTemp(parent, ".download-")
	if err != nil {
		return Paths{}, err
	}
	defer os.RemoveAll(stage)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return Paths{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Paths{}, fmt.Errorf("mysql: download server (preinstall for offline use): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Paths{}, fmt.Errorf("mysql: download server: HTTP %d", resp.StatusCode)
	}
	archive, err := os.CreateTemp(parent, ".archive-")
	if err != nil {
		return Paths{}, err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(archive, h), io.LimitReader(resp.Body, 256<<20+1))
	if err != nil {
		return Paths{}, err
	}
	if n > 256<<20 {
		return Paths{}, errors.New("mysql: server archive exceeds size limit")
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return Paths{}, errors.New("mysql: server archive checksum mismatch")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		return Paths{}, err
	}
	if err = extract(archive, stage); err != nil {
		return Paths{}, err
	}
	if err = validate(stage); err != nil {
		return Paths{}, err
	}
	if err = os.WriteFile(filepath.Join(stage, ".complete"), []byte(a.SHA256), 0600); err != nil {
		return Paths{}, err
	}
	if err = os.Rename(stage, dir); err != nil {
		// A concurrent caller may already have installed the same verified bundle.
		if marker, e := os.ReadFile(filepath.Join(dir, ".complete")); e != nil || string(marker) != a.SHA256 {
			return Paths{}, err
		}
		if e := validate(dir); e != nil {
			return Paths{}, e
		}
	}
	return paths(dir), nil
}
func validate(dir string) error {
	for _, name := range []string{"bin/mysqlmem", "share/charsets/Index.xml", "share/english/errmsg.sys", "lib/private/icudt77l/unames.icu"} {
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("mysql: invalid bundle file %s", name)
		}
		if name == "bin/mysqlmem" && info.Mode()&0111 == 0 {
			return errors.New("mysql: server is not executable")
		}
	}
	return nil
}
func extract(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := header.Name
		if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != strings.TrimSuffix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("mysql: unsafe archive path %q", name)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			total += header.Size
			if header.Size < 0 || total > 512<<20 {
				return errors.New("mysql: expanded bundle exceeds size limit")
			}
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 {
				mode = 0700
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(f, tr, header.Size)
			closeErr := f.Close()
			if err = errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("mysql: unsupported archive entry %q", name)
		}
	}
}

// Locate resolves explicit build paths, a local source checkout, or a release.
func Locate(ctx context.Context, binary, base, charsets string) (Paths, error) {
	if binary == "" && base == "" && charsets == "" {
		if _, err := os.Stat("bin/mysqlmem"); err != nil {
			return Resolve(ctx)
		}
	}
	if binary == "" {
		binary = "bin/mysqlmem"
	}
	if base == "" {
		base = ".work/mysql-build"
	}
	if charsets == "" {
		charsets = ".work/mysql/share/charsets"
	}
	values := []*string{&binary, &base, &charsets}
	for _, v := range values {
		p, err := filepath.Abs(*v)
		if err != nil {
			return Paths{}, err
		}
		*v = p
	}
	return Paths{binary, base, charsets}, nil
}
