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
	"strings"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"mysqlmem/internal/parentwatch"
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
	Database         string // Optional default database.
	CreateDatabase   bool   // Create Database if missing before selecting it.
	FastShutdown     bool   // Start with innodb_fast_shutdown=2; restart needs crash recovery.
	DataDir          string
	StartupTimeout   time.Duration
	ShutdownTimeout  time.Duration
}

// DB provides database/sql operations plus ownership of the child server.
// The SQL pool is private; Close shuts down and reaps the child process.
// Close outstanding Rows and transactions before calling Close.
type DB struct {
	pool            *sql.DB
	cmd             *exec.Cmd
	parentPipe      *os.File
	done            chan struct{}
	waitErr         error // written before done closes
	workDir         string
	log             *os.File
	shutdownTimeout time.Duration
	adminConfig     *driver.Config
	dsn             string
	retainWork      bool
	tmpfs           bool
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
	if o.CreateDatabase && o.Database == "" {
		return nil, errors.New("mysql: CreateDatabase requires Database")
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
		lease, err := parentwatch.Start(init)
		if err != nil {
			return fail(fmt.Errorf("initialize database: %w", err))
		}
		err = init.Wait()
		_ = lease.Close()
		if err != nil {
			return fail(fmt.Errorf("initialize database: %w", err))
		}
	}
	args = append(args, "--socket="+socket, "--pid-file="+filepath.Join(work, "mysqld.pid"), "--skip-networking", "--innodb-buffer-pool-size=64M")
	if o.FastShutdown {
		args = append(args, "--innodb-fast-shutdown=2")
	}
	cmd := exec.Command(o.Binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	lease, err := parentwatch.Start(cmd)
	if err != nil {
		return fail(err)
	}
	d := &DB{tmpfs: o.Tmpfs, cmd: cmd, parentPipe: lease, done: make(chan struct{}), workDir: work, log: logFile, shutdownTimeout: o.ShutdownTimeout}
	ownedLog = false
	go func() { d.waitErr = cmd.Wait(); close(d.done) }()
	config := driver.NewConfig()
	config.User = "root"
	config.Net = "unix"
	config.Addr = socket
	// Always become ready against the system connection before selecting a DB.
	config.DBName = ""
	config.Timeout = time.Second
	config.Logger = log.New(logFile, "driver: ", log.LstdFlags)
	connector, err := driver.NewConnector(config)
	if err != nil {
		d.abort()
		return fail(err)
	}
	d.pool = sql.OpenDB(connector)
	d.adminConfig = config.Clone()
	d.dsn = config.FormatDSN()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err = d.pool.PingContext(ctx); err == nil {
			break
		}
		select {
		case <-d.done:
			d.pool.Close()
			d.log.Close()
			d.releaseParent()
			return fail(unexpectedExit("server exited before readiness", d.waitErr))
		case <-ctx.Done():
			d.pool.Close()
			d.abort()
			return fail(fmt.Errorf("startup: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
	startupFailure := func(cause error) (*DB, error) {
		d.retainWork = true
		return fail(errors.Join(cause, d.Close()))
	}
	if o.CreateDatabase {
		quoted := "`" + strings.ReplaceAll(o.Database, "`", "``") + "`"
		if _, err := d.pool.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+quoted); err != nil {
			return startupFailure(fmt.Errorf("create database: %w", err))
		}
	}
	if o.Database != "" {
		if err := d.pool.Close(); err != nil {
			return startupFailure(err)
		}
		config.DBName = o.Database
		connector, err := driver.NewConnector(config)
		if err != nil {
			return startupFailure(err)
		}
		d.pool = sql.OpenDB(connector)
		d.dsn = config.FormatDSN()
		if err := d.pool.PingContext(ctx); err != nil {
			return startupFailure(fmt.Errorf("select database: %w", err))
		}
	}
	return d, nil
}

func (d *DB) releaseParent() error {
	if d.parentPipe != nil {
		return d.parentPipe.Close()
	}
	return nil
}
func (d *DB) abort() { _ = d.cmd.Process.Kill(); <-d.done; _ = d.releaseParent(); _ = d.log.Close() }

// Close sends SQL SHUTDOWN, closes connections, and waits for the child to exit.
// It is idempotent and safe for concurrent callers. On timeout the child is
// killed and an error is returned. Failed runs retain their logs. A caller-
// supplied DataDir is never removed; default temporary data is removed on success.
func (d *DB) Close() error {
	d.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), d.shutdownTimeout)
		defer cancel()
		// Close client connections before stopping the server. The shutdown
		// connection is separately owned, so Unwrap().Close cannot strand it.
		poolErr := d.pool.Close()
		select {
		case <-d.done:
			d.closeErr = unexpectedExit("server exited before Close", d.waitErr)
		default:
			d.closeErr = d.requestShutdown(ctx)
		}
		select {
		case <-d.done:
		default:
			select {
			case <-d.done:
			case <-ctx.Done():
				_ = d.cmd.Process.Kill()
				<-d.done
				d.closeErr = errors.Join(d.closeErr, fmt.Errorf("mysql: shutdown timeout: %w", ctx.Err()))
			}
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
		waitErr := d.waitErr
		if waitErr != nil && errors.Is(d.closeErr, waitErr) {
			waitErr = nil
		}
		d.closeErr = errors.Join(d.closeErr, poolErr, waitErr, d.releaseParent(), d.log.Close())
		if d.closeErr == nil && !d.retainWork {
			d.closeErr = os.RemoveAll(d.workDir)
		} else if d.closeErr != nil {
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

func unexpectedExit(stage string, err error) error {
	if err == nil {
		return fmt.Errorf("mysql: %s (exit status 0)", stage)
	}
	return fmt.Errorf("mysql: %s: %w", stage, err)
}

func (d *DB) requestShutdown(ctx context.Context) error {
	pool := d.pool
	if d.adminConfig != nil {
		connector, err := driver.NewConnector(d.adminConfig.Clone())
		if err != nil {
			return err
		}
		pool = sql.OpenDB(connector)
		defer pool.Close()
	}
	pool.SetMaxIdleConns(0)
	conn, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(raw any) error {
		executor, ok := raw.(sqldriver.ExecerContext)
		if !ok {
			return errors.New("mysql: driver does not support shutdown execution")
		}
		if _, err := executor.ExecContext(ctx, "SHUTDOWN", nil); err != nil {
			return err
		}
		// Discard the connection; SQL SHUTDOWN has ended its lifetime.
		return sqldriver.ErrBadConn
	})
}
