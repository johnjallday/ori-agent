package backuprecovery

import (
	"context"
	"errors"
	"time"
)

var (
	errProviderUnavailable   = errors.New("backup metadata is unavailable on this platform")
	errTrustedExecutable     = errors.New("backup executable is unavailable")
	errProviderImage         = errors.New("backup provider image is unavailable")
	errSandboxProfile        = errors.New("backup metadata containment is unavailable")
	errDestinationAbsent     = errors.New("backup destination is absent")
	errIdentityMismatch      = errors.New("backup storage identity changed")
	errCredentialUnavailable = errors.New("backup credential is unavailable")
)

// Fixed provider ids. Nothing else is ever a configuration's provider.
const (
	providerRestic      = "restic"
	providerTimeMachine = "time_machine"

	// ProviderTimeMachine is the exported id summary readers compare against.
	ProviderTimeMachine = providerTimeMachine
)

// TimeMachinePlatformSupported reports whether this build can ever read Time
// Machine (Apple-silicon macOS). It is a build fact, not an OS query.
func TimeMachinePlatformSupported() bool { return timeMachinePlatformSupported }

type backupPasswordUse func(context.Context, func([]byte) error) error

type providerReadResult struct {
	configuration localConfiguration
	observedAt    time.Time
	snapshots     []localSnapshot
	capacity      *localCapacity
	timeMachine   *localTimeMachine // Time Machine reads only
}

// Private adapter boundary, not a runtime grant or public command interface.
// The service must hold exact reviewed/receipt authority and WorkGate admission
// across read, credential resolution, final publication and child/scratch drain.
type localMetadataProvider interface {
	read(context.Context, localConfiguration, backupPasswordUse) (providerReadResult, error)
	close()
}
