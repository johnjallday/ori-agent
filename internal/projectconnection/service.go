// Package projectconnection implements Ori's closed, host-owned project
// connection adapter. Blueprint declarations constrain modes and extensions;
// they never choose code, scanners, paths, routes, or commands.
package projectconnection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	maxEntryCandidates = 64
	connectionRunKey   = "setup_journey_run_id"
)

var (
	connectionCommitMu sync.Mutex
	ErrUnavailable     = errors.New("project connection is unavailable")
	ErrInvalid         = errors.New("project connection request is invalid")
	ErrChanged         = errors.New("project connection selection changed")
)

type SelectionResolver interface {
	Resolve(string) (string, error)
}

type folderStore interface {
	workspace.Store
	GetFolderPath(string) (string, error)
}

type Service struct {
	store      folderStore
	selections SelectionResolver
	grouping   *grouprequirements.Service
	now        func() time.Time
	// recordCreated is told about every workspace this service creates, so
	// the host can mark it as owned by this data directory.
	recordCreated func(workspaceID string)
}

func NewService(store folderStore, selections SelectionResolver) *Service {
	return &Service{store: store, selections: selections, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) SetGroupRequirementService(service *grouprequirements.Service) {
	s.grouping = service
}

// SetCreatedWorkspaceRecorder registers the host's record of locally created
// workspaces. On startup the host wipes agent snapshots that belong only to
// workspaces it has no record of, so a Home or project created by guided
// setup and then staffed would lose its agents on the next restart whenever
// the workspace root is unconfirmed. The generic workspace API records its
// creations the same way. The recorder is called only for workspaces this
// service created, never for one it reused.
func (s *Service) SetCreatedWorkspaceRecorder(record func(workspaceID string)) {
	s.recordCreated = record
}

func (s *Service) recordCreatedWorkspace(workspaceID string) {
	if s == nil || s.recordCreated == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	s.recordCreated(workspaceID)
}

type Scope struct {
	OwnerUserID string
	RunID       string
	Template    projecttemplates.Template
}

type Request struct {
	ModeID           projecttemplates.ProjectConnectionMode `json:"mode_id"`
	SelectionToken   string                                 `json:"selection_token,omitempty"`
	EntryName        string                                 `json:"entry_name,omitempty"`
	WorkspaceName    string                                 `json:"workspace_name"`
	ProjectName      string                                 `json:"project_name,omitempty"`
	GroupComposition string                                 `json:"group_composition,omitempty"`
}

type Projection struct {
	ModeID                projecttemplates.ProjectConnectionMode `json:"mode_id"`
	WorkspaceName         string                                 `json:"workspace_name"`
	ProjectName           string                                 `json:"project_name,omitempty"`
	ParentWorkspaceName   string                                 `json:"parent_workspace_name"`
	HomeWillBeCreated     bool                                   `json:"home_will_be_created"`
	SelectedFolder        string                                 `json:"selected_folder,omitempty"`
	EntryName             string                                 `json:"entry_name"`
	EntryCandidates       []string                               `json:"entry_candidates,omitempty"`
	CreatedFiles          []string                               `json:"created_files,omitempty"`
	DefaultsStatement     string                                 `json:"defaults_statement,omitempty"`
	GroupRequirementState string                                 `json:"group_requirement_state,omitempty"`
	GroupComposition      string                                 `json:"group_composition,omitempty"`
}

type Preview struct {
	Projection    Projection
	InputDigest   string
	OwnerDigest   string
	selectedRoot  string
	selectedEntry string
}

type CommitResult struct {
	HomeWorkspaceID    string
	ProjectWorkspaceID string
	ModeID             projecttemplates.ProjectConnectionMode
}

func NormalizeRequest(request Request) Request {
	request.SelectionToken = strings.TrimSpace(request.SelectionToken)
	request.WorkspaceName = strings.TrimSpace(request.WorkspaceName)
	request.ProjectName = strings.TrimSpace(request.ProjectName)
	request.EntryName = strings.TrimSpace(request.EntryName)
	request.GroupComposition = strings.ToLower(strings.TrimSpace(request.GroupComposition))
	return request
}

func InputDigest(request Request) (string, error) {
	return digestJSON(NormalizeRequest(request))
}

func (s *Service) Preview(_ context.Context, scope Scope, request Request) (Preview, error) {
	if s == nil || s.store == nil || scope.Template.ProjectConnection == nil ||
		strings.TrimSpace(scope.OwnerUserID) == "" || strings.TrimSpace(scope.RunID) == "" {
		return Preview{}, ErrUnavailable
	}
	request = NormalizeRequest(request)
	if !validDisplayName(request.WorkspaceName) || !scope.Template.ProjectConnection.Supports(request.ModeID) {
		return Preview{}, ErrInvalid
	}
	inputDigest, err := digestJSON(request)
	if err != nil {
		return Preview{}, ErrInvalid
	}
	preview := Preview{InputDigest: inputDigest, Projection: Projection{
		ModeID: request.ModeID, WorkspaceName: request.WorkspaceName, ProjectName: request.ProjectName,
	}}
	groupOwnerDigest := ""
	if scope.Template.GroupRequirement != nil {
		if s.grouping == nil {
			return Preview{}, ErrUnavailable
		}
		operationKind := grouprequirements.OperationConnectProject
		if request.ModeID == projecttemplates.ProjectConnectionNewProject {
			operationKind = grouprequirements.OperationCreateProject
		}
		evaluation := s.grouping.Evaluate(grouprequirements.Input{
			OwnerUserID: scope.OwnerUserID, OperationKind: operationKind, Template: scope.Template,
			Composition: request.GroupComposition, InputDigest: inputDigest,
		})
		preview.Projection.GroupRequirementState = string(evaluation.State)
		preview.Projection.GroupComposition = evaluation.SelectedComposition
		switch evaluation.State {
		case grouprequirements.StateReadyStandalone:
			scope.Template = evaluation.EffectiveTemplate
		case grouprequirements.StateReadyGrouped:
			preview.Projection.ParentWorkspaceName = evaluation.HomeName
			preview.Projection.HomeWillBeCreated = evaluation.HomeWillBeCreated
		default:
			if evaluation.State == grouprequirements.StateSourceUnavailable || evaluation.State == grouprequirements.StateTargetAmbiguous {
				return Preview{}, ErrUnavailable
			}
			return Preview{}, ErrChanged
		}
		groupOwnerDigest = digestStrings(evaluation.DefinitionDigest, string(evaluation.State), evaluation.SelectedComposition, evaluation.HomeWorkspaceID)
	} else {
		if scope.Template.AssistantProgram == nil {
			return Preview{}, ErrUnavailable
		}
		key, keyErr := homeKey(scope)
		if keyErr != nil {
			return Preview{}, ErrUnavailable
		}
		station, stationErr := workspace.NewAssistantProgramStore(s.store).FindStation(key)
		if stationErr != nil && !errors.Is(stationErr, workspace.ErrAssistantStationNotFound) {
			return Preview{}, ErrUnavailable
		}
		preview.Projection.ParentWorkspaceName = scope.Template.AssistantProgram.StationName
		preview.Projection.HomeWillBeCreated = errors.Is(stationErr, workspace.ErrAssistantStationNotFound)
		if stationErr == nil && station != nil {
			preview.Projection.ParentWorkspaceName = station.Name
		}
	}
	switch request.ModeID {
	case projecttemplates.ProjectConnectionExistingProject:
		if s.selections == nil || strings.TrimSpace(request.SelectionToken) == "" || request.ProjectName != "" {
			return Preview{}, ErrInvalid
		}
		selectedRoot, resolveErr := s.selections.Resolve(request.SelectionToken)
		if resolveErr != nil {
			return Preview{}, ErrUnavailable
		}
		scan, scanErr := ScanExistingProject(selectedRoot, scope.Template.ProjectConnection.AttachExisting)
		if scanErr != nil {
			return Preview{}, ErrUnavailable
		}
		if owner, ownerErr := FindFolderOwner(s.store, scan.Root, connectionChildID(scope.RunID)); ownerErr != nil || owner != nil {
			return Preview{}, ErrChanged
		}
		entry, chooseErr := SelectProjectEntry(request.EntryName, scan.Candidates)
		if chooseErr != nil {
			return Preview{}, ErrInvalid
		}
		preview.selectedRoot = scan.Root
		preview.selectedEntry = entry
		preview.Projection.SelectedFolder = scan.Root
		preview.Projection.EntryName = entry
		preview.Projection.EntryCandidates = append([]string(nil), scan.Candidates...)
		preview.OwnerDigest = digestStrings(templateIdentity(scope.Template), groupOwnerDigest, scan.Digest, entry)
	case projecttemplates.ProjectConnectionNewProject:
		if strings.TrimSpace(request.SelectionToken) != "" || request.EntryName != "" || !validDisplayName(request.ProjectName) || !scope.Template.HasSkeleton {
			return Preview{}, ErrInvalid
		}
		entry, resolveErr := projecttemplates.ResolveProjectEntryForName(scope.Template, request.ProjectName)
		if resolveErr != nil {
			return Preview{}, ErrUnavailable
		}
		files, filesErr := projecttemplates.PreviewInstantiation(scope.Template, request.ProjectName)
		if filesErr != nil || !validCreatedProjectEntries(files, entry, scope.Template.ProjectConnection.AttachExisting) {
			return Preview{}, ErrUnavailable
		}
		preview.selectedEntry = entry
		preview.Projection.EntryName = entry
		preview.Projection.CreatedFiles = append([]string(nil), files...)
		preview.Projection.DefaultsStatement = "The starter project file begins with the blueprint's documented defaults. The project application is not opened by creation."
		preview.OwnerDigest = digestStrings(templateIdentity(scope.Template), groupOwnerDigest, request.ProjectName, entry, strings.Join(files, "\x00"))
	default:
		return Preview{}, ErrInvalid
	}
	return preview, nil
}

func (s *Service) Commit(ctx context.Context, scope Scope, request Request, reviewedInputDigest, reviewedOwnerDigest string) (CommitResult, error) {
	connectionCommitMu.Lock()
	defer connectionCommitMu.Unlock()
	request = NormalizeRequest(request)
	current, err := s.Preview(ctx, scope, request)
	if err != nil {
		return CommitResult{}, err
	}
	if current.InputDigest != reviewedInputDigest || current.OwnerDigest != reviewedOwnerDigest {
		return CommitResult{}, ErrChanged
	}
	if current.selectedEntry == "" {
		return CommitResult{}, ErrInvalid
	}

	programs := workspace.NewAssistantProgramStore(s.store)
	var home *workspace.Workspace
	homeCreated := false
	var groupSnapshot *workspace.GroupRequirementSnapshot
	if scope.Template.GroupRequirement != nil {
		if s.grouping == nil {
			return CommitResult{}, ErrUnavailable
		}
		operationKind := grouprequirements.OperationConnectProject
		if request.ModeID == projecttemplates.ProjectConnectionNewProject {
			operationKind = grouprequirements.OperationCreateProject
		}
		operationDigest := digestStrings(scope.RunID, string(request.ModeID), reviewedInputDigest, reviewedOwnerDigest)
		reviewDigest := digestStrings(reviewedInputDigest, reviewedOwnerDigest)
		effective, homeID, snapshot, groupErr := s.grouping.CommitReviewed(grouprequirements.Input{
			OwnerUserID: scope.OwnerUserID, OperationKind: operationKind, Template: scope.Template,
			Composition: request.GroupComposition, InputDigest: current.InputDigest,
		}, connectionChildID(scope.RunID), reviewDigest, operationDigest)
		if groupErr != nil {
			return CommitResult{}, ErrChanged
		}
		scope.Template = effective
		groupSnapshot = snapshot
		if homeID != "" {
			home, err = s.store.Get(homeID)
			if err != nil || home == nil {
				return CommitResult{}, ErrUnavailable
			}
		}
	} else {
		key, keyErr := homeKey(scope)
		if keyErr != nil {
			return CommitResult{}, ErrUnavailable
		}
		home, homeCreated, err = programs.EnsureStation(key, scope.Template.AssistantProgram)
		if err != nil {
			return CommitResult{}, ErrUnavailable
		}
	}

	childID := connectionChildID(scope.RunID)
	_, childLookupErr := s.store.Get(childID)
	childCreated := childLookupErr != nil
	child, err := s.ensureChild(scope, request, current, home, childID, groupSnapshot)
	if err != nil {
		s.rollbackNewState(scope.RunID, home, homeCreated, childID, childCreated)
		return CommitResult{}, err
	}
	if home != nil {
		linkedHome, _, linkErr := programs.EnsureProjectStation(child.ID)
		if linkErr != nil || linkedHome == nil || linkedHome.ID != home.ID {
			s.rollbackNewState(scope.RunID, home, homeCreated, childID, childCreated)
			return CommitResult{}, ErrUnavailable
		}
	}
	// Starter tasks can execute later, so they are persisted only after the
	// required topology (or explicit standalone snapshot) is canonical.
	if err := s.ensureStarterTasks(scope, request.ModeID, child.ID); err != nil {
		s.rollbackNewState(scope.RunID, home, homeCreated, childID, childCreated)
		return CommitResult{}, ErrUnavailable
	}
	// Recorded only once the commit can no longer roll back. The project is
	// recorded even when a retry of this run reused it: ensureChild accepts an
	// existing project only when it carries this run's marker, so it was
	// created here, possibly by an attempt that stopped before recording it.
	if homeCreated && home != nil {
		s.recordCreatedWorkspace(home.ID)
	}
	s.recordCreatedWorkspace(child.ID)
	result := CommitResult{ProjectWorkspaceID: child.ID, ModeID: request.ModeID}
	if home != nil {
		result.HomeWorkspaceID = home.ID
	}
	return result, nil
}

// ObservedResult discovers and verifies the deterministic child consequence,
// allowing a claimed operation to reconcile after a process restart before its
// journey receipt was finalized.
func (s *Service) ObservedResult(scope Scope, homeID, projectID string) (CommitResult, bool) {
	if s == nil || s.store == nil {
		return CommitResult{}, false
	}
	if projectID == "" {
		projectID = connectionChildID(scope.RunID)
	}
	project, err := s.projectRecord(projectID)
	if err != nil || project == nil {
		return CommitResult{}, false
	}
	link := project.GetAssistantProjectLink()
	locator, locatorErr := workspace.GetProjectEntryLocator(project.SharedData)
	if locatorErr != nil || locator == nil {
		return CommitResult{}, false
	}
	provenance := project.GetTemplateProvenance()
	standalone := provenance != nil && provenance.GroupRequirement != nil &&
		provenance.GroupRequirement.StructurallyValid() &&
		provenance.GroupRequirement.SelectedComposition == workspace.GroupRequirementCompositionStandalone
	if !standalone {
		if link == nil {
			return CommitResult{}, false
		}
		if homeID == "" {
			homeID = link.StationWorkspaceID
		}
	}
	mode := projecttemplates.ProjectConnectionNewProject
	if locator.Kind == workspace.ProjectEntryDirectoryReference {
		mode = projecttemplates.ProjectConnectionExistingProject
	}
	if !s.Observe(scope, homeID, projectID, mode) {
		return CommitResult{}, false
	}
	return CommitResult{HomeWorkspaceID: homeID, ProjectWorkspaceID: projectID, ModeID: mode}, true
}

// Observe verifies the durable canonical consequences for reconciliation. It
// never trusts display names, physical nesting, or a journey receipt alone.
func (s *Service) Observe(scope Scope, homeID, projectID string, mode projecttemplates.ProjectConnectionMode) bool {
	if s == nil || s.store == nil || projectID == "" || projectID != connectionChildID(scope.RunID) {
		return false
	}
	project, err := s.projectRecord(projectID)
	if err != nil || project == nil || project.OwnerUserID != scope.OwnerUserID || project.SharedData[connectionRunKey] != scope.RunID {
		return false
	}
	provenance := project.GetTemplateProvenance()
	link := project.GetAssistantProjectLink()
	if provenance == nil || provenance.TemplateID != scope.Template.ID {
		return false
	}
	if snapshot := provenance.GroupRequirement; snapshot != nil {
		if scope.Template.GroupRequirement == nil || !snapshot.StructurallyValid() || snapshot.Policy != string(scope.Template.GroupRequirement.Policy) {
			return false
		}
		if snapshot.SelectedComposition == workspace.GroupRequirementCompositionStandalone {
			if homeID != "" || link != nil {
				return false
			}
		} else {
			if homeID == "" || link == nil || snapshot.ProgramKey == nil || project.ParentID != homeID ||
				link.StationWorkspaceID != homeID || link.ID != snapshot.ProjectLinkID ||
				link.Key.Normalize() != snapshot.ProgramKey.Normalize() {
				return false
			}
			station, stationErr := s.store.Get(homeID)
			if stationErr != nil || station == nil {
				return false
			}
			state := station.GetAssistantProgramState()
			if state == nil || !containsProjectID(state.LinkedProjectIDs, project.ID) {
				return false
			}
		}
	} else {
		if scope.Template.AssistantProgram == nil || homeID == "" {
			return false
		}
		expectedKey, keyErr := homeKey(scope)
		if link == nil || keyErr != nil || link.StationWorkspaceID != homeID ||
			!templateProvenanceMatches(scope.Template, provenance) ||
			link.Key.Normalize() != expectedKey || link.Key.ProgramID != scope.Template.AssistantProgram.ID {
			return false
		}
	}
	locator, err := workspace.GetProjectEntryLocator(project.SharedData)
	if err != nil || locator == nil ||
		(mode == projecttemplates.ProjectConnectionExistingProject && locator.Kind != workspace.ProjectEntryDirectoryReference) ||
		(mode == projecttemplates.ProjectConnectionNewProject && locator.Kind != workspace.ProjectEntryManagedWorkspace) {
		return false
	}
	root, err := s.store.GetFolderPath(project.ID)
	if err != nil {
		return false
	}
	_, err = workspace.ResolveProjectEntry(project, root)
	return err == nil
}

// projectRecord overlays folder-owned facts on the primary identity. SQLite
// does not store the project path or template provenance; its absence there is
// not evidence that a successful project creation failed.
func (s *Service) projectRecord(id string) (*workspace.Workspace, error) {
	project, err := s.store.Get(id)
	if err != nil || project == nil {
		return project, err
	}
	if reader, ok := s.store.(interface {
		GetFolderWorkspace(string) (*workspace.Workspace, error)
	}); ok {
		canonical, readErr := reader.GetFolderWorkspace(id)
		if readErr != nil || canonical == nil || canonical.ID != project.ID {
			return nil, ErrUnavailable
		}
		project.ProjectPath = canonical.ProjectPath
		project.TemplateProvenance = canonical.TemplateProvenance
	}
	return project, nil
}

func (s *Service) ensureChild(scope Scope, request Request, preview Preview, home *workspace.Workspace, childID string, groupSnapshot *workspace.GroupRequirementSnapshot) (*workspace.Workspace, error) {
	expectedParentID := ""
	if home != nil {
		expectedParentID = home.ID
	}
	if existing, err := s.store.Get(childID); err == nil && existing != nil {
		existing, err = s.projectRecord(childID)
		if err != nil {
			return nil, ErrUnavailable
		}
		if existing.OwnerUserID != scope.OwnerUserID || existing.ParentID != expectedParentID || existing.SharedData[connectionRunKey] != scope.RunID {
			return nil, ErrChanged
		}
		if groupSnapshot != nil {
			provenance := existing.GetTemplateProvenance()
			if provenance == nil || provenance.GroupRequirement == nil ||
				provenance.GroupRequirement.OperationDigest != groupSnapshot.OperationDigest {
				return nil, ErrChanged
			}
		}
		if _, resolveErr := workspace.ResolveProjectEntry(existing, mustFolderPath(s.store, existing.ID)); resolveErr == nil {
			return existing, nil
		}
		if request.ModeID == projecttemplates.ProjectConnectionExistingProject {
			return nil, ErrChanged
		}
		return s.finishManagedProject(existing, scope.Template, request.ProjectName, preview.selectedEntry)
	}

	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: request.WorkspaceName})
	child.ID = childID
	child.OwnerUserID = scope.OwnerUserID
	child.ParentID = expectedParentID
	child.SharedData = map[string]any{connectionRunKey: scope.RunID}
	child.SetTemplateProvenance(templateProvenance(scope.Template, s.now(), groupSnapshot))
	if request.ModeID == projecttemplates.ProjectConnectionExistingProject {
		if err := RecordAttachedProject(child, request.WorkspaceName, preview.selectedRoot, preview.selectedEntry, connectionReferenceID(scope.RunID)); err != nil {
			return nil, err
		}
	}
	if err := s.store.Save(child); err != nil {
		return nil, ErrUnavailable
	}
	if request.ModeID == projecttemplates.ProjectConnectionNewProject {
		return s.finishManagedProject(child, scope.Template, request.ProjectName, preview.selectedEntry)
	}
	return s.store.Get(child.ID)
}

func (s *Service) finishManagedProject(child *workspace.Workspace, template projecttemplates.Template, projectName, expectedEntry string) (*workspace.Workspace, error) {
	root, err := s.store.GetFolderPath(child.ID)
	if err != nil {
		return nil, ErrUnavailable
	}
	projectPath, err := projecttemplates.SanitizeProjectName(projectName)
	if err != nil {
		return nil, ErrInvalid
	}
	entryTarget := filepath.Join(root, filepath.FromSlash(projectPath), filepath.FromSlash(expectedEntry))
	if _, statErr := os.Lstat(entryTarget); statErr != nil {
		result, instantiateErr := projecttemplates.InstantiateTemplate(template, root, projectName)
		if instantiateErr != nil || result.ProjectPath != projectPath || result.ProjectEntryPath != expectedEntry {
			return nil, ErrUnavailable
		}
	}
	if err := s.store.Update(child.ID, func(current *workspace.Workspace) error {
		if current.SharedData[connectionRunKey] != child.SharedData[connectionRunKey] {
			return ErrChanged
		}
		current.ProjectPath = projectPath
		return workspace.SetProjectEntryPath(current.SharedData, expectedEntry)
	}); err != nil {
		return nil, ErrUnavailable
	}
	return s.store.Get(child.ID)
}

func (s *Service) rollbackNewState(runID string, home *workspace.Workspace, homeCreated bool, childID string, childCreated bool) {
	if childCreated {
		if child, err := s.store.Get(childID); err == nil && child != nil && child.SharedData[connectionRunKey] == runID && child.GetAssistantProjectLink() == nil {
			_ = s.store.Delete(childID)
		}
	}
	if homeCreated && home != nil {
		if current, err := s.store.Get(home.ID); err == nil && current != nil {
			state := current.GetAssistantProgramState()
			if state != nil && !state.Hired && len(state.LinkedProjectIDs) == 0 {
				_ = s.store.Delete(home.ID)
			}
		}
	}
}

func (s *Service) ensureStarterTasks(scope Scope, mode projecttemplates.ProjectConnectionMode, childID string) error {
	starters, err := projecttemplates.StarterTasksForConnection(scope.Template, mode)
	if err != nil {
		return err
	}
	return s.store.Update(childID, func(current *workspace.Workspace) error {
		existing := make(map[string]struct{}, len(current.Tasks))
		for _, task := range current.Tasks {
			existing[task.ID] = struct{}{}
		}
		additions := make([]workspace.Task, 0, len(starters))
		for index, starter := range starters {
			id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("ori.setup-journey.starter-task\x00"+scope.RunID+"\x00"+strconv.Itoa(index))).String()
			if _, ok := existing[id]; ok {
				continue
			}
			context := map[string]any{"template_id": scope.Template.ID, "template_starter_task": true}
			if starter.Setup {
				context["template_setup"] = true
			}
			task := workspace.Task{
				ID: id, WorkspaceID: childID, Description: starter.Description, Details: starter.Details,
				Priority: 1, Status: workspace.TaskStatusPending,
				RequiredCapabilities: workspace.NormalizeCapabilityKeys(starter.Requires),
				FileFallbackFor:      workspace.NormalizeCapabilityKeys(starter.FileFallbackFor), Context: context,
			}
			current.ApplyEntryAgentDefault(&task)
			additions = append(additions, task)
		}
		if len(additions) == 0 {
			return nil
		}
		return current.AddTasks(additions)
	})
}

func validCreatedProjectEntries(files []string, authoritative string, declaration *projecttemplates.AttachExistingDeclaration) bool {
	if declaration == nil {
		return true
	}
	allowed := make(map[string]struct{}, len(declaration.EntryExtensions))
	for _, extension := range declaration.EntryExtensions {
		allowed[strings.ToLower(extension)] = struct{}{}
	}
	matches := make([]string, 0, 1)
	for _, file := range files {
		if _, ok := allowed[strings.ToLower(filepath.Ext(file))]; ok {
			matches = append(matches, file)
		}
	}
	return len(matches) == 1 && matches[0] == authoritative
}

func validDisplayName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func containsProjectID(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func templateIdentity(template projecttemplates.Template) string {
	if template.AssistantProgram == nil {
		return ""
	}
	if owner := template.PluginOwner; owner != nil {
		return digestStrings(template.ID, owner.PluginID, owner.PluginVersion, owner.BlueprintID, strconv.Itoa(owner.BlueprintVersion), template.AssistantProgram.ID, strconv.Itoa(template.AssistantProgram.SchemaVersion))
	}
	if template.UserSetupQuest != nil && template.UserSetupQuest.Declaration != nil {
		return digestStrings("user_template", template.ID, template.UserSetupQuest.AttachmentID,
			projecttemplates.UserSetupQuestDefinitionDigest(template.UserSetupQuest), projecttemplates.UserSetupQuestExecutionDigest(template),
			template.AssistantProgram.ID, strconv.Itoa(template.AssistantProgram.SchemaVersion))
	}
	return ""
}

func templateProvenanceMatches(template projecttemplates.Template, provenance *workspace.TemplateProvenance) bool {
	if provenance == nil || template.AssistantProgram == nil || provenance.AssistantProgram == nil {
		return false
	}
	if template.PluginOwner != nil {
		return provenance.UserTemplateOwner == nil && provenance.PluginOwner != nil &&
			provenance.PluginOwner.PluginID == template.PluginOwner.PluginID &&
			provenance.PluginOwner.PluginVersion == template.PluginOwner.PluginVersion
	}
	if template.UserSetupQuest == nil || template.UserSetupQuest.Declaration == nil || provenance.PluginOwner != nil || provenance.UserTemplateOwner == nil {
		return false
	}
	owner := provenance.UserTemplateOwner
	return owner.TemplateID == template.ID && owner.AttachmentID == template.UserSetupQuest.AttachmentID &&
		owner.QuestID == template.UserSetupQuest.Declaration.ID &&
		owner.DefinitionDigest == projecttemplates.UserSetupQuestDefinitionDigest(template.UserSetupQuest) &&
		owner.ExecutionDigest == projecttemplates.UserSetupQuestExecutionDigest(template)
}

func templateProvenance(template projecttemplates.Template, now time.Time, snapshots ...*workspace.GroupRequirementSnapshot) *workspace.TemplateProvenance {
	version := template.BuiltinVersion
	if template.PluginOwner != nil {
		version = template.PluginOwner.BlueprintVersion
	}
	var userOwner *workspace.UserTemplateOwner
	if template.PluginOwner == nil && template.UserSetupQuest != nil && template.UserSetupQuest.Declaration != nil {
		userOwner = &workspace.UserTemplateOwner{
			TemplateID: template.ID, AttachmentID: template.UserSetupQuest.AttachmentID,
			QuestID:          template.UserSetupQuest.Declaration.ID,
			DefinitionDigest: projecttemplates.UserSetupQuestDefinitionDigest(template.UserSetupQuest),
			ExecutionDigest:  projecttemplates.UserSetupQuestExecutionDigest(template),
		}
	}
	var snapshot *workspace.GroupRequirementSnapshot
	if len(snapshots) > 0 {
		snapshot = snapshots[0]
	}
	return &workspace.TemplateProvenance{
		TemplateID: template.ID, TemplateName: template.Name, Builtin: template.Builtin, Version: version, AppliedAt: now,
		PluginOwner: template.PluginOwner, UserTemplateOwner: userOwner, DirectoryRequirements: template.DirectoryRequirements,
		AutomationRecipes: template.AutomationRecipes, IntakeRequirements: template.IntakeRequirements, CapabilityRequirements: template.CapabilityRequirements,
		Plugins: template.Tools.Plugins, PluginSources: template.Tools.PluginSources,
		RuntimeRequirements: template.RuntimeRequirements, SetupWizard: template.SetupWizard,
		AssistantProgram: template.AssistantProgram, GroupRequirement: snapshot,
	}
}

func connectionChildID(runID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("ori.setup-journey.project\x00"+runID)).String()
}

func connectionReferenceID(runID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("ori.setup-journey.directory-reference\x00"+runID)).String()
}

func mustFolderPath(store folderStore, id string) string {
	path, _ := store.GetFolderPath(id)
	return path
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func digestStrings(values ...string) string {
	digest := sha256.New()
	for _, value := range values {
		_, _ = digest.Write([]byte(value))
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}
