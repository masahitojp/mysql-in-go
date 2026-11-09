package mysqltest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyTree copies a closed server's data. No hard links or shared writable
// database files are created. Symlinks and special files are rejected.
func copyTree(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(dst, relative)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported baseline file: %s", relative)
		}
		return copyFile(ctx, path, dest)
	})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func copyFile(ctx context.Context, src, dst string) (err error) {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if e := output.Close(); err == nil {
			err = e
		}
	}()
	_, err = io.Copy(output, contextReader{ctx: ctx, reader: input})
	return err
}
