//go:build !darwin

package systempower

import (
	"context"
	"runtime"
)

func (s *Service) platform() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

// PowerSource is always unknown off macOS: Ori does not read power state on a
// platform where it cannot act on the answer.
func (s *Service) PowerSource(context.Context) Source { return SourceUnknown }

// Sleep refuses. There is deliberately no platform substitute — a run that
// "slept" by waiting would keep consuming the very allowance it was waiting for.
func (s *Service) Sleep(context.Context) error { return ErrUnsupported }

// SupportsSleep is false on every non-macOS build.
func (s *Service) SupportsSleep() bool { return false }
