package mysqltest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func discardFixture(t *testing.T, discard bool) *Fixture {
	t.Helper()
	if os.Getenv("MYSQLMEM_DISCARD_CHILDREN") != "1" {
		t.Skip("run make test-fixture-discard")
	}
	o := fixtureOptions(t)
	o.Tmpfs = true
	// FastShutdown is deliberately irrelevant for disposable children.
	o.FastShutdown = true
	f, err := New(t.Context(), Options{MySQL: o, DiscardChildren: discard, Prepare: func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "CREATE TABLE items(id INT PRIMARY KEY,label VARCHAR(32)) ENGINE=InnoDB"); err != nil {
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
	return f
}

func TestDiscardChildrenRejectDiskStorage(t *testing.T) {
	if f, err := New(t.Context(), Options{DiscardChildren: true}); err == nil || f != nil {
		t.Fatalf("got %v %v", f, err)
	}
}

func TestDiscardedTransactionDoesNotLeakIntegration(t *testing.T) {
	f := discardFixture(t, true)
	original, err := os.ReadFile(filepath.Join(f.dir, "baseline", "auto.cnf"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var mode int
	if err := first.QueryRow("SELECT @@innodb_fast_shutdown").Scan(&mode); err != nil || mode != 1 {
		t.Fatalf("discard child need not enable mode 2: %d %v", mode, err)
	}
	tx, err := first.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE items SET label='uncommitted' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback() // Release the caller's client resources; no SQL reset is needed.
	if _, err := os.Stat(first.dir); !os.IsNotExist(err) {
		t.Fatalf("child data remains: %v", err)
	}
	if len(f.slots) != 0 {
		t.Fatal("capacity not released after disposal")
	}
	second, err := f.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var label string
	if err := second.QueryRow("SELECT label FROM items WHERE id=1").Scan(&label); err != nil || label != "seed" {
		t.Fatalf("seed=%q %v", label, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(f.dir, "baseline", "auto.cnf"))
	if err != nil || string(after) != string(original) {
		t.Fatalf("baseline changed: %v", err)
	}
}

func TestDiscardTimingIntegration(t *testing.T) {
	for _, discard := range []bool{false, true} {
		name := "graceful"
		if discard {
			name = "discard"
		}
		t.Run(name, func(t *testing.T) {
			f := discardFixture(t, discard)
			t.Logf("one-time prepare=%.1f ms", float64(f.PreparationTime())/float64(time.Millisecond))
			var copySum, startSum, closeSum, totalSum time.Duration
			for i := 0; i < 5; i++ {
				begin := time.Now()
				c, err := f.Open(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				var got int
				if err := c.QueryRow("SELECT 1").Scan(&got); err != nil || got != 1 {
					t.Fatalf("SELECT 1=%d %v", got, err)
				}
				stop := time.Now()
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				closeTime := time.Since(stop)
				total := time.Since(begin)
				copySum += c.CopyTime
				startSum += c.StartTime
				closeSum += closeTime
				totalSum += total
				t.Logf("sample %d: copy=%.1f start=%.1f close=%.1f total=%.1f ms", i+1, float64(c.CopyTime)/float64(time.Millisecond), float64(c.StartTime)/float64(time.Millisecond), float64(closeTime)/float64(time.Millisecond), float64(total)/float64(time.Millisecond))
			}
			t.Logf("mean (5): copy=%.1f start=%.1f close=%.1f total=%.1f ms", float64(copySum)/5/float64(time.Millisecond), float64(startSum)/5/float64(time.Millisecond), float64(closeSum)/5/float64(time.Millisecond), float64(totalSum)/5/float64(time.Millisecond))
		})
	}
}
