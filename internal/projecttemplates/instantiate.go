package projecttemplates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/skills"
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

type templateTokenValues struct {
	projectSlug string
	date        string
}

func newTemplateTokenValues(projectSlug string) templateTokenValues {
	return templateTokenValues{
		projectSlug: projectSlug,
		date:        nowFunc().Format("2006-01-02"),
	}
}

func (values templateTokenValues) substitute(name string) string {
	name = strings.ReplaceAll(name, "{{name}}", values.projectSlug)
	name = strings.ReplaceAll(name, "{{date}}", values.date)
	return name
}

// InstantiationResult carries the fatal scaffold result separately from the
// optional project-entry warning. A project can be fully created even when its
// declared entry file cannot be verified.
type BundledSkillInstaller interface {
	BundledSkillMatches(agentName, skillName, text string) (bool, error)
	GetSkill(agentName, skillName string) (*skills.Skill, bool, error)
	InstallBundledSkill(agentName, skillName, text string, replace bool) error
}

// InstallBundledSkills installs non-colliding template skills into the entry
// agent as disabled and untrusted. Same-content skills are reused; different
// content stays untouched for the explicit collision review.
func InstallBundledSkills(installer BundledSkillInstaller, agentName string, bundled []BundledSkill) error {
	for _, candidate := range bundled {
		if matches, err := installer.BundledSkillMatches(agentName, candidate.Name, candidate.Text); err != nil {
			return err
		} else if matches {
			continue
		}
		if _, found, err := installer.GetSkill(agentName, candidate.Name); err != nil {
			return err
		} else if found {
			continue
		}
		if err := installer.InstallBundledSkill(agentName, candidate.Name, candidate.Text, false); err != nil {
			return err
		}
	}
	return nil
}

type InstantiationResult struct {
	ProjectPath      string
	ProjectEntryPath string
	ProjectWarning   string
}

// PreviewInstantiation returns the bounded portable file list that the same
// template/name substitution path will create. It reads names and file types
// only; it never reads template file contents or creates a destination.
func PreviewInstantiation(template Template, projectName string) ([]string, error) {
	slug, err := SanitizeProjectName(projectName)
	if err != nil || !template.HasSkeleton {
		return nil, ErrNoSkeleton
	}
	values := newTemplateTokenValues(slug)
	files := make([]string, 0)
	err = fs.WalkDir(os.DirFS(template.Path), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if relative == "." || relative == ManifestFileName {
			return nil
		}
		if (relative == DashboardDirName || relative == "skills") && entry.IsDir() {
			return fs.SkipDir
		}
		if entry.Type()&fs.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		if len(files) >= maxPluginBlueprintFiles {
			return errors.New("template preview exceeds file limit")
		}
		resolved, resolveErr := substituteRelPathWithValues(relative, values)
		if resolveErr != nil {
			return resolveErr
		}
		files = append(files, filepath.ToSlash(resolved))
		return nil
	})
	if err != nil || len(files) == 0 {
		return nil, ErrNoSkeleton
	}
	sort.Strings(files)
	return files, nil
}

// Instantiate copies the template at templatePath into a new project folder
// inside workspaceFolder and returns the project folder's path relative to
// the workspace folder. The copy substitutes {{name}} and {{date}} in file
// and folder names only; contents are byte-copied unmodified. Symlinks are
// skipped, the root template.json is excluded, and any failure removes the
// partially created project folder.
func Instantiate(templatePath, workspaceFolder, projectName string) (string, error) {
	result, err := instantiateTemplate(templatePath, workspaceFolder, projectName, nil, nil)
	return result.ProjectPath, err
}

// InstantiateTemplate copies a normalized Template and resolves its optional
// project entry with the exact same token values used for scaffold filenames.
// A blueprint that declares inputs is scaffolded with its declared defaults.
func InstantiateTemplate(tpl Template, workspaceFolder, projectName string) (InstantiationResult, error) {
	return InstantiateTemplateWithInputs(tpl, workspaceFolder, projectName, nil)
}

// InstantiateTemplateWithInputs is InstantiateTemplate plus the values the user
// chose for the blueprint's declared inputs, still in the raw shape the caller
// received them. They are validated here against the declaration — the same
// check the create endpoint already ran — so an unchecked value can never
// reach a file, and a caller that supplies nothing gets the declared defaults.
func InstantiateTemplateWithInputs(tpl Template, workspaceFolder, projectName string, provided map[string]json.RawMessage) (InstantiationResult, error) {
	substitution, err := planInputSubstitution(tpl, provided)
	if err != nil {
		return InstantiationResult{}, err
	}
	return instantiateTemplate(tpl.Path, workspaceFolder, projectName, tpl.ProjectEntry, substitution)
}

// inputSubstitution is the resolved, bounded rewrite plan: exactly which source
// files may be rewritten, and exactly what each declared token becomes. Nothing
// outside this plan is ever anything but a byte copy.
type inputSubstitution struct {
	files  map[string]struct{}
	values map[string]string
}

func planInputSubstitution(tpl Template, provided map[string]json.RawMessage) (*inputSubstitution, error) {
	if tpl.HasInvalidInputs() {
		return nil, fmt.Errorf("%w: %s", ErrInvalidInputs, tpl.InputsError)
	}
	if tpl.Inputs == nil {
		if len(provided) > 0 {
			return nil, fmt.Errorf("%w: this blueprint does not ask for any inputs", ErrInputValue)
		}
		return nil, nil
	}
	values, err := ResolveInputValues(tpl.Inputs, provided)
	if err != nil {
		return nil, err
	}
	files := make(map[string]struct{}, len(tpl.Inputs.ApplyTo))
	for _, relative := range tpl.Inputs.ApplyTo {
		files[relative] = struct{}{}
	}
	return &inputSubstitution{files: files, values: values}, nil
}

func (substitution *inputSubstitution) appliesTo(relPath string) bool {
	if substitution == nil {
		return false
	}
	_, ok := substitution.files[relPath]
	return ok
}

// apply replaces each declared token with its resolved value. Order does not
// matter: the tokens are distinct and no resolved value can contain one,
// because numbers are digits and option values are plain portable text.
func (substitution *inputSubstitution) apply(content string) string {
	for id, value := range substitution.values {
		content = strings.ReplaceAll(content, inputTokenPrefix+id+"}}", value)
	}
	return content
}

func instantiateTemplate(templatePath, workspaceFolder, projectName string, entry *ProjectEntry, substitution *inputSubstitution) (InstantiationResult, error) {
	slug, err := SanitizeProjectName(projectName)
	if err != nil {
		return InstantiationResult{}, err
	}
	values := newTemplateTokenValues(slug)

	srcInfo, err := os.Stat(templatePath)
	if err != nil || !srcInfo.IsDir() {
		return InstantiationResult{}, fmt.Errorf("%w: %q is not a folder", ErrTemplateNotFound, templatePath)
	}
	if !hasSkeletonFiles(templatePath) {
		return InstantiationResult{}, fmt.Errorf("%w: %q", ErrNoSkeleton, templatePath)
	}
	wsInfo, err := os.Stat(workspaceFolder)
	if err != nil || !wsInfo.IsDir() {
		return InstantiationResult{}, fmt.Errorf("workspace folder %q is not accessible", workspaceFolder)
	}

	destRoot := filepath.Join(workspaceFolder, slug)
	if _, err := os.Lstat(destRoot); err == nil {
		return InstantiationResult{}, fmt.Errorf("%w: %q already exists in the workspace folder", ErrProjectExists, slug)
	} else if !os.IsNotExist(err) {
		return InstantiationResult{}, fmt.Errorf("failed to inspect project folder %q: %w", destRoot, err)
	}

	if err := copyTemplateTree(templatePath, destRoot, values, srcInfo.Mode().Perm(), substitution); err != nil {
		// Best-effort cleanup so a failed instantiation leaves no partial
		// project folder behind (and the caller never persists ProjectPath).
		_ = os.RemoveAll(destRoot)
		return InstantiationResult{}, err
	}

	result := InstantiationResult{ProjectPath: slug}
	if entry != nil {
		entryPath, err := resolveInstantiatedProjectEntry(destRoot, entry, values)
		if err != nil {
			result.ProjectWarning = fmt.Sprintf("project was created, but its entry file is unavailable: %v", err)
		} else {
			result.ProjectEntryPath = entryPath
		}
	}
	return result, nil
}

// copyTemplateTree walks the template and materializes it under destRoot.
func copyTemplateTree(templatePath, destRoot string, values templateTokenValues, rootPerm fs.FileMode, substitution *inputSubstitution) error {
	if err := os.MkdirAll(destRoot, normalizeDirPerm(rootPerm)); err != nil {
		return fmt.Errorf("failed to create project folder: %w", err)
	}

	// Track produced paths: distinct template entries could substitute to the
	// same destination (e.g. "{{name}}.txt" next to a literal "my-song.txt"),
	// which must fail rather than silently overwrite.
	produced := map[string]struct{}{}
	// Track the declared apply_to files actually seen. A blueprint that lists a
	// file the scaffold no longer has must fail rather than create a project
	// whose values were quietly never written.
	substituted := map[string]struct{}{}

	if err := fs.WalkDir(os.DirFS(templatePath), ".", func(relPath string, d fs.DirEntry, err error) error {
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
		// The dashboard is installed into the workspace folder's .ori sidecar
		// by InstallDashboard. Copying it here as well would put a second,
		// unreachable copy inside the project folder.
		if (relPath == DashboardDirName || relPath == "skills") && d.IsDir() {
			return fs.SkipDir
		}
		// Symlinks are skipped in v1: copying the pointer would smuggle in
		// machine-local absolute paths (breaking portability), and following
		// it could pull arbitrary files from outside the template.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		destRel, err := substituteRelPathWithValues(relPath, values)
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
		if substitution.appliesTo(relPath) {
			substituted[relPath] = struct{}{}
			return copyFileWithInputs(filepath.Join(templatePath, relPath), destPath, info.Mode().Perm(), substitution)
		}
		return copyFile(filepath.Join(templatePath, relPath), destPath, info.Mode().Perm())
	}); err != nil {
		return err
	}
	if substitution != nil {
		for relPath := range substitution.files {
			if _, seen := substituted[relPath]; !seen {
				return fmt.Errorf("%w: apply_to path %q is no longer part of the scaffold", ErrInvalidInputs, relPath)
			}
		}
	}
	return nil
}

// substituteRelPath applies token substitution to every segment of a
// slash-separated fs.WalkDir path and re-validates the result so a template
// cannot smuggle in traversal segments.
func substituteRelPath(relPath, slug string) (string, error) {
	return substituteRelPathWithValues(relPath, newTemplateTokenValues(slug))
}

func substituteRelPathWithValues(relPath string, values templateTokenValues) (string, error) {
	segments := strings.Split(relPath, "/")
	for i, segment := range segments {
		substituted := values.substitute(segment)
		if strings.Contains(substituted, "{{fields.") {
			return "", fmt.Errorf("template entry %q references an unknown field token", relPath)
		}
		// Names resolve before any chosen value is known and go straight into
		// filesystem paths, so they stay limited to {{name}} and {{date}}.
		if strings.Contains(substituted, inputTokenPrefix) {
			return "", fmt.Errorf("template entry %q uses an input token in its name", relPath)
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

// copyFileWithInputs writes the one kind of file that is not a byte copy: a
// file the blueprint listed in apply_to. The size and text checks that ran at
// load run again here, because the scaffold could have changed since, and the
// result is refused if any token survived — a project carrying the author's
// raw tokens is worse than a failed instantiation.
func copyFileWithInputs(src, dst string, perm fs.FileMode, substitution *inputSubstitution) error {
	info, err := os.Lstat(src)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("failed to read template file %q", src)
	}
	if info.Size() > maxInputApplyToFileBytes {
		return fmt.Errorf("template file %q is larger than the substitution limit", src)
	}
	data, err := os.ReadFile(src) // #nosec G304 -- src is a template entry path validated by the caller's traversal/collision guards
	if err != nil {
		return fmt.Errorf("failed to open template file %q: %w", src, err)
	}
	if isBinary(data) {
		return fmt.Errorf("template file %q is not a text file", src)
	}
	content := substitution.apply(string(data))
	if strings.Contains(content, inputTokenPrefix) {
		return fmt.Errorf("template file %q still contains an unresolved input token", src)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, (perm|0o600)&fs.ModePerm) // #nosec G304 -- dst is constructed under destRoot with traversal/collision guards applied by the caller
	if err != nil {
		return fmt.Errorf("failed to create project file %q: %w", dst, err)
	}
	if _, err := io.WriteString(out, content); err != nil {
		_ = out.Close()
		return fmt.Errorf("failed to write project file %q: %w", dst, err)
	}
	return out.Close()
}
