package workspace

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Reasons a contained read is refused. None carries a filesystem path, so a
// caller can report one without leaking where a folder lives.
var (
	ErrSourceOutside    = errors.New("source path is outside its approved folder")
	ErrSourceExcluded   = errors.New("source is not readable here")
	ErrSourceMissing    = errors.New("source file not found")
	ErrSourceNotRegular = errors.New("source is not a regular file")
	ErrSourceTooLarge   = errors.New("source file is too large to read")
	ErrSourceChanged    = errors.New("source changed while it was being read")
	ErrSourceUnreadable = errors.New("source could not be read")
)

// ContainedFile is one regular file read from inside an approved folder.
type ContainedFile struct {
	Data    []byte
	Size    int64
	ModTime time.Time
}

// oriRecordFiles are Ori's own records. A linked folder can contain a workspace
// folder, and these hold what an ordinary file read must not hand out: reviewed
// memory (which has its own eligible reader), absolute folder locations, agent
// definitions and tool-server settings. The names are matched without case.
var oriRecordFiles = []string{MemoryFileName, "workspace.json", "agent_settings.json", "mcp_servers.json", "skills_state.json"}

// ContainedEntryExcluded reports whether one path component is left out of
// contained listings and reads: a hidden entry or one of Ori's own records.
func ContainedEntryExcluded(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	for _, record := range oriRecordFiles {
		if strings.EqualFold(name, record) {
			return true
		}
	}
	return false
}

// ContainedRelativePath normalizes a relative path for a read inside an
// approved folder. It refuses an absolute path, a climb out of the folder, a
// hidden component, and Ori's own record files.
func ContainedRelativePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsRune(trimmed, 0) || filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return "", ErrSourceOutside
	}
	clean := path.Clean(filepath.ToSlash(trimmed))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.VolumeName(filepath.FromSlash(clean)) != "" {
		return "", ErrSourceOutside
	}
	for _, part := range strings.Split(clean, "/") {
		if ContainedEntryExcluded(part) {
			return "", ErrSourceExcluded
		}
	}
	return clean, nil
}

// ReadContainedFile reads one regular file from inside root, an approved folder
// the caller resolved from canonical state.
//
// Checking a path and then opening it leaves a gap in which a folder on the way
// can be swapped for a link that points elsewhere. This opens through an
// os.Root instead, so every component is resolved relative to the folder that
// was actually opened and nothing can lead outside it. The open file is then
// the only thing consulted: its type and size are read from the handle, the
// bytes come from that same handle, and the handle and the folder are checked
// again afterwards. A file or folder replaced along the way is reported as
// changed; a substitute is never read in its place.
func ReadContainedFile(root, relativePath string, maxBytes int64) (ContainedFile, error) {
	rel, err := ContainedRelativePath(relativePath)
	if err != nil {
		return ContainedFile{}, err
	}
	if !filepath.IsAbs(root) || maxBytes <= 0 {
		return ContainedFile{}, ErrSourceUnreadable
	}
	root = filepath.Clean(root)
	rootInfo, err := os.Lstat(root) // #nosec G304 G703 -- a folder resolved from canonical state by the caller, never a request path
	if err != nil {
		return ContainedFile{}, ErrSourceMissing
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ContainedFile{}, ErrSourceOutside
	}
	folder, err := os.OpenRoot(root)
	if err != nil {
		return ContainedFile{}, ErrSourceUnreadable
	}
	defer func() { _ = folder.Close() }()
	if opened, err := folder.Stat("."); err != nil || !os.SameFile(rootInfo, opened) {
		return ContainedFile{}, ErrSourceChanged
	}
	// Inspect before opening so a pipe or device is refused without being
	// opened; O_NONBLOCK keeps an open from waiting if one was swapped in after.
	before, err := folder.Stat(filepath.FromSlash(rel))
	if err != nil {
		return ContainedFile{}, containedOpenError(err)
	}
	if !before.Mode().IsRegular() {
		return ContainedFile{}, ErrSourceNotRegular
	}
	if before.Size() > maxBytes {
		return ContainedFile{}, ErrSourceTooLarge
	}
	file, err := folder.OpenFile(filepath.FromSlash(rel), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ContainedFile{}, containedOpenError(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return ContainedFile{}, ErrSourceUnreadable
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return ContainedFile{}, ErrSourceChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return ContainedFile{}, ErrSourceUnreadable
	}
	if int64(len(data)) > maxBytes {
		return ContainedFile{}, ErrSourceTooLarge
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || int64(len(data)) != info.Size() {
		return ContainedFile{}, ErrSourceChanged
	}
	if current, err := os.Lstat(root); err != nil || !os.SameFile(rootInfo, current) { // #nosec G304 G703 -- same canonical folder as above
		return ContainedFile{}, ErrSourceChanged
	}
	return ContainedFile{Data: data, Size: info.Size(), ModTime: info.ModTime()}, nil
}

// ListContainedEntries lists names inside root below relativePath (empty for the
// folder itself), with the linked-directory bounds: at most depth levels
// (capped at DirectoryListMaxDepth) and DirectoryListMaxEntries entries. It
// walks through an os.Root, lists a symbolic link without following it, and
// skips hidden entries and Ori's own record files. It reads names, sizes and
// times only; no file is opened.
func ListContainedEntries(root, relativePath string, depth int) (DirectoryListing, error) {
	start := "."
	if strings.TrimSpace(relativePath) != "" && strings.TrimSpace(relativePath) != "." {
		rel, err := ContainedRelativePath(relativePath)
		if err != nil {
			return DirectoryListing{}, err
		}
		start = rel
	}
	if !filepath.IsAbs(root) {
		return DirectoryListing{}, ErrSourceUnreadable
	}
	root = filepath.Clean(root)
	rootInfo, err := os.Lstat(root) // #nosec G304 G703 -- a folder resolved from canonical state by the caller, never a request path
	if err != nil {
		return DirectoryListing{}, ErrSourceMissing
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return DirectoryListing{}, ErrSourceOutside
	}
	folder, err := os.OpenRoot(root)
	if err != nil {
		return DirectoryListing{}, ErrSourceUnreadable
	}
	defer func() { _ = folder.Close() }()
	if opened, err := folder.Stat("."); err != nil || !os.SameFile(rootInfo, opened) {
		return DirectoryListing{}, ErrSourceChanged
	}
	if depth <= 0 || depth > DirectoryListMaxDepth {
		depth = DirectoryListMaxDepth
	}
	if info, err := folder.Stat(filepath.FromSlash(start)); err != nil {
		return DirectoryListing{}, containedOpenError(err)
	} else if !info.IsDir() {
		return DirectoryListing{}, ErrSourceNotRegular
	}
	tree := folder.FS()
	var listing DirectoryListing
	var walk func(dir string, level int) error
	walk = func(dir string, level int) error {
		entries, err := fs.ReadDir(tree, dir)
		if err != nil {
			// An unreadable subfolder is skipped; the starting folder's error surfaces.
			if level == 1 {
				return containedOpenError(err)
			}
			return nil
		}
		for _, entry := range entries {
			name := entry.Name()
			if ContainedEntryExcluded(name) {
				continue
			}
			if len(listing.Entries) >= DirectoryListMaxEntries {
				listing.Truncated = true
				return nil
			}
			full := path.Join(dir, name)
			item := DirectoryEntry{Name: name, RelativePath: full, Kind: "file"}
			switch {
			case entry.Type()&fs.ModeSymlink != 0:
				item.Kind = "link"
			case entry.IsDir():
				item.Kind = "folder"
			case !entry.Type().IsRegular():
				item.Kind = "other"
			}
			if info, err := entry.Info(); err == nil {
				item.ModTime = info.ModTime()
				if item.Kind == "file" {
					item.Size = info.Size()
				}
			}
			listing.Entries = append(listing.Entries, item)
			if item.Kind == "folder" && level < depth {
				if err := walk(full, level+1); err != nil {
					return err
				}
				if listing.Truncated {
					return nil
				}
			}
		}
		return nil
	}
	if err := walk(start, 1); err != nil {
		return DirectoryListing{}, err
	}
	return listing, nil
}

// AttachmentSourcePath is the workspace-relative path of an attachment's own
// stored file, or "" when the attachment has none. A legacy attachment that
// only remembers where a file once was (OriginalPath, a file:// link) has no
// stored file: that remembered location is metadata and is never read.
func AttachmentSourcePath(workspaceID string, meta *AttachmentFileMeta) string {
	return filepath.ToSlash(extractAttachmentRelativePath(workspaceID, meta))
}

func containedOpenError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrSourceMissing
	case errors.Is(err, fs.ErrPermission):
		return ErrSourceUnreadable
	}
	// os.Root refuses a path that would leave the folder, including through a
	// symbolic link.
	if strings.Contains(err.Error(), "escapes") {
		return ErrSourceOutside
	}
	return ErrSourceUnreadable
}

// ProjectEntrySource returns the approved folder and the relative path of a
// workspace's exact project entry, for a contained read. It reuses
// ResolveProjectEntry for every identity and containment check and searches for
// nothing: the entry is the persisted typed locator's file or it is unavailable.
// Sample-library and other capability-owned folders are never a source.
func ProjectEntrySource(ws *Workspace, workspaceRoot string) (root, relativePath string, err error) {
	resolved, err := ResolveProjectEntry(ws, workspaceRoot)
	if err != nil {
		return "", "", err
	}
	candidates := []string{filepath.Clean(workspaceRoot), filepath.Dir(filepath.Clean(workspaceRoot))}
	if resolved.Locator.Kind == ProjectEntryDirectoryReference {
		ref, err := ws.GetDirectoryReference(resolved.Locator.DirectoryReferenceID)
		if err != nil || ref == nil || strings.TrimSpace(ref.Purpose) != "" {
			return "", "", ErrProjectEntryUnavailable
		}
		candidates = []string{filepath.Clean(ref.Path)}
	}
	for _, candidate := range candidates {
		rel, err := filepath.Rel(candidate, resolved.AbsolutePath)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return candidate, filepath.ToSlash(rel), nil
		}
	}
	return "", "", ErrProjectEntryUnsafe
}
