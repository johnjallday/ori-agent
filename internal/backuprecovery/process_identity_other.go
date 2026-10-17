//go:build !darwin || !arm64

package backuprecovery

import "os"

// No process identity fallback on an unvalidated platform or architecture.
func pinnedResticProcess(*os.Process) bool { return false }
