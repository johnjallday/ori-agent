package backuprecovery

import "strings"

const backupHelperPrefix = "--ori-backup-helper="

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
	case backupHelperPrefix + "timemachine-v1":
		return true, runCompiledBackupHelper("timemachine-v1")
	default:
		return true, 2
	}
}
