//go:build darwin && arm64

package backuprecovery

import "errors"

// Fixed private exit categories contain no provider text, paths or identifiers.
func helperFailureCode(err error) int {
	switch {
	case errors.Is(err, errDestinationAbsent):
		return 10
	case errors.Is(err, errIdentityMismatch), errors.Is(err, errStorageConflict):
		return 11
	case errors.Is(err, errReadProcessLimit), errors.Is(err, errMetadataLimit):
		return 12
	case errors.Is(err, errCredentialUnavailable):
		return 13
	default:
		return 1
	}
}
func helperFailureError(code int) error {
	switch code {
	case 10:
		return errDestinationAbsent
	case 11:
		return errIdentityMismatch
	case 12:
		return errReadProcessLimit
	case 13:
		return errCredentialUnavailable
	default:
		return errMetadataRead
	}
}
