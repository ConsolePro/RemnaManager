package installer

import (
	"bytes"
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
type ExecRunner struct {
	Log    io.Writer
	Redact func([]byte) string
}

// StreamRunner keeps database dumps out of memory and out of diagnostic logs.
type StreamRunner interface {
	RunTo(context.Context, io.Writer, string, ...string) error
}

type StreamInputRunner interface {
	RunFrom(context.Context, io.Reader, string, ...string) error
}

func (r ExecRunner) RunFrom(ctx context.Context, input io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = input
	cmd.Stdout, cmd.Stderr = r.Log, r.Log
	return cmd.Run()
}

func (r ExecRunner) RunTo(ctx context.Context, output io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdout = output
	cmd.Stderr = r.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func (r ExecRunner) Run(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if input != nil {
		cmd.Stdin = bytesReader(input)
	}
	cmd.WaitDelay = 5 * time.Second
	var captured bytes.Buffer
	output := io.MultiWriter(&captured, &setupProgressWriter{ctx: ctx})
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	data := captured.Bytes()
	if r.Log != nil {
		output := string(data)
		if r.Redact != nil {
			output = r.Redact(data)
		}
		_, _ = fmt.Fprintf(r.Log, "[%s] %s: exit=%v\n%s\n", time.Now().Format(time.RFC3339), name, err, output)
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
