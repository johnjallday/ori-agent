//go:build darwin || linux

package backuprecovery

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Nested helper children must remain in the outer owned pipeline group. Giving
// diskutil/restic a new group would let it outlive a killed or blocked helper.
// The group-leading helper can cancel/reap each direct child; the outer owner
// also kills the complete pipeline on a helper deadline or abnormal exit.
func configurePipelineReadProcess(cmd *exec.Cmd) bool {
	if syscall.Getpgrp() != os.Getpid() {
		return false
	}
	cmd.SysProcAttr = nil
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return cmd.Process.Kill()
	}
	return true
}

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
