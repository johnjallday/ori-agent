package backuprecovery

import (
	"errors"
	"strings"
)

const backupHelperPrefix = "--ori-backup-helper="

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

// RunHelperProcess is the server's private child-process entrypoint. Call it
// before dotenv, settings, stores, ports or browser initialization. It does not
// install/activate anything; normal invocations are left completely untouched.
// The owning service must authorize, bound and reap every child it launches.
func RunHelperProcess(arguments []string) (handled bool, exitCode int) {
	if len(arguments) == 0 || !strings.HasPrefix(arguments[0], "--ori-backup-helper") {
		return false, 0
	}
	if len(arguments) != 1 {
		return true, 2
	}
	switch arguments[0] {
	case backupHelperPrefix + "broker-v1":
		return true, runCompiledBackupHelper("broker-v1")
	case backupHelperPrefix + "reader-v1":
		return true, runCompiledBackupHelper("reader-v1")
	default:
		return true, 2
	}
}
