//go:build !darwin || !arm64

package backuprecovery

import "context"

// Time Machine is a macOS facility; elsewhere the provider is platform_unsupported.
func newPlatformTimeMachineProvider(context.Context, string) (localMetadataProvider, error) {
	return nil, errProviderUnavailable
}

// timeMachinePlatformSupported reports whether this build can ever read Time Machine.
const timeMachinePlatformSupported = false
