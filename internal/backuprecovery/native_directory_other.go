//go:build !darwin || !arm64

package backuprecovery

// Unsupported targets never hold a native directory or collect mount evidence.
type nativeDirectory struct{}

func readNativeDirectory(string) (*nativeDirectory, error) { return nil, errStorageIdentity }
func (*nativeDirectory) sameSelectedDirectory() bool       { return false }
func (*nativeDirectory) close()                            {}
