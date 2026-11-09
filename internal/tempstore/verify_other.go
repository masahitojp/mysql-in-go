//go:build !linux

package tempstore

import "errors"

func Verify(string) error {
	return errors.New("explicit tmpfs storage is currently supported on Linux only")
}
