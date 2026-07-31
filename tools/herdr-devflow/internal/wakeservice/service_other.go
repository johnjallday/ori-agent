//go:build !darwin

package wakeservice

import (
	"net"
	"os"
)

func platformSupported() bool { return false }

func effectiveUID() int { return -1 }

func peerUID(net.Conn) (int, error) { return -1, ErrUnsupported }

func fileOwnerUID(os.FileInfo) (int, bool) { return -1, false }
