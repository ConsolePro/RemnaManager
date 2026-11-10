package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Runner interface {
	Run(context.Context, []byte, string, ...string) ([]byte, error)
}
type ExecRunner struct{ Log io.Writer }

func (r ExecRunner) Run(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if input != nil {
		cmd.Stdin = bytesReader(input)
	}
	cmd.WaitDelay = 5 * time.Second
	data, err := cmd.CombinedOutput()
	if r.Log != nil {
		_, _ = fmt.Fprintf(r.Log, "[%s] %s: exit=%v\n%s\n", time.Now().Format(time.RFC3339), name, err, data)
	}
	if err != nil {
		return data, fmt.Errorf("%s failed: %w", name, err)
	}
	return data, nil
}

// writePrivate replaces only a regular managed file; it never follows symlinks.
func writeFile(path string, data []byte, mode os.FileMode) error {
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular target: %s", path)
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".remna-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
