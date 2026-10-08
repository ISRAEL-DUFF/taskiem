//go:build unix

package container

import (
	"errors"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the program in its own process group, so a timeout
// kills everything it started.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = cmd.Process.Kill()
	}
}

func signalOf(ee *exec.ExitError) string {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() { //nolint:misspell // the syscall package's spelling
		return ws.Signal().String()
	}
	return ""
}
