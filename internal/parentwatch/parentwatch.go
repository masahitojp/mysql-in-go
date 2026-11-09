// Package parentwatch ties a cooperating child to its parent's pipe lifetime.
// It does not use PR_SET_PDEATHSIG or depend on a particular Go OS thread.
package parentwatch

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const descriptorEnv = "MYSQL_IN_GO_PARENT_FD"

// Start starts cmd with a read-only lifetime pipe inherited by the child.
// The caller must retain the writer until cmd.Wait returns, then close it.
// The child executable must call WatchFromEnv; other executables are unprotected.
func Start(cmd *exec.Cmd) (*os.File, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, reader)
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, descriptorEnv+"=") {
			filtered = append(filtered, entry)
		}
	}
	cmd.Env = append(filtered, descriptorEnv+"="+strconv.Itoa(fd))
	err = cmd.Start()
	_ = reader.Close()
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	return writer, nil
}

// WatchFromEnv arms the child before entering native MySQL. Standalone launches
// without the internal environment variable are unchanged. EOF/read failure
// force-exits the entire child with code 125; this is not graceful SQL shutdown.
// No directory is deleted here because a fixture may own/share the baseline.
func WatchFromEnv() error {
	value, ok := os.LookupEnv(descriptorEnv)
	if !ok {
		return nil
	}
	_ = os.Unsetenv(descriptorEnv)
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return fmt.Errorf("invalid parent pipe descriptor %q", value)
	}
	reader := os.NewFile(uintptr(fd), "parent-lifetime")
	if reader == nil {
		return fmt.Errorf("invalid parent pipe descriptor %d", fd)
	}
	info, err := reader.Stat()
	if err != nil {
		reader.Close()
		return err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		reader.Close()
		return fmt.Errorf("parent descriptor %d is not a pipe", fd)
	}
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		_ = reader.Close()
		_, _ = fmt.Fprintln(os.Stderr, "mysqlmem: parent pipe closed; terminating child (exit 125)")
		os.Exit(125)
	}()
	return nil
}
