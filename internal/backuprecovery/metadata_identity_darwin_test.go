//go:build darwin

package backuprecovery

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestMetadataAnchorRefusesNegativeDarwinDeviceNumbers(t *testing.T) {
	info := unix.Stat_t{Dev: -1, Ino: 1, Mode: unix.S_IFREG | 0700, Nlink: 1, Size: 1}
	if anchorID(info) != (anchoredIdentity{}) || metadataStatEqual(info, info) || trustedExecutableStat(info) {
		t.Fatal("negative device number became unsigned authority")
	}
}
