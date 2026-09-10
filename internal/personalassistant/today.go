package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	TodaySectionAvailable    = "available"
	TodaySectionHealthyEmpty = "healthy_empty"
	TodaySectionPartial      = "partial"
	TodaySectionUnavailable  = "unavailable"

	todayBriefCap    = 5
	todayDecisionCap = 5
	todayPriorityCap = 10
	todayFollowUpCap = 10
	todayResultCap   = 5
)

var todaySafeSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

// TodaySourceHealth reports one independent canonical read. Unavailable never
// means empty, and its bounded reason codes contain no internal error text.
type TodaySourceHealth struct {
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type TodayItem struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	State  string `json:"state,omitempty"`
	// Attribution names the agent whose work this is, so the user can see who
	// did it. It is the assignee recorded on the canonical record and is empty
	// when no agent is recorded — never inferred.
	Attribution string               `json:"attribution,omitempty"`
	Route       string               `json:"route"`
	Ref         dailybrief.SourceRef `json:"ref"`
	DueAt       *time.Time           `json:"due_at,omitempty"`
	SourceAt    time.Time            `json:"source_at,omitempty"`
}

type TodaySection struct {
	Health TodaySourceHealth `json:"health"`
	Items  []TodayItem       `json:"items"`
}

type TodayBriefProjection struct {
	Health         TodaySourceHealth `json:"health"`
	RevisionID     string            `json:"revision_id,omitempty"`
	OpeningSummary string            `json:"opening_summary,omitempty"`
	GeneratedAt    time.Time         `json:"generated_at,omitempty"`
	Degraded       bool              `json:"degraded,omitempty"`
	DataGaps       []string          `json:"data_gaps,omitempty"`
	Items          []TodayItem       `json:"items"`
}

// TodayStudioProjection reports what the user's domain specialist has done.
//
// It is a read, and only a read. The assistant can see across workspaces via
// its bounded overview, but it cannot act across them — `agentcomm.DelegateTask`
// requires both agents in one workspace, and the specialist lives in its own.
// So this section names who did the work and links to the workspace where the
// user can address that agent directly. Nothing here implies the assistant can
// hand work to it.
type TodayStudioProjection struct {
	Health TodaySourceHealth `json:"health"`
	// Domain is the user's own words for this work, e.g. "music projects".
	Domain string `json:"domain,omitempty"`
	// SpecialistName is the named expert the domain's workspace template seeds.
	SpecialistName        string               `json:"specialist_name,omitempty"`
	WorkspaceName         string               `json:"workspace_name,omitempty"`
	Route                 string               `json:"route,omitempty"`
	HomeWorkspaceName     string               `json:"home_workspace_name,omitempty"`
	HomeRoute             string               `json:"home_route,omitempty"`
	ConnectedProjectCount int                  `json:"connected_project_count"`
	Projects              []TodayStudioProject `json:"projects"`
	Items                 []TodayItem          `json:"items"`
}

// TodayStudioProject is one exact Assistant Project Link resolved to its
// current canonical workspace. Names and routes are display only; membership
// comes from the stable link, never from either value.
type TodayStudioProject struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Route       string `json:"route,omitempty"`
	State       string `json:"state"`
}

// TodaySpecialistSetupProjection consumes the setup journey's canonical root
// and child reconciliation. It carries navigation/presentation affordances
// only and grants the Personal Assistant no project, file, catalog, or runtime
// mutation authority.
type TodaySpecialistSetupProjection struct {
	Health                TodaySourceHealth             `json:"health"`
	JourneyID             string                        `json:"journey_id"`
	Title                 string                        `json:"title"`
	Lifecycle             string                        `json:"lifecycle"`
	CurrentStepID         string                        `json:"current_step_id,omitempty"`
	ConnectedProjectCount int                           `json:"connected_project_count"`
	ChildRunCount         int                           `json:"child_run_count"`
	UnfinishedChildCount  int                           `json:"unfinished_child_count"`
	Runs                  []TodaySpecialistSetupRun     `json:"runs"`
	SampleLibrary         *TodaySampleLibraryProjection `json:"sample_library,omitempty"`
	Actions               []TodaySpecialistSetupAction  `json:"actions"`
}

type TodaySpecialistSetupRun struct {
	RunID              string `json:"run_id"`
	RunKind            string `json:"run_kind"`
	Lifecycle          string `json:"lifecycle"`
	CurrentStepID      string `json:"current_step_id,omitempty"`
	ProjectWorkspaceID string `json:"project_workspace_id,omitempty"`
	ProjectName        string `json:"project_name,omitempty"`
	ProjectRoute       string `json:"project_route,omitempty"`
	SelectedModeID     string `json:"selected_mode_id,omitempty"`
}

type TodaySampleLibraryProjection struct {
	State               string `json:"state"`
	CapabilityInstalled bool   `json:"capability_installed"`
	ActiveRootCount     int    `json:"active_root_count"`
	IndexedRootCount    int    `json:"indexed_root_count"`
	AnalysisRootCount   int    `json:"analysis_root_count"`
}

type TodaySpecialistSetupAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Route string `json:"route,omitempty"`
}

type TodayLinks struct {
	PersonalHQ       string `json:"personal_hq,omitempty"`
	WorkingAgreement string `json:"working_agreement,omitempty"`
	Memory           string `json:"memory,omitempty"`
	Advanced         string `json:"advanced"`
}

// TodayProjection is the bounded server-owned Home projection. It carries only
// canonical IDs and routes derived from validated server records.
type TodayProjection struct {
	State           string                 `json:"state"`
	Relationship    APIState               `json:"relationship_state"`
	StateVersion    int64                  `json:"state_version,omitempty"`
	DisplayName     string                 `json:"display_name,omitempty"`
	Appearance      *types.AgentAppearance `json:"appearance,omitempty"`
	HQWorkspaceID   string                 `json:"hq_workspace_id,omitempty"`
	HQWorkspaceSlug string                 `json:"hq_workspace_slug,omitempty"`
	Model           SourceAvailability     `json:"model"`
	Brief           TodayBriefProjection   `json:"brief"`
	Decisions       TodaySection           `json:"decisions"`
	Priorities      TodaySection           `json:"priorities"`
	FollowUps       TodaySection           `json:"follow_ups"`
	Results         TodaySection           `json:"results"`
	// Studio is present only when the user accepted a domain specialist and a
	// workspace built from its blueprint exists. Otherwise there is nothing
	// honest to report and the section is absent rather than empty.
	Studio          *TodayStudioProjection          `json:"studio,omitempty"`
	SpecialistSetup *TodaySpecialistSetupProjection `json:"specialist_setup,omitempty"`
	NextCheckIn     *time.Time                      `json:"next_check_in,omitempty"`
	Links           TodayLinks                      `json:"links"`
	GeneratedAt     time.Time                       `json:"generated_at"`
}

type todayRelationshipReader interface {
	Get(ctx context.Context, userID string) (*Projection, error)
}

type todayBriefReader interface {
	GetCurrent(ctx context.Context, workspaceID string) (*dailybrief.Revision, error)
}

type todayFollowUpReader interface {
	List(ctx context.Context, filter followup.Filter) ([]*followup.FollowUp, error)
}

type groundedFollowUp struct {
	item  *followup.FollowUp
	ref   dailybrief.SourceRef
	route string
}

type todaySpecialistSetupReader interface {
	GetSpecialistSetup(ctx context.Context, userID string) (*TodaySpecialistSetupProjection, error)
}

// TodayService reads canonical stores independently; it never generates a
// brief, mutates a Ticket, or changes a follow-up.
type TodayService struct {
	relationship       todayRelationshipReader
	briefs             todayBriefReader
	workspaces         workspace.Store
	followUpWorkspaces dailybrief.WorkspaceSource
	followUps          todayFollowUpReader
	setup              todaySpecialistSetupReader
	now                func() time.Time
}

func NewTodayService(relationship todayRelationshipReader, briefs todayBriefReader, workspaces workspace.Store, followUps todayFollowUpReader) *TodayService {
	return &TodayService{
		relationship: relationship, briefs: briefs, workspaces: workspaces,
		followUpWorkspaces: workspaces, followUps: followUps, now: time.Now,
	}
}

// SetFollowUpWorkspaceSource selects the provenance-hydrating read source used
// only for follow-up-owner scope. Production points this at the canonical
// folder store while ordinary Today task reads retain the composed workspace
// store. It grants no write capability.
func (s *TodayService) SetFollowUpWorkspaceSource(source dailybrief.WorkspaceSource) {
	if s != nil {
		s.followUpWorkspaces = source
	}
}

// SetSpecialistSetupReader adds the optional canonical root/child/add-on
// overview after setup services are wired. The reader is read-only from the
// Personal Assistant's perspective.
func (s *TodayService) SetSpecialistSetupReader(reader todaySpecialistSetupReader) {
	if s != nil {
		s.setup = reader
	}
}

func (s *TodayService) Get(ctx context.Context, userID string) (*TodayProjection, error) {
	now := time.Now().UTC()
	if s != nil && s.now != nil {
		now = s.now().UTC()
	}
	out := &TodayProjection{
		State: "unavailable", GeneratedAt: now, Links: TodayLinks{Advanced: "/agents"},
		Brief:     TodayBriefProjection{Health: todayUnavailable("not_loaded"), Items: []TodayItem{}},
		Decisions: emptyTodaySection(), Priorities: emptyTodaySection(),
		FollowUps: emptyTodaySection(), Results: emptyTodaySection(),
	}
	if s == nil || s.relationship == nil {
		return nil, errors.New("personal assistant today: relationship service unavailable")
	}
	relationship, err := s.relationship.Get(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	out.Relationship = relationship.State
	out.StateVersion = relationship.StateVersion
	out.DisplayName = relationship.DisplayName
	out.Appearance = relationship.Appearance
	out.Model = relationship.Availability.Model
	switch relationship.State {
	case APIStateNeedsHire, APIStateHiring:
		out.State = "needs_hire"
		return out, nil
	case APIStateNeedsHQ, APIStateProvisioningHQ:
		// A genuinely hired assistant with no HQ. Today must not fetch or imply
		// an empty HQ record — there is nothing to fetch yet — and it must not
		// read as broken: this is an expected setup stage.
		out.State = "needs_hq"
		out.Links = TodayLinks{PersonalHQ: "/?quest=build-hq", Advanced: "/agents"}
		return out, nil
	case APIStateRepairNeeded:
		out.State = "repair_needed"
		return out, nil
	case APIStateActive, APIStatePaused:
		recordEvent(EventTodayViewed, EventData{
			AssistantID: relationship.AssistantID, WorkspaceID: relationship.HQWorkspaceID,
			State: string(relationship.State),
		})
	default:
		return nil, fmt.Errorf("personal assistant today: unsupported relationship state %q", relationship.State)
	}

	out.HQWorkspaceID = relationship.HQWorkspaceID
	ws, route, workspaceErr := s.loadHQ(relationship.HQWorkspaceID)
	if workspaceErr != nil {
		out.Brief.Health = todayUnavailable("hq_unavailable")
		out.Decisions.Health = todayUnavailable("hq_unavailable")
		out.Priorities.Health = todayUnavailable("hq_unavailable")
		out.FollowUps.Health = todayUnavailable("hq_unavailable")
		out.Results.Health = todayUnavailable("hq_unavailable")
		out.State = "partial"
		if relationship.State == APIStatePaused {
			out.State = "paused"
		}
		return out, nil
	}
	out.HQWorkspaceSlug = ws.FolderSlug
	out.Links = TodayLinks{
		PersonalHQ: route, WorkingAgreement: "/?personal-assistant=working-agreement",
		Memory: route + "#memory", Advanced: "/agents",
	}

	tasksByID := make(map[string]workspace.Task, len(ws.Tasks))
	for _, task := range ws.Tasks {
		tasksByID[task.ID] = task
	}
	s.loadTicketsAndResults(ws, route, now, out)
	followUpsByRef := s.loadFollowUps(ctx, userID, relationship, now, out)
	s.loadBrief(ctx, userID, ws.ID, route, tasksByID, followUpsByRef, out)
	out.Decisions = decisionsFromFollowUps(followUpsByRef, out.FollowUps.Health, now)
	out.Studio = s.loadStudio(userID, relationship.SpecialistSlug, relationship.HQWorkspaceID)
	out.SpecialistSetup = s.loadSpecialistSetup(ctx, userID, relationship.SpecialistSlug)
	out.NextCheckIn = nextTodayCheckIn(relationship, now)
	out.State = todayOverallState(relationship, out)
	return out, nil
}

// loadStudio reports finished work from exact Assistant Project Links. A
// matching blueprint name or physical parent is never enough: those are
// presentation and organization, not program-membership authority.
func (s *TodayService) loadStudio(userID, slug, hqID string) *TodayStudioProjection {
	entry, ok := specialist.Get(slug)
	if !ok || entry.SetupJourney == nil || s.workspaces == nil {
		return nil
	}
	workspaces, err := s.workspaces.ListActive()
	if err != nil {
		return &TodayStudioProjection{
			Health: todayUnavailable("read_failed"), Domain: entry.DisplayName,
			Projects: []TodayStudioProject{}, Items: []TodayItem{},
		}
	}
	userID = strings.TrimSpace(userID)
	expectedProgramID := strings.TrimSpace(entry.SetupJourney.ExpectedAssistantProgramID)
	var stations []*workspace.Workspace
	for _, candidate := range workspaces {
		if candidate == nil || strings.TrimSpace(candidate.ID) == strings.TrimSpace(hqID) {
			continue
		}
		state := candidate.GetAssistantProgramState()
		if state == nil || state.Declaration == nil {
			continue
		}
		key := state.Key.Normalize()
		if key.OwnerUserID != userID || key.ProgramID != expectedProgramID || state.Declaration.ID != expectedProgramID {
			continue
		}
		stations = append(stations, candidate)
	}
	if len(stations) == 0 {
		return nil
	}
	out := &TodayStudioProjection{
		Domain: entry.DisplayName, Projects: []TodayStudioProject{}, Items: []TodayItem{},
	}
	if len(stations) != 1 {
		out.Health = todayUnavailable("ambiguous_home")
		return out
	}
	station := stations[0]
	out.HomeWorkspaceName = truncateRunes(station.Name, 100)
	if route, ok := todayWorkspaceRoute(station, true); ok {
		out.HomeRoute = route
	} else {
		out.Health = todayUnavailable("workspace_slug_invalid")
		return out
	}

	linked, err := workspace.NewAssistantProgramStore(s.workspaces).LinkedProjects(station.ID)
	if err != nil {
		out.Health = todayUnavailable("read_failed")
		return out
	}
	sort.SliceStable(linked, func(i, j int) bool {
		left, right := strings.ToLower(linked[i].Name), strings.ToLower(linked[j].Name)
		if left != right {
			return left < right
		}
		return linked[i].ID < linked[j].ID
	})
	type studioResult struct {
		task  workspace.Task
		route string
	}
	results := make([]studioResult, 0)
	state := station.GetAssistantProgramState()
	var firstValidProject *workspace.Workspace
	invalidProject := false
	for _, project := range linked {
		if owner := strings.TrimSpace(project.OwnerUserID); owner != "" && owner != userID {
			invalidProject = true
			continue
		}
		provenance := project.GetTemplateProvenance()
		link := project.GetAssistantProjectLink()
		if provenance == nil || provenance.PluginOwner == nil || provenance.AssistantProgram == nil || link == nil ||
			provenance.PluginOwner.BlueprintID != entry.SetupJourney.ExpectedBlueprintID ||
			!strings.EqualFold(provenance.PluginOwner.PluginID, state.Key.PluginID) ||
			provenance.AssistantProgram.ID != expectedProgramID {
			invalidProject = true
			continue
		}
		route, safe := todayWorkspaceRoute(project, false)
		if !safe {
			invalidProject = true
			continue
		}
		out.Projects = append(out.Projects, TodayStudioProject{
			WorkspaceID: project.ID, Name: truncateRunes(project.Name, 100),
			Route: route, State: string(project.Status),
		})
		if firstValidProject == nil {
			firstValidProject = project
		}
		for _, task := range project.Tasks {
			if task.CanonicalState() != workspace.TicketStateReview || strings.TrimSpace(task.Result) == "" {
				continue
			}
			results = append(results, studioResult{task: task, route: route})
		}
	}
	out.ConnectedProjectCount = len(out.Projects)
	if len(out.Projects) == 1 {
		out.WorkspaceName = out.Projects[0].Name
		out.Route = out.Projects[0].Route
		out.SpecialistName = primaryProjectAgentName(state, firstValidProject)
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := taskSourceTime(results[i].task), taskSourceTime(results[j].task)
		if !left.Equal(right) {
			return left.After(right)
		}
		if results[i].task.ID != results[j].task.ID {
			return results[i].task.ID < results[j].task.ID
		}
		return results[i].task.WorkspaceID < results[j].task.WorkspaceID
	})
	var updated time.Time
	for _, result := range results {
		if len(out.Items) >= todayResultCap {
			break
		}
		task := result.task
		sourceAt := taskSourceTime(task)
		if sourceAt.After(updated) {
			updated = sourceAt
		}
		out.Items = append(out.Items, TodayItem{
			ID: task.ID, Kind: "studio_result", Title: truncateRunes(task.Description, 200),
			State: string(task.CanonicalState()), Attribution: truncateRunes(strings.TrimSpace(task.To), 100),
			Route:    recordTodayRoute(result.route, "ticket", task.ID),
			Ref:      dailybrief.SourceRef{WorkspaceID: task.WorkspaceID, EntityType: "task", EntityID: task.ID, Timestamp: sourceAt},
			SourceAt: sourceAt,
		})
	}
	out.Health = todayHealthForItems(out.Items, updated)
	if invalidProject {
		out.Health = todayUnavailable("stale_references")
	}
	return out
}

func todayWorkspaceRoute(ws *workspace.Workspace, assistant bool) (string, bool) {
	if ws == nil {
		return "", false
	}
	slug := strings.TrimSpace(ws.FolderSlug)
	if !todaySafeSlug.MatchString(slug) {
		return "", false
	}
	route := "/workspaces/" + url.PathEscape(slug)
	if assistant {
		route += "/assistant"
	}
	return route, true
}

func primaryProjectAgentName(state *workspace.AssistantProgramState, project *workspace.Workspace) string {
	if state == nil || state.Declaration == nil || project == nil {
		return ""
	}
	link := project.GetAssistantProjectLink()
	if link == nil {
		return ""
	}
	bindings := make(map[string]string, len(link.ProjectBindings.Bindings))
	for _, binding := range link.ProjectBindings.Bindings {
		bindings[binding.RoleID] = binding.AgentName
	}
	for _, role := range state.Declaration.Roles {
		if role.Scope == workspace.AssistantRoleScopeProject && role.Primary {
			return truncateRunes(strings.TrimSpace(bindings[role.ID]), 100)
		}
	}
	return ""
}

func (s *TodayService) loadSpecialistSetup(ctx context.Context, userID, slug string) *TodaySpecialistSetupProjection {
	entry, ok := specialist.Get(slug)
	if !ok || entry.SetupJourney == nil || s.setup == nil {
		return nil
	}
	projection, err := s.setup.GetSpecialistSetup(ctx, strings.TrimSpace(userID))
	if err != nil || projection == nil {
		return &TodaySpecialistSetupProjection{
			Health: todayUnavailable("read_failed"), JourneyID: entry.SetupJourney.ID,
			Title: entry.SetupJourney.Title, Runs: []TodaySpecialistSetupRun{},
			Actions: []TodaySpecialistSetupAction{},
		}
	}
	return projection
}

func (s *TodayService) loadHQ(workspaceID string) (*workspace.Workspace, string, error) {
	if s.workspaces == nil {
		return nil, "", errors.New("workspace store unavailable")
	}
	ws, err := s.workspaces.Get(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil || strings.TrimSpace(ws.ID) != strings.TrimSpace(workspaceID) {
		return nil, "", errors.New("workspace missing")
	}
	slug := strings.TrimSpace(ws.FolderSlug)
	if !todaySafeSlug.MatchString(slug) {
		return nil, "", errors.New("workspace slug invalid")
	}
	return ws, "/workspaces/" + url.PathEscape(slug), nil
}

func (s *TodayService) loadTicketsAndResults(ws *workspace.Workspace, route string, now time.Time, out *TodayProjection) {
	priorities := make([]workspace.Task, 0)
	results := make([]workspace.Task, 0)
	todayEnd := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), time.UTC)
	for _, task := range ws.Tasks {
		switch task.CanonicalState() {
		case workspace.TicketStateReady:
			// Undated Ready work is an explicit current priority. Dated work
			// enters Today only once due; future commitments remain in HQ.
			if task.DueDate == nil || !task.DueDate.After(todayEnd) {
				priorities = append(priorities, task)
			}
		case workspace.TicketStateReview:
			if strings.TrimSpace(task.Result) != "" {
				results = append(results, task)
			}
		}
	}
	sort.SliceStable(priorities, func(i, j int) bool {
		left, right := priorities[i], priorities[j]
		if (left.DueDate == nil) != (right.DueDate == nil) {
			return left.DueDate != nil
		}
		if left.DueDate != nil && right.DueDate != nil && !left.DueDate.Equal(*right.DueDate) {
			return left.DueDate.Before(*right.DueDate)
		}
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		if left.StateRank != right.StateRank {
			return left.StateRank < right.StateRank
		}
		return left.ID < right.ID
	})
	sort.SliceStable(results, func(i, j int) bool {
		left, right := taskSourceTime(results[i]), taskSourceTime(results[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return results[i].ID < results[j].ID
	})
	out.Priorities = taskTodaySection(priorities, route, "ticket", todayPriorityCap)
	out.Results = taskTodaySection(results, route, "result", todayResultCap)
	_ = now
}

func taskTodaySection(tasks []workspace.Task, route, kind string, cap int) TodaySection {
	items := make([]TodayItem, 0, min(len(tasks), cap))
	var updated time.Time
	for _, task := range tasks {
		if len(items) >= cap {
			break
		}
		sourceAt := taskSourceTime(task)
		if sourceAt.After(updated) {
			updated = sourceAt
		}
		items = append(items, TodayItem{
			ID: task.ID, Kind: kind, Title: truncateRunes(task.Description, 200),
			State: string(task.CanonicalState()), Route: recordTodayRoute(route, "ticket", task.ID),
			Ref:   dailybrief.SourceRef{WorkspaceID: task.WorkspaceID, EntityType: "task", EntityID: task.ID, Timestamp: sourceAt},
			DueAt: task.DueDate, SourceAt: sourceAt,
		})
	}
	return TodaySection{Health: todayHealthForItems(items, updated), Items: items}
}

func taskSourceTime(task workspace.Task) time.Time {
	if task.CompletedAt != nil {
		return task.CompletedAt.UTC()
	}
	if task.StartedAt != nil {
		return task.StartedAt.UTC()
	}
	return task.CreatedAt.UTC()
}

func (s *TodayService) loadFollowUps(ctx context.Context, userID string, relationship *Projection, now time.Time, out *TodayProjection) map[string]groundedFollowUp {
	byRef := map[string]groundedFollowUp{}
	if s.followUps == nil {
		out.FollowUps = TodaySection{Health: todayUnavailable("service_unavailable"), Items: []TodayItem{}}
		return byRef
	}
	cfg := todayFollowUpConfig(relationship)
	scope := dailybrief.ResolveWorkspaceScope(s.followUpWorkspaces, cfg, userID)
	ownerScope := dailybrief.ResolveFollowUpOwnerScope(s.followUpWorkspaces, scope, cfg.WorkspaceID, userID)
	partial := len(ownerScope.Gaps) > 0
	successfulReads, failedReads := 0, 0
	accepted := make([]groundedFollowUp, 0)
	for _, owner := range ownerScope.Owners {
		rows, err := s.followUps.List(ctx, followup.Filter{
			UserID: strings.TrimSpace(userID), WorkspaceID: owner.WorkspaceID,
			Statuses: []followup.Status{followup.StatusActive, followup.StatusReopened},
		})
		if err != nil {
			partial = true
			failedReads++
			continue
		}
		successfulReads++
		baseRoute := "/workspaces/" + url.PathEscape(owner.WorkspaceSlug)
		for _, item := range rows {
			if item == nil || item.UserID != strings.TrimSpace(userID) || item.WorkspaceID != owner.WorkspaceID ||
				strings.TrimSpace(item.ID) == "" || item.ID != strings.TrimSpace(item.ID) ||
				(item.Status != followup.StatusActive && item.Status != followup.StatusReopened) {
				continue
			}
			copyItem := *item
			ref := dailybrief.SourceRef{
				WorkspaceID: owner.WorkspaceID, WorkspaceSlug: owner.WorkspaceSlug,
				EntityType: "follow_up", EntityID: copyItem.ID, Timestamp: copyItem.UpdatedAt,
			}
			if _, duplicate := byRef[ref.Key()]; duplicate {
				continue
			}
			grounded := groundedFollowUp{
				item: &copyItem, ref: ref, route: recordTodayRoute(baseRoute, "follow_up", copyItem.ID),
			}
			byRef[ref.Key()] = grounded
			accepted = append(accepted, grounded)
		}
	}

	sort.SliceStable(accepted, func(i, j int) bool {
		left, right := accepted[i], accepted[j]
		leftStale, rightStale := left.item.IsStale(now), right.item.IsStale(now)
		if leftStale != rightStale {
			return leftStale
		}
		if (left.item.DueAt == nil) != (right.item.DueAt == nil) {
			return left.item.DueAt != nil
		}
		if left.item.DueAt != nil && right.item.DueAt != nil && !left.item.DueAt.Equal(*right.item.DueAt) {
			return left.item.DueAt.Before(*right.item.DueAt)
		}
		if !left.item.UpdatedAt.Equal(right.item.UpdatedAt) {
			return left.item.UpdatedAt.Before(right.item.UpdatedAt)
		}
		if left.ref.WorkspaceID != right.ref.WorkspaceID {
			return left.ref.WorkspaceID < right.ref.WorkspaceID
		}
		return left.item.ID < right.item.ID
	})
	items := make([]TodayItem, 0, min(len(accepted), todayFollowUpCap))
	var updated time.Time
	for _, grounded := range accepted {
		if len(items) >= todayFollowUpCap {
			break
		}
		if grounded.item.UpdatedAt.After(updated) {
			updated = grounded.item.UpdatedAt
		}
		items = append(items, followUpTodayItem(grounded))
	}
	health := todayHealthForItems(items, updated)
	switch {
	case successfulReads == 0 && (failedReads > 0 || len(ownerScope.Owners) == 0):
		health = todayUnavailable("read_failed")
	case partial:
		health = TodaySourceHealth{Status: TodaySectionPartial, Reason: "some_sources_unavailable", UpdatedAt: updated}
	}
	out.FollowUps = TodaySection{Health: health, Items: items}
	return byRef
}

func todayFollowUpConfig(relationship *Projection) dailybrief.Config {
	cfg := dailybrief.Config{Scope: dailybrief.ScopeSelected}
	if relationship == nil {
		return cfg
	}
	cfg.WorkspaceID = strings.TrimSpace(relationship.HQWorkspaceID)
	if relationship.DailyBrief == nil {
		return cfg
	}
	brief := relationship.DailyBrief
	if brief.Scope == dailybrief.ScopeAll || brief.Scope == dailybrief.ScopeSelected {
		cfg.Scope = brief.Scope
	}
	cfg.SelectedWorkspaceIDs = append([]string(nil), brief.SelectedWorkspaceIDs...)
	cfg.IncludeFutureWorkspaces = brief.IncludeFutureWorkspaces
	cfg.UpdatedAt = brief.UpdatedAt
	return cfg
}

func decisionsFromFollowUps(byRef map[string]groundedFollowUp, sourceHealth TodaySourceHealth, now time.Time) TodaySection {
	if sourceHealth.Status == TodaySectionUnavailable {
		return TodaySection{Health: sourceHealth, Items: []TodayItem{}}
	}
	decisions := make([]groundedFollowUp, 0)
	for _, grounded := range byRef {
		if grounded.item.Category == followup.CategoryNeedsDecision &&
			(grounded.item.Status == followup.StatusActive || grounded.item.Status == followup.StatusReopened) {
			decisions = append(decisions, grounded)
		}
	}
	sort.SliceStable(decisions, func(i, j int) bool {
		left, right := decisions[i], decisions[j]
		if left.item.IsStale(now) != right.item.IsStale(now) {
			return left.item.IsStale(now)
		}
		if !left.item.UpdatedAt.Equal(right.item.UpdatedAt) {
			return left.item.UpdatedAt.Before(right.item.UpdatedAt)
		}
		if left.ref.WorkspaceID != right.ref.WorkspaceID {
			return left.ref.WorkspaceID < right.ref.WorkspaceID
		}
		return left.item.ID < right.item.ID
	})
	items := make([]TodayItem, 0, min(len(decisions), todayDecisionCap))
	var updated time.Time
	for _, item := range decisions {
		if len(items) >= todayDecisionCap {
			break
		}
		items = append(items, followUpTodayItem(item))
		if item.item.UpdatedAt.After(updated) {
			updated = item.item.UpdatedAt
		}
	}
	health := todayHealthForItems(items, updated)
	if sourceHealth.Status == TodaySectionPartial {
		health = TodaySourceHealth{Status: TodaySectionPartial, Reason: sourceHealth.Reason, UpdatedAt: updated}
	}
	return TodaySection{Health: health, Items: items}
}

func followUpTodayItem(grounded groundedFollowUp) TodayItem {
	item := grounded.item
	return TodayItem{
		ID: item.ID, Kind: "follow_up", Title: truncateRunes(item.Title, 200), Detail: truncateRunes(item.Counterparty, 100),
		State: string(item.Status), Route: grounded.route, Ref: grounded.ref,
		DueAt: item.DueAt, SourceAt: item.UpdatedAt,
	}
}

func (s *TodayService) loadBrief(ctx context.Context, userID, workspaceID, route string, tasks map[string]workspace.Task, followUps map[string]groundedFollowUp, out *TodayProjection) {
	out.Brief = TodayBriefProjection{Health: todayUnavailable("service_unavailable"), Items: []TodayItem{}}
	if s.briefs == nil {
		return
	}
	revision, err := s.briefs.GetCurrent(ctx, workspaceID)
	if errors.Is(err, dailybrief.ErrRevisionNotFound) {
		out.Brief.Health = TodaySourceHealth{Status: TodaySectionHealthyEmpty}
		return
	}
	if err != nil || revision == nil {
		out.Brief.Health = todayUnavailable("read_failed")
		return
	}
	if revision.UserID != strings.TrimSpace(userID) || revision.WorkspaceID != workspaceID {
		out.Brief.Health = todayUnavailable("ownership_mismatch")
		return
	}
	var content dailybrief.BriefContent
	if err := json.Unmarshal([]byte(revision.ContentJSON), &content); err != nil {
		out.Brief.Health = todayUnavailable("malformed_content")
		return
	}
	items := make([]TodayItem, 0, todayBriefCap)
	referenced, dropped := 0, 0
	appendRef := func(title, detail string, ref dailybrief.SourceRef) {
		if len(items) >= todayBriefCap || strings.TrimSpace(title) == "" {
			return
		}
		referenced++
		item, ok := groundedTodayItem(title, detail, ref, route, tasks, followUps)
		if ok {
			items = append(items, item)
		} else {
			dropped++
		}
	}
	for _, item := range content.NeedsAttention {
		appendRef(item.Title, item.Reason, item.Ref)
	}
	for _, item := range content.TodaysPlan {
		appendRef(item.Title, item.Reason, item.Ref)
	}
	for _, item := range content.SinceLastBrief {
		appendRef(item.Title, item.Summary, item.Ref)
	}
	health := TodaySourceHealth{Status: TodaySectionAvailable, UpdatedAt: revision.GeneratedAt}
	opening := truncateRunes(content.OpeningSummary, 500)
	gaps := boundedTodayGaps(content.DataGaps)
	if dropped > 0 {
		health = TodaySourceHealth{Status: TodaySectionUnavailable, Reason: "stale_references", UpdatedAt: revision.GeneratedAt}
		opening = ""
		if len(gaps) < 5 {
			gaps = append(gaps, "stale brief references were omitted")
		}
	}
	if referenced == 0 && strings.TrimSpace(opening) == "" {
		health.Status = TodaySectionHealthyEmpty
	}
	out.Brief = TodayBriefProjection{
		Health: health, RevisionID: revision.ID, OpeningSummary: opening,
		GeneratedAt: revision.GeneratedAt, Degraded: content.Degraded,
		DataGaps: gaps, Items: items,
	}
}

func groundedTodayItem(title, detail string, ref dailybrief.SourceRef, route string, tasks map[string]workspace.Task, followUps map[string]groundedFollowUp) (TodayItem, bool) {
	switch ref.EntityType {
	case "task":
		task, ok := tasks[ref.EntityID]
		if !ok || ref.WorkspaceID != task.WorkspaceID {
			return TodayItem{}, false
		}
		return TodayItem{ID: task.ID, Kind: "brief", Title: truncateRunes(title, 200), Detail: truncateRunes(detail, 300), Route: recordTodayRoute(route, "ticket", task.ID), Ref: ref, SourceAt: ref.Timestamp}, true
	case "follow_up":
		grounded, ok := followUps[ref.Key()]
		if !ok || grounded.item == nil || ref.WorkspaceID != grounded.item.WorkspaceID || ref.EntityID != grounded.item.ID {
			return TodayItem{}, false
		}
		return TodayItem{
			ID: grounded.item.ID, Kind: "brief", Title: truncateRunes(title, 200), Detail: truncateRunes(detail, 300),
			Route: grounded.route, Ref: grounded.ref, SourceAt: grounded.ref.Timestamp,
		}, true
	default:
		return TodayItem{}, false
	}
}

func boundedTodayGaps(gaps []string) []string {
	out := make([]string, 0, min(len(gaps), 5))
	for _, gap := range gaps {
		if len(out) >= 5 {
			break
		}
		if gap = strings.TrimSpace(gap); gap != "" {
			out = append(out, truncateRunes(gap, 100))
		}
	}
	return out
}

func nextTodayCheckIn(relationship *Projection, now time.Time) *time.Time {
	if relationship == nil || relationship.DailyBrief == nil || relationship.State == APIStatePaused {
		return nil
	}
	cfg := dailybrief.Config{
		Timezone: relationship.DailyBrief.Timezone, ScheduleDays: relationship.DailyBrief.ScheduleDays,
		ScheduleTime: relationship.DailyBrief.ScheduleTime, ScheduleEnabled: relationship.DailyBrief.ScheduleEnabled,
	}
	next, ok, err := dailybrief.NextOccurrence(cfg, now)
	if err != nil || !ok {
		return nil
	}
	return &next
}

func todayOverallState(relationship *Projection, out *TodayProjection) string {
	if relationship.State == APIStatePaused {
		return "paused"
	}
	health := []TodaySourceHealth{out.Brief.Health, out.Decisions.Health, out.Priorities.Health, out.FollowUps.Health, out.Results.Health}
	unavailable, itemCount := false, len(out.Brief.Items)+len(out.Decisions.Items)+len(out.Priorities.Items)+len(out.FollowUps.Items)+len(out.Results.Items)
	// Studio counts only when it is actually being reported. An absent studio
	// is not a degraded source, so it must not turn Today "partial".
	if out.Studio != nil {
		health = append(health, out.Studio.Health)
		itemCount += len(out.Studio.Items)
	}
	if out.SpecialistSetup != nil {
		health = append(health, out.SpecialistSetup.Health)
	}
	for _, source := range health {
		if source.Status == TodaySectionUnavailable || source.Status == TodaySectionPartial {
			unavailable = true
		}
	}
	if unavailable {
		return "partial"
	}
	if !relationship.Availability.Model.Available {
		return "model_unavailable"
	}
	if itemCount == 0 && strings.TrimSpace(out.Brief.OpeningSummary) == "" {
		return "healthy_empty"
	}
	return "active"
}

func todayHealthForItems(items []TodayItem, updated time.Time) TodaySourceHealth {
	status := TodaySectionAvailable
	if len(items) == 0 {
		status = TodaySectionHealthyEmpty
	}
	return TodaySourceHealth{Status: status, UpdatedAt: updated}
}

func todayUnavailable(reason string) TodaySourceHealth {
	return TodaySourceHealth{Status: TodaySectionUnavailable, Reason: reason}
}

func emptyTodaySection() TodaySection {
	return TodaySection{Health: todayUnavailable("not_loaded"), Items: []TodayItem{}}
}

func recordTodayRoute(base, kind, id string) string {
	values := url.Values{}
	values.Set(kind, strings.TrimSpace(id))
	return base + "?" + values.Encode()
}
