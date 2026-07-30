//go:build darwin

package systempower

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
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

var _ = time.Second
