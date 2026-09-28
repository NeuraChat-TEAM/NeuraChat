//go:build !windows

package notion

import (
	"io"
	"os/exec"
)

func prepareBackgroundCommand(cmd *exec.Cmd) {}

func bindBackgroundCommand(cmd *exec.Cmd) (io.Closer, error) { return nil, nil }
