//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Fixed Darwin arm64 ABI from sys/proc_info.h: proc_regionwithpathinfo. The
// kernel copies a bounded record; paths, VM addresses and labels are discarded.
// No VM contents are read. Caller must own an unreaped direct child throughout.
type processRegionRecord struct {
	protection, maxProtection, inheritance, flags uint32
	offset                                        uint64
	details                                       [14]uint32
	address, size                                 uint64
	stat                                          processVnodeStat
	vnodeType, padding                            int32
	fsid                                          [2]int32
	path                                          [1024]byte
}

type processVnodeStat struct {
	device                                                         uint32
	mode, links                                                    uint16
	inode                                                          uint64
	uid, gid                                                       uint32
	atime, atimeNS, mtime, mtimeNS, ctime, ctimeNS, birth, birthNS int64
	size, blocks                                                   int64
	blockSize                                                      int32
	flags, generation, rdev                                        uint32
	spare                                                          [2]int64
}

func mappedExecutableMatches(process *os.Process, expected unix.Stat_t) bool {
	if process == nil || process.Pid <= 0 || expected.Dev <= 0 || !trustedExecutableStatWithin(expected, maxApplicationHelperBytes) || unsafe.Sizeof(processRegionRecord{}) != 1272 {
		return false
	}
	const procInfoCallPIDInfo = 2
	const procPIDRegionPathInfo = 8
	const vmProtExecute = 4
	var address uint64
	for range 16 {
		var record processRegionRecord
		// #nosec G103 -- fixed read-only proc_pidinfo flavor on our owned child;
		// exact SDK-sized record, never an API-selected PID/pointer/flavor.
		n, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, procInfoCallPIDInfo, uintptr(process.Pid), procPIDRegionPathInfo, uintptr(address), uintptr(unsafe.Pointer(&record)), unsafe.Sizeof(record))
		runtime.KeepAlive(&record)
		if errno != 0 || n != unsafe.Sizeof(record) {
			return false
		}
		if record.protection&vmProtExecute != 0 {
			// Require the FIRST executable mapping, not an arbitrary later mapping
			// which a different image could use as a decoy. Mach-O __TEXT starts
			// at file offset zero; an unexpected mapping fails unavailable.
			s := record.stat
			return record.offset == 0 && uint64(s.device) == uint64(expected.Dev) && s.inode == expected.Ino &&
				uint32(s.mode) == uint32(expected.Mode) && uint64(s.links) == uint64(expected.Nlink) &&
				s.uid == expected.Uid && s.gid == expected.Gid && s.size == expected.Size &&
				s.mtime == expected.Mtim.Sec && s.mtimeNS == expected.Mtim.Nsec && s.ctime == expected.Ctim.Sec && s.ctimeNS == expected.Ctim.Nsec
		}
		if record.size == 0 || record.address > ^uint64(0)-record.size || record.address+record.size <= address {
			return false
		}
		address = record.address + record.size
	}
	return false
}

// checkedResticProcess binds kernel signing identity AND the mapped executable
// vnode to the retained complete-file digest. It runs only in the bounded helper
// before secret transfer, with the provider waiting on its still-empty pipe.
func checkedResticProcess(ctx context.Context, process *os.Process, checked *verifiedExecutable) bool {
	return checked != nil && pinnedResticProcess(process) && mappedExecutableMatches(process, checked.before) && checked.unchanged(ctx) && pinnedResticProcess(process)
}
