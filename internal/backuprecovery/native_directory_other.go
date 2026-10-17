//go:build !darwin || !arm64

package backuprecovery

func readNativeDirectory(string) (*nativeDirectory, error) { return nil, errStorageIdentity }
func (*nativeDirectory) sameSelectedDirectory() bool       { return false }
