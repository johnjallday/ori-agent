//go:build !darwin || !arm64

package backuprecovery

// Unsupported builds do not even consume a path or credential from stdin.
func runCompiledBackupHelper(string) int { return 1 }
