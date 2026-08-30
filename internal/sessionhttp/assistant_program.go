package sessionhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxAssistantProgramRequestBytes = 32 << 10

var assistantHireMu sync.Mutex

type assistantProgramHireRequest struct {
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Version  int64  `json:"version"`
}

type assistantProgramSummary struct {
	Available        bool                                   `json:"available"`
	ActivationNeeded bool                                   `json:"activation_needed,omitempty"`
	StationID        string                                 `json:"station_id,omitempty"`
	ProjectID        string                                 `json:"project_id,omitempty"`
	IsStation        bool                                   `json:"is_station,omitempty"`
	Hired            bool                                   `json:"hired,omitempty"`
	PluginAvailable  bool                                   `json:"plugin_available"`
	StateRevision    int64                                  `json:"state_revision,omitempty"`
	PrimaryName      string                                 `json:"primary_name,omitempty"`
	Provider         string                                 `json:"provider,omitempty"`
	Model            string                                 `json:"model,omitempty"`
	StageID          string                                 `json:"stage_id,omitempty"`
	StageLabel       string                                 `json:"stage_label,omitempty"`
	Level            int                                    `json:"level,omitempty"`
	AcceptedTasks    int                                    `json:"accepted_tasks,omitempty"`
	NextThreshold    int                                    `json:"next_threshold,omitempty"`
	Remaining        int                                    `json:"remaining,omitempty"`
	PromotionPending bool                                   `json:"promotion_pending,omitempty"`
	Declaration      *workspace.AssistantProgramDeclaration `json:"declaration,omitempty"`
	Roster           []workspace.AssistantRoleBinding       `json:"roster,omitempty"`
	Projects         []assistantProgramProject              `json:"projects,omitempty"`
}

type assistantProgramProject struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FolderSlug string `json:"folder_slug"`
	Status     string `json:"status"`
}

func (h *Handler) decodeAssistantProgramJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAssistantProgramRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		_ = orihttp.RespondBadRequest(w, "Invalid assistant program request")
		return false
	}
	return true
}

func (h *Handler) assistantProgramStation(workspaceID string) (*workspace.Workspace, *workspace.Workspace, error) {
	if h == nil || h.workspaceTaskStore == nil {
		return nil, nil, errors.New("assistant program storage is unavailable")
	}
	current, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if current.GetAssistantProgramState() != nil {
		return current, nil, nil
	}
	link := current.GetAssistantProjectLink()
	if link == nil {
		return nil, current, workspace.ErrAssistantProgramUnavailable
	}
	station, err := h.workspaceTaskStore.Get(link.StationWorkspaceID)
	if err != nil || station.GetAssistantProgramState() == nil {
		return nil, current, workspace.ErrAssistantStationNotFound
	}
	return station, current, nil
}

func (h *Handler) syncAssistantPluginAvailability(station *workspace.Workspace) *workspace.Workspace {
	if h == nil || h.installedPluginLister == nil || h.workspaceTaskStore == nil || station == nil {
		return station
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return station
	}
	installed, err := h.installedPluginLister.List()
	available := false
	if err != nil {
		installed = nil
	}
	for _, candidate := range installed {
		if candidate.Enabled && strings.EqualFold(strings.TrimSpace(candidate.Name), state.Key.PluginID) {
			available = true
			break
		}
	}
	if available != state.PluginAvailable {
		if err := workspace.NewAssistantProgramStore(h.workspaceTaskStore).SetPluginAvailable(station.ID, available); err == nil {
			station, _ = h.workspaceTaskStore.Get(station.ID)
		}
	}
	return station
}

func (h *Handler) requireAssistantWritable(w http.ResponseWriter, station *workspace.Workspace) (*workspace.Workspace, bool) {
	station = h.syncAssistantPluginAvailability(station)
	state := station.GetAssistantProgramState()
	if state == nil || !state.PluginAvailable {
		_ = orihttp.RespondConflict(w, "The assistant contribution is disabled; existing data is read-only")
		return station, false
	}
	return station, true
}

func (h *Handler) buildAssistantProgramSummary(station, project *workspace.Workspace) (assistantProgramSummary, error) {
	station = h.syncAssistantPluginAvailability(station)
	state := station.GetAssistantProgramState()
	if state == nil || state.Declaration == nil {
		return assistantProgramSummary{}, workspace.ErrAssistantStationNotFound
	}
	summary := assistantProgramSummary{
		Available: true, StationID: station.ID, IsStation: project == nil,
		Hired: state.Hired, PluginAvailable: state.PluginAvailable, StateRevision: state.StateRevision,
		PrimaryName: state.PrimaryName, Provider: state.Provider, Model: state.Model,
		StageID: state.StageID, Level: state.Level, AcceptedTasks: state.AcceptedCompletions,
		Declaration: workspace.CloneAssistantProgramDeclaration(state.Declaration),
		Roster:      append([]workspace.AssistantRoleBinding(nil), state.Roster...),
	}
	if project != nil {
		summary.ProjectID = project.ID
	}
	for index, stage := range state.Declaration.Stages {
		if stage.ID == state.StageID || state.StageID == "" && index == 0 {
			summary.StageLabel = stage.Label
			if index+1 < len(state.Declaration.Stages) {
				summary.NextThreshold = state.Declaration.Stages[index+1].AcceptedCompletionThreshold
				summary.Remaining = summary.NextThreshold - state.AcceptedCompletions
				if summary.Remaining < 0 {
					summary.Remaining = 0
				}
			}
			break
		}
	}
	summary.PromotionPending = state.PromotionReceipt != nil && state.PromotionReceipt.AcknowledgedAt == nil
	projects, err := workspace.NewAssistantProgramStore(h.workspaceTaskStore).LinkedProjects(station.ID)
	if err != nil {
		return assistantProgramSummary{}, err
	}
	for _, linked := range projects {
		summary.Projects = append(summary.Projects, assistantProgramProject{ID: linked.ID, Name: linked.Name, FolderSlug: linked.FolderSlug, Status: string(linked.Status)})
	}
	sort.Slice(summary.Projects, func(i, j int) bool {
		if summary.Projects[i].Name == summary.Projects[j].Name {
			return summary.Projects[i].ID < summary.Projects[j].ID
		}
		return summary.Projects[i].Name < summary.Projects[j].Name
	})
	return summary, nil
}

func (h *Handler) GetAssistantProgram(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	station, project, err := h.assistantProgramStation(workspaceID)
	if errors.Is(err, workspace.ErrAssistantProgramUnavailable) && project != nil {
		provenance := project.GetTemplateProvenance()
		activationNeeded := provenance != nil && provenance.PluginOwner != nil
		_ = orihttp.RespondSuccess(w, assistantProgramSummary{Available: false, ActivationNeeded: activationNeeded, ProjectID: project.ID})
		return
	}
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	summary, err := h.buildAssistantProgramSummary(station, project)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Assistant program could not be read")
		return
	}
	_ = orihttp.RespondSuccess(w, summary)
}

func (h *Handler) ActivateAssistantProgram(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if h == nil || h.workspaceTaskStore == nil {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant program storage is unavailable")
		return
	}
	project, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if project.GetAssistantProjectLink() == nil {
		provenance := project.GetTemplateProvenance()
		if provenance == nil || provenance.PluginOwner == nil || h.projectTemplateResolver == nil {
			_ = orihttp.RespondBadRequest(w, "This workspace cannot activate an assistant program")
			return
		}
		template, resolveErr := h.projectTemplateResolver(provenance.TemplateID, "")
		if resolveErr != nil || template.AssistantProgram == nil || template.PluginOwner == nil ||
			!strings.EqualFold(template.PluginOwner.PluginID, provenance.PluginOwner.PluginID) {
			_ = orihttp.RespondConflict(w, "A compatible assistant program is not available")
			return
		}
		if h.installedPluginLister != nil {
			installed, listErr := h.installedPluginLister.List()
			enabled := false
			for _, candidate := range installed {
				if candidate.Enabled && strings.EqualFold(strings.TrimSpace(candidate.Name), provenance.PluginOwner.PluginID) {
					enabled = true
					break
				}
			}
			if listErr != nil || !enabled {
				_ = orihttp.RespondConflict(w, "The compatible assistant contribution is disabled")
				return
			}
		}
		if err := h.workspaceTaskStore.Update(project.ID, func(current *workspace.Workspace) error {
			currentProvenance := current.GetTemplateProvenance()
			if currentProvenance == nil || currentProvenance.PluginOwner == nil {
				return workspace.ErrAssistantProgramUnavailable
			}
			currentProvenance.AssistantProgram = workspace.CloneAssistantProgramDeclaration(template.AssistantProgram)
			current.SetTemplateProvenance(currentProvenance)
			return nil
		}); err != nil {
			_ = orihttp.RespondInternalError(w, "Assistant activation could not be persisted")
			return
		}
	}
	station, _, err := workspace.NewAssistantProgramStore(h.workspaceTaskStore).EnsureProjectStation(project.ID)
	if err != nil {
		_ = orihttp.RespondConflict(w, "Assistant activation could not be completed")
		return
	}
	summary, err := h.buildAssistantProgramSummary(station, project)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Assistant activation could not be read")
		return
	}
	_ = orihttp.RespondSuccess(w, summary)
}

func normalizeAssistantHireName(value string) (string, error) {
	name := strings.Join(strings.Fields(value), " ")
	if err := validateTemplateAgentOverrideName(name); err != nil {
		return "", err
	}
	return name, nil
}

func assistantRoleAgentNames(primaryName string, declaration *workspace.AssistantProgramDeclaration) ([]string, error) {
	if declaration == nil || len(declaration.Roles) == 0 {
		return nil, workspace.ErrAssistantProgramUnavailable
	}
	names := make([]string, len(declaration.Roles))
	seen := make(map[string]struct{}, len(declaration.Roles))
	for index, role := range declaration.Roles {
		name := strings.TrimSpace(role.Label)
		if role.Primary {
			name = primaryName
		}
		key := strings.ToLower(name)
		if name == "" {
			return nil, errors.New("assistant role name is empty")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("assistant role name %q is duplicated", name)
		}
		seen[key] = struct{}{}
		names[index] = name
	}
	return names, nil
}

func (h *Handler) existingAgentNameConflict(names []string) string {
	if h == nil || h.agentStore == nil {
		return "agent storage is unavailable"
	}
	wanted := make(map[string]string, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = name
	}
	for _, existing := range h.agentStore.ListAgents() {
		if name, conflict := wanted[strings.ToLower(strings.TrimSpace(existing))]; conflict {
			return name
		}
	}
	return ""
}

func assistantRosterInstances(names []string, declaration *workspace.AssistantProgramDeclaration) ([]workspace.AgentInstance, []workspace.AssistantRoleBinding) {
	instances := workspace.AgentInstancesFromNames(names...)
	bindings := make([]workspace.AssistantRoleBinding, 0, len(instances))
	for index := range instances {
		role := declaration.Roles[index]
		instances[index].Role = role.Label
		instances[index].Description = role.Description
		instances[index].EntryPoint = role.Primary
		bindings = append(bindings, workspace.AssistantRoleBinding{RoleID: role.ID, AgentInstanceID: instances[index].ID, AgentName: instances[index].Name})
	}
	return instances, bindings
}

func mergeAssistantRoster(existing, roster []workspace.AgentInstance) ([]workspace.AgentInstance, error) {
	rosterNames := make(map[string]struct{}, len(roster))
	out := append([]workspace.AgentInstance(nil), roster...)
	for _, instance := range roster {
		rosterNames[strings.ToLower(strings.TrimSpace(instance.Name))] = struct{}{}
	}
	for _, instance := range existing {
		if _, conflict := rosterNames[strings.ToLower(strings.TrimSpace(instance.Name))]; conflict {
			return nil, fmt.Errorf("workspace already contains assistant role %q", instance.Name)
		}
		out = append(out, instance)
	}
	return out, nil
}

func cloneAgentInstances(values []workspace.AgentInstance) []workspace.AgentInstance {
	return append([]workspace.AgentInstance(nil), values...)
}

func (h *Handler) HireAssistantProgram(w http.ResponseWriter, r *http.Request) {
	var request assistantProgramHireRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	name, err := normalizeAssistantHireName(request.Name)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.Model = strings.TrimSpace(request.Model)
	if len(request.Provider) > 120 || len(request.Model) > 240 {
		_ = orihttp.RespondBadRequest(w, "Provider or model is too long")
		return
	}
	if h.assistantModelValidator != nil && (request.Provider != "" || request.Model != "") {
		effectiveProvider, effectiveModel := request.Provider, request.Model
		if effectiveProvider == "" && h.systemModelReader != nil {
			effectiveProvider, _ = h.systemModelReader.GetSystemModel()
		}
		if err := h.assistantModelValidator(effectiveProvider, effectiveModel); err != nil {
			_ = orihttp.RespondBadRequest(w, "Selected provider or model is unavailable")
			return
		}
	}

	assistantHireMu.Lock()
	defer assistantHireMu.Unlock()
	station, project, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	station = h.syncAssistantPluginAvailability(station)
	state := station.GetAssistantProgramState()
	if !state.PluginAvailable {
		_ = orihttp.RespondConflict(w, "The assistant contribution is disabled; existing data remains available")
		return
	}
	if state.Hired {
		summary, _ := h.buildAssistantProgramSummary(station, project)
		_ = orihttp.RespondSuccess(w, summary)
		return
	}
	if state.StateRevision != request.Version {
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "Assistant program changed; reload and try again", "current_version": state.StateRevision})
		return
	}
	names, err := assistantRoleAgentNames(name, state.Declaration)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	if conflict := h.existingAgentNameConflict(names); conflict != "" {
		_ = orihttp.RespondConflict(w, fmt.Sprintf("Agent name %q is already in use", conflict))
		return
	}

	createdNames := make([]string, 0, len(names))
	rollbackAgents := func() {
		for index := len(createdNames) - 1; index >= 0; index-- {
			_ = h.agentStore.DeleteAgent(createdNames[index])
		}
	}
	for index, role := range state.Declaration.Roles {
		spec := projecttemplates.AgentSpec{
			Name: names[index], Role: role.Role, Type: role.Type, SystemPrompt: role.SystemPrompt,
			Provider: request.Provider, Model: request.Model,
			Tools: projecttemplates.ToolDefaults{Skills: append([]string(nil), role.Skills...)},
		}
		config, _ := h.templateAgentCreateConfig(spec)
		if err := h.agentStore.CreateAgent(names[index], config); err != nil {
			rollbackAgents()
			_ = orihttp.RespondConflict(w, "Assistant roster could not be created")
			return
		}
		createdNames = append(createdNames, names[index])
	}
	instances, bindings := assistantRosterInstances(names, state.Declaration)
	projects, err := workspace.NewAssistantProgramStore(h.workspaceTaskStore).LinkedProjects(station.ID)
	if err != nil {
		rollbackAgents()
		_ = orihttp.RespondInternalError(w, "Linked projects could not be read")
		return
	}
	type previousRoster struct {
		workspaceID string
		instances   []workspace.AgentInstance
		entry       string
	}
	previous := make([]previousRoster, 0, len(projects)+1)
	applyRoster := func(target *workspace.Workspace) error {
		return h.workspaceTaskStore.Update(target.ID, func(current *workspace.Workspace) error {
			merged, mergeErr := mergeAssistantRoster(current.GetAgentInstances(), instances)
			if mergeErr != nil {
				return mergeErr
			}
			previous = append(previous, previousRoster{workspaceID: current.ID, instances: current.GetAgentInstances(), entry: current.EntryAgentName()})
			current.AgentInstances = cloneAgentInstances(merged)
			return current.SetEntryAgentName(name)
		})
	}
	err = applyRoster(station)
	if err == nil {
		for _, linked := range projects {
			if err = applyRoster(linked); err != nil {
				break
			}
		}
	}
	if err != nil {
		for index := len(previous) - 1; index >= 0; index-- {
			entry := previous[index]
			_ = h.workspaceTaskStore.Update(entry.workspaceID, func(current *workspace.Workspace) error {
				current.AgentInstances = cloneAgentInstances(entry.instances)
				return current.SetEntryAgentName(entry.entry)
			})
		}
		rollbackAgents()
		_ = orihttp.RespondConflict(w, "Assistant roster conflicts with an existing workspace agent")
		return
	}

	now := time.Now().UTC()
	if err := h.workspaceTaskStore.Update(station.ID, func(current *workspace.Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.StateRevision != request.Version {
			return workspace.ErrAssistantProgramVersionConflict
		}
		currentState.Hired = true
		currentState.HiredAt = &now
		currentState.PrimaryName = name
		currentState.Provider = request.Provider
		currentState.Model = request.Model
		currentState.Roster = bindings
		currentState.StageID = currentState.Declaration.Stages[0].ID
		currentState.Level = 1
		currentState.StageEnteredAt = map[string]time.Time{currentState.StageID: now}
		if len(currentState.LinkedProjectIDs) >= currentState.Declaration.Reflection.MinimumProjects {
			currentState.Reflection.ScheduleTaskID = workspace.AssistantReflectionScheduleID(station.ID)
			nextReflection := now.Add(time.Duration(currentState.Declaration.Reflection.CadenceHours) * time.Hour)
			currentState.Reflection.NextEligibleAt = &nextReflection
		}
		currentState.StateRevision++
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		for index := len(previous) - 1; index >= 0; index-- {
			entry := previous[index]
			_ = h.workspaceTaskStore.Update(entry.workspaceID, func(current *workspace.Workspace) error {
				current.AgentInstances = cloneAgentInstances(entry.instances)
				return current.SetEntryAgentName(entry.entry)
			})
		}
		rollbackAgents()
		_ = orihttp.RespondConflict(w, "Assistant program changed; reload and try again")
		return
	}
	for index, role := range state.Declaration.Roles {
		if h.applyAgentTools != nil && len(role.Skills) > 0 {
			h.applyAgentTools(station.ID, names[index], projecttemplates.ToolDefaults{Skills: role.Skills})
		}
	}
	station, _ = h.workspaceTaskStore.Get(station.ID)
	summary, err := h.buildAssistantProgramSummary(station, project)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Assistant was hired but its summary could not be read")
		return
	}
	_ = orihttp.RespondSuccess(w, summary)
}

func (h *Handler) assistantLearningStore() (*workspace.AssistantLearningStore, bool) {
	if h == nil || h.workspaceTaskStore == nil {
		return nil, false
	}
	resolver, ok := h.workspaceTaskStore.(workspace.FolderResolver)
	if !ok {
		return nil, false
	}
	return workspace.NewAssistantLearningStore(resolver), true
}

func (h *Handler) GetAssistantLearnings(w http.ResponseWriter, r *http.Request) {
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	document, err := learningStore.Read(station.ID)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Assistant learnings could not be read")
		return
	}
	_ = orihttp.RespondSuccess(w, document)
}

func (h *Handler) RunAssistantReflection(w http.ResponseWriter, r *http.Request) {
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	station = h.syncAssistantPluginAvailability(station)
	learningStore, ok := h.assistantLearningStore()
	if !ok || h.assistantReflectionModel == nil {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant reflection is unavailable")
		return
	}
	result, err := workspace.NewAssistantReflectionService(h.workspaceTaskStore, learningStore, h.assistantReflectionModel).Run(r.Context(), station.ID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, workspace.ErrAssistantReflectionUnavailable) {
			status = http.StatusServiceUnavailable
		}
		_ = orihttp.RespondJSON(w, status, map[string]any{"error": err.Error(), "run_id": result.RunID})
		return
	}
	_ = orihttp.RespondSuccess(w, result)
}

type assistantDocumentVersionRequest struct {
	Version int64 `json:"version"`
}

type assistantSuggestionGenerateRequest struct {
	Version   int64  `json:"version"`
	ProjectID string `json:"project_id,omitempty"`
}

type assistantLearningEditRequest struct {
	Version    int64  `json:"version"`
	Text       string `json:"text"`
	Type       string `json:"type"`
	Confidence string `json:"confidence"`
}

func (h *Handler) GenerateAssistantSuggestions(w http.ResponseWriter, r *http.Request) {
	var request assistantSuggestionGenerateRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, currentProject, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	station = h.syncAssistantPluginAvailability(station)
	projectID := strings.TrimSpace(request.ProjectID)
	if currentProject != nil {
		projectID = currentProject.ID
	}
	if projectID == "" {
		_ = orihttp.RespondBadRequest(w, "A linked project is required")
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	document, err := workspace.NewAssistantSuggestionService(h.workspaceTaskStore, learningStore).Generate(station.ID, projectID, request.Version)
	if err != nil {
		_ = orihttp.RespondConflict(w, err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, document)
}

func (h *Handler) AcceptAssistantSuggestion(w http.ResponseWriter, r *http.Request) {
	h.mutateAssistantSuggestion(w, r, true)
}

func (h *Handler) DismissAssistantSuggestion(w http.ResponseWriter, r *http.Request) {
	h.mutateAssistantSuggestion(w, r, false)
}

func (h *Handler) mutateAssistantSuggestion(w http.ResponseWriter, r *http.Request, accept bool) {
	var request assistantDocumentVersionRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	var writable bool
	if station, writable = h.requireAssistantWritable(w, station); !writable {
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	service := workspace.NewAssistantSuggestionService(h.workspaceTaskStore, learningStore)
	suggestionID := strings.TrimSpace(r.PathValue("suggestionID"))
	if accept {
		suggestion, acceptErr := service.Accept(station.ID, suggestionID, request.Version)
		if acceptErr != nil {
			_ = orihttp.RespondConflict(w, acceptErr.Error())
			return
		}
		_ = orihttp.RespondSuccess(w, suggestion)
		return
	}
	if err := service.Dismiss(station.ID, suggestionID, request.Version); err != nil {
		_ = orihttp.RespondConflict(w, err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"dismissed": true})
}

func (h *Handler) EditAssistantCandidate(w http.ResponseWriter, r *http.Request) {
	var request assistantLearningEditRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	var writable bool
	if station, writable = h.requireAssistantWritable(w, station); !writable {
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	candidate, err := learningStore.EditCandidate(station.ID, strings.TrimSpace(r.PathValue("candidateID")), request.Text, request.Type, request.Confidence, request.Version)
	if err != nil {
		_ = orihttp.RespondConflict(w, "Candidate changed or contains unsafe content")
		return
	}
	_ = orihttp.RespondSuccess(w, candidate)
}

func (h *Handler) DeleteAssistantCandidate(w http.ResponseWriter, r *http.Request) {
	var request assistantDocumentVersionRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	var writable bool
	if station, writable = h.requireAssistantWritable(w, station); !writable {
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	if err := learningStore.DeleteCandidate(station.ID, strings.TrimSpace(r.PathValue("candidateID")), request.Version); err != nil {
		_ = orihttp.RespondConflict(w, "Candidate changed; reload and try again")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"deleted": true})
}

func (h *Handler) ApproveAssistantCandidate(w http.ResponseWriter, r *http.Request) {
	h.mutateAssistantCandidate(w, r, true)
}

func (h *Handler) RejectAssistantCandidate(w http.ResponseWriter, r *http.Request) {
	h.mutateAssistantCandidate(w, r, false)
}

func (h *Handler) mutateAssistantCandidate(w http.ResponseWriter, r *http.Request, approve bool) {
	var request assistantDocumentVersionRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	var writable bool
	if station, writable = h.requireAssistantWritable(w, station); !writable {
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	candidateID := strings.TrimSpace(r.PathValue("candidateID"))
	if approve {
		learning, approveErr := learningStore.ApproveCandidate(station.ID, candidateID, request.Version)
		if approveErr != nil {
			_ = orihttp.RespondConflict(w, "Candidate changed; reload and try again")
			return
		}
		_ = orihttp.RespondSuccess(w, learning)
		return
	}
	if err := learningStore.RejectCandidate(station.ID, candidateID, request.Version); err != nil {
		_ = orihttp.RespondConflict(w, "Candidate changed; reload and try again")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"rejected": true})
}

func (h *Handler) EditAssistantLearning(w http.ResponseWriter, r *http.Request) {
	var request assistantLearningEditRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	var writable bool
	if station, writable = h.requireAssistantWritable(w, station); !writable {
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	learning, err := learningStore.EditLearning(station.ID, strings.TrimSpace(r.PathValue("learningID")), request.Text, request.Type, request.Confidence, request.Version)
	if err != nil {
		_ = orihttp.RespondConflict(w, "Learning changed or contains unsafe content")
		return
	}
	_ = orihttp.RespondSuccess(w, learning)
}

func (h *Handler) DeleteAssistantLearning(w http.ResponseWriter, r *http.Request) {
	var request assistantDocumentVersionRequest
	if !h.decodeAssistantProgramJSON(w, r, &request) {
		return
	}
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	var writable bool
	if station, writable = h.requireAssistantWritable(w, station); !writable {
		return
	}
	learningStore, ok := h.assistantLearningStore()
	if !ok {
		_ = orihttp.RespondServiceUnavailable(w, "Assistant learning storage is unavailable")
		return
	}
	if err := learningStore.DeleteLearning(station.ID, strings.TrimSpace(r.PathValue("learningID")), request.Version); err != nil {
		_ = orihttp.RespondConflict(w, "Learning changed; reload and try again")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"deleted": true})
}

func (h *Handler) AcknowledgeAssistantPromotion(w http.ResponseWriter, r *http.Request) {
	station, _, err := h.assistantProgramStation(strings.TrimSpace(r.PathValue("workspaceID")))
	if err != nil {
		_ = orihttp.RespondNotFound(w, "Assistant program not found")
		return
	}
	var writable bool
	if station, writable = h.requireAssistantWritable(w, station); !writable {
		return
	}
	if err := h.workspaceTaskStore.Update(station.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		if state == nil {
			return workspace.ErrAssistantStationNotFound
		}
		if state.PromotionReceipt != nil && state.PromotionReceipt.AcknowledgedAt == nil {
			now := time.Now().UTC()
			state.PromotionReceipt.AcknowledgedAt = &now
			state.StateRevision++
			current.SetAssistantProgramState(state)
		}
		return nil
	}); err != nil {
		_ = orihttp.RespondConflict(w, "Promotion acknowledgement could not be saved")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"acknowledged": true})
}
