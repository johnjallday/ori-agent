package downloadsjanitor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
)

// caseInsensitiveFilesystem reports whether this platform's filesystem folds
// case by default. It is a platform assumption rather than a per-volume probe:
// getting it wrong in the permissive direction (treating distinct folders as
// the same) only ever refuses a setup, which is the safe failure.
func caseInsensitiveFilesystem() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

// RootGuards are the locations a managed folder may never be, or contain.
//
// They are injected rather than resolved inside the validator so tests exercise
// the real rules against temporary directories instead of the developer's home
// folder — and so a deployment with a relocated data directory is protected by
// the same code.
type RootGuards struct {
	// HomeDir is the user's home folder. The home folder ITSELF is refused
	// (FR-48); folders inside it are the normal case and stay allowed.
	HomeDir string
	// DataDir is Ori's application-data root: settings, database, vaults.
	DataDir string
	// WorkspaceRoot is where Ori keeps workspace folders.
	WorkspaceRoot string
	// ExtraForbidden are additional roots a caller wants protected (e.g. a
	// specific workspace's project directory).
	ExtraForbidden []string
}

// DefaultRootGuards resolves the locations this deployment must protect.
//
// It reads the same resolvers the rest of Ori uses, so a relocated data
// directory (ORI_DATA_DIR) or a custom workspace root is protected without
// anything here being told about it. Tests build RootGuards directly against
// temporary directories instead of calling this.
func DefaultRootGuards() RootGuards {
	guards := RootGuards{
		DataDir:       config.DefaultDataDir(),
		WorkspaceRoot: config.DefaultWorkspaceRoot(),
	}
	if home, err := os.UserHomeDir(); err == nil {
		guards.HomeDir = home
	}
	return guards
}

// canonicalizeRoot turns a user-confirmed selection into the one absolute,
// symlink-resolved path everything else derives from (FR-46, FR-47).
//
// Symlink resolution is what makes ownership and overlap checks meaningful. A
// symlink at ~/Inbox pointing to ~/Downloads is the SAME folder; storing the
// link path would let two workspaces each "own" it, and would let a later
// containment check compare against a path the filesystem does not actually
// use. Resolving here means every stored root, every overlap comparison, and
// every action-time re-check speak about the same real directory.
func canonicalizeRoot(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", setupErr(CodeInvalidPath, "Choose a folder to tidy.", RepairChooseFolder, nil)
	}
	if strings.ContainsRune(path, 0) {
		return "", setupErr(CodeInvalidPath, "That folder path is not valid.", RepairChooseFolder, nil)
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", setupErr(CodeInvalidPath, "Ori could not locate your home folder. Choose the folder directly instead.", RepairChooseFolder, err)
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", setupErr(CodeInvalidPath, "That folder path is not valid.", RepairChooseFolder, err)
	}
	abs = filepath.Clean(abs)

	// Resolve before stat: a link to a deleted target should report "no longer
	// exists" rather than appearing to exist because the link does.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		switch {
		case os.IsNotExist(err):
			return "", setupErr(CodeRootMissing, "That folder no longer exists. Choose a folder that is on this computer.", RepairChooseFolder, err)
		case os.IsPermission(err):
			return "", setupErr(CodePermissionDenied, permissionGuidance("that folder"), RepairGrantPermission, err)
		default:
			return "", setupErr(CodeInvalidPath, "Ori could not open that folder.", RepairChooseFolder, err)
		}
	}
	return filepath.Clean(resolved), nil
}

// rejectUnsafeRoot refuses selections too broad to be an inbox-style folder
// (FR-48).
//
// The test is deliberately structural rather than a denylist of names: a
// selection is refused when it is the filesystem or a volume root, when it IS
// one of the guarded locations, or when it is an ANCESTOR of one. That last
// case is the one a name-based check would miss — granting a parent of Ori's
// data directory hands over the database and vaults just as surely as granting
// the directory itself.
func rejectUnsafeRoot(root string, guards RootGuards) error {
	cleaned := filepath.Clean(root)

	if isFilesystemOrVolumeRoot(cleaned) {
		return setupErr(CodeInvalidPath,
			"That is a whole drive rather than a folder. Choose one inbox-style folder, such as Downloads or Desktop.",
			RepairChooseFolder, nil)
	}

	// The home directory itself is too broad; folders inside it are fine.
	if guard := cleanGuard(guards.HomeDir); guard != "" && pathsEqual(cleaned, guard) {
		return setupErr(CodeInvalidPath,
			"That is your whole home folder. Choose one folder inside it, such as Downloads or Desktop.",
			RepairChooseFolder, nil)
	}

	protected := []struct {
		path    string
		message string
	}{
		{guards.DataDir, "That folder holds Ori's own data. Choose a different folder."},
		{guards.WorkspaceRoot, "That folder holds your Ori workspaces. Choose a different folder."},
	}
	for _, extra := range guards.ExtraForbidden {
		protected = append(protected, struct {
			path    string
			message string
		}{extra, "That folder is used by Ori itself. Choose a different folder."})
	}

	for _, entry := range protected {
		guard := cleanGuard(entry.path)
		if guard == "" {
			continue
		}
		// Equal, or the selection contains the guarded location.
		if pathsEqual(cleaned, guard) || isAncestor(cleaned, guard) {
			return setupErr(CodeInvalidPath, entry.message, RepairChooseFolder, nil)
		}
	}

	return nil
}

// isFilesystemOrVolumeRoot reports whether path is the root of a filesystem or
// a mounted volume, both of which are whole-drive grants rather than folders.
func isFilesystemOrVolumeRoot(path string) bool {
	cleaned := filepath.Clean(path)
	if cleaned == string(filepath.Separator) {
		return true
	}
	// A path whose parent is itself is a root (covers Windows drive roots such
	// as C:\ as well as "/").
	if parent := filepath.Dir(cleaned); parent == cleaned {
		return true
	}
	// macOS mounts external volumes at /Volumes/<name>; the volume root is the
	// whole drive.
	if parent := filepath.Dir(cleaned); pathsEqual(parent, "/Volumes") {
		return true
	}
	return false
}

// isAncestor reports whether ancestor contains descendant. Both are assumed
// cleaned and absolute.
func isAncestor(ancestor, descendant string) bool {
	if pathsEqual(ancestor, descendant) {
		return false
	}
	rel, err := filepath.Rel(ancestor, descendant)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	// A descendant's relative path never needs to climb out.
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// RootsOverlap reports whether two canonical roots are the same folder or one
// contains the other (FR-49).
//
// Exact match, ancestor, and descendant all count: two File Janitors managing
// nested folders would race to propose and act on the same files, and the
// action journal could no longer say which install owned an outcome.
func RootsOverlap(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	left := filepath.Clean(a)
	right := filepath.Clean(b)
	return pathsEqual(left, right) || isAncestor(left, right) || isAncestor(right, left)
}

func cleanGuard(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	// Resolve guards too: comparing a resolved selection against an unresolved
	// guard would let a symlinked data directory slip past.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

// pathsEqual compares two cleaned absolute paths.
//
// macOS and Windows default to case-insensitive filesystems, so treating
// /Users/me/Downloads and /Users/me/downloads as different folders would let a
// second workspace claim the same directory under a different spelling. Linux
// is case-sensitive, where folding would wrongly merge two real folders.
func pathsEqual(a, b string) bool {
	if caseInsensitiveFilesystem() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// conflictError reports a folder already managed by another workspace. The
// owning workspace is named only to the authorized local user, who is the only
// caller that ever reaches this (FR-49, FR-140).
func conflictError(owningWorkspaceName, owningWorkspaceID string) error {
	name := strings.TrimSpace(owningWorkspaceName)
	if name == "" {
		name = "Another workspace"
	}
	return &SetupError{
		Code: CodeFolderConflict,
		Message: fmt.Sprintf(
			"%s is already tidying that folder. Two workspaces cannot manage the same folder.", name),
		Repair:              RepairChooseFolder,
		ConflictWorkspaceID: strings.TrimSpace(owningWorkspaceID),
	}
}
