package mysql_test

import (
	"os"
	"path/filepath"
	"testing"

	"mysqlmem/mysql"
)

// TestSelectOne demonstrates testing real MySQL through the public Go API.
// Each run owns a fresh database, and Cleanup runs even when an assertion fails.
func TestSelectOne(t *testing.T) {
	if os.Getenv("MYSQLMEM_INTEGRATION") != "1" {
		t.Skip("run make test-select-one after building bin/mysqlmem")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	db, err := mysql.StartContext(t.Context(), mysql.Options{
		Binary:           filepath.Join(root, "bin/mysqlmem"),
		BaseDir:          filepath.Join(root, ".work/mysql-build"),
		CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets"),
	})
	if err != nil {
		t.Fatalf("start MySQL: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close MySQL: %v", err)
		}
	})

	var got int
	if err := db.QueryRowContext(t.Context(), "SELECT 1").Scan(&got); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if got != 1 {
		t.Fatalf("SELECT 1 = %d; want 1", got)
	}
	t.Logf("SELECT 1 = %d (assertion passed)", got)
}
