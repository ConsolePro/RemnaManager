package updater

import (
	"errors"
	"os"
	"syscall"
)

func managerLock(target string) (func(), error) {
	path := target + ".update.lock"
	if err := privatePath(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("another manager update is running")
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }, nil
}
func Restart() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(path, os.Args, os.Environ())
}
