//go:build !darwin && !linux

package backuprecovery

import "os/exec"

// Unsupported platforms never silently run without owned process-group cleanup.
func configureReadProcess(_ *exec.Cmd) bool { return false }
