//go:build darwin || linux

package backuprecovery

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureReadProcess(cmd *exec.Cmd) bool {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		if errors.Is(err, syscall.EPERM) {
			// Darwin can refuse group signalling while the owned child exits.
			// Never target an unrelated PID or elevate privileges as a fallback.
			return cmd.Process.Kill()
		}
		return err
	}
	return true
}
