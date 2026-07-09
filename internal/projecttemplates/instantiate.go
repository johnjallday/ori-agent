package projecttemplates

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	// ErrTemplateNotFound reports an unknown template ID or unusable folder.
	ErrTemplateNotFound = errors.New("template not found")
	// ErrProjectExists reports that the workspace already has a project or the
	// target folder is already taken.
	ErrProjectExists = errors.New("workspace already has a project")
	// ErrGroupWorkspace reports that the target workspace is a group, which
	// cannot hold a project in v1 (group MCP roots are scoped to files/ and
	// notes/, so a sibling project folder would be invisible to group agents).
	ErrGroupWorkspace = errors.New("group workspaces cannot have a project")
	// ErrReservedName reports a project name that collides with a reserved
	// workspace entry.
	ErrReservedName = errors.New("project name is reserved")
	// ErrNoSkeleton reports an attempt to instantiate a metadata-only template
	// (one with no files beyond template.json). The create flow branches on
	// Template.HasSkeleton and skips instantiation for these, so reaching here
	// is a caller error rather than a normal path.
	ErrNoSkeleton = errors.New("template has no skeleton to instantiate")
)

// reservedProjectNames are workspace-folder entries a project folder must
// never shadow. Slugified names are lowercase alphanumerics and hyphens, so
// dotted names like workspace.json cannot collide, but they are listed for
// completeness alongside every directory the workspace layout owns.
var reservedProjectNames = map[string]struct{}{
	workspace.WorkspaceConfigFile: {},
	workspace.FilesDir:            {},
	workspace.NotesDir:            {},
	workspace.OutputsDir:          {},
	workspace.WorkspaceAgentsDir:  {},
	workspace.SubWorkspacesDir:    {},
	"tasks":                       {},
	"sessions":                    {},
}

// SanitizeProjectName converts a user-supplied project name into the
// filesystem-safe folder name used for the project, reusing the workspace
// slug rules so projects follow the same conventions as workspace folders.
func SanitizeProjectName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("project name is required")
	}
	slug := workspace.Slugify(name)
	if _, reserved := reservedProjectNames[slug]; reserved {
		return "", fmt.Errorf("%w: %q", ErrReservedName, slug)
	}
	return slug, nil
}

// ValidateTarget is the shared instantiation guard for every entry point:
// groups cannot hold projects in v1, and a workspace holds at most one.
// The returned errors are user-relayable.
func ValidateTarget(isGroup bool, existingProjectPath string) error {
	if isGroup {
		return ErrGroupWorkspace
	}
	if strings.TrimSpace(existingProjectPath) != "" {
		return fmt.Errorf("%w (project_path %q)", ErrProjectExists, strings.TrimSpace(existingProjectPath))
	}
	return nil
}

// nowFunc is stubbed in tests to pin {{date}} substitution.
var nowFunc = time.Now

// substituteTokens replaces the supported tokens in a path name. Replacement
// values are slug/date strings (lowercase alphanumerics and hyphens), so
// substitution can never introduce path separators.
func substituteTokens(name, projectSlug string) string {
	name = strings.ReplaceAll(name, "{{name}}", projectSlug)
	name = strings.ReplaceAll(name, "{{date}}", nowFunc().Format("2006-01-02"))
	return name
}

// Instantiate copies the template at templatePath into a new project folder
// inside workspaceFolder and returns the project folder's path relative to
// the workspace folder. The copy substitutes {{name}} and {{date}} in file
// and folder names only; contents are byte-copied unmodified. Symlinks are
// skipped, the root template.json is excluded, and any failure removes the
// partially created project folder.
func Instantiate(templatePath, workspaceFolder, projectName string) (string, error) {
	slug, err := SanitizeProjectName(projectName)
	if err != nil {
		return "", err
	}

	srcInfo, err := os.Stat(templatePath)
	if err != nil || !srcInfo.IsDir() {
		return "", fmt.Errorf("%w: %q is not a folder", ErrTemplateNotFound, templatePath)
	}
	if !hasSkeletonFiles(templatePath) {
		return "", fmt.Errorf("%w: %q", ErrNoSkeleton, templatePath)
	}
	wsInfo, err := os.Stat(workspaceFolder)
	if err != nil || !wsInfo.IsDir() {
		return "", fmt.Errorf("workspace folder %q is not accessible", workspaceFolder)
	}

	destRoot := filepath.Join(workspaceFolder, slug)
	if _, err := os.Lstat(destRoot); err == nil {
		return "", fmt.Errorf("%w: %q already exists in the workspace folder", ErrProjectExists, slug)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to inspect project folder %q: %w", destRoot, err)
	}

	if err := copyTemplateTree(templatePath, destRoot, slug, srcInfo.Mode().Perm()); err != nil {
		// Best-effort cleanup so a failed instantiation leaves no partial
		// project folder behind (and the caller never persists ProjectPath).
		_ = os.RemoveAll(destRoot)
		return "", err
	}

	return slug, nil
}

// copyTemplateTree walks the template and materializes it under destRoot.
func copyTemplateTree(templatePath, destRoot, slug string, rootPerm fs.FileMode) error {
	if err := os.MkdirAll(destRoot, normalizeDirPerm(rootPerm)); err != nil {
		return fmt.Errorf("failed to create project folder: %w", err)
	}

	// Track produced paths: distinct template entries could substitute to the
	// same destination (e.g. "{{name}}.txt" next to a literal "my-song.txt"),
	// which must fail rather than silently overwrite.
	produced := map[string]struct{}{}

	return fs.WalkDir(os.DirFS(templatePath), ".", func(relPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to read template entry %q: %w", relPath, err)
		}
		if relPath == "." {
			return nil
		}
		// The manifest is metadata for the picker, not project content.
		if relPath == ManifestFileName {
			return nil
		}
		// Symlinks are skipped in v1: copying the pointer would smuggle in
		// machine-local absolute paths (breaking portability), and following
		// it could pull arbitrary files from outside the template.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		destRel, err := substituteRelPath(relPath, slug)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destRoot, destRel)
		if !strings.HasPrefix(destPath, destRoot+string(filepath.Separator)) {
			return fmt.Errorf("template entry %q escapes the project folder", relPath)
		}
		key := strings.ToLower(destPath)
		if _, dup := produced[key]; dup {
			return fmt.Errorf("template entries collide at %q after name substitution", destRel)
		}
		produced[key] = struct{}{}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to inspect template entry %q: %w", relPath, err)
		}

		if d.IsDir() {
			if err := os.Mkdir(destPath, normalizeDirPerm(info.Mode().Perm())); err != nil {
				return fmt.Errorf("failed to create folder %q: %w", destRel, err)
			}
			return nil
		}
		return copyFile(filepath.Join(templatePath, relPath), destPath, info.Mode().Perm())
	})
}

// substituteRelPath applies token substitution to every segment of a
// slash-separated fs.WalkDir path and re-validates the result so a template
// cannot smuggle in traversal segments.
func substituteRelPath(relPath, slug string) (string, error) {
	segments := strings.Split(relPath, "/")
	for i, segment := range segments {
		substituted := substituteTokens(segment, slug)
		if strings.Contains(substituted, "{{fields.") {
			return "", fmt.Errorf("template entry %q references an unknown field token", relPath)
		}
		if substituted == "" || substituted == "." || substituted == ".." ||
			strings.ContainsAny(substituted, `/\`) {
			return "", fmt.Errorf("template entry %q has an invalid name after substitution", relPath)
		}
		segments[i] = substituted
	}
	joined := filepath.Join(segments...)
	if !filepath.IsLocal(joined) {
		return "", fmt.Errorf("template entry %q escapes the project folder", relPath)
	}
	return joined, nil
}

// normalizeDirPerm keeps template-provided directory modes but guarantees the
// owner can always traverse and write what was just created.
func normalizeDirPerm(perm fs.FileMode) fs.FileMode {
	return (perm | 0o700) & fs.ModePerm
}

// copyFile byte-copies src to dst preserving the source permission bits
// (owner read/write forced so the copy is always manageable). src and dst are
// not user-supplied directly: callers (copyTemplateTree, copyFolderVerbatim)
// derive them from a template directory that was already validated to be a
// folder, with traversal/collision guards applied to every destination path.
func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src) // #nosec G304 -- src is a template entry path validated by the caller's traversal/collision guards
	if err != nil {
		return fmt.Errorf("failed to open template file %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, (perm|0o600)&fs.ModePerm) // #nosec G304 -- dst is constructed under destRoot with traversal/collision guards applied by the caller
	if err != nil {
		return fmt.Errorf("failed to create project file %q: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("failed to copy template file %q: %w", src, err)
	}
	return out.Close()
}
