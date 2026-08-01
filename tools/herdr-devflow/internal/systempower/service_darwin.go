//go:build darwin

package systempower

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	pmsetPath     = "/usr/bin/pmset"
	osascriptPath = "/usr/bin/osascript"
)

func (s *Service) platform() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

func (s *Service) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.Run != nil {
		return s.Run(ctx, name, args...)
	}
	bounded, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	// #nosec G204 -- the executable and arguments are fixed constants in this
	// package; nothing from a run, a plan, or a payload reaches them.
	return exec.CommandContext(bounded, name, args...).CombinedOutput()
}

// PowerSource reports where the machine's power is coming from.
//
// An unreadable answer is SourceUnknown rather than an assumption, and unknown
// never satisfies the external-power gate.
func (s *Service) PowerSource(ctx context.Context) Source {
	if s.platform() != "darwin" {
		return SourceUnknown
	}
	output, err := s.run(ctx, pmsetPath, "-g", "batt")
	if err != nil {
		return SourceUnknown
	}
	text := strings.ToLower(string(output))
	switch {
	case strings.Contains(text, "'ac power'"), strings.Contains(text, "ac power"):
		return SourceAC
	case strings.Contains(text, "battery power"):
		return SourceBattery
	default:
		return SourceUnknown
	}
}

// Sleep puts the whole machine to sleep.
//
// Every gate that decides whether this is safe lives in the caller. This
// function's only job is to do exactly what it says, on the one platform where
// it means anything.
func (s *Service) Sleep(ctx context.Context) error {
	if s.platform() != "darwin" {
		return ErrUnsupported
	}
	if _, err := s.run(ctx, osascriptPath, "-e", "tell application \"System Events\" to sleep"); err != nil {
		return err
	}
	return nil
}

// SupportsSleep reports whether this build can sleep the machine.
func (s *Service) SupportsSleep() bool { return s.platform() == "darwin" }

// AcquireIdleSleepAssertion starts a user-owned caffeinate process. It is
// deliberately separate from the root wake daemon; a dispatcher restart sees
// an unknown assertion as absent and reacquires it before relying on it.
func (s *Service) AcquireIdleSleepAssertion(ctx context.Context, runID string) (string, error) {
	if s.platform() != "darwin" {
		return "", ErrUnsupported
	}
	if s.AcquireAssertion != nil {
		return s.AcquireAssertion(ctx, runID)
	}
	command := exec.Command("/usr/bin/caffeinate", "-dimsu") // #nosec G204 -- fixed binary and flags.
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start idle-sleep assertion: %w", err)
	}
	id := strconv.Itoa(command.Process.Pid)
	s.assertions.Store(id, command)
	return id, nil
}

func (s *Service) IdleSleepAssertionActive(ctx context.Context, id string) bool {
	if s.platform() != "darwin" || id == "" {
		return false
	}
	if s.CheckAssertion != nil {
		return s.CheckAssertion(ctx, id)
	}
	value, ok := s.assertions.Load(id)
	if ok {
		command, commandOK := value.(*exec.Cmd)
		return commandOK && command.Process != nil && command.Process.Signal(syscall.Signal(0)) == nil
	}
	pid, err := strconv.Atoi(id)
	if err != nil || pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	return err == nil && process.Signal(syscall.Signal(0)) == nil
}

func (s *Service) ReleaseIdleSleepAssertion(ctx context.Context, id string) error {
	if s.platform() != "darwin" {
		return ErrUnsupported
	}
	if s.ReleaseAssertion != nil {
		return s.ReleaseAssertion(ctx, id)
	}
	value, ok := s.assertions.LoadAndDelete(id)
	if ok {
		command, commandOK := value.(*exec.Cmd)
		if !commandOK || command.Process == nil {
			return fmt.Errorf("idle-sleep assertion %s has no process", id)
		}
		return command.Process.Kill()
	}
	pid, err := strconv.Atoi(id)
	if err != nil || pid <= 0 {
		return fmt.Errorf("idle-sleep assertion %s is not a process identity", id)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGTERM)
}

var _ = time.Second
