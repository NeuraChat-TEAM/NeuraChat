//go:build !windows

package notcode

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}

func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
