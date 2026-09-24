package skillshttp

// Update checks for marketplace skills. Ori does not ask the skills tool
// what changed: it downloads a fresh copy of each skill the same way an
// install does and compares digests. That works on any machine, because the
// digest to compare with travels in the skill's own source file.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// errSkillLocallyModified refuses an update that would replace the user's
// own edits, unless the request says to go ahead anyway.
var errSkillLocallyModified = errors.New("the skill was changed since it was installed")

// errNotAMarketplaceSkill refuses to update a folder with no source file.
var errNotAMarketplaceSkill = errors.New("this skill was not installed from the marketplace")

// renameSkillPath is os.Rename; tests replace it to make a step fail.
var renameSkillPath = os.Rename

// skillUpdateStatus is one skill's update check.
type skillUpdateStatus struct {
	Name            string `json:"name"`
	Package         string `json:"package"`
	UpdateAvailable bool   `json:"update_available"`
	LocallyModified bool   `json:"locally_modified"`
	Error           string `json:"error,omitempty"`
}

// checkSkillUpdates downloads a fresh copy of every marketplace skill and
// reports which have changed upstream and which the user has edited. Every
// download folder is deleted afterwards.
func (h *Handler) checkSkillUpdates(ctx context.Context) ([]skillUpdateStatus, error) {
	installed, err := h.listMarketplaceSkills()
	if err != nil {
		return nil, err
	}
	statuses := make([]skillUpdateStatus, 0, len(installed))
	for _, skill := range installed {
		if err := ctx.Err(); err != nil {
			return statuses, err
		}
		statuses = append(statuses, h.checkSkillUpdate(ctx, skill))
	}
	return statuses, nil
}

func (h *Handler) checkSkillUpdate(ctx context.Context, skill installedMarketplaceSkill) skillUpdateStatus {
	status := skillUpdateStatus{Name: skill.Name, Package: skill.Package}
	source, _, err := readSkillSource(skill.Path)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	if current, err := skillFolderDigest(skill.Path); err == nil {
		status.LocallyModified = current != source.TreeDigest
	}
	checkCtx, cancel := context.WithTimeout(ctx, marketplaceInstallTimeout)
	defer cancel()
	download, err := h.downloadSkill(checkCtx, source.Package)
	if err != nil {
		if errors.Is(err, errNodeRequired) {
			status.Error = nodeRequiredMessage
		} else {
			status.Error = "could not download the latest version"
		}
		return status
	}
	defer download.cleanup()
	fresh, err := skillFolderDigest(download.dir)
	if err != nil {
		status.Error = "could not read the latest version"
		return status
	}
	status.UpdateAvailable = fresh != source.TreeDigest
	return status
}

// updateSkillFolder replaces a marketplace skill with a fresh download. The
// new copy is staged next to the old folder and swapped in with two renames,
// so the skill is never half-written; if the swap fails, the old folder is
// put back. A skill the user edited is refused unless force is set.
func (h *Handler) updateSkillFolder(ctx context.Context, name string, force bool) (SkillSource, string, error) {
	if !isPlainSkillFolderName(name) {
		return SkillSource{}, "", errors.New("invalid skill name")
	}
	skillsDir, err := h.skillsFolder()
	if err != nil {
		return SkillSource{}, "", err
	}
	dir := filepath.Join(skillsDir, name)
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return SkillSource{}, "", os.ErrNotExist
	}
	source, found, err := readSkillSource(dir)
	if err != nil {
		return SkillSource{}, "", err
	}
	if !found {
		return SkillSource{}, "", errNotAMarketplaceSkill
	}
	if !force {
		current, err := skillFolderDigest(dir)
		if err != nil || current != source.TreeDigest {
			return SkillSource{}, "", errSkillLocallyModified
		}
	}

	download, err := h.downloadSkill(ctx, source.Package)
	if err != nil {
		return SkillSource{}, "", err
	}
	defer download.cleanup()

	staged := filepath.Join(skillsDir, ".ori-update-"+name)
	aside := filepath.Join(skillsDir, ".ori-replaced-"+name)
	for _, leftover := range []string{staged, aside} {
		if err := os.RemoveAll(leftover); err != nil {
			return SkillSource{}, download.output, err
		}
	}
	if err := moveSkillFolder(download.dir, staged); err != nil {
		return SkillSource{}, download.output, fmt.Errorf("stage the new version: %w", err)
	}
	digest, err := skillFolderDigest(staged)
	if err != nil {
		_ = os.RemoveAll(staged)
		return SkillSource{}, download.output, fmt.Errorf("read the new version: %w", err)
	}
	updated := SkillSource{
		Package: source.Package, InstalledAt: source.InstalledAt,
		UpdatedAt: time.Now().UTC(), TreeDigest: digest,
	}
	if err := writeSkillSource(staged, updated); err != nil {
		_ = os.RemoveAll(staged)
		return SkillSource{}, download.output, fmt.Errorf("record the new version: %w", err)
	}
	if err := renameSkillPath(dir, aside); err != nil {
		_ = os.RemoveAll(staged)
		return SkillSource{}, download.output, fmt.Errorf("set the old version aside: %w", err)
	}
	if err := renameSkillPath(staged, dir); err != nil {
		restoreErr := renameSkillPath(aside, dir)
		_ = os.RemoveAll(staged)
		if restoreErr != nil {
			return SkillSource{}, download.output, fmt.Errorf("put the new version in place: %w; the old version is at %s", err, aside)
		}
		return SkillSource{}, download.output, fmt.Errorf("put the new version in place: %w", err)
	}
	_ = os.RemoveAll(aside)
	updated.SchemaVersion = skillSourceSchemaVersion
	return updated, download.output, nil
}
