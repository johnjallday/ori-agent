package workspace

import (
	"encoding/binary"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Finder aliases look like regular files to Lstat but Launch Services would
// resolve one to a different document. Inspect FinderInfo on a metadata-only
// handle to the same listed version. Never open the project for reading.
func ordinaryDesktopDocument(path string, version SourceVersion) bool {
	file, err := os.OpenFile(path, unix.O_EVTONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0) // #nosec G304 G703 -- server-resolved candidate; verify its identity before inspecting metadata
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || sourceVersion(info) != version {
		return false
	}
	var attr [32]byte
	n, err := unix.Fgetxattr(int(file.Fd()), "com.apple.FinderInfo", attr[:]) // #nosec G115 -- macOS file descriptor returned by open
	return errors.Is(err, unix.ENOATTR) || (err == nil && n == len(attr) && binary.BigEndian.Uint16(attr[8:10])&0x8000 == 0 && string(attr[:4]) != "APPL")
}
