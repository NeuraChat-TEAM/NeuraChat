//go:build !windows

package notcode

import (
	"io"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) {}

func bindToParentLifetime(cmd *exec.Cmd) (io.Closer, error) { return nil, nil }

func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
