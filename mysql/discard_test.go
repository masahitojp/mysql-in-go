package mysql

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestDiscardRejectsUnverifiedStorage(t *testing.T) {
	if err := (&DB{}).Discard(); err == nil {
		t.Fatal("non-tmpfs storage accepted")
	}
}

func TestDiscardKillsReapsAndDeletesIntegration(t *testing.T) {
	if os.Getenv("MYSQLMEM_DISCARD_CHILDREN") != "1" {
		t.Skip("run make test-fixture-discard")
	}
	o := integrationOptions(t)
	o.Tmpfs = true
	db, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var data string
	if err := db.QueryRow("SELECT @@datadir").Scan(&data); err != nil {
		t.Fatal(err)
	}
	if err := db.Discard(); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if !errors.As(db.waitErr, &exit) {
		t.Fatalf("not a forced exit: %v", db.waitErr)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("not SIGKILL: %v", db.waitErr)
	}
	if err := syscall.Kill(db.cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child not reaped: %v", err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("datadir remains: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close after Discard: %v", err)
	}
	if err := db.Discard(); err != nil {
		t.Fatalf("repeated Discard: %v", err)
	}
}
