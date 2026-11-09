package mysqltest

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"mysqlmem/internal/tempstore"
	"mysqlmem/mysql"
)

func TestCopyIsIndependent(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")
	if err := os.Mkdir(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("nested", "item")
	if err := os.WriteFile(filepath.Join(src, path), []byte("seed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(t.Context(), src, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, path), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(src, path)); err != nil || string(data) != "seed" {
		t.Fatalf("baseline changed: %q %v", data, err)
	}
}
func TestCopyRejectsSymlinksAndCancellation(t *testing.T) {
	src := t.TempDir()
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(t.Context(), src, filepath.Join(t.TempDir(), "copy")); err == nil {
		t.Fatal("symlink accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := copyTree(ctx, t.TempDir(), filepath.Join(t.TempDir(), "copy")); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
func TestInvalidFixtureOptions(t *testing.T) {
	for _, o := range []Options{{MaxChildren: -1}, {MySQL: mysql.Options{DataDir: t.TempDir()}}} {
		if f, err := New(t.Context(), o); err == nil || f != nil {
			t.Fatalf("got %v %v", f, err)
		}
	}
}
func TestAdmissionCancellationAndClose(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	f := &Fixture{dir: t.TempDir(), life: life, cancel: cancel, slots: make(chan struct{}, 1), children: make(map[*Child]struct{})}
	f.slots <- struct{}{}
	ctx, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if _, err := f.Open(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capacity wait: %v", err)
	}
	result := make(chan error, 1)
	go func() { _, err := f.Open(context.Background()); result <- err }()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("close wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not released")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
func fixtureOptions(t *testing.T) mysql.Options {
	t.Helper()
	if os.Getenv("MYSQLMEM_INTEGRATION") != "1" {
		t.Skip("run make test-fixture with bin/mysqlmem built")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return mysql.Options{Binary: filepath.Join(root, "bin/mysqlmem"), BaseDir: filepath.Join(root, ".work/mysql-build"), CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets"), Tmpfs: os.Getenv("MYSQLMEM_TMPFS") == "1"}
}
func TestFixtureIntegration(t *testing.T) {
	var prepares atomic.Int32
	f, err := New(t.Context(), Options{MySQL: fixtureOptions(t), MaxChildren: 2, Prepare: func(ctx context.Context, db *sql.DB) error {
		prepares.Add(1)
		if _, err := db.ExecContext(ctx, "CREATE TABLE items(id INT PRIMARY KEY, label VARCHAR(32)) ENGINE=InnoDB"); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, "INSERT INTO items VALUES(1,'seed')")
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	if os.Getenv("MYSQLMEM_TMPFS") == "1" {
		if err := tempstore.Verify(f.dir); err != nil {
			t.Fatal(err)
		}
		t.Logf("verified tmpfs baseline and child root: %s", f.dir)
	}
	t.Logf("one-time prepare: %.1f ms", float64(f.PreparationTime())/float64(time.Millisecond))
	t.Run("committed change", func(t *testing.T) {
		db := f.DB(t)
		if db != f.DB(t) {
			t.Fatal("same test did not reuse its child")
		}
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), "DELETE FROM items"); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 0 {
			t.Fatalf("count %d %v", count, err)
		}
	})
	t.Run("seed restored", func(t *testing.T) {
		var label string
		if err := f.DB(t).QueryRowContext(t.Context(), "SELECT label FROM items WHERE id=1").Scan(&label); err != nil || label != "seed" {
			t.Fatalf("seed %q %v", label, err)
		}
	})
	t.Run("parallel isolation", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			t.Run(string(rune('A'+i)), func(t *testing.T) {
				t.Parallel()
				db := f.DB(t)
				if _, err := db.ExecContext(t.Context(), "INSERT INTO items VALUES(2,'private')"); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 2 {
					t.Fatalf("count %d %v", count, err)
				}
			})
		}
	})
	if prepares.Load() != 1 {
		t.Fatalf("Prepare ran %d times", prepares.Load())
	}
	f.mu.Lock()
	active := len(f.children)
	f.mu.Unlock()
	if active != 0 || len(f.slots) != 0 {
		t.Fatalf("children/slots remain: %d/%d", active, len(f.slots))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatalf("baseline remains: %v", err)
	}
	if _, err := f.Open(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("open after close: %v", err)
	}
}
func TestFixtureTimingIntegration(t *testing.T) {
	f, err := New(t.Context(), Options{MySQL: fixtureOptions(t), Prepare: func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "CREATE TABLE items(id INT PRIMARY KEY) ENGINE=InnoDB"); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, "INSERT INTO items VALUES(1)")
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	if os.Getenv("MYSQLMEM_TMPFS") == "1" {
		if err := tempstore.Verify(f.dir); err != nil {
			t.Fatal(err)
		}
		t.Logf("verified tmpfs baseline and child root: %s", f.dir)
	}
	t.Logf("one-time prepare: %.1f ms", float64(f.PreparationTime())/float64(time.Millisecond))
	var copySum, startSum, totalSum time.Duration
	for i := 0; i < 5; i++ {
		begin := time.Now()
		child, err := f.Open(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { child.Close() })
		var one int
		if err := child.QueryRowContext(t.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("SELECT 1: %d %v", one, err)
		}
		stop := time.Now()
		if err := child.Close(); err != nil {
			t.Fatal(err)
		}
		closeTime := time.Since(stop)
		total := time.Since(begin)
		copySum += child.CopyTime
		startSum += child.StartTime
		totalSum += total
		t.Logf("child %d: copy=%.1f ms start=%.1f ms close=%.1f ms total=%.1f ms", i+1, float64(child.CopyTime)/float64(time.Millisecond), float64(child.StartTime)/float64(time.Millisecond), float64(closeTime)/float64(time.Millisecond), float64(total)/float64(time.Millisecond))
	}
	t.Logf("mean (5 children): copy=%.1f ms start=%.1f ms total=%.1f ms", float64(copySum)/5/float64(time.Millisecond), float64(startSum)/5/float64(time.Millisecond), float64(totalSum)/5/float64(time.Millisecond))
}
func TestPrepareFailureIntegration(t *testing.T) {
	cause := errors.New("seed failed")
	f, err := New(t.Context(), Options{MySQL: fixtureOptions(t), Prepare: func(context.Context, *sql.DB) error { return cause }})
	if f != nil || !errors.Is(err, cause) {
		t.Fatalf("got %v %v", f, err)
	}
}

func TestCloseActiveChildrenIntegration(t *testing.T) {
	f, err := New(t.Context(), Options{MySQL: fixtureOptions(t), MaxChildren: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	child, err := f.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := f.Open(context.Background()); result <- err }()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("waiting child: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not canceled")
	}
	var one int
	if err := child.QueryRow("SELECT 1").Scan(&one); err == nil {
		t.Fatal("child still accepts queries")
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatalf("fixture directory remains: %v", err)
	}
}
