//go:build darwin && arm64

package backuprecovery

import (
	"encoding/hex"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// This is the CodeDirectory hash of the same SHA-256-pinned official release,
// measured by codesign --display and the kernel for our sandboxed probe child.
// It supplements, never substitutes for, full-file descriptor/digest checks.
const resticReleaseCodeHash = "6f23d5d672ad8600f5867ae25464f992835d35db"

// pinnedResticProcess queries only a process object the caller already owns.
// It must run after the fixed sandbox launcher execs restic and BEFORE any
// password is released. A matching version banner or pre-exec pathname check
// cannot establish which executable the launcher actually entered. This result
// attests ONLY kernel CodeDirectory identity/status, not the mapped vnode's
// equality to our retained descriptor or complete-file bytes. The reader must
// use checkedResticProcess to compose mapped-image binding and digest rechecks,
// plus containment/owned reaping; this boolean alone must NEVER release a credential.
// Never expose a caller-selected PID or csops operation through an API/tool.
func pinnedResticProcess(process *os.Process) bool {
	digest, valid := processCodeIdentity(process)
	return valid && hex.EncodeToString(digest[:]) == resticReleaseCodeHash
}

func processCodeIdentity(process *os.Process) ([20]byte, bool) {
	if process == nil || process.Pid <= 0 {
		return [20]byte{}, false
	}
	const csOpsStatus = 0
	const csOpsCDHash = 5
	const csValid = 1
	var digest [20]byte
	// #nosec G103 -- read-only csops operation; kernel output is exactly this
	// fixed-size owned buffer. No caller-controlled pointer/operation/length.
	//nolint:staticcheck // SA1019: x/sys has no libSystem csops wrapper; only this fixed read-only flavor on our owned child is accepted.
	_, _, errno := unix.Syscall6(unix.SYS_CSOPS, uintptr(process.Pid), csOpsCDHash, uintptr(unsafe.Pointer(&digest[0])), uintptr(len(digest)), 0, 0)
	runtime.KeepAlive(&digest)
	if errno != 0 {
		return [20]byte{}, false
	}
	var flags uint32
	// #nosec G103 -- read-only signature-status query into a fixed uint32 buffer.
	//nolint:staticcheck // SA1019: x/sys has no libSystem csops wrapper; the supported ABI writes exactly the fixed uint32 buffer.
	_, _, errno = unix.Syscall6(unix.SYS_CSOPS, uintptr(process.Pid), csOpsStatus, uintptr(unsafe.Pointer(&flags)), 4, 0, 0)
	runtime.KeepAlive(&flags)
	return digest, errno == 0 && flags&csValid != 0
}
