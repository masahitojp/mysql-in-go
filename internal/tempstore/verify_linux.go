package tempstore

import (
	"fmt"
	"syscall"
)

// Verify checks the actual filesystem, including caller-supplied data paths.
func Verify(path string) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return fmt.Errorf("statfs %s: %w", path, err)
	}
	const tmpfsMagic = 0x01021994
	if uint64(stat.Type) != tmpfsMagic {
		return fmt.Errorf("%s is not tmpfs (filesystem type %#x)", path, stat.Type)
	}
	return nil
}
