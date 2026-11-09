package fixture_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"mysqlmem/mysql"
	"mysqlmem/mysqltest"
)

func TestPreparedDatabase(t *testing.T) {
	if os.Getenv("MYSQLMEM_INTEGRATION") != "1" {
		t.Skip("run make test-fixture after building bin/mysqlmem")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	f, err := mysqltest.New(t.Context(), mysqltest.Options{
		MySQL:           mysql.Options{Binary: filepath.Join(root, "bin/mysqlmem"), BaseDir: filepath.Join(root, ".work/mysql-build"), CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets"), Tmpfs: os.Getenv("MYSQLMEM_TMPFS") == "1"},
		MaxChildren:     2,
		DiscardChildren: os.Getenv("MYSQLMEM_DISCARD_CHILDREN") == "1",
		Prepare: func(ctx context.Context, db *sql.DB) error {
			if _, err := db.ExecContext(ctx, "CREATE TABLE items(id INT PRIMARY KEY, label VARCHAR(32)) ENGINE=InnoDB"); err != nil {
				return err
			}
			_, err := db.ExecContext(ctx, "INSERT INTO items VALUES(1,'seed')")
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Run("commit deletion", func(t *testing.T) {
		db := f.DB(t)
		if _, err := db.ExecContext(t.Context(), "DELETE FROM items"); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM items").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("got count=%d; want 0", count)
		}
	})
	t.Run("next test still has seed", func(t *testing.T) {
		db := f.DB(t)
		var label string
		if err := db.QueryRowContext(t.Context(), "SELECT label FROM items WHERE id=1").Scan(&label); err != nil {
			t.Fatal(err)
		}
		if label != "seed" {
			t.Fatalf("got label=%q; want seed", label)
		}
	})
}
