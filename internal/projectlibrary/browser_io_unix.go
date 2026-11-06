//go:build darwin || linux || freebsd

package projectlibrary

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

func browserIOError(err error) error {
	if errors.Is(err, unix.ENOENT) {
		return ErrBrowserMissing
	}
	return ErrBrowserDenied // Never echo a composed filesystem path.
}

// Walk even the absolute ancestors with no-follow directory descriptors. A
// pathname check followed by Open would allow an ancestor-swap escape.
func browserOpenRoot(source browserSource) (int, error) {
	if !strings.HasPrefix(source.Root.Path, "/") {
		return -1, ErrBrowserDenied
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, browserIOError(err)
	}
	for _, part := range strings.Split(strings.TrimPrefix(source.Root.Path, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(fd)
			return -1, ErrBrowserDenied
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return -1, browserIOError(err)
		}
		fd = next
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || unixIdentity(&st) != source.Root.FileIdentity {
		_ = unix.Close(fd)
		return -1, ErrBrowserChanged
	}
	// The authoritative locator must still name a regular project entry. This
	// is metadata only, not a project-format parser or a read of its contents.
	if unix.Fstatat(fd, source.Locator.RelativePath, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
		_ = unix.Close(fd)
		return -1, ErrBrowserMissing
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return -1, ErrBrowserChanged
	}
	return fd, nil
}

func browserCheckRoot(source browserSource) error {
	fd, err := browserOpenRoot(source)
	if err == nil {
		_ = unix.Close(fd)
	}
	return err
}

func browserOpenDirectory(source browserSource, relative string) (*os.File, error) {
	fd, err := browserOpenRoot(source)
	if err != nil {
		return nil, err
	}
	if relative != "" {
		for _, part := range strings.Split(relative, "/") {
			next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			_ = unix.Close(fd)
			if err != nil {
				return nil, browserIOError(err)
			}
			fd = next
		}
	}
	return os.NewFile(uintptr(fd), "project-directory"), nil
}

func browserCheckLocation(source browserSource, relative, version string, file bool) error {
	parent := relative
	if file {
		parent = path.Dir(relative)
		if parent == "." {
			parent = ""
		}
	}
	dir, err := browserOpenDirectory(source, parent)
	if err != nil {
		return ErrBrowserChanged
	}
	defer func() { _ = dir.Close() }()
	var st unix.Stat_t
	if file {
		if unix.Fstatat(int(dir.Fd()), path.Base(relative), &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || browserVersion(source, relative, unixIdentity(&st), st.Size, time.Unix(st.Mtim.Unix()).UTC()) != version {
			return ErrBrowserChanged
		}
	} else if unix.Fstat(int(dir.Fd()), &st) != nil || browserVersion(source, relative, unixIdentity(&st), 0, time.Time{}) != version {
		return ErrBrowserChanged
	}
	return nil
}

func browserReadDirectory(ctx context.Context, source browserSource, relative string) (BrowserListing, error) {
	listing := BrowserListing{Path: relative, Entries: []BrowserEntry{}}
	dir, err := browserOpenDirectory(source, relative)
	if err != nil {
		return listing, err
	}
	defer func() { _ = dir.Close() }()
	var directoryStat unix.Stat_t
	if unix.Fstat(int(dir.Fd()), &directoryStat) != nil {
		return BrowserListing{}, ErrBrowserChanged
	}
	listing.Witness = browserVersion(source, relative, unixIdentity(&directoryStat), 0, time.Time{})
	// Names only: no path-based DirEntry.Info fallback on filesystems that
	// omit directory-entry types. All metadata below is descriptor-relative.
	entries, err := dir.Readdirnames(BrowserMaxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return BrowserListing{}, browserIOError(err)
	}
	if len(entries) > BrowserMaxEntries {
		listing.Truncated, listing.Reason = true, "entry_limit"
		entries = entries[:BrowserMaxEntries]
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return BrowserListing{}, err
		}
		rel := path.Join(relative, entry)
		if !browserRelative(rel, true) {
			listing.Truncated, listing.Reason = true, "unsupported_names"
			continue
		}
		var st unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), entry, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
				return BrowserListing{}, ErrBrowserDenied
			}
			listing.Truncated, listing.Reason = true, "changed_entries"
			continue
		}
		row := BrowserEntry{Name: entry, Path: rel, Kind: "unsupported", Size: st.Size, Modified: time.Unix(st.Mtim.Unix()).UTC()}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			row.Kind = "folder"
		case unix.S_IFLNK:
			row.Kind = "link"
		case unix.S_IFREG:
			row.Kind = "file"
			row.Version = browserVersion(source, rel, unixIdentity(&st), st.Size, row.Modified)
		}
		listing.Entries = append(listing.Entries, row)
	}
	sort.Slice(listing.Entries, func(i, j int) bool { return listing.Entries[i].Name < listing.Entries[j].Name })
	if err := browserCheckRoot(source); err != nil {
		return BrowserListing{}, err
	}
	return listing, ctx.Err()
}

type browserContextReader struct {
	ctx context.Context
	io.Reader
}

func (r browserContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func browserReadPreview(ctx context.Context, source browserSource, relative, version string) (BrowserPreview, error) {
	fail := BrowserPreview{}
	media, limit := browserPreviewType(relative)
	if limit == 0 {
		return fail, ErrBrowserUnsupported
	}
	parent := path.Dir(relative)
	if parent == "." {
		parent = ""
	}
	dir, err := browserOpenDirectory(source, parent)
	if err != nil {
		return fail, err
	}
	defer func() { _ = dir.Close() }()
	fd, err := unix.Openat(int(dir.Fd()), path.Base(relative), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0) // #nosec G304 G703 -- validated base name opened read-only under an independently authorized, pinned no-follow descriptor.
	if err != nil {
		return fail, browserIOError(err)
	}
	file := os.NewFile(uintptr(fd), "project-preview")
	defer func() { _ = file.Close() }()
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG {
		return fail, ErrBrowserDenied
	}
	if before.Size > int64(limit) {
		return fail, ErrBrowserLarge
	}
	if browserVersion(source, relative, unixIdentity(&before), before.Size, time.Unix(before.Mtim.Unix()).UTC()) != version {
		return fail, ErrBrowserChanged
	}
	data, err := io.ReadAll(io.LimitReader(browserContextReader{ctx, file}, int64(limit)+1))
	if err != nil {
		if ctx.Err() != nil {
			return fail, ctx.Err()
		}
		return fail, ErrBrowserMissing
	}
	if len(data) > limit {
		return fail, ErrBrowserLarge
	}
	var after unix.Stat_t
	if unix.Fstat(fd, &after) != nil || browserVersion(source, relative, unixIdentity(&after), after.Size, time.Unix(after.Mtim.Unix()).UTC()) != version {
		return fail, ErrBrowserChanged
	}
	// Check the directory entry as well as the open descriptor: a replaced file
	// can leave the old descriptor unchanged while the name points elsewhere.
	if unix.Fstatat(int(dir.Fd()), path.Base(relative), &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		after.Mode&unix.S_IFMT != unix.S_IFREG || browserVersion(source, relative, unixIdentity(&after), after.Size, time.Unix(after.Mtim.Unix()).UTC()) != version {
		return fail, ErrBrowserChanged
	}
	if strings.HasPrefix(media, "image/") {
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 ||
			(media == "image/png" && format != "png") || (media == "image/jpeg" && format != "jpeg") {
			return fail, ErrBrowserUnsupported
		}
		if config.Width > 16_000_000/config.Height {
			return fail, ErrBrowserLarge
		}
	} else if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return fail, ErrBrowserUnsupported
	}
	if err := browserCheckRoot(source); err != nil {
		return fail, err
	}
	return BrowserPreview{MediaType: media, Bytes: data}, ctx.Err()
}
