//go:build windows

package notcode

import (
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows"
)

// hideWindow keeps bun/ngrok from flashing a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// kill terminates the whole process tree: bun spawns children that would
// otherwise keep port 3000 busy after the app exits.
func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	taskkill := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
	hideWindow(taskkill)
	if err := taskkill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
