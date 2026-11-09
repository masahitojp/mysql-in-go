// Package mysql manages a Bao-linked MySQL server as a child process.
// The caller stays in a separate Go process and continues after DB.Close.
package mysql

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"mysqlmem/internal/tempstore"
)

// Options locates the server and its runtime assets. Relative paths are
// relative to the caller's working directory. Zero values use this repository's
// build layout. DataDir, when set, persists across Close and subsequent Start.
type Options struct {
	Tmpfs            bool // Linux only: keep control files and default data on verified /dev/shm tmpfs.
	Binary           string
	BaseDir          string
	CharacterSetsDir string
	Database         string // Optional default database; it must already exist.
	DataDir          string
	StartupTimeout   time.Duration
	ShutdownTimeout  time.Duration
}

// DB provides database/sql operations plus ownership of the child server.
// Call Close rather than DB.DB.Close to shut down and reap the child process.
// Close outstanding Rows and transactions before calling Close.
type DB struct {
	*sql.DB
	cmd             *exec.Cmd
	done            chan struct{}
	waitErr         error // written before done closes
	workDir         string
	log             *os.File
	shutdownTimeout time.Duration
	dsn             string
	once            sync.Once
	closeErr        error
}

// Start initializes a new database (unless DataDir already contains one),
// starts the server with TCP disabled, and waits until SQL connections work.
// It accepts zero or one Options value. The local server uses passwordless
// root authentication over a Unix socket in an owner-only directory.
func Start(options ...Options) (*DB, error) {
	return StartContext(context.Background(), options...)
}

// StartContext uses ctx only for startup. Canceling it after a successful Start
// does not terminate the server; the caller owns it until Close.
func StartContext(ctx context.Context, options ...Options) (*DB, error) {
	if len(options) > 1 {
		return nil, errors.New("mysql: expected zero or one Options value")
	}
	var o Options
	if len(options) == 1 {
		o = options[0]
	}
	if o.StartupTimeout < 0 || o.ShutdownTimeout < 0 {
		return nil, errors.New("mysql: timeouts must not be negative")
	}
	if o.StartupTimeout == 0 {
		o.StartupTimeout = 2 * time.Minute
	}
	if o.ShutdownTimeout == 0 {
		o.ShutdownTimeout = 30 * time.Second
	}
	defaults := []struct {
		value    *string
		fallback string
	}{{&o.Binary, "bin/mysqlmem"}, {&o.BaseDir, ".work/mysql-build"}, {&o.CharacterSetsDir, ".work/mysql/share/charsets"}}
	for _, p := range defaults {
		if *p.value == "" {
			*p.value = p.fallback
		}
		abs, err := filepath.Abs(*p.value)
		if err != nil {
			return nil, err
		}
		*p.value = abs
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("mysql: required path %s: %w", abs, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.StartupTimeout)
	defer cancel()
	parent, err := tempstore.Parent(o.Tmpfs)
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(parent, "mysql-in-go-")
	if err != nil {
		return nil, err
	}
	// Retain startup failures for diagnosis; successful Close removes this directory.
	fail := func(err error) (*DB, error) {
		return nil, fmt.Errorf("mysql: %w (logs: %s)", err, filepath.Join(work, "server.log"))
	}
	logFile, err := os.OpenFile(filepath.Join(work, "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.RemoveAll(work)
		return nil, err
	}
	ownedLog := true
	defer func() {
		if ownedLog {
			logFile.Close()
		}
	}()
	data := filepath.Join(work, "data")
	if o.DataDir != "" {
		data, err = filepath.Abs(o.DataDir)
		if err != nil {
			return fail(err)
		}
	}
	if err := os.MkdirAll(data, 0700); err != nil {
		return fail(err)
	}
	if o.Tmpfs {
		if err := tempstore.Verify(data); err != nil {
			return fail(err)
		}
	}
	entries, err := os.ReadDir(data)
	if err != nil {
		return fail(err)
	}
	initialized := false
	if len(entries) > 0 {
		info, err := os.Stat(filepath.Join(data, "mysql"))
		initialized = err == nil && info.IsDir()
		if !initialized {
			return fail(errors.New("DataDir is nonempty but does not contain a MySQL database"))
		}
	}
	socket := filepath.Join(work, "mysql.sock")
	if len(socket) >= 108 {
		return fail(errors.New("temporary directory makes Unix socket path too long"))
	}
	args := []string{"--no-defaults", "--basedir=" + o.BaseDir, "--datadir=" + data, "--lc-messages-dir=" + filepath.Join(o.BaseDir, "share"), "--character-sets-dir=" + o.CharacterSetsDir}
	if o.Tmpfs {
		sqlTemp := filepath.Join(work, "sql-tmp")
		if err := os.Mkdir(sqlTemp, 0700); err != nil {
			return fail(err)
		}
		args = append(args, "--tmpdir="+sqlTemp)
	}
	if !initialized {
		init := exec.CommandContext(ctx, o.Binary, append(append([]string{}, args...), "--initialize-insecure")...)
		init.Stdout = logFile
		init.Stderr = logFile
		if err := init.Run(); err != nil {
			return fail(fmt.Errorf("initialize database: %w", err))
		}
	}
	args = append(args, "--socket="+socket, "--pid-file="+filepath.Join(work, "mysqld.pid"), "--skip-networking", "--innodb-buffer-pool-size=64M")
	cmd := exec.Command(o.Binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	d := &DB{cmd: cmd, done: make(chan struct{}), workDir: work, log: logFile, shutdownTimeout: o.ShutdownTimeout}
	ownedLog = false
	go func() { d.waitErr = cmd.Wait(); close(d.done) }()
	config := driver.NewConfig()
	config.User = "root"
	config.Net = "unix"
	config.Addr = socket
	config.DBName = o.Database
	config.Timeout = time.Second
	config.Logger = log.New(logFile, "driver: ", log.LstdFlags)
	connector, err := driver.NewConnector(config)
	if err != nil {
		d.abort()
		return fail(err)
	}
	d.DB = sql.OpenDB(connector)
	d.dsn = config.FormatDSN()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err = d.DB.PingContext(ctx); err == nil {
			return d, nil
		}
		select {
		case <-d.done:
			d.DB.Close()
			d.log.Close()
			return fail(fmt.Errorf("server exited before readiness: %v", d.waitErr))
		case <-ctx.Done():
			d.DB.Close()
			d.abort()
			return fail(fmt.Errorf("startup: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
}

func (d *DB) abort() { _ = d.cmd.Process.Kill(); <-d.done; _ = d.log.Close() }

// Close sends SQL SHUTDOWN, closes connections, and waits for the child to exit.
// It is idempotent and safe for concurrent callers. On timeout the child is
// killed and an error is returned. Failed runs retain their logs. A caller-
// supplied DataDir is never removed; default temporary data is removed on success.
func (d *DB) Close() error {
	d.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.shutdownTimeout)
		defer cancel()
		// Drain idle connections while the server is still alive.
		d.DB.SetMaxIdleConns(0)
		select {
		case <-d.done:
			d.closeErr = fmt.Errorf("mysql: server exited before Close: %v", d.waitErr)
		default:
			// A SHUTDOWN may close its connection before the reply is read.
			// Use the raw connection so database/sql cannot retry the command.
			conn, err := d.DB.Conn(ctx)
			if err != nil {
				d.closeErr = err
			} else {
				d.closeErr = conn.Raw(func(raw any) error {
					executor, ok := raw.(sqldriver.ExecerContext)
					if !ok {
						return errors.New("mysql: driver does not support shutdown execution")
					}
					_, err := executor.ExecContext(ctx, "SHUTDOWN", nil)
					if err != nil {
						return err
					}
					// SHUTDOWN owns this connection's end of life. Discard it
					// immediately instead of returning it to the idle pool.
					return sqldriver.ErrBadConn
				})
				_ = conn.Close()
			}
		}
		poolErr := d.DB.Close()
		select {
		case <-d.done:
		case <-ctx.Done():
			_ = d.cmd.Process.Kill()
			<-d.done
			d.closeErr = errors.Join(d.closeErr, fmt.Errorf("mysql: shutdown timeout: %w", ctx.Err()))
		}
		// A clean process exit confirms successful shutdown even when its
		// final protocol reply was lost. SQL errors remain errors.
		if d.waitErr == nil && !errors.Is(d.closeErr, context.DeadlineExceeded) {
			if shutdownDisconnect(d.closeErr) {
				d.closeErr = nil
			}
			if shutdownDisconnect(poolErr) {
				poolErr = nil
			}
		}
		d.closeErr = errors.Join(d.closeErr, poolErr, d.waitErr, d.log.Close())
		if d.closeErr == nil {
			d.closeErr = os.RemoveAll(d.workDir)
		} else {
			d.closeErr = fmt.Errorf("mysql: close: %w (logs: %s)", d.closeErr, filepath.Join(d.workDir, "server.log"))
		}
	})
	return d.closeErr
}

func shutdownDisconnect(err error) bool {
	return errors.Is(err, sqldriver.ErrBadConn) || errors.Is(err, driver.ErrInvalidConn) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}

// DSN returns the connection string for this instance. Close clients before Close.
func (d *DB) DSN() string { return d.dsn }
