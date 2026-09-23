package skillshttp

// Marketplace skills live in the Workspace Directory's Skills folder. Ori
// still uses the external `skills` tool (npx skills) to download them, but
// never with -g: the tool runs in a new empty folder and Ori moves the result
// into place, so nothing is written to ~/.agents or ~/.config/agents.
//
// Layout the tool leaves behind, confirmed with skills 1.7.0 running
// `npx --yes skills add vercel-labs/skills@find-skills -y --agent universal
// --copy` in an empty folder:
//
//	<folder>/.agents/skills/find-skills/SKILL.md
//	<folder>/skills-lock.json   {"version":1,"skills":{"find-skills":{"source":
//	                             "vercel-labs/skills","sourceType":"github",...}}}
//
// Nothing is written under HOME. The whole folder, lock file included, is
// deleted after the skill folder has been moved out of it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/platform"
)

// errNodeRequired is returned when npx cannot be found: the marketplace needs
// Node.js, which provides it. nodeRequiredMessage is what the user is told.
var errNodeRequired = errors.New("npx not found")

const nodeRequiredMessage = "Node.js is required to use the skills marketplace. Install Node.js (it includes npx) and try again."

// skillNameTakenError refuses a skill whose folder name is already in use.
type skillNameTakenError struct{ Name string }

func (e *skillNameTakenError) Error() string {
	return fmt.Sprintf("A skill named %q is already in your Skills folder.", e.Name)
}

// skillDownloadError is a failed `skills add`, with the tool's output.
type skillDownloadError struct {
	Output string
	Err    error
}

func (e *skillDownloadError) Error() string { return "failed to download the skill package" }
func (e *skillDownloadError) Unwrap() error { return e.Err }

// Replaceable in tests: the system Trash.
var (
	trashSupported = platform.TrashSupported
	moveToTrash    = platform.MoveToTrash
)

// skillDownload is a package the skills tool fetched into a temporary folder.
type skillDownload struct {
	root   string // the temporary folder; cleanup removes it
	name   string // the downloaded skill's folder name
	dir    string // <root>/.agents/skills/<name>
	output string
}

func (d *skillDownload) cleanup() {
	if d != nil && d.root != "" {
		_ = os.RemoveAll(d.root)
	}
}

// skillDownloadsDir is where packages are downloaded before they are moved
// into the Skills folder. It is in the data dir, never the Workspace
// Directory, so a sync service never sees a half-finished download.
func skillDownloadsDir() string {
	return filepath.Join(config.DefaultDataDir(), "skill-downloads")
}

// downloadSkill runs `skills add <package>` without -g in a new empty folder
// and finds the one skill folder it produced. The caller must call cleanup,
// on every path, once it has taken what it needs.
func (h *Handler) downloadSkill(ctx context.Context, packageSpec string) (*skillDownload, error) {
	if err := os.MkdirAll(skillDownloadsDir(), 0o750); err != nil {
		return nil, fmt.Errorf("prepare the download folder: %w", err)
	}
	root, err := os.MkdirTemp(skillDownloadsDir(), "download-")
	if err != nil {
		return nil, fmt.Errorf("prepare the download folder: %w", err)
	}
	download := &skillDownload{root: root}
	output, err := h.runSkillsCLIInDir(ctx, root, "add", packageSpec, "-y", "--agent", "universal", "--copy")
	download.output = output
	if err != nil {
		download.cleanup()
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errNodeRequired
		}
		return nil, &skillDownloadError{Output: output, Err: err}
	}
	downloaded := filepath.Join(root, ".agents", "skills")
	entries, err := os.ReadDir(downloaded)
	if err != nil {
		download.cleanup()
		return nil, &skillDownloadError{Output: output, Err: fmt.Errorf("the skills tool left no skill folder: %w", err)}
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(downloaded, entry.Name(), "SKILL.md")); err == nil {
			names = append(names, entry.Name())
		}
	}
	if len(names) != 1 {
		download.cleanup()
		return nil, &skillDownloadError{Output: output, Err: fmt.Errorf("expected one downloaded skill, found %d", len(names))}
	}
	download.name = names[0]
	download.dir = filepath.Join(downloaded, names[0])
	return download, nil
}

// installedMarketplaceSkill is one skill in the Skills folder with a source
// file.
type installedMarketplaceSkill struct {
	Name        string    `json:"name"`
	Package     string    `json:"package"`
	Location    string    `json:"location"`
	Path        string    `json:"path"`
	InstalledAt time.Time `json:"installed_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// skillsFolder returns the Skills folder of the current Workspace Directory.
func (h *Handler) skillsFolder() (string, error) {
	if h.manager == nil {
		return "", errors.New("skills are unavailable")
	}
	dir := h.manager.PersonalSkillsDir()
	if dir == "" {
		return "", errors.New("the Skills folder is not configured")
	}
	return dir, nil
}

// skillLocation is how a skill folder is shown: "Ori Workspaces/Skills/<name>".
func skillLocation(skillsDir, name string) string {
	return filepath.Join(filepath.Base(filepath.Dir(skillsDir)), filepath.Base(skillsDir), name)
}

// installSkillPackage downloads a package and moves its skill into the Skills
// folder with a source file. A folder of that name already there is refused
// and left untouched.
func (h *Handler) installSkillPackage(ctx context.Context, packageSpec string) (installedMarketplaceSkill, string, error) {
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return installedMarketplaceSkill{}, "", err
	}
	download, err := h.downloadSkill(ctx, packageSpec)
	if err != nil {
		return installedMarketplaceSkill{}, "", err
	}
	defer download.cleanup()

	destination := filepath.Join(skillsDir, download.name)
	if _, err := os.Lstat(destination); err == nil {
		return installedMarketplaceSkill{}, download.output, &skillNameTakenError{Name: download.name}
	}
	digest, err := skillFolderDigest(download.dir)
	if err != nil {
		return installedMarketplaceSkill{}, download.output, fmt.Errorf("read the downloaded skill: %w", err)
	}
	now := time.Now().UTC()
	source := SkillSource{Package: packageSpec, InstalledAt: now, UpdatedAt: now, TreeDigest: digest}
	if err := writeSkillSource(download.dir, source); err != nil {
		return installedMarketplaceSkill{}, download.output, fmt.Errorf("record the skill's source: %w", err)
	}
	if err := os.MkdirAll(skillsDir, 0o750); err != nil {
		return installedMarketplaceSkill{}, download.output, fmt.Errorf("prepare the Skills folder: %w", err)
	}
	if err := moveSkillFolder(download.dir, destination); err != nil {
		return installedMarketplaceSkill{}, download.output, fmt.Errorf("move the skill into the Skills folder: %w", err)
	}
	return installedMarketplaceSkill{
		Name: download.name, Package: packageSpec, Location: skillLocation(skillsDir, download.name),
		Path: destination, InstalledAt: now, UpdatedAt: now,
	}, download.output, nil
}

// listMarketplaceSkills lists the skills in the Skills folder that have a
// source file, which is every skill installed from the marketplace.
func (h *Handler) listMarketplaceSkills() ([]installedMarketplaceSkill, error) {
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(skillsDir)
	if errors.Is(err, os.ErrNotExist) {
		return []installedMarketplaceSkill{}, nil
	}
	if err != nil {
		return nil, err
	}
	installed := []installedMarketplaceSkill{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(skillsDir, entry.Name())
		source, found, err := readSkillSource(dir)
		if err != nil || !found {
			continue
		}
		installed = append(installed, installedMarketplaceSkill{
			Name: entry.Name(), Package: source.Package, Location: skillLocation(skillsDir, entry.Name()),
			Path: dir, InstalledAt: source.InstalledAt, UpdatedAt: source.UpdatedAt,
		})
		if len(installed) >= marketplaceMaxInstalled {
			break
		}
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Name < installed[j].Name })
	return installed, nil
}

// removeSkillFolder moves one skill folder of the Skills folder to the system
// Trash. It never deletes permanently: without a Trash it refuses.
func (h *Handler) removeSkillFolder(name string) (string, error) {
	if !isPlainSkillFolderName(name) {
		return "", errors.New("invalid skill name")
	}
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(skillsDir, name)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("not a skill folder")
	}
	if !trashSupported() {
		return "", errors.New("this system has no Trash, so the skill was not removed; delete its folder yourself if you want it gone")
	}
	return moveToTrash(dir)
}

// isPlainSkillFolderName accepts only a single visible folder name: no path,
// no "..", nothing that could name a location outside the Skills folder.
func isPlainSkillFolderName(name string) bool {
	return name != "" && isValidMarketplaceSkillName(name) && !strings.HasPrefix(name, ".") &&
		filepath.Base(name) == name && filepath.IsLocal(name)
}

// moveSkillFolder moves a folder into place, copying when the Skills folder
// is on another volume than the data dir.
func moveSkillFolder(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copySkillTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

// copySkillTree copies a plain tree of files and folders; symlinks and other
// special files are refused.
func copySkillTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o750)
		case info.Mode().IsRegular():
			return copySkillFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("%s is not a plain file", rel)
		}
	})
}

func copySkillFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) // #nosec G304 -- a file inside a skill folder being copied into the Skills folder
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode) // #nosec G304 -- a new file inside the destination skill folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
