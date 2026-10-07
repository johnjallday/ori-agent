package homeprofile

import (
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Detect looks for installed applications and records what it finds as hints.
// It runs only because the owner pressed Detect: never on a read. Rows the
// owner confirmed or hid survive; a main application the owner chose or
// confirmed is never replaced.
func (s *Service) Detect(ownerID, homeID, requestID string) (View, error) {
	// Both lookups happen before the Home write: the library read may wait on
	// another write, and neither belongs inside a store callback.
	found := s.installedApps()
	var formats map[string]int
	if home, err := s.lookup(ownerID, homeID); err == nil {
		formats = s.libraryFormats(home)
	}
	return s.write(ownerID, homeID, requestID, actionDetect, nil,
		func(_ *workspace.Workspace, profile *workspace.HomeProfile, now time.Time) error {
			applyDetection(profile, found, now)
			applyMainAppRule(profile, formats)
			return nil
		})
}

func (s *Service) installedApps() []folderdigest.InstalledApp {
	if s.deps.InstalledApps == nil {
		return nil
	}
	return s.deps.InstalledApps()
}

func (s *Service) libraryFormats(home *workspace.Workspace) map[string]int {
	if s.deps.LibraryFormats == nil {
		return nil
	}
	return s.deps.LibraryFormats(home)
}

// lookup returns the owner's Home for a read made ahead of a write. The write
// itself checks ownership again inside the store.
func (s *Service) lookup(ownerID, homeID string) (*workspace.Workspace, error) {
	if s == nil || s.deps.Workspaces == nil {
		return nil, ErrNotFound
	}
	homeID = strings.TrimSpace(homeID)
	if homeID == "" {
		return nil, ErrNotFound
	}
	home, err := s.deps.Workspaces.Get(homeID)
	if err != nil || home == nil || home.ID != homeID {
		return nil, ErrNotFound
	}
	if _, ok := ownedHome(ownerID, home); !ok {
		return nil, ErrNotFound
	}
	return home, nil
}

// applyDetection merges one detection into the record. An application found
// is marked detected now. One that is no longer found keeps its row only when
// something the owner did still refers to it (a confirmation, a "Not mine", a
// chosen or confirmed main application, a templates list); otherwise it goes.
func applyDetection(profile *workspace.HomeProfile, found []folderdigest.InstalledApp, now time.Time) {
	at := now
	profile.DetectedAt = &at
	present := make(map[string]string, len(found))
	for _, app := range found {
		if app.ToolID != "" && app.Name != "" {
			present[app.ToolID] = app.Name
		}
	}
	mainKept := profile.MainApp != nil && mainAppIsOwners(profile.MainApp)
	next := make([]workspace.HomeProfileApp, 0, len(profile.Apps)+len(found))
	listed := make(map[string]bool, len(profile.Apps))
	for _, row := range profile.Apps {
		if name, ok := present[row.ID]; ok {
			seen := now
			row.Name, row.Detected, row.DetectedAt = name, true, &seen
		} else {
			referenced := row.ConfirmedAt != nil || row.Hidden ||
				(mainKept && profile.MainApp.ID == row.ID) ||
				(profile.Templates != nil && profile.Templates.AppID == row.ID && len(profile.Templates.Items) > 0)
			if !referenced {
				continue
			}
			row.Detected = false
		}
		listed[row.ID] = true
		next = append(next, row)
	}
	for _, app := range found {
		if _, ok := present[app.ToolID]; !ok || listed[app.ToolID] || len(next) >= workspace.HomeProfileMaxApps {
			continue
		}
		seen := now
		listed[app.ToolID] = true
		next = append(next, workspace.HomeProfileApp{ID: app.ToolID, Name: app.Name, Detected: true, DetectedAt: &seen})
	}
	sortByToolTable(next)
	profile.Apps = next
	if profile.MainApp != nil && !listed[profile.MainApp.ID] {
		profile.MainApp = nil
	}
}

// sortByToolTable keeps rows in the host table's order, so the card lists
// applications the same way on every Home; a row the table no longer has
// keeps its place after them.
func sortByToolTable(rows []workspace.HomeProfileApp) {
	rank := make(map[string]int)
	for index, tool := range folderdigest.InstallableTools() {
		rank[tool.ToolID] = index + 1
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rank[rows[i].ID], rank[rows[j].ID]
		if left == 0 || right == 0 {
			return left != 0 && right == 0
		}
		return left < right
	})
}

// mainAppIsOwners is true once the owner chose or confirmed the main
// application: from then on it is an instruction and detection leaves it be.
func mainAppIsOwners(main *workspace.HomeProfileMainApp) bool {
	return main != nil && (main.Source == workspace.HomeProfileSourceOwner || main.ConfirmedAt != nil)
}

// applyMainAppRule proposes a main application while the owner has not said.
// One application found: that one. Several: the one whose project format has
// strictly the most entries in the Home's library. A tie, an empty library or
// nothing found leaves it unset for the owner to pick. Folder and file names
// are never consulted.
func applyMainAppRule(profile *workspace.HomeProfile, formats map[string]int) {
	if mainAppIsOwners(profile.MainApp) {
		return
	}
	var candidates []workspace.HomeProfileApp
	for _, app := range profile.VisibleApps() {
		if app.Detected {
			candidates = append(candidates, app)
		}
	}
	profile.MainApp = nil
	switch {
	case len(candidates) == 1:
		profile.MainApp = &workspace.HomeProfileMainApp{ID: candidates[0].ID,
			Source: workspace.HomeProfileSourceDetected, Reason: workspace.HomeProfileMainAppOnly}
	case len(candidates) > 1:
		best, bestCount, tied := "", 0, false
		for _, app := range candidates {
			count := formats[folderdigest.ProjectFormatForTool(app.ID)]
			switch {
			case count > bestCount:
				best, bestCount, tied = app.ID, count, false
			case count == bestCount:
				tied = true
			}
		}
		if best != "" && bestCount > 0 && !tied {
			profile.MainApp = &workspace.HomeProfileMainApp{ID: best,
				Source: workspace.HomeProfileSourceDetected, Reason: workspace.HomeProfileMainAppLibrary}
		}
	}
}
