//go:build !darwin || !arm64

package backuprecovery

import "testing"

func TestNativeDirectoryUnavailableOnUnsupportedBuild(t *testing.T) {
	if directory, err := readNativeDirectory("/must-not-be-inspected"); directory != nil || err != errStorageIdentity {
		t.Fatal("unsupported platform inspected a directory")
	}
	if (&nativeDirectory{}).sameSelectedDirectory() {
		t.Fatal("unsupported build claimed native identity")
	}
	(*nativeDirectory)(nil).close()
}
