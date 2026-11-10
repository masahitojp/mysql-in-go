//go:build linux

package mysql

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/masahitojp/mysql-in-go/internal/parentwatch"
)

type parentReport struct {
	PID  int
	Log  string
	Work string
}

// This helper is a separate Go parent process, not the test's own process.
func TestParentDeathHelper(t *testing.T) {
	mode := os.Getenv("MYSQLMEM_PARENT_HELPER")
	if mode == "" {
		return
	}
	o := integrationOptions(t)
	o.DataDir = os.Getenv("MYSQLMEM_PARENT_DATA")
	var report parentReport
	if mode == "initialize" {
		report.Work = os.Getenv("MYSQLMEM_PARENT_WORK")
		report.Log = filepath.Join(report.Work, "server.log")
		log, err := os.Create(report.Log)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(o.Binary, "--no-defaults", "--basedir="+o.BaseDir, "--datadir="+o.DataDir, "--lc-messages-dir="+filepath.Join(o.BaseDir, "share"), "--character-sets-dir="+o.CharacterSetsDir, "--initialize-insecure")
		cmd.Stdout = log
		cmd.Stderr = log
		writer, err := parentwatch.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		report.PID = cmd.Process.Pid
	} else {
		db, err := StartContext(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		report = parentReport{PID: db.cmd.Process.Pid, Log: filepath.Join(db.workDir, "server.log"), Work: db.workDir}
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func processRunning(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	end := strings.LastIndex(string(stat), ") ")
	return end < 0 || len(stat) <= end+2 || stat[end+2] != 'Z'
}

func TestParentDeathStopsMySQLIntegration(t *testing.T) {
	_ = integrationOptions(t)
	for _, mode := range []string{"serving", "initialize"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			parent := exec.Command(os.Args[0], "-test.run=^TestParentDeathHelper$")
			parent.Env = append(os.Environ(), "MYSQLMEM_PARENT_HELPER="+mode, "MYSQLMEM_PARENT_DATA="+filepath.Join(root, "data"), "MYSQLMEM_PARENT_WORK="+root)
			output, err := parent.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr strings.Builder
			parent.Stderr = &stderr
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			parentDone := make(chan error, 1)
			go func() { parentDone <- parent.Wait() }()
			var report parentReport
			t.Cleanup(func() {
				parent.Process.Kill()
				if report.PID != 0 && processRunning(report.PID) {
					syscall.Kill(report.PID, syscall.SIGKILL)
				}
				if report.Work != "" && report.Work != root && !processRunning(report.PID) {
					os.RemoveAll(report.Work)
				}
			})
			type response struct {
				report parentReport
				err    error
			}
			ready := make(chan response, 1)
			go func() {
				var value parentReport
				err := json.NewDecoder(bufio.NewReader(output)).Decode(&value)
				ready <- response{value, err}
			}()
			select {
			case value := <-ready:
				report = value.report
				if err := value.err; err != nil {
					<-parentDone
					t.Fatalf("parent helper: %v %s", err, stderr.String())
				}
			case <-time.After(30 * time.Second):
				t.Fatal("parent readiness timeout")
			}
			if err := parent.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			<-parentDone
			deadline := time.Now().Add(5 * time.Second)
			for processRunning(report.PID) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if processRunning(report.PID) {
				t.Fatalf("MySQL %d survived parent SIGKILL", report.PID)
			}
			text, err := os.ReadFile(report.Log)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(text), "parent pipe closed") {
				t.Fatalf("missing pipe EOF termination marker: %s", text)
			}
			t.Logf("%s: parent SIGKILL -> pipe EOF -> MySQL %d exited", mode, report.PID)
		})
	}
}
