package server

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/samplelibrary"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxTodaySetupProjects = 32

var todaySetupSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

// personalAssistantSetupReportingAdapter maps the canonical journey/program/
// add-on owners into one bounded Home read. It exposes navigation and setup
// presentation only; no consequential method is part of this interface.
type personalAssistantSetupReportingAdapter struct {
	journeys   *setupjourney.Service
	workspaces workspace.Store
	samples    *samplelibrary.Service
}

func (a *personalAssistantSetupReportingAdapter) GetSpecialistSetup(ctx context.Context, userID string) (*personalassistant.TodaySpecialistSetupProjection, error) {
	if a == nil || a.journeys == nil || a.workspaces == nil {
		return nil, errors.New("specialist setup reporting is unavailable")
	}
	overview, err := a.journeys.Overview(ctx, strings.TrimSpace(userID))
	if err != nil || overview == nil || overview.Root == nil {
		return nil, errors.New("specialist setup reporting is unavailable")
	}
	root := overview.Root
	out := &personalassistant.TodaySpecialistSetupProjection{
		Health: personalassistant.TodaySourceHealth{
			Status: personalassistant.TodaySectionAvailable, UpdatedAt: root.UpdatedAt,
		},
		JourneyID: root.Journey.ID, Title: boundedSetupLabel(root.Journey.Title),
		Lifecycle: string(root.Lifecycle), CurrentStepID: root.CurrentStepID,
		ChildRunCount: overview.ChildCount,
		Runs:          make([]personalassistant.TodaySpecialistSetupRun, 0, 1+len(overview.Children)),
		Actions:       make([]personalassistant.TodaySpecialistSetupAction, 0, 7),
	}

	projects, home, projectErr := a.linkedProjects(root.Receipts.HomeWorkspaceID)
	if projectErr != nil {
		out.Health = personalassistant.TodaySourceHealth{
			Status: personalassistant.TodaySectionUnavailable, Reason: "program_unavailable",
			UpdatedAt: root.UpdatedAt,
		}
	}
	out.ConnectedProjectCount = len(projects)
	projectByID := make(map[string]*workspace.Workspace, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
	}
	out.Runs = append(out.Runs, setupRunForToday(root, projectByID[root.Receipts.ProjectWorkspaceID]))
	for _, child := range overview.Children {
		if child.Lifecycle != setupjourney.LifecycleReady {
			out.UnfinishedChildCount++
		}
		out.Runs = append(out.Runs, setupRunForToday(child, projectByID[child.Receipts.ProjectWorkspaceID]))
	}
	if overview.Truncated {
		out.Health = personalassistant.TodaySourceHealth{
			Status: personalassistant.TodaySectionUnavailable, Reason: "run_list_truncated",
			UpdatedAt: root.UpdatedAt,
		}
	}

	setupActionID, setupActionLabel := "continue_setup", "Continue setup"
	if root.Lifecycle == setupjourney.LifecycleReady {
		setupActionID, setupActionLabel = "review_setup", "Review setup"
	}
	out.Actions = append(out.Actions, personalassistant.TodaySpecialistSetupAction{ID: setupActionID, Label: setupActionLabel})
	if root.Lifecycle == setupjourney.LifecycleReady {
		out.Actions = append(out.Actions, personalassistant.TodaySpecialistSetupAction{ID: "connect_another", Label: "Connect another project"})
	}
	if home != nil {
		if route, ok := setupWorkspaceRoute(home, true); ok {
			out.Actions = append(out.Actions, personalassistant.TodaySpecialistSetupAction{ID: "open_home", Label: "Open Home", Route: route})
			label := "Add sample library"
			if home.HasInstalledCapability(workspace.CapabilitySampleLibrary) {
				label = "Manage sample library"
			}
			out.Actions = append(out.Actions, personalassistant.TodaySpecialistSetupAction{ID: "manage_samples", Label: label, Route: route + "#sampleLibraryPanel"})
		}
		out.SampleLibrary = a.sampleProjection(ctx, home)
	}
	if project := projectByID[root.Receipts.ProjectWorkspaceID]; project != nil {
		if route, ok := setupWorkspaceRoute(project, false); ok {
			out.Actions = append(out.Actions, personalassistant.TodaySpecialistSetupAction{ID: "open_project", Label: "Open project", Route: route})
			liveLabel := "Set up live control"
			if root.Receipts.SelectedModeID != "" && root.Receipts.SelectedModeID != "file_only" {
				liveLabel = "Review live control"
				if root.Lifecycle == setupjourney.LifecycleNeedsAttention {
					liveLabel = "Repair live control"
				}
			}
			out.Actions = append(out.Actions, personalassistant.TodaySpecialistSetupAction{
				ID: "live_setup", Label: liveLabel, Route: route + "?panel=settings",
			})
		}
	}
	return out, nil
}

func (a *personalAssistantSetupReportingAdapter) linkedProjects(homeID string) ([]*workspace.Workspace, *workspace.Workspace, error) {
	homeID = strings.TrimSpace(homeID)
	if homeID == "" {
		return []*workspace.Workspace{}, nil, nil
	}
	home, err := a.workspaces.Get(homeID)
	if err != nil || home == nil || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing || home.GetAssistantProgramState() == nil {
		return []*workspace.Workspace{}, nil, errors.New("assistant program Home is unavailable")
	}
	projects, err := workspace.NewAssistantProgramStore(a.workspaces).LinkedProjects(home.ID)
	if err != nil {
		return []*workspace.Workspace{}, home, err
	}
	sort.SliceStable(projects, func(i, j int) bool {
		left, right := strings.ToLower(projects[i].Name), strings.ToLower(projects[j].Name)
		if left != right {
			return left < right
		}
		return projects[i].ID < projects[j].ID
	})
	if len(projects) > maxTodaySetupProjects {
		projects = projects[:maxTodaySetupProjects]
	}
	return projects, home, nil
}

func (a *personalAssistantSetupReportingAdapter) sampleProjection(ctx context.Context, home *workspace.Workspace) *personalassistant.TodaySampleLibraryProjection {
	projection := &personalassistant.TodaySampleLibraryProjection{
		State: "not_installed", CapabilityInstalled: home.HasInstalledCapability(workspace.CapabilitySampleLibrary),
	}
	if !projection.CapabilityInstalled {
		return projection
	}
	if a.samples == nil {
		projection.State = "unavailable"
		return projection
	}
	state, roots, err := a.samples.Snapshot(ctx, home.ID)
	if errors.Is(err, samplelibrary.ErrNotFound) {
		projection.State = "setup_needed"
		return projection
	}
	if err != nil {
		projection.State = "needs_attention"
		return projection
	}
	projection.State = state.Lifecycle
	for _, root := range roots {
		if root.State != "active" {
			continue
		}
		projection.ActiveRootCount++
		if root.Generation > 0 {
			projection.IndexedRootCount++
		}
		if root.HashEnabled || root.TagsEnabled {
			projection.AnalysisRootCount++
		}
	}
	return projection
}

func setupRunForToday(run *setupjourney.JourneyProjection, project *workspace.Workspace) personalassistant.TodaySpecialistSetupRun {
	out := personalassistant.TodaySpecialistSetupRun{
		RunID: run.RunID, RunKind: string(run.RunKind), Lifecycle: string(run.Lifecycle),
		CurrentStepID: run.CurrentStepID, ProjectWorkspaceID: run.Receipts.ProjectWorkspaceID,
		SelectedModeID: run.Receipts.SelectedModeID,
	}
	if project != nil {
		out.ProjectName = boundedSetupLabel(project.Name)
		out.ProjectRoute, _ = setupWorkspaceRoute(project, false)
	}
	return out
}

func setupWorkspaceRoute(ws *workspace.Workspace, assistant bool) (string, bool) {
	if ws == nil {
		return "", false
	}
	slug := strings.TrimSpace(ws.FolderSlug)
	if !todaySetupSlug.MatchString(slug) {
		return "", false
	}
	route := "/workspaces/" + url.PathEscape(slug)
	if assistant {
		route += "/assistant"
	}
	return route, true
}

func boundedSetupLabel(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120])
	}
	return value
}
