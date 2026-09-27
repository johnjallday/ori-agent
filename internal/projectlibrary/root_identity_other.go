//go:build !darwin && !linux && !freebsd

package projectlibrary

import "os"

// No durable folder identity on this platform: never grant by path alone.
func directoryIdentity(os.FileInfo) (string, error) { return "", ErrUnavailable }
