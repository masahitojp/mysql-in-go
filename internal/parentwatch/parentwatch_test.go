package parentwatch

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWatchHelper(t *testing.T) {
	if os.Getenv("MYSQLMEM_WATCH_HELPER") != "1" {
		return
	}
	if err := WatchFromEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if os.Getenv(descriptorEnv) != "" {
		os.Exit(3)
	}
	fmt.Println("ready")
	for {
		time.Sleep(time.Hour)
	}
}
func TestPipeEOFStopsChildAndIsNotInheritedBySibling(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWatchHelper$")
	cmd.Env = append(os.Environ(), "MYSQLMEM_WATCH_HELPER=1", descriptorEnv+"=invalid-old-value")
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		writer.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("unexpected helper output: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper readiness timeout")
	}
	sibling := exec.Command("sleep", "10")
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { sibling.Process.Kill(); sibling.Wait() }()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 125 {
			t.Fatalf("got %v, want exit 125", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit after EOF (writer leaked?)")
	}
}
func TestInvalidParentDescriptorIsRejected(t *testing.T) {
	for _, value := range []string{"garbage", "0", "-1", "999999"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(descriptorEnv, value)
			if err := WatchFromEnv(); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
}
func TestFailedStartClosesPipe(t *testing.T) {
	cmd := exec.Command("/does-not-exist/mysqlmem")
	if writer, err := Start(cmd); err == nil || writer != nil {
		t.Fatalf("got %v %v", writer, err)
	}
}
