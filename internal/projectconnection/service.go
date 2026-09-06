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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
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
	now        func() time.Time
}

func NewService(store folderStore, selections SelectionResolver) *Service {
	return &Service{store: store, selections: selections, now: func() time.Time { return time.Now().UTC() }}
}

type Scope struct {
	OwnerUserID string
	RunID       string
	Template    projecttemplates.Template
}

type Request struct {
	ModeID         projecttemplates.ProjectConnectionMode `json:"mode_id"`
	SelectionToken string                                 `json:"selection_token,omitempty"`
	EntryName      string                                 `json:"entry_name,omitempty"`
	WorkspaceName  string                                 `json:"workspace_name"`
	ProjectName    string                                 `json:"project_name,omitempty"`
}

type Projection struct {
	ModeID              projecttemplates.ProjectConnectionMode `json:"mode_id"`
	WorkspaceName       string                                 `json:"workspace_name"`
	ProjectName         string                                 `json:"project_name,omitempty"`
	ParentWorkspaceName string                                 `json:"parent_workspace_name"`
	HomeWillBeCreated   bool                                   `json:"home_will_be_created"`
	SelectedFolder      string                                 `json:"selected_folder,omitempty"`
	EntryName           string                                 `json:"entry_name"`
	EntryCandidates     []string                               `json:"entry_candidates,omitempty"`
	CreatedFiles        []string                               `json:"created_files,omitempty"`
	DefaultsStatement   string                                 `json:"defaults_statement,omitempty"`
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
	return request
}

func InputDigest(request Request) (string, error) {
	return digestJSON(NormalizeRequest(request))
}

func (s *Service) Preview(_ context.Context, scope Scope, request Request) (Preview, error) {
	if s == nil || s.store == nil || scope.Template.ProjectConnection == nil || scope.Template.PluginOwner == nil ||
		scope.Template.AssistantProgram == nil || strings.TrimSpace(scope.OwnerUserID) == "" || strings.TrimSpace(scope.RunID) == "" {
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
	key := workspace.AssistantProgramKey{
		OwnerUserID: scope.OwnerUserID, PluginID: scope.Template.PluginOwner.PluginID,
		ProgramID: scope.Template.AssistantProgram.ID,
	}.Normalize()
	station, stationErr := workspace.NewAssistantProgramStore(s.store).FindStation(key)
	if stationErr != nil && !errors.Is(stationErr, workspace.ErrAssistantStationNotFound) {
		return Preview{}, ErrUnavailable
	}
	preview := Preview{InputDigest: inputDigest, Projection: Projection{
		ModeID: request.ModeID, WorkspaceName: request.WorkspaceName, ProjectName: request.ProjectName,
		ParentWorkspaceName: scope.Template.AssistantProgram.StationName,
		HomeWillBeCreated:   errors.Is(stationErr, workspace.ErrAssistantStationNotFound),
	}}
	if stationErr == nil && station != nil {
		preview.Projection.ParentWorkspaceName = station.Name
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
		root, candidates, scanDigest, scanErr := scanExistingProject(selectedRoot, scope.Template.ProjectConnection.AttachExisting)
		if scanErr != nil {
			return Preview{}, scanErr
		}
		if s.ownedByAnotherProject(scope.RunID, root) {
			return Preview{}, ErrChanged
		}
		entry, chooseErr := selectEntry(request.EntryName, candidates)
		if chooseErr != nil {
			return Preview{}, chooseErr
		}
		preview.selectedRoot = root
		preview.selectedEntry = entry
		preview.Projection.SelectedFolder = root
		preview.Projection.EntryName = entry
		preview.Projection.EntryCandidates = append([]string(nil), candidates...)
		preview.OwnerDigest = digestStrings(templateIdentity(scope.Template), scanDigest, entry)
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
		preview.OwnerDigest = digestStrings(templateIdentity(scope.Template), request.ProjectName, entry, strings.Join(files, "\x00"))
	default:
		return Preview{}, ErrInvalid
	}
	return preview, nil
}

func (s *Service) Commit(ctx context.Context, scope Scope, request Request, reviewedInputDigest, reviewedOwnerDigest string) (CommitResult, error) {
	connectionCommitMu.Lock()
	defer connectionCommitMu.Unlock()
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
	key := workspace.AssistantProgramKey{
		OwnerUserID: scope.OwnerUserID,
		PluginID:    scope.Template.PluginOwner.PluginID,
		ProgramID:   scope.Template.AssistantProgram.ID,
	}.Normalize()
	programs := workspace.NewAssistantProgramStore(s.store)
	home, homeCreated, err := programs.EnsureStation(key, scope.Template.AssistantProgram)
	if err != nil {
		return CommitResult{}, ErrUnavailable
	}
	childID := connectionChildID(scope.RunID)
	_, childLookupErr := s.store.Get(childID)
	childCreated := childLookupErr != nil
	child, err := s.ensureChild(scope, request, current, home, childID)
	if err != nil {
		s.rollbackNewState(scope.RunID, home, homeCreated, childID, childCreated)
		return CommitResult{}, err
	}
	if err := s.ensureStarterTasks(scope, request.ModeID, child.ID); err != nil {
		s.rollbackNewState(scope.RunID, home, homeCreated, childID, childCreated)
		return CommitResult{}, ErrUnavailable
	}
	linkedHome, _, err := programs.EnsureProjectStation(child.ID)
	if err != nil || linkedHome.ID != home.ID {
		s.rollbackNewState(scope.RunID, home, homeCreated, childID, childCreated)
		return CommitResult{}, ErrUnavailable
	}
	return CommitResult{HomeWorkspaceID: home.ID, ProjectWorkspaceID: child.ID, ModeID: request.ModeID}, nil
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
	if link == nil || locatorErr != nil || locator == nil {
		return CommitResult{}, false
	}
	if homeID == "" {
		homeID = link.StationWorkspaceID
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
	if s == nil || s.store == nil || scope.Template.PluginOwner == nil || scope.Template.AssistantProgram == nil ||
		projectID == "" || homeID == "" || projectID != connectionChildID(scope.RunID) {
		return false
	}
	project, err := s.projectRecord(projectID)
	if err != nil || project == nil || project.OwnerUserID != scope.OwnerUserID || project.SharedData[connectionRunKey] != scope.RunID {
		return false
	}
	provenance := project.GetTemplateProvenance()
	link := project.GetAssistantProjectLink()
	if provenance == nil || provenance.PluginOwner == nil || link == nil || link.StationWorkspaceID != homeID ||
		provenance.TemplateID != scope.Template.ID || provenance.PluginOwner.PluginID != scope.Template.PluginOwner.PluginID ||
		provenance.PluginOwner.PluginVersion != scope.Template.PluginOwner.PluginVersion ||
		link.Key.OwnerUserID != scope.OwnerUserID || link.Key.PluginID != scope.Template.PluginOwner.PluginID ||
		link.Key.ProgramID != scope.Template.AssistantProgram.ID {
		return false
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

func (s *Service) ensureChild(scope Scope, request Request, preview Preview, home *workspace.Workspace, childID string) (*workspace.Workspace, error) {
	if existing, err := s.store.Get(childID); err == nil && existing != nil {
		existing, err = s.projectRecord(childID)
		if err != nil {
			return nil, ErrUnavailable
		}
		if existing.OwnerUserID != scope.OwnerUserID || existing.ParentID != home.ID || existing.SharedData[connectionRunKey] != scope.RunID {
			return nil, ErrChanged
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
	child.ParentID = home.ID
	child.SharedData = map[string]any{connectionRunKey: scope.RunID}
	child.SetTemplateProvenance(templateProvenance(scope.Template, s.now()))
	if request.ModeID == projecttemplates.ProjectConnectionExistingProject {
		referenceID := connectionReferenceID(scope.RunID)
		if err := child.AddDirectoryReference(workspace.DirectoryReference{ID: referenceID, Name: request.WorkspaceName, Path: preview.selectedRoot}); err != nil {
			return nil, ErrUnavailable
		}
		if err := workspace.SetProjectEntryLocator(child.SharedData, workspace.ProjectEntryLocator{
			SchemaVersion: workspace.ProjectEntryLocatorSchemaVersion,
			Kind:          workspace.ProjectEntryDirectoryReference, DirectoryReferenceID: referenceID,
			RelativePath: preview.selectedEntry,
		}); err != nil {
			return nil, ErrInvalid
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

func (s *Service) ownedByAnotherProject(runID, selectedRoot string) bool {
	selectedInfo, err := os.Stat(selectedRoot)
	if err != nil {
		return true
	}
	ids, err := s.store.List()
	if err != nil {
		return true
	}
	expectedChildID := connectionChildID(runID)
	for _, id := range ids {
		candidate, getErr := s.store.Get(id)
		if getErr != nil || candidate == nil || candidate.ID == expectedChildID {
			continue
		}
		locator, locatorErr := workspace.GetProjectEntryLocator(candidate.SharedData)
		if locatorErr != nil || locator == nil || locator.Kind != workspace.ProjectEntryDirectoryReference {
			continue
		}
		reference, referenceErr := candidate.GetDirectoryReference(locator.DirectoryReferenceID)
		if referenceErr != nil || reference == nil {
			continue
		}
		info, statErr := os.Stat(reference.Path)
		if statErr == nil && os.SameFile(selectedInfo, info) {
			return true
		}
	}
	return false
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

func scanExistingProject(selected string, declaration *projecttemplates.AttachExistingDeclaration) (string, []string, string, error) {
	if declaration == nil || len(declaration.EntryExtensions) == 0 {
		return "", nil, "", ErrUnavailable
	}
	root, err := filepath.Abs(filepath.Clean(strings.TrimSpace(selected)))
	if err != nil || !filepath.IsAbs(root) {
		return "", nil, "", ErrUnavailable
	}
	info, err := os.Lstat(root) // #nosec G304 -- root came only from Ori's trusted native picker token
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", nil, "", ErrUnavailable
	}
	entries, err := os.ReadDir(root) // #nosec G304 -- exact no-follow picker root checked above
	if err != nil {
		return "", nil, "", ErrUnavailable
	}
	allowed := make(map[string]struct{}, len(declaration.EntryExtensions))
	for _, extension := range declaration.EntryExtensions {
		allowed[strings.ToLower(extension)] = struct{}{}
	}
	candidates := make([]string, 0)
	facts := []string{root, info.ModTime().UTC().Format(time.RFC3339Nano)}
	for _, entry := range entries {
		if len(candidates) >= maxEntryCandidates {
			return "", nil, "", ErrUnavailable
		}
		if _, ok := allowed[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			continue
		}
		entryInfo, statErr := os.Lstat(filepath.Join(root, entry.Name())) // #nosec G304 -- one direct child of the checked root
		if statErr != nil || entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, entry.Name())
		facts = append(facts, entry.Name(), entryInfo.Mode().String(), entryInfo.ModTime().UTC().Format(time.RFC3339Nano), strconv.FormatInt(entryInfo.Size(), 10))
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return "", nil, "", ErrUnavailable
	}
	return root, candidates, digestStrings(facts...), nil
}

func selectEntry(requested string, candidates []string) (string, error) {
	if requested == "" {
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		// A review may return the bounded exact candidates before consent is
		// issued for one. The client repeats the review with one exact name;
		// commit still rejects an empty selection.
		return "", nil
	}
	if filepath.Base(requested) != requested || strings.ContainsAny(requested, `/\\`) {
		return "", ErrInvalid
	}
	for _, candidate := range candidates {
		if candidate == requested {
			return candidate, nil
		}
	}
	return "", ErrInvalid
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

func templateIdentity(template projecttemplates.Template) string {
	owner := template.PluginOwner
	if owner == nil {
		return ""
	}
	return digestStrings(template.ID, owner.PluginID, owner.PluginVersion, owner.BlueprintID, strconv.Itoa(owner.BlueprintVersion), template.AssistantProgram.ID, strconv.Itoa(template.AssistantProgram.SchemaVersion))
}

func templateProvenance(template projecttemplates.Template, now time.Time) *workspace.TemplateProvenance {
	version := template.BuiltinVersion
	if template.PluginOwner != nil {
		version = template.PluginOwner.BlueprintVersion
	}
	return &workspace.TemplateProvenance{
		TemplateID: template.ID, TemplateName: template.Name, Builtin: template.Builtin, Version: version, AppliedAt: now,
		PluginOwner: template.PluginOwner, DirectoryRequirements: template.DirectoryRequirements,
		AutomationRecipes: template.AutomationRecipes, CapabilityRequirements: template.CapabilityRequirements,
		Plugins: template.Tools.Plugins, PluginSources: template.Tools.PluginSources,
		RuntimeRequirements: template.RuntimeRequirements, SetupWizard: template.SetupWizard,
		AssistantProgram: template.AssistantProgram,
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
