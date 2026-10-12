// Package folderpath checks a folder path a person typed or pasted, as opposed
// to one chosen in the native folder dialog.
//
// The dialog is a gesture the page cannot make: whatever comes back was chosen
// by a person. A typed path arrives from the page, so the server alone decides
// whether it may stand in for a dialog choice. Check is that decision, shared by
// every surface that accepts a typed path.
package folderpath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

// WarningOutsideHome marks a folder that is real and allowed, but not under the
// user's home folder. The surface says so before the user confirms.
const WarningOutsideHome = "outside_home"

// WarningNote is the sentence a surface shows for a warning code; "" for none.
func WarningNote(warning string) string {
	if warning == WarningOutsideHome {
		return "This folder is outside your home folder. Check that it is the one you mean."
	}
	return ""
}

// Error is a refusal whose Message is safe to show the user as it is.
type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(message string) error { return &Error{Message: message} }

// Message returns the sentence to show for err: a refusal's own message, or a
// plain fallback for anything else.
func Message(err error) string {
	var refusal *Error
	if errors.As(err, &refusal) && refusal.Message != "" {
		return refusal.Message
	}
	return "Ori could not open that folder. Check the path and try again."
}

// Checked is a typed path that passed.
type Checked struct {
	// Path is the canonical, symlink-resolved absolute path.
	Path string
	// Name is the folder's base name, for a card title or a link's name.
	Name string
	// Warning is "" or WarningOutsideHome.
	Warning string
}

// credentialFolders are the folders under a home whose whole purpose is to hold
// credentials. A typed path may not be one, sit inside one, or contain one.
var credentialFolders = []string{
	".ssh",
	".gnupg",
	".aws",
	".azure",
	".kube",
	".docker",
	".password-store",
	filepath.Join(".config", "gcloud"),
	filepath.Join(".config", "gh"),
	filepath.Join("Library", "Keychains"),
}

// Check validates a typed or pasted folder path against guards.
//
// It expands a leading "~", requires an absolute path to an existing directory,
// and applies File Janitor's root rules (not a whole drive, not the home folder
// itself, not a folder that holds Ori's data or workspaces). On top of those it
// refuses anything inside Ori's data directory, where the database and vaults
// live (unless the data directory is the home folder itself, as in a sandbox),
// and anything that overlaps a credential folder. A folder outside the home
// folder is allowed and carries WarningOutsideHome.
func Check(raw string, guards filejanitor.RootGuards) (Checked, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return Checked{}, refuse("Type or paste a folder path first.")
	}
	if strings.ContainsRune(path, 0) {
		return Checked{}, refuse("That folder path is not valid.")
	}
	home := strings.TrimSpace(guards.HomeDir)
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home == "" {
			return Checked{}, refuse("Ori could not locate your home folder. Use the full path instead.")
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	if !filepath.IsAbs(path) {
		return Checked{}, refuse("Use the full path to the folder, starting with / or ~/.")
	}

	root, err := filejanitor.ValidateRoot(path, guards)
	if err != nil {
		var setupErr *filejanitor.SetupError
		if errors.As(err, &setupErr) && setupErr.Message != "" {
			return Checked{}, refuse(setupErr.Message)
		}
		return Checked{}, refuse("Ori could not open that folder. Check the path and try again.")
	}

	resolvedHome := canonical(home)
	// ValidateRoot refuses the data directory and any folder that contains it.
	// A folder inside it is the database and the vaults themselves. That holds
	// for a data directory of its own. A sandboxed server keeps its data in the
	// home folder itself, where the same rule would refuse every folder the
	// user has, so there ValidateRoot's rule is the whole of it.
	if data := canonical(guards.DataDir); data != "" && filejanitor.RootsOverlap(root, data) {
		if resolvedHome == "" || !within(data, resolvedHome) {
			return Checked{}, refuse("That folder holds Ori's own data. Choose a different folder.")
		}
	}
	if resolvedHome != "" {
		for _, name := range credentialFolders {
			if filejanitor.RootsOverlap(root, canonical(filepath.Join(resolvedHome, name))) {
				return Checked{}, refuse("That folder holds passwords or keys. Choose a different folder.")
			}
		}
	}

	checked := Checked{Path: root, Name: filepath.Base(root)}
	if resolvedHome == "" || !within(resolvedHome, root) {
		checked.Warning = WarningOutsideHome
	}
	return checked, nil
}

// canonical resolves a guard the way ValidateRoot resolves a selection, so the
// two compare as the same real directory. A guard that does not exist yet is
// compared by its cleaned absolute spelling.
func canonical(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	// The folder itself may be missing while its parent is a symlink (macOS
	// temp and home volumes are): resolve the nearest existing ancestor.
	parent, base := filepath.Dir(abs), filepath.Base(abs)
	if parent != abs {
		if _, err := os.Lstat(parent); err == nil {
			return filepath.Join(canonical(parent), base)
		}
	}
	return filepath.Clean(abs)
}

// within reports whether child is parent or lies inside it. Both are canonical.
func within(parent, child string) bool {
	// macOS and Windows fold case by default; File Janitor's rules assume the
	// same, and the permissive direction here only ever adds a warning.
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
