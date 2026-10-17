//go:build !darwin && !linux

package backuprecovery

import "os"

func localRootFileIdentity(os.FileInfo) (uint64, uint64, bool) { return 0, 0, false }
