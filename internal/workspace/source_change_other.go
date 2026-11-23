//go:build !darwin && !freebsd && !linux

package workspace

import "os"

func sourceChange(os.FileInfo) (int64, uint64) { return 0, 0 }
