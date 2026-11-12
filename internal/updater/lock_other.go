//go:build !linux

package updater

import (
	"errors"
	"os"
)

func managerLock(target string) (func(), error) {
	path := target + ".update.lock"
	if err := privatePath(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return func() { _ = file.Close(); _ = os.Remove(path) }, nil
}
func Restart() error { return errors.New("in-process restart requires Linux") }
