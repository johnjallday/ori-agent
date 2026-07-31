//go:build !darwin

package wakeinstall

import "io/fs"

func ownerUID(fs.FileInfo) (int, bool) {
	return 0, false
}
