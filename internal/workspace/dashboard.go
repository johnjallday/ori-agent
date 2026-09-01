package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

const (
	// SidecarDirName is the hidden per-workspace directory holding Ori's own
	// sidecar state inside a workspace folder. It is hidden deliberately: a
	// visible directory at the workspace root would collide with the user's own
	// project folders.
	SidecarDirName = ".ori"

	// CustomDashboardDirName is the directory under SidecarDirName holding a
	// user-authored dashboard. Every file in it is servable to the dashboard
	// frame, so a dashboard may be more than one file.
	CustomDashboardDirName = "dashboard"

	// CustomDashboardEntryAsset is the fixed entry file, relative to the
	// dashboard's asset root. It is a constant rather than a configurable value
	// so no user-controlled string ever becomes a path segment.
	CustomDashboardEntryAsset = "index.html"

	// maxDashboardAssetEntries bounds the fingerprint walk. Discovery runs on
	// every request, so a pathological directory must not become a per-request
	// filesystem crawl. A dashboard needing more files than this is misusing the
	// folder.
	maxDashboardAssetEntries = 512

	// dashboardAssetVersionPrefix versions the fingerprint scheme itself, so a
	// future change to how the version is derived cannot collide with a value
	// produced by the current scheme.
	dashboardAssetVersionPrefix = "d1"

	// MaxDashboardEntryHashBytes bounds the entry-asset read during
	// fingerprinting. It matches workspacesurface.MaxAssetBytes: an entry larger
	// than that cannot be served anyway, so hashing further bytes buys nothing.
	MaxDashboardEntryHashBytes = 8 << 20
)

// ErrDashboardFolderUnavailable reports that the workspace's folder could not be
// resolved at all. It is distinct from "this workspace has no dashboard", which
// is not an error.
var ErrDashboardFolderUnavailable = errors.New("workspace dashboard folder is unavailable")

// CustomDashboard is a discovered user-authored dashboard: where its files live
// and which one is the entry point.
//
// The contents are untrusted. This value only records that the files are present
// and where they are; nothing here implies they are safe to execute outside the
// sandboxed frame they are served into.
type CustomDashboard struct {
	// AssetRoot is absolute and cleaned, as workspacesurface.Binding requires.
	AssetRoot string
	// EntryAsset is always CustomDashboardEntryAsset, relative to AssetRoot.
	EntryAsset string
	// AssetVersion fingerprints the dashboard's files. It changes whenever the
	// dashboard changes, which is what makes an edited dashboard actually reach
	// the browser. See dashboardAssetVersion for what goes into it.
	AssetVersion string
}

// EntryPath is the absolute path of the entry file. Failure messages name it,
// because a user debugging their own HTML has no devtools access into the
// opaque frame and the file path is their only reliable signal.
func (d CustomDashboard) EntryPath() string {
	if d.AssetRoot == "" {
		return ""
	}
	return filepath.Join(d.AssetRoot, d.EntryAsset)
}

// DashboardStore discovers user-authored dashboards in workspace folders. It
// holds no state: discovery reads the filesystem on every call, so creating the
// file and reloading is enough to make a dashboard appear, and deleting it is
// enough to make it disappear. Disk is truth.
type DashboardStore struct {
	resolver FolderResolver
}

// NewDashboardStore creates a dashboard store over the same canonical folder
// resolution that locates workspace.json.
func NewDashboardStore(resolver FolderResolver) *DashboardStore {
	return &DashboardStore{resolver: resolver}
}

// Find reports whether workspaceID has a custom dashboard and, if so, where it
// is. A workspace with no dashboard returns ok=false and a nil error — absence
// is the common case, not a failure. A non-nil error means the workspace folder
// itself could not be resolved.
//
// Presence requires the dashboard directory to be a real directory and the entry
// file to be a real regular file, neither of them a symlink. Those are exactly
// the invariants workspacesurface.ReadAsset enforces when it later serves the
// file, and the two must agree: a dashboard that is discoverable but unservable
// would show the user a tab that can never open.
//
// An entry file that exists but cannot be read (permissions, for instance) is
// still "present". The failure then surfaces at open time, where the host can
// name the path and the reason, rather than silently hiding the tab.
func (s *DashboardStore) Find(workspaceID string) (CustomDashboard, bool, error) {
	if s == nil || s.resolver == nil {
		return CustomDashboard{}, false, ErrDashboardFolderUnavailable
	}
	folder, err := s.resolver.GetFolderPath(workspaceID)
	if err != nil {
		return CustomDashboard{}, false, fmt.Errorf("%w: %v", ErrDashboardFolderUnavailable, err)
	}
	// workspaceID is only a lookup key inside GetFolderPath, never a path
	// segment, and both directory names appended here are package constants.
	assetRoot, err := filepath.Abs(filepath.Join(folder, SidecarDirName, CustomDashboardDirName))
	if err != nil {
		return CustomDashboard{}, false, fmt.Errorf("%w: %v", ErrDashboardFolderUnavailable, err)
	}

	rootInfo, err := os.Lstat(assetRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return CustomDashboard{}, false, nil
	}
	entryInfo, err := os.Lstat(filepath.Join(assetRoot, CustomDashboardEntryAsset))
	if err != nil || !entryInfo.Mode().IsRegular() {
		return CustomDashboard{}, false, nil
	}
	version, err := dashboardAssetVersion(assetRoot)
	if err != nil {
		return CustomDashboard{}, false, err
	}
	return CustomDashboard{
		AssetRoot: assetRoot, EntryAsset: CustomDashboardEntryAsset, AssetVersion: version,
	}, true, nil
}

// dashboardAssetVersion fingerprints a dashboard directory.
//
// The entry asset is hashed by content; every other file contributes its
// relative path, size, and modification time. That split is deliberate, and it
// follows from how the assets are cached rather than from a general preference
// for one method:
//
//   - The entry asset is served `Cache-Control: no-store` and is therefore
//     always re-fetched, so its own version never affects freshness. It is
//     content-hashed anyway because it is small, always present, and content
//     hashing is immune to an edit that happens to preserve size and timestamp.
//   - Sibling assets are served `immutable` for a year, keyed by this version in
//     their URL path. They are the files that can actually go stale, so the
//     version MUST move when any of them changes. Content-hashing every sibling
//     on every request would re-read multi-megabyte images on each page load;
//     path, size, and modification time move on any real edit at nanosecond
//     resolution and cost one stat each.
//
// Directory names are included too, so adding or removing an empty directory is
// still a change. Symlinks contribute their path only: workspacesurface.ReadAsset
// refuses to serve them, so their targets are irrelevant.
func dashboardAssetVersion(assetRoot string) (string, error) {
	digest := sha256.New()
	writeField := func(values ...string) {
		for _, value := range values {
			_, _ = digest.Write([]byte(value))
			_, _ = digest.Write([]byte{0})
		}
	}
	writeField(dashboardAssetVersionPrefix)

	// The path is the discovered asset root joined with a package constant; no
	// caller-supplied string reaches it. The file is hashed, never executed or
	// echoed, and the read is bounded by MaxDashboardEntryHashBytes.
	entry, err := os.Open(filepath.Join(assetRoot, CustomDashboardEntryAsset)) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("read dashboard entry asset: %w", err)
	}
	writeField("entry")
	_, copyErr := io.Copy(digest, io.LimitReader(entry, MaxDashboardEntryHashBytes))
	closeErr := entry.Close()
	if copyErr != nil || closeErr != nil {
		return "", fmt.Errorf("read dashboard entry asset: %w", errors.Join(copyErr, closeErr))
	}

	entries := 0
	walkErr := filepath.WalkDir(assetRoot, func(path string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == assetRoot {
			return nil
		}
		entries++
		if entries > maxDashboardAssetEntries {
			return fmt.Errorf("dashboard has more than %d files", maxDashboardAssetEntries)
		}
		relative, err := filepath.Rel(assetRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		switch {
		case item.Type()&fs.ModeSymlink != 0:
			writeField("l", relative)
		case item.IsDir():
			writeField("d", relative)
		default:
			info, err := item.Info()
			if err != nil {
				return err
			}
			writeField("f", relative, strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10))
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("fingerprint dashboard assets: %w", walkErr)
	}
	// workspacesurface.canonicalAssetVersion accepts up to 128 characters of
	// [a-z0-9._-]; 128 bits of hex is unambiguous here and keeps the frame URL
	// readable while debugging.
	return dashboardAssetVersionPrefix + "-" + hex.EncodeToString(digest.Sum(nil)[:16]), nil
}
