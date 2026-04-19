//go:build !windows

package updatemanager

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// RestartApplication restarts the current application
func (m *Manager) RestartApplication() {
	logger.Info("Restarting application...", nil)

	// Get the executable path
	executable, err := os.Executable()
	if err != nil {
		logger.Error("Failed to get executable path", logger.Fields{"err": err})
		return
	}

	// Get the current working directory and arguments
	args := os.Args
	cwd, _ := os.Getwd()

	// Start new process
	cmd := exec.Command(executable, args[1:]...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		logger.Error("Failed to start new process", logger.Fields{"err": err})
		return
	}

	logger.Debug("New process started, triggering graceful shutdown...", logger.Fields{"pid": cmd.Process.Pid})

	// Signal the current process to shut down gracefully via SIGTERM.
	// This allows deferred cleanup (DB close, HTTP shutdown) to run,
	// unlike os.Exit(0) which skips all defers.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		logger.Error("Failed to send SIGTERM, falling back to os.Exit", logger.Fields{"err": err})
		os.Exit(0)
	}
}
