// Package tempstore selects verified storage for disposable database files.
package tempstore

import "fmt"

// Parent returns the explicit tmpfs root, or the usual OS temporary directory
// (empty string for os.MkdirTemp). It never mounts filesystems or falls back.
func Parent(tmpfs bool) (string, error) {
	if !tmpfs {
		return "", nil
	}
	const root = "/dev/shm"
	if err := Verify(root); err != nil {
		return "", fmt.Errorf("tmpfs storage unavailable: %w", err)
	}
	return root, nil
}
