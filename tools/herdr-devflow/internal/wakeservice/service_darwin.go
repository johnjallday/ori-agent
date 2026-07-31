//go:build darwin

package wakeservice

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func platformSupported() bool {
	version, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return false
	}
	majorText, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	return err == nil && major >= MinimumMacOSMajor
}

func effectiveUID() int { return os.Geteuid() }

func fileOwnerUID(info os.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*unix.Stat_t)
	if !ok {
		return -1, false
	}
	return int(stat.Uid), true
}

func peerUID(connection net.Conn) (int, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return -1, fmt.Errorf("wake service accepts Unix-domain connections only")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return -1, fmt.Errorf("access Unix connection: %w", err)
	}
	uid := -1
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			credentialErr = err
			return
		}
		uid = int(credentials.Uid)
	}); err != nil {
		return -1, fmt.Errorf("inspect peer credentials: %w", err)
	}
	if credentialErr != nil {
		return -1, fmt.Errorf("inspect peer credentials: %w", credentialErr)
	}
	if uid < 0 {
		return -1, fmt.Errorf("peer uid is unavailable")
	}
	return uid, nil
}
