//go:build darwin

package filesearch

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/platform"
	"golang.org/x/sys/unix"
)

func dispatch(ctx context.Context, v Source, r entry, reveal bool, send func(context.Context, string, string) error) error {
	if platform.DesktopOpenDisabled() {
		return ErrUnavailable
	}
	parent := filepath.Dir(r.relative)
	if parent == "." {
		parent = ""
	}
	n := strings.Count(r.relative, "/")
	if n > 32 || strings.Count(r.chain, "/") != n {
		return ErrMissing
	}
	d := directory{parent, r.chain, n}
	if !verifyChain(v, d) {
		return ErrMissing
	}
	path := filepath.Join(v.root, r.relative)
	// #nosec G304 G703 -- root, generation and ancestor identities were validated; no-follow metadata descriptor pins the object.
	fd, err := unix.Open(path, unix.O_NOFOLLOW_ANY|unix.O_EVTONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrMissing
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	matches := func(observed *unix.Stat_t) bool {
		return eligible(observed) && observed.Mode&unix.S_IFMT == unix.S_IFREG && observed.Nlink == 1 && fmt.Sprintf("%s:%d:%d", witness(observed), observed.Mode, observed.Size) == r.witness && observed.Mtim.Nano() == r.modified
	}
	if unix.Fstat(fd, &st) != nil || !matches(&st) {
		return ErrMissing
	}
	viewer := ""
	if !reveal {
		viewer = platform.ReviewDocumentViewer(r.name)
		if viewer == "" || st.Mode&0111 != 0 {
			return ErrUnavailable
		}
		var info [32]byte
		size, e := unix.Fgetxattr(fd, "com.apple.FinderInfo", info[:])
		if !errors.Is(e, unix.ENOATTR) && (e != nil || size != len(info) || binary.BigEndian.Uint16(info[8:10])&0x8000 != 0) {
			return ErrUnavailable
		}
	}
	pin := fmt.Sprintf("/.vol/%d/%d", st.Dev, st.Ino)
	var pinned, latest unix.Stat_t
	if unix.Stat(pin, &pinned) != nil || witness(&pinned) != witness(&st) || unix.Lstat(path, &latest) != nil || !matches(&latest) || !verifyChain(v, d) || ctx.Err() != nil {
		return ErrMissing
	}
	if send == nil {
		send = sendDocument
	}
	if err = send(ctx, viewer, pin); err != nil {
		return ErrUnavailable
	}
	return nil
}
func sendDocument(ctx context.Context, viewer, pin string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"-R", pin}
	if viewer != "" {
		args = []string{"-a", "/System/Applications/" + viewer + ".app", pin}
	}
	// #nosec G204 G702 -- fixed system reader or reveal, only a verified kernel file reference as document argument.
	return exec.CommandContext(ctx, "/usr/bin/open", args...).Run()
}
