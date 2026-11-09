package mysql

import (
	"context"
	"database/sql"
	"errors"
	"mysqlmem/internal/tempstore"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestInvalidOptions(t *testing.T) {
	for _, test := range []struct {
		name    string
		options []Options
	}{
		{"too many options", []Options{{}, {}}},
		{"negative timeout", []Options{{StartupTimeout: -time.Second}}},
		{"missing binary", []Options{{Binary: filepath.Join(t.TempDir(), "missing")}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if db, err := Start(test.options...); err == nil || db != nil {
				t.Fatalf("got db=%v err=%v", db, err)
			}
		})
	}
}

func fakeOptions(t *testing.T, body string) Options {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "server")
	script := "#!/bin/sh\nfor arg do\n if [ \"$arg\" = --initialize-insecure ]; then exit 0; fi\ndone\n" + body + "\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return Options{Binary: binary, BaseDir: dir, CharacterSetsDir: dir, StartupTimeout: time.Second}
}

func removeFailureLogs(t *testing.T, err error) {
	t.Helper()
	text := err.Error()
	i := strings.LastIndex(text, "(logs: ")
	if i >= 0 {
		path := strings.TrimSuffix(text[i+len("(logs: "):], ")")
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
	}
}

func TestStartupExitReturnsError(t *testing.T) {
	_, err := Start(fakeOptions(t, "exit 7"))
	if err == nil || !strings.Contains(err.Error(), "exited before readiness") {
		t.Fatalf("got %v", err)
	}
	removeFailureLogs(t, err)
}

func TestStartupTimeoutReapsChild(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	o := fakeOptions(t, "echo $$ > '"+pidfile+"'\nexec sleep 60")
	o.StartupTimeout = 300 * time.Millisecond
	_, err := Start(o)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	removeFailureLogs(t, err)
	data, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child %d still exists: %v", pid, err)
	}
}

func TestNonemptyDataDirIsNotOverwritten(t *testing.T) {
	o := fakeOptions(t, "exit 7")
	o.DataDir = t.TempDir()
	sentinel := filepath.Join(o.DataDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Start(o)
	if err == nil || !strings.Contains(err.Error(), "nonempty") {
		t.Fatalf("got %v", err)
	}
	removeFailureLogs(t, err)
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("changed existing data: %q %v", data, err)
	}
}

func integrationOptions(t *testing.T) Options {
	t.Helper()
	if os.Getenv("MYSQLMEM_INTEGRATION") != "1" {
		t.Skip("set MYSQLMEM_INTEGRATION=1 after building bin/mysqlmem")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return Options{Binary: filepath.Join(root, "bin/mysqlmem"), BaseDir: filepath.Join(root, ".work/mysql-build"), CharacterSetsDir: filepath.Join(root, ".work/mysql/share/charsets")}
}

func TestIntegrationCloseReturnsToGoAndRemovesTemporaryData(t *testing.T) {
	db, err := Start(integrationOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1: %d %v", one, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(db.workDir); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remains: %v", err)
	}
	if err := syscall.Kill(db.cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("server not reaped: %v", err)
	}
	if err := db.QueryRow("SELECT 1").Scan(&one); err == nil {
		t.Fatal("query succeeded after Close")
	}
	t.Log("parent Go process continues after Close")
}

func TestIntegrationPersistentDataSurvivesRestart(t *testing.T) {
	o := integrationOptions(t)
	o.DataDir = filepath.Join(t.TempDir(), "data")
	first, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	for _, q := range []string{"CREATE DATABASE demo", "CREATE TABLE demo.items(id INT PRIMARY KEY) ENGINE=InnoDB", "INSERT INTO demo.items VALUES(42)"} {
		if _, err := first.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	var id int
	if err := second.QueryRow("SELECT id FROM demo.items").Scan(&id); err != nil || id != 42 {
		t.Fatalf("persisted row: %d %v", id, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseTimeoutKillsAndReapsChild(t *testing.T) {
	o := fakeOptions(t, "exec sleep 60")
	work := t.TempDir()
	logFile, err := os.Create(filepath.Join(work, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(o.Binary)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	pool, err := sql.Open("mysql", "root@unix("+filepath.Join(work, "missing.sock")+")/")
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		logFile.Close()
		t.Fatal(err)
	}
	db := &DB{DB: pool, cmd: cmd, done: make(chan struct{}), log: logFile, workDir: work, shutdownTimeout: 50 * time.Millisecond}
	go func() { db.waitErr = cmd.Wait(); close(db.done) }()
	err = db.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected shutdown timeout, got %v", err)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child still exists: %v", err)
	}
}

func TestTmpfsDataLifecycleIntegration(t *testing.T) {
	if os.Getenv("MYSQLMEM_TMPFS") != "1" {
		t.Skip("set MYSQLMEM_TMPFS=1 to test explicit tmpfs storage")
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
	if err := tempstore.Verify(data); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(data, "/dev/shm/") {
		t.Fatalf("unexpected data path: %s", data)
	}
	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1: %d %v", one, err)
	}
	var sqlTemp string
	if err := db.QueryRow("SELECT @@tmpdir").Scan(&sqlTemp); err != nil {
		t.Fatal(err)
	}
	if err := tempstore.Verify(sqlTemp); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified tmpfs datadir: %s; SQL tmpdir: %s", data, sqlTemp)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("data remains after Close: %v", err)
	}
}

func TestTmpfsRejectsDiskDataDirIntegration(t *testing.T) {
	if os.Getenv("MYSQLMEM_TMPFS") != "1" {
		t.Skip("requires explicit tmpfs integration run")
	}
	o := integrationOptions(t)
	o.Tmpfs = true
	root, err := filepath.Abs("../.work")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.MkdirTemp(root, "tmpfs-reject-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	o.DataDir = data
	if tempstore.Verify(o.DataDir) == nil {
		t.Skip("workspace is also tmpfs")
	}
	db, err := Start(o)
	if err == nil || db != nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("disk datadir accepted with Tmpfs=true")
	}
	if !strings.Contains(err.Error(), "not tmpfs") {
		t.Fatalf("unexpected error: %v", err)
	}
	removeFailureLogs(t, err)
}
