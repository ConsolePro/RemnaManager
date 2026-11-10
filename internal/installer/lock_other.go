//go:build !linux

package installer

import (
	"errors"
	"os"
)

func lockFile(string) (*os.File, error) { return nil, errors.New("real installation requires Linux") }
