//go:build darwin

package filesearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW_ANY | unix.O_CLOEXEC | unix.O_NONBLOCK

func witness(st *unix.Stat_t) string {
	return fmt.Sprintf("%d:%d:%d:%d", st.Dev, st.Ino, st.Btim.Sec, st.Btim.Nsec)
}
func eligible(st *unix.Stat_t) bool {
	return st.Ino != 0 && st.Btim.Sec != 0 && st.Flags&(unix.SF_DATALESS|unix.UF_HIDDEN) == 0
}
func openDirectory(path string) (*os.File, unix.Stat_t, error) {
	var st unix.Stat_t
	// #nosec G304 G703 -- host-selected root or a checked child; Darwin refuses symlinks in every component.
	fd, err := unix.Open(path, dirFlags, 0)
	if err != nil {
		return nil, st, ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), path)
	if err = unix.Fstat(fd, &st); err != nil || !eligible(&st) {
		_ = f.Close()
		return nil, st, ErrUnavailable
	}
	return f, st, nil
}

// A directory born after a legacy reference cannot be its original target.
// Older birth time alone is not proof; legacy links need explicit verification.
func rootExistedAt(path, identity string, at time.Time) bool {
	f, st, err := openDirectory(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return witness(&st) == identity && !time.Unix(st.Btim.Sec, st.Btim.Nsec).After(at)
}

func rootWitness(path string) (string, error) {
	f, st, err := openDirectory(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	// A volume can be mounted below a user folder as well as /Volumes.
	var parent unix.Stat_t
	if unix.Stat(filepath.Dir(path), &parent) != nil || parent.Dev != st.Dev {
		return "", ErrRoot
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(int(f.Fd()), &fs) != nil || fs.Flags&unix.MNT_LOCAL == 0 || strings.TrimRight(string(fs.Fstypename[:]), "\x00") != "apfs" {
		return "", ErrUnavailable
	}
	return witness(&st), nil
}

type directory struct {
	rel, chain string
	depth      int
}

// Verify every indexed ancestor including the root, independently of whether a
// path beneath a renamed/replaced parent happens to have the same spelling.
func verifyChain(v Source, d directory) bool {
	parts := strings.Split(d.chain, "/")
	names := strings.Split(d.rel, "/")
	p := v.root
	for i, w := range parts {
		if i > 0 {
			p = filepath.Join(p, names[i-1])
		}
		f, st, e := openDirectory(p)
		if e != nil {
			return false
		}
		_ = f.Close()
		if witness(&st) != w {
			return false
		}
	}
	return true
}
func walk(ctx context.Context, v Source, publish func([]entry) error) error {
	q := []directory{{chain: v.witness}}
	seen, dirs, files, bytes := 0, 1, 0, 0
	var incomplete error
	first := true
	for len(q) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		d := q[0]
		q = q[1:]
		if !verifyChain(v, d) {
			if d.depth == 0 {
				return ErrUnavailable
			}
			incomplete = ErrMissing
			continue
		}
		dir, st, err := openDirectory(filepath.Join(v.root, d.rel))
		if err != nil {
			if d.depth == 0 {
				return ErrUnavailable
			}
			incomplete = errUnreadable
			continue
		}
		// Inspect pinned directory entries only. ReadDir batches bound memory in a flat directory.
		err = func() error {
			defer func() { _ = dir.Close() }()
			if witness(&st) != lastWitness(d.chain) {
				return ErrMissing
			}
			batch := make([]entry, 0, 64)
			last := time.Now()
			flush := func() error {
				if len(batch) == 0 {
					return nil
				}
				if !verifyChain(v, d) {
					return ErrMissing
				}
				if err := publish(batch); err != nil {
					return err
				}
				batch = batch[:0]
				last = time.Now()
				first = false
				return nil
			}
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				names, readErr := dir.Readdirnames(64)
				for _, name := range names {
					if err := ctx.Err(); err != nil {
						return err
					}
					seen++
					if seen > 50000 {
						return ErrLimit
					}
					if strings.HasPrefix(name, ".") {
						continue
					}
					if !goodName(name) || len(name) > 255 {
						incomplete = ErrLimit
						continue
					}
					rel := filepath.Join(d.rel, name)
					if len(rel) > 4096 {
						incomplete = ErrLimit
						continue
					}
					var child unix.Stat_t
					if unix.Fstatat(int(dir.Fd()), name, &child, unix.AT_SYMLINK_NOFOLLOW) != nil {
						incomplete = errUnreadable
						continue
					}
					if child.Dev != st.Dev || !eligible(&child) {
						continue
					}
					switch child.Mode & unix.S_IFMT {
					case unix.S_IFDIR:
						if skipDirectory(name) {
							continue
						}
						if d.depth >= 32 || dirs >= 4096 {
							incomplete = ErrLimit
							continue
						}
						dirs++
						q = append(q, directory{rel: rel, chain: d.chain + "/" + witness(&child), depth: d.depth + 1})
					case unix.S_IFREG:
						if child.Nlink != 1 {
							continue
						}
						w := fmt.Sprintf("%s:%d:%d", witness(&child), child.Mode, child.Size)
						bytes += len(rel) + len(name) + len(folded(name)) + len(w) + len(d.chain) + 96
						files++
						if files > 25000 || bytes > 16<<20 {
							return ErrLimit
						}
						batch = append(batch, entry{relative: rel, name: name, modified: child.Mtim.Nano(), witness: w, chain: d.chain})
						if first || len(batch) == 64 || time.Since(last) >= 50*time.Millisecond {
							if e := flush(); e != nil {
								return e
							}
						}
					}
				}
				if readErr != nil {
					if !errors.Is(readErr, io.EOF) {
						if d.depth == 0 {
							return ErrUnavailable
						}
						incomplete = errUnreadable
					}
					return flush()
				}
			}
		}()
		if errors.Is(err, ErrMissing) {
			incomplete = ErrMissing
			continue
		}
		if err != nil {
			return err
		}
	}
	return incomplete
}
func lastWitness(s string) string { return s[strings.LastIndex(s, "/")+1:] }
