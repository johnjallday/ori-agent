//go:build !darwin || !arm64

package backuprecovery

import "context"

// No path inspection, password callback, subprocess or automatic fallback.
func newPlatformProvider(context.Context, string, string) (localMetadataProvider, error) {
	return nil, errProviderUnavailable
}
