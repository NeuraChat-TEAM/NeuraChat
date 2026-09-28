//go:build windows

package notcode

import (
	"fmt"
	"io"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// hideWindow keeps bun/ngrok from flashing a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow: true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP |
			windows.CREATE_NO_WINDOW,
	}
}

type jobGuard struct{ handle windows.Handle }

func (g *jobGuard) Close() error {
	if g == nil || g.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(g.handle)
	g.handle = 0
	return err
}

// bindToParentLifetime помещает дочерний процесс в Windows Job с
// KILL_ON_JOB_CLOSE. Если Neura аварийно завершается, ОС закрывает handle и
// убивает всё дерево bun/notcode без оставшегося пустого cmd.exe.
func bindToParentLifetime(cmd *exec.Cmd) (io.Closer, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	guard := &jobGuard{handle: job}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = guard.Close()
		return nil, err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = guard.Close()
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		_ = guard.Close()
		return nil, err
	}
	return guard, nil
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
