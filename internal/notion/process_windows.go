//go:build windows

package notion

import (
	"io"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

func prepareBackgroundCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow: true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP |
			windows.CREATE_NO_WINDOW,
	}
}

type processJobGuard struct{ handle windows.Handle }

func (g *processJobGuard) Close() error {
	if g == nil || g.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(g.handle)
	g.handle = 0
	return err
}

func bindBackgroundCommand(cmd *exec.Cmd) (io.Closer, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	guard := &processJobGuard{handle: job}
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
