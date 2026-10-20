//go:build windows

package resourcecatalog

// lockFile has no cross-process lock on Windows; the process mutex in
// withLock still serializes writers in one Ori. The first provider is macOS-only.
func lockFile(string) (func(), error) { return func() {}, nil }
