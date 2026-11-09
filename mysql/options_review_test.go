package mysql

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	driver "github.com/go-sql-driver/mysql"
)

func TestCreateDatabaseRequiresName(t *testing.T) {
	if _, err := Start(Options{CreateDatabase: true}); err == nil || !strings.Contains(err.Error(), "requires Database") {
		t.Fatalf("got %v", err)
	}
}

func TestCloseReportsServerExitClearly(t *testing.T) {
	for _, test := range []struct {
		name    string
		waitErr error
		want    string
	}{
		{"clean exit", nil, "exit status 0"},
		{"failure", errors.New("exit status 7"), "exit status 7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			work := t.TempDir()
			file, err := os.Create(filepath.Join(work, "server.log"))
			if err != nil {
				t.Fatal(err)
			}
			pool, err := sql.Open("mysql", "root@unix(/nonexistent/mysql.sock)/")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			close(done)
			db := &DB{pool: pool, done: done, waitErr: test.waitErr, log: file, workDir: work}
			err = db.Close()
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "<nil>") {
				t.Fatalf("unexpected close message: %v", err)
			}
			if test.waitErr != nil && !errors.Is(err, test.waitErr) {
				t.Fatalf("lost exit cause: %v", err)
			}
		})
	}
}

func TestCreateDatabaseAndForwardingIntegration(t *testing.T) {
	o := integrationOptions(t)
	o.Database = "quoted`database"
	o.CreateDatabase = true
	db, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	var name string
	if err := db.QueryRow("SELECT DATABASE()").Scan(&name); err != nil || name != o.Database {
		t.Fatalf("database %q %v", name, err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE items(id INT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO items VALUES(42)"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	statement, err := db.PrepareContext(t.Context(), "SELECT id FROM items WHERE id=?")
	if err != nil {
		t.Fatal(err)
	}
	var id int
	if err := statement.QueryRowContext(t.Context(), 42).Scan(&id); err != nil || id != 42 {
		statement.Close()
		t.Fatalf("id %d %v", id, err)
	}
	statement.Close()
	rows, err := db.Query("SELECT id FROM items")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing row")
	}
	if err := rows.Scan(&id); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if db.Stats().MaxOpenConnections != 2 {
		t.Fatal("pool configuration was not forwarded")
	}
	if err := db.Unwrap().Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("owner could not stop server after explicit pool close: %v", err)
	}
	select {
	case <-db.done:
	default:
		t.Fatal("server still running")
	}
}

func TestMissingDatabaseFailsWithoutCreatingItIntegration(t *testing.T) {
	o := integrationOptions(t)
	o.Database = "must_not_be_created"
	db, err := Start(o)
	if db != nil {
		db.Close()
		t.Fatal("missing database unexpectedly selected")
	}
	var mysqlErr *driver.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1049 {
		t.Fatalf("want MySQL 1049; got %v", err)
	}
	removeFailureLogs(t, err)
}

func TestFastShutdownRecoveryIntegration(t *testing.T) {
	o := integrationOptions(t)
	o.FastShutdown = true
	o.DataDir = filepath.Join(t.TempDir(), "data")
	o.Database = "test"
	o.CreateDatabase = true
	first, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	var mode int
	if err := first.QueryRow("SELECT @@innodb_fast_shutdown").Scan(&mode); err != nil || mode != 2 {
		t.Fatalf("mode %d %v", mode, err)
	}
	if _, err := first.ExecContext(context.Background(), "CREATE TABLE items(id INT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec("INSERT INTO items VALUES(42)"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	o.FastShutdown = false
	second, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	var id int
	if err := second.QueryRow("SELECT id FROM items").Scan(&id); err != nil || id != 42 {
		t.Fatalf("recovery row %d %v", id, err)
	}
	if err := second.QueryRow("SELECT @@innodb_fast_shutdown").Scan(&mode); err != nil || mode != 1 {
		t.Fatalf("default mode %d %v", mode, err)
	}
}
