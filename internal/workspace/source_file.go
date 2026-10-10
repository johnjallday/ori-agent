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
	ErrSourceLinked     = errors.New("source is reached through a link, which is not followed")
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

// isWorkspaceFolder reports whether dir is one of Ori's workspace folders: it
// holds a workspace record.
func isWorkspaceFolder(dir *os.Root) bool {
	info, err := dir.Lstat(WorkspaceConfigFile)
	return err == nil && !info.IsDir()
}

// workspaceFolderEntryExcluded reports whether name, directly inside one of
// Ori's workspace folders, is Ori's own: the workspace's agent snapshots, or the
// folder that holds its child workspaces.
func workspaceFolderEntryExcluded(name string) bool {
	return strings.EqualFold(name, WorkspaceAgentsDir) || strings.EqualFold(name, SubWorkspacesDir)
}

// openContainedDir opens the directory name directly inside parent without
// following a link. The entry is inspected first, and the directory that was
// then opened must be that same entry: a link put in its place in between opens
// something else and is refused. The returned handle stays on that directory
// whatever is renamed afterwards.
//
// An approved folder can be, or contain, Ori's own workspace folders. Reading
// one workspace must not reach another's records, so a directory below the
// approved folder that is itself a workspace folder is not entered, and neither
// are a workspace folder's agent snapshots or its child workspaces.
func openContainedDir(parent *os.Root, name string) (*os.Root, error) {
	if workspaceFolderEntryExcluded(name) && isWorkspaceFolder(parent) {
		return nil, ErrSourceExcluded
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, containedOpenError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSourceLinked
	}
	if !info.IsDir() {
		return nil, ErrSourceNotRegular
	}
	sub, err := parent.OpenRoot(name)
	if err != nil {
		return nil, containedOpenError(err)
	}
	if opened, err := sub.Stat("."); err != nil || !os.SameFile(info, opened) {
		_ = sub.Close()
		return nil, ErrSourceChanged
	}
	if isWorkspaceFolder(sub) {
		_ = sub.Close()
		return nil, ErrSourceExcluded
	}
	return sub, nil
}

// containedParent walks from folder to the directory that holds rel's last
// component, one directory at a time and never through a link, and returns that
// directory with the last component's name. A link inside the folder is not
// followed either: it could give an excluded entry (a hidden file, one of Ori's
// records) a name that is not excluded.
func containedParent(folder *os.Root, rel string) (dir *os.Root, name string, release func(), err error) {
	parts := strings.Split(rel, "/")
	dir, release = folder, func() {}
	for _, part := range parts[:len(parts)-1] {
		next, err := openContainedDir(dir, part)
		release()
		if errors.Is(err, ErrSourceNotRegular) {
			err = ErrSourceMissing // a file where a folder was named
		}
		if err != nil {
			return nil, "", func() {}, err
		}
		dir, release = next, func() { _ = next.Close() }
	}
	return dir, parts[len(parts)-1], release, nil
}

// ReadContainedFile reads one regular file from inside root, an approved folder
// the caller resolved from canonical state.
//
// Checking a path and then opening it leaves a gap in which a folder on the way
// can be swapped for a link that points elsewhere. This opens through an
// os.Root instead, so nothing can lead outside the folder that was actually
// opened, and it walks to the file one directory handle at a time without
// following any link, so nothing inside the folder can be reached under another
// name. The open file is then the only thing consulted: its type and size are
// read from the handle, the bytes come from that same handle, and the handle
// and the folder are checked again afterwards. A file or folder replaced along
// the way is reported as changed; a substitute is never read in its place.
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
	dir, name, release, err := containedParent(folder, rel)
	if err != nil {
		return ContainedFile{}, err
	}
	defer release()
	// Inspect before opening so a link, a pipe or a device is refused without
	// being opened; O_NONBLOCK keeps an open from waiting if one was swapped in
	// after. The entry seen here must be the file that is then opened.
	before, err := dir.Lstat(name)
	if err != nil {
		return ContainedFile{}, containedOpenError(err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return ContainedFile{}, ErrSourceLinked
	}
	if !before.Mode().IsRegular() {
		return ContainedFile{}, ErrSourceNotRegular
	}
	if before.Size() > maxBytes {
		return ContainedFile{}, ErrSourceTooLarge
	}
	file, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
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
// walks through an os.Root one directory handle at a time, lists a symbolic
// link without following it (a link on the way to relativePath is refused), and
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
	top := folder
	if start != "." {
		parent, name, release, err := containedParent(folder, start)
		if err != nil {
			return DirectoryListing{}, err
		}
		top, err = openContainedDir(parent, name)
		release()
		if err != nil {
			return DirectoryListing{}, err
		}
		defer func() { _ = top.Close() }()
	}
	var listing DirectoryListing
	var walk func(current *os.Root, dir string, level int) error
	walk = func(current *os.Root, dir string, level int) error {
		entries, err := fs.ReadDir(current.FS(), ".")
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
			// Read through the verified handle, not entry.Info(): on Go 1.26.0
			// that re-resolves the folder's path, so a folder swapped for a link
			// in between would describe an excluded entry under a visible name.
			if info, err := current.Lstat(name); err == nil {
				item.ModTime = info.ModTime()
				if item.Kind == "file" {
					item.Size = info.Size()
				}
			}
			var sub *os.Root
			if item.Kind == "folder" {
				// Opened before it is listed: another workspace's folder, or one of
				// Ori's own inside a workspace folder, is not named at all.
				opened, err := openContainedDir(current, name)
				if errors.Is(err, ErrSourceExcluded) {
					continue
				}
				sub = opened // nil when unreadable or swapped for a link: listed, left unexpanded
			}
			listing.Entries = append(listing.Entries, item)
			if sub != nil {
				var below error
				if level < depth {
					below = walk(sub, full, level+1)
				}
				_ = sub.Close()
				if below != nil {
					return below
				}
				if listing.Truncated {
					return nil
				}
			}
		}
		return nil
	}
	if err := walk(top, start, 1); err != nil {
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
