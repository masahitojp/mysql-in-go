package tempstore

import (
	"runtime"
	"testing"
)

func TestDiskUsesDefaultTemporaryDirectory(t *testing.T) {
	path, err := Parent(false)
	if err != nil || path != "" {
		t.Fatalf("got %q %v", path, err)
	}
}
func TestTmpfsParentIsVerifiedOrRejected(t *testing.T) {
	path, err := Parent(true)
	if runtime.GOOS != "linux" {
		if err == nil {
			t.Fatal("unsupported OS accepted")
		}
		return
	}
	if err != nil {
		t.Skipf("host has no usable /dev/shm: %v", err)
	}
	if path != "/dev/shm" {
		t.Fatalf("got %q", path)
	}
	if err := Verify(path); err != nil {
		t.Fatal(err)
	}
}
