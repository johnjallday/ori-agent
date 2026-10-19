package filejanitor

import (
	"os"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SelectedSetupReview is the read-only owner display for a server-held build
// selection. Only the host's held-selection resolver may supply Path; this is
// not a public raw-path grant endpoint. Revision contains no reusable path.
type SelectedSetupReview struct {
	Path     string           `json:"path"`
	Revision string           `json:"root_revision"`
	Terms    SetupReviewTerms `json:"terms"`
}

func (s *Service) ReviewSelectedSetup(workspaceID, path string, recipes []workspace.AutomationRecipe) (SelectedSetupReview, error) {
	root, err := resolveSetupRoot(path)
	if err != nil {
		return SelectedSetupReview{}, err
	}
	info, err := os.Stat(root) // #nosec G304 -- canonical server-held selection; read-only identity check
	if err != nil || platformFileID(info) == "" {
		return SelectedSetupReview{}, setupErr(CodeFolderChanged, "The selected folder is unavailable. Choose it again.", RepairChooseFolder, nil)
	}
	if err := s.ensureRootAvailable(workspaceID, root); err != nil {
		return SelectedSetupReview{}, err
	}
	var recipe workspace.AutomationRecipe
	for _, candidate := range recipes {
		if matchesDirectoryRequirementKey(candidate.DirectoryKey) {
			recipe = candidate
			break
		}
	}
	settings := NewSettings("")
	automation := reviewAutomationSettings(settings, recipe)
	revision, err := setupDigest([]string{root, platformFileID(info)})
	if err != nil {
		return SelectedSetupReview{}, err
	}
	return SelectedSetupReview{Path: root, Revision: revision, Terms: setupReviewTerms(automation)}, nil
}

func setupReviewTerms(automation AutomationReview) SetupReviewTerms {
	return SetupReviewTerms{
		PrivacyMode: ContentModeMetadataOnly, FilingRootName: DefaultFilingRootName,
		WatchEvents: automation.WatchEvents, DebounceSeconds: automation.WatchDebounceSecond,
		SettlingSeconds: automation.SettlingSecond, DailyScanLocalTime: automation.DailyScanLocalTime,
		Timezone: automation.Timezone, ExcludesFiled: true,
	}
}
