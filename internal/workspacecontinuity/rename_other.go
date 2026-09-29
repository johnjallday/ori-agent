//go:build !darwin && !linux && !windows

package workspacecontinuity

import "os"

func renameNewDirectory(_ *os.Root, _, _ string) error {
	// Inspection is available, but never emulate an exclusive rename with an
	// unsafe check-then-replace sequence on an unsupported platform.
	return ErrUnsafe
}
