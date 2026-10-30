package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

// projectEntryPlan reads only an installed, active blueprint of the version the
// plan names. A not-yet-installed declaration is not guessed: the existing
// runtime choice remains the fallback after installation. No journey is opened.
func (h *folderSetupHost) projectEntryPlan(ctx context.Context, req personalassistant.FolderSetupRequest, target setupTarget, plan personalassistant.FolderSetupPlan) (personalassistant.FolderSetupPlan, error) {
	if req.Path == "" {
		return plan.Stamped(), nil
	}
	// An earlier explicit file pick is already a choice, even before the
	// blueprint is installed. Disclose it, without inventing other candidates.
	known := plan.Stamped()
	if personalassistant.ValidFolderEntryName(req.Offer.EntryName) {
		known = personalassistant.WithFolderSetupEntry(plan, req.Offer.EntryName, nil)
	}
	if h.builder == nil || h.builder.pluginHandler == nil || plan.Intent.Integration != personalassistant.FolderInstallReady {
		return known, nil
	}
	resolver := installedProjectTemplateResolver{manager: h.builder.pluginHandler.Manager()}
	template, err := resolver.ResolveProjectTemplate(ctx, setupjourney.ReadScope{
		OwnerUserID: req.UserID, IntegrationPluginID: target.entry.PluginID, IntegrationVersion: plan.Intent.IntegrationVersion,
		ExpectedBlueprintID: target.entry.ExpectedBlueprintID, ExpectedAssistantProgramID: target.entry.ExpectedProgramID,
	})
	if err != nil {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	if template.ProjectConnection == nil || template.ProjectConnection.AttachExisting == nil || len(template.ProjectConnection.AttachExisting.EntryExtensions) == 0 {
		return known, nil
	}
	return projectEntryFilePlan(plan, req.Path, req.Offer.EntryName, template.ProjectConnection.AttachExisting)
}

// projectEntryFilePlan adds one file consequence to the project's folder step.
// Names come only from the bounded, no-follow metadata scan, never browser paths.
func projectEntryFilePlan(plan personalassistant.FolderSetupPlan, root, preferred string, declaration *projecttemplates.AttachExistingDeclaration) (personalassistant.FolderSetupPlan, error) {
	before, err := os.Lstat(root) // #nosec G304 -- canonical subject path resolved from this owner's witnessed review
	if err != nil {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	scan, err := projectconnection.ScanExistingProject(root, declaration)
	if errors.Is(err, projectconnection.ErrNoProjectEntry) {
		after, statErr := os.Lstat(root) // #nosec G304 -- the same trusted review root must also survive an empty scan
		if statErr != nil || !os.SameFile(before, after) {
			return personalassistant.FolderSetupPlan{}, errSetupUnavailable
		}
		return personalassistant.WithFolderSetupEntry(plan, "", nil), nil
	}
	if err != nil {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	modified := make(map[string]time.Time, len(scan.Candidates))
	for _, name := range scan.Candidates {
		if !personalassistant.ValidFolderEntryName(name) {
			return personalassistant.FolderSetupPlan{}, errSetupUnavailable
		}
		info, statErr := os.Lstat(filepath.Join(root, name)) // #nosec G304 -- validated direct-child name from the bounded scan of the trusted review root
		if statErr != nil || !info.Mode().IsRegular() {
			return personalassistant.FolderSetupPlan{}, errSetupUnavailable
		}
		modified[name] = info.ModTime()
	}
	after, err := os.Lstat(root) // #nosec G304 -- same trusted root, compared again to refuse a replaced directory
	if err != nil || !os.SameFile(before, after) {
		return personalassistant.FolderSetupPlan{}, errSetupUnavailable
	}
	sort.Slice(scan.Candidates, func(i, j int) bool {
		a, b := scan.Candidates[i], scan.Candidates[j]
		if modified[a].Equal(modified[b]) {
			return a < b
		}
		return modified[a].After(modified[b])
	})
	chosen := scan.Candidates[0]
	for _, name := range scan.Candidates {
		if name == preferred {
			chosen = name
			break
		}
	}
	var candidates []string
	if len(scan.Candidates) > 1 {
		candidates = scan.Candidates
	}
	// In a handoff the file consequence remains after the separate Open boundary.
	return personalassistant.WithFolderSetupEntry(plan, chosen, candidates), nil
}
