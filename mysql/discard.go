package mysql

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Discard force-kills and reaps a tmpfs-backed server, then removes its owned
// temporary files without SQL SHUTDOWN. It shares Close's idempotent lifetime.
// A caller-supplied DataDir is NOT removed: only its owner may delete it.
// The data is not a clean baseline and must not be used as one.
// Rows/transactions should be closed by their clients before disposal.
func (d *DB) Discard() error {
	if !d.tmpfs {
		return errors.New("mysql: Discard requires verified tmpfs storage")
	}
	d.once.Do(func() {
		// Drain idle clients while the server is still alive, avoiding COM_QUIT
		// attempts against already-dead connections. This is not a SQL reset.
		d.pool.SetMaxIdleConns(0)
		select {
		case <-d.done:
			d.closeErr = unexpectedExit("server exited before Discard", d.waitErr)
		default:
			killErr := d.cmd.Process.Kill()
			<-d.done // Never delete native database files before the process exits.
			d.closeErr = killErr
			if killErr == nil {
				var exit *exec.ExitError
				if !errors.As(d.waitErr, &exit) {
					d.closeErr = unexpectedExit("unexpected Discard exit", d.waitErr)
				} else {
					status, ok := exit.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
						d.closeErr = unexpectedExit("unexpected Discard exit", d.waitErr)
					}
				}
			}
		}
		d.closeErr = errors.Join(d.closeErr, d.pool.Close(), d.releaseParent(), d.log.Close())
		if d.closeErr == nil {
			d.closeErr = os.RemoveAll(d.workDir)
		} else {
			d.closeErr = fmt.Errorf("mysql: discard: %w (logs: %s)", d.closeErr, d.workDir)
		}
	})
	return d.closeErr
}
