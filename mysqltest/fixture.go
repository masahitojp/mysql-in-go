// Package mysqltest prepares one cold MySQL baseline and starts independent
// child servers from private file copies for individual tests.
package mysqltest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"mysqlmem/internal/tempstore"
	"mysqlmem/mysql"
)

var ErrClosed = errors.New("mysqltest: fixture closed")

type Options struct {
	MySQL       mysql.Options
	Prepare     func(context.Context, *sql.DB) error
	MaxChildren int // Zero defaults to two. Includes copying, starting and closing.
}

type Fixture struct {
	mu          sync.Mutex
	opts        mysql.Options
	dir         string
	cancel      context.CancelFunc
	life        context.Context
	slots       chan struct{}
	ops         sync.WaitGroup
	children    map[*Child]struct{}
	owners      map[string]*owned
	closed      bool
	failure     error
	once        sync.Once
	closeErr    error
	preparation time.Duration
}
type owned struct {
	ready chan struct{}
	child *Child
	err   error
}

// Child owns a copied datadir and server. Close clients/Rows/transactions before
// Close. Use Unwrap only when an integration explicitly needs a raw SQL pool.
type Child struct {
	server    *mysql.DB
	fixture   *Fixture
	dir       string
	once      sync.Once
	err       error
	CopyTime  time.Duration
	StartTime time.Duration
}

// New prepares and cleanly stops one server, then retains its datadir as a cold
// baseline. The startup context does not govern the returned fixture's lifetime.
// Prepare must return with all Rows and transactions closed.
func New(ctx context.Context, o Options) (*Fixture, error) {
	begin := time.Now()
	if o.MaxChildren < 0 {
		return nil, errors.New("mysqltest: MaxChildren must not be negative")
	}
	if o.MySQL.DataDir != "" {
		return nil, errors.New("mysqltest: DataDir is owned by the fixture; do not specify it")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o.MaxChildren == 0 {
		o.MaxChildren = 2
	}
	// Resolve build assets once; later tests may change their working directory.
	for _, path := range []struct {
		value    *string
		fallback string
	}{
		{&o.MySQL.Binary, "bin/mysqlmem"},
		{&o.MySQL.BaseDir, ".work/mysql-build"},
		{&o.MySQL.CharacterSetsDir, ".work/mysql/share/charsets"},
	} {
		if *path.value == "" {
			*path.value = path.fallback
		}
		absolute, err := filepath.Abs(*path.value)
		if err != nil {
			return nil, err
		}
		*path.value = absolute
	}
	if o.MySQL.Database == "" {
		o.MySQL.Database = "test"
	}
	parent, err := tempstore.Parent(o.MySQL.Tmpfs)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "mysql-fixture-")
	if err != nil {
		return nil, err
	}
	baseline := o.MySQL
	baseline.Database = ""
	baseline.CreateDatabase = false
	baseline.FastShutdown = false
	baseline.DataDir = filepath.Join(dir, "baseline")
	server, err := mysql.StartContext(ctx, baseline)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	// Close/reap the server before deleting data on every preparation failure.
	fail := func(cause error) (*Fixture, error) {
		stop := server.Close()
		if stop == nil {
			stop = os.RemoveAll(dir)
		}
		if stop != nil {
			return nil, fmt.Errorf("mysqltest: preparation failed; retained %s: %w", dir, errors.Join(cause, stop))
		}
		return nil, cause
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if server.Close() == nil {
				_ = os.RemoveAll(dir)
			}
			panic(recovered)
		}
	}()
	quoted := "`" + strings.ReplaceAll(o.MySQL.Database, "`", "``") + "`"
	if _, err := server.ExecContext(ctx, "CREATE DATABASE "+quoted); err != nil {
		return fail(err)
	}
	cfg, err := driver.ParseDSN(server.DSN())
	if err != nil {
		return fail(err)
	}
	cfg.DBName = o.MySQL.Database
	connector, err := driver.NewConnector(cfg)
	if err != nil {
		return fail(err)
	}
	pool := sql.OpenDB(connector)
	defer pool.Close()
	if o.Prepare != nil {
		err = o.Prepare(ctx, pool)
	}
	poolErr := pool.Close()
	if err != nil || poolErr != nil {
		return fail(errors.Join(err, poolErr))
	}
	if _, err := server.ExecContext(ctx, "SET GLOBAL innodb_fast_shutdown=0"); err != nil {
		return fail(err)
	}
	if err := server.Close(); err != nil {
		return nil, fmt.Errorf("mysqltest: baseline shutdown; retained %s: %w", dir, err)
	}
	life, cancel := context.WithCancel(context.Background())
	return &Fixture{opts: o.MySQL, dir: dir, cancel: cancel, life: life, slots: make(chan struct{}, o.MaxChildren), children: make(map[*Child]struct{}), owners: make(map[string]*owned), preparation: time.Since(begin)}, nil
}

func (f *Fixture) PreparationTime() time.Duration { return f.preparation }

// Open waits for bounded capacity, copies the stopped baseline, and starts a
// private server. Call Child.Close to release capacity; writes never reach peers.
func (f *Fixture) Open(ctx context.Context) (*Child, error) {
	f.mu.Lock()
	if f.closed || f.failure != nil {
		err := errors.Join(ErrClosed, f.failure)
		f.mu.Unlock()
		return nil, err
	}
	f.ops.Add(1)
	f.mu.Unlock()
	defer f.ops.Done()
	startup, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(f.life, cancel)
	defer stop()
	select {
	case f.slots <- struct{}{}:
	case <-startup.Done():
		return nil, f.contextError(ctx)
	}
	release := true
	defer func() {
		if release {
			<-f.slots
		}
	}()
	if err := startup.Err(); err != nil {
		return nil, f.contextError(ctx)
	}
	dir, err := os.MkdirTemp(f.dir, "child-")
	if err != nil {
		return nil, err
	}
	begin := time.Now()
	if err := copyTree(startup, filepath.Join(f.dir, "baseline"), filepath.Join(dir, "data")); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	copyTime := time.Since(begin)
	opts := f.opts
	opts.DataDir = filepath.Join(dir, "data")
	begin = time.Now()
	server, err := mysql.StartContext(startup, opts)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	c := &Child{server: server, fixture: f, dir: dir, CopyTime: copyTime, StartTime: time.Since(begin)}
	// Track it before releasing the in-flight operation so Close cannot miss it.
	f.mu.Lock()
	f.children[c] = struct{}{}
	closed := f.closed || f.failure != nil
	f.mu.Unlock()
	release = false
	if closed {
		_ = c.Close()
		return nil, ErrClosed
	}
	return c, nil
}
func (f *Fixture) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrClosed
}

// DB returns one test-owned pool per t.Name. Repeated calls in the same test
// share the child. Cleanup stops/removes it even when an assertion fails.
func (f *Fixture) DB(t testing.TB) *sql.DB {
	t.Helper()
	key := t.Name()
	f.mu.Lock()
	if f.closed || f.failure != nil {
		err := errors.Join(ErrClosed, f.failure)
		f.mu.Unlock()
		t.Fatal(err)
		return nil
	}
	entry, exists := f.owners[key]
	if !exists {
		entry = &owned{ready: make(chan struct{})}
		f.owners[key] = entry
	}
	f.mu.Unlock()
	if !exists {
		entry.child, entry.err = f.Open(t.Context())
		if entry.err == nil {
			t.Cleanup(func() {
				if err := entry.child.Close(); err != nil {
					t.Errorf("mysqltest cleanup: %v", err)
				}
			})
		}
		close(entry.ready)
	} else {
		select {
		case <-entry.ready:
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
			return nil
		}
	}
	if entry.err != nil {
		t.Fatalf("mysqltest startup: %v", entry.err)
		return nil
	}
	return entry.child.Unwrap()
}

func (c *Child) Close() error {
	c.once.Do(func() {
		c.err = c.server.Close()
		if c.err == nil {
			c.err = os.RemoveAll(c.dir)
		}
		f := c.fixture
		f.mu.Lock()
		delete(f.children, c)
		if c.err != nil {
			f.failure = errors.Join(f.failure, c.err)
			f.cancel()
		}
		f.mu.Unlock()
		<-f.slots
	})
	return c.err
}

// Close stops admission and in-flight startups, closes all children, then
// removes the baseline. Failed cleanup is returned and its files are retained.
func (f *Fixture) Close() error {
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.cancel()
		f.mu.Unlock()
		f.ops.Wait()
		f.mu.Lock()
		children := make([]*Child, 0, len(f.children))
		for c := range f.children {
			children = append(children, c)
		}
		f.mu.Unlock()
		for _, c := range children {
			f.closeErr = errors.Join(f.closeErr, c.Close())
		}
		f.mu.Lock()
		f.closeErr = errors.Join(f.closeErr, f.failure)
		f.mu.Unlock()
		if f.closeErr == nil {
			f.closeErr = os.RemoveAll(f.dir)
		}
		if f.closeErr != nil {
			f.closeErr = fmt.Errorf("mysqltest: retained %s: %w", f.dir, f.closeErr)
		}
	})
	return f.closeErr
}
