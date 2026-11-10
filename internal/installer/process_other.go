//go:build !linux

package installer

import "os/exec"

func configureCommand(*exec.Cmd) {}
