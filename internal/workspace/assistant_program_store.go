package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrAssistantProgramUnavailable = errors.New("workspace has no assistant program declaration")
	ErrAssistantStationNotFound    = errors.New("assistant station not found")
)

var assistantProgramProvisionMu sync.Mutex

// AssistantProgramStore owns generic station lookup/link mutations. It relies
// only on stable persisted IDs and Store.Update; names and tags are display-only.
type AssistantProgramStore struct {
	store Store
	now   func() time.Time
}

func NewAssistantProgramStore(store Store) *AssistantProgramStore {
	return &AssistantProgramStore{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func assistantStationFolderSlug(key AssistantProgramKey) string {
	normalized := key.Normalize()
	digest := sha256.Sum256([]byte(normalized.OwnerUserID + "\x00" + normalized.PluginID + "\x00" + normalized.ProgramID))
	return "assistant-" + hex.EncodeToString(digest[:8])
}

func (service *AssistantProgramStore) FindStation(key AssistantProgramKey) (*Workspace, error) {
	if service == nil || service.store == nil {
		return nil, ErrAssistantStationNotFound
	}
	key = key.Normalize()
	ids, err := service.store.List()
	if err != nil {
		return nil, err
	}
	var found *Workspace
	for _, id := range ids {
		candidate, getErr := service.store.Get(id)
		if getErr != nil || candidate == nil {
			continue
		}
		state := candidate.GetAssistantProgramState()
		if state == nil || state.Key.Normalize() != key {
			continue
		}
		if found != nil && found.ID != candidate.ID {
			return nil, fmt.Errorf("multiple assistant stations exist for stable key")
		}
		found = candidate
	}
	if found == nil {
		return nil, ErrAssistantStationNotFound
	}
	return found, nil
}

// EnsureProjectStation creates or reuses one inert station shell and links a
// compatible project. It performs no hire, schedule, grant, or agent creation.
func (service *AssistantProgramStore) EnsureProjectStation(projectID string) (*Workspace, bool, error) {
	if service == nil || service.store == nil {
		return nil, false, errors.New("assistant program storage is unavailable")
	}
	assistantProgramProvisionMu.Lock()
	defer assistantProgramProvisionMu.Unlock()

	project, err := service.store.Get(projectID)
	if err != nil {
		return nil, false, err
	}
	if existing := project.GetAssistantProjectLink(); existing != nil {
		station, getErr := service.store.Get(existing.StationWorkspaceID)
		if getErr == nil && station.GetAssistantProgramState() != nil {
			return station, false, nil
		}
		return nil, false, ErrAssistantStationNotFound
	}
	provenance := project.GetTemplateProvenance()
	// SyncStore reads are SQLite-primary, while template provenance remains a
	// canonical workspace.json field. Resolve that portable declaration before
	// failing closed so first-run station provisioning works in production, not
	// only with stores that keep every field in one record.
	if provenance == nil || provenance.PluginOwner == nil || provenance.AssistantProgram == nil {
		type canonicalWorkspaceReader interface {
			GetFolderWorkspace(string) (*Workspace, error)
		}
		if reader, ok := service.store.(canonicalWorkspaceReader); ok {
			if portable, portableErr := reader.GetFolderWorkspace(projectID); portableErr == nil && portable != nil {
				provenance = portable.GetTemplateProvenance()
			}
		}
	}
	if provenance == nil || provenance.PluginOwner == nil || provenance.AssistantProgram == nil {
		return nil, false, ErrAssistantProgramUnavailable
	}
	key := AssistantProgramKey{
		OwnerUserID: project.OwnerUserID,
		PluginID:    provenance.PluginOwner.PluginID,
		ProgramID:   provenance.AssistantProgram.ID,
	}.Normalize()
	if !key.Valid() {
		return nil, false, ErrAssistantProgramUnavailable
	}

	station, findErr := service.FindStation(key)
	created := false
	if errors.Is(findErr, ErrAssistantStationNotFound) {
		station = NewWorkspace(CreateWorkspaceParams{
			Name:        provenance.AssistantProgram.StationName,
			Description: provenance.AssistantProgram.StationDescription,
		})
		station.FolderSlug = assistantStationFolderSlug(key)
		station.OwnerUserID = key.OwnerUserID
		station.SetAssistantProgramState(&AssistantProgramState{
			SchemaVersion:   AssistantProgramSchemaVersion,
			StateRevision:   1,
			Key:             key,
			Declaration:     provenance.AssistantProgram,
			PluginAvailable: true,
		})
		if err := service.store.Save(station); err != nil {
			return nil, false, fmt.Errorf("create assistant station: %w", err)
		}
		created = true
	} else if findErr != nil {
		return nil, false, findErr
	}

	liveProjectIDs := make([]string, 0)
	if !created {
		linked, linkedErr := service.LinkedProjects(station.ID)
		if linkedErr != nil {
			return nil, false, linkedErr
		}
		for _, existingProject := range linked {
			liveProjectIDs = append(liveProjectIDs, existingProject.ID)
		}
	}
	now := service.now()
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		if state == nil || state.Key.Normalize() != key {
			return ErrAssistantStationNotFound
		}
		state.LinkedProjectIDs = NormalizeAssistantProjectIDs(append(liveProjectIDs, project.ID))
		state.PluginAvailable = true
		if state.Hired && state.Declaration != nil && len(state.LinkedProjectIDs) >= state.Declaration.Reflection.MinimumProjects && state.Reflection.ScheduleTaskID == "" {
			state.Reflection.ScheduleTaskID = AssistantReflectionScheduleID(current.ID)
			next := service.now().Add(time.Duration(state.Declaration.Reflection.CadenceHours) * time.Hour)
			state.Reflection.NextEligibleAt = &next
		}
		state.StateRevision++
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		if created {
			_ = service.store.Delete(station.ID)
		}
		return nil, false, err
	}
	station, err = service.store.Get(station.ID)
	if err != nil {
		return nil, false, err
	}
	stationState := station.GetAssistantProgramState()
	link := &AssistantProjectLink{
		SchemaVersion:      AssistantProgramSchemaVersion,
		StationWorkspaceID: station.ID,
		Key:                key,
		DeclarationVersion: provenance.AssistantProgram.SchemaVersion,
		LinkedAt:           now,
		StateRevision:      1,
	}
	if err := service.store.Update(project.ID, func(current *Workspace) error {
		if existing := current.GetAssistantProjectLink(); existing != nil {
			if existing.StationWorkspaceID == station.ID && existing.Key.Normalize() == key {
				return nil
			}
			return ErrAssistantProgramVersionConflict
		}
		if stationState != nil && stationState.Hired {
			merged, mergeErr := mergeAssistantStationRoster(current.GetAgentInstances(), station.GetAgentInstances())
			if mergeErr != nil {
				return mergeErr
			}
			current.AgentInstances = merged
			if err := current.SetEntryAgentName(stationState.PrimaryName); err != nil {
				return err
			}
		}
		current.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		_ = service.store.Update(station.ID, func(current *Workspace) error {
			state := current.GetAssistantProgramState()
			if state != nil {
				filtered := make([]string, 0, len(state.LinkedProjectIDs))
				for _, id := range state.LinkedProjectIDs {
					if id != project.ID {
						filtered = append(filtered, id)
					}
				}
				state.LinkedProjectIDs = NormalizeAssistantProjectIDs(filtered)
				if state.Declaration != nil && len(state.LinkedProjectIDs) < state.Declaration.Reflection.MinimumProjects {
					state.Reflection.ScheduleTaskID = ""
					state.Reflection.NextEligibleAt = nil
				}
				state.StateRevision++
				current.SetAssistantProgramState(state)
			}
			return nil
		})
		if created {
			if current, getErr := service.store.Get(station.ID); getErr == nil {
				state := current.GetAssistantProgramState()
				if state != nil && len(state.LinkedProjectIDs) == 0 && !state.Hired {
					_ = service.store.Delete(station.ID)
				}
			}
		}
		return nil, false, err
	}
	station, err = service.store.Get(station.ID)
	return station, created, err
}

func mergeAssistantStationRoster(existing, roster []AgentInstance) ([]AgentInstance, error) {
	rosterNames := make(map[string]struct{}, len(roster))
	rosterIDs := make(map[string]struct{}, len(roster))
	merged := append([]AgentInstance(nil), roster...)
	for _, instance := range roster {
		rosterNames[strings.ToLower(strings.TrimSpace(instance.Name))] = struct{}{}
		rosterIDs[instance.ID] = struct{}{}
	}
	for _, instance := range existing {
		if _, same := rosterIDs[instance.ID]; same {
			continue
		}
		if _, conflict := rosterNames[strings.ToLower(strings.TrimSpace(instance.Name))]; conflict {
			return nil, fmt.Errorf("workspace already contains assistant role %q", instance.Name)
		}
		merged = append(merged, instance)
	}
	return merged, nil
}

const (
	assistantCompletionReceiptLimit = 512
	assistantProgressMarkerKey      = "assistant_progress_station_id"
)

// RecordAcceptedCompletion advances persona progression from one explicit,
// user-accepted project completion. The recent station ledger plus a permanent
// marker on the canonical task gives retries set semantics without an unbounded
// station record; reflection, tool access, and project mutation are not involved.
func (service *AssistantProgramStore) RecordAcceptedCompletion(projectID, fingerprint string) (*AssistantProgramState, bool, error) {
	if service == nil || service.store == nil {
		return nil, false, errors.New("assistant program storage is unavailable")
	}
	project, err := service.store.Get(projectID)
	if err != nil {
		return nil, false, err
	}
	link := project.GetAssistantProjectLink()
	fingerprint = strings.TrimSpace(fingerprint)
	if link == nil || fingerprint == "" {
		return nil, false, ErrAssistantProgramUnavailable
	}
	taskID := ""
	if strings.HasPrefix(fingerprint, "task:") {
		taskID = strings.TrimSpace(strings.TrimPrefix(fingerprint, "task:"))
		if task, taskErr := project.GetTask(taskID); taskErr == nil && task != nil && task.Context != nil {
			if marker, _ := task.Context[assistantProgressMarkerKey].(string); marker == link.StationWorkspaceID {
				station, stationErr := service.store.Get(link.StationWorkspaceID)
				if stationErr != nil {
					return nil, false, stationErr
				}
				return station.GetAssistantProgramState(), false, nil
			}
		}
	}
	fingerprint = project.ID + ":" + fingerprint
	promoted := false
	recorded := false
	if err := service.store.Update(link.StationWorkspaceID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		if state == nil || state.Key.Normalize() != link.Key.Normalize() {
			return ErrAssistantStationNotFound
		}
		if !state.Hired || !state.PluginAvailable || state.Declaration == nil {
			return nil
		}
		for _, receipt := range state.CompletionReceipts {
			if receipt.Fingerprint == fingerprint {
				recorded = true
				return nil
			}
		}
		now := service.now()
		state.CompletionReceipts = append(state.CompletionReceipts, AssistantCompletionReceipt{Fingerprint: fingerprint, RecordedAt: now})
		if len(state.CompletionReceipts) > assistantCompletionReceiptLimit {
			state.CompletionReceipts = append([]AssistantCompletionReceipt(nil), state.CompletionReceipts[len(state.CompletionReceipts)-assistantCompletionReceiptLimit:]...)
		}
		state.AcceptedCompletions++
		recorded = true
		currentIndex := 0
		for index, stage := range state.Declaration.Stages {
			if stage.ID == state.StageID {
				currentIndex = index
			}
		}
		eligibleIndex := currentIndex
		for index, stage := range state.Declaration.Stages {
			if state.AcceptedCompletions >= stage.AcceptedCompletionThreshold {
				eligibleIndex = index
			}
		}
		if eligibleIndex > currentIndex {
			stage := state.Declaration.Stages[eligibleIndex]
			state.StageID = stage.ID
			state.Level = eligibleIndex + 1
			if state.StageEnteredAt == nil {
				state.StageEnteredAt = make(map[string]time.Time)
			}
			state.StageEnteredAt[stage.ID] = now
			state.PromotionReceipt = &AssistantPromotionReceipt{StageID: stage.ID, CreatedAt: now}
			promoted = true
		}
		state.StateRevision++
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		return nil, false, err
	}
	station, err := service.store.Get(link.StationWorkspaceID)
	if err != nil {
		return nil, false, err
	}
	if recorded && taskID != "" {
		if err := service.store.Update(project.ID, func(current *Workspace) error {
			return current.MutateTask(taskID, func(task *Task) error {
				if task.Context == nil {
					task.Context = make(map[string]any)
				}
				task.Context[assistantProgressMarkerKey] = link.StationWorkspaceID
				return nil
			})
		}); err != nil {
			return station.GetAssistantProgramState(), promoted, err
		}
	}
	return station.GetAssistantProgramState(), promoted, nil
}

// SubscribeAssistantProgression observes canonical durable completion events.
// It re-reads the task before awarding and uses the stable task ID as the
// idempotency fingerprint, so duplicate event delivery and manual/API overlap
// cannot increment twice.
func SubscribeAssistantProgression(bus *EventBus, store Store) string {
	if bus == nil || store == nil {
		return ""
	}
	return bus.SubscribeToEventType(EventTaskCompleted, func(event Event) {
		accepted, _ := event.Data["accepted"].(bool)
		if !accepted {
			return
		}
		workspaceID := strings.TrimSpace(event.WorkspaceID)
		taskID, _ := event.Data["task_id"].(string)
		taskID = strings.TrimSpace(taskID)
		if workspaceID == "" || taskID == "" {
			return
		}
		project, err := store.Get(workspaceID)
		if err != nil || project == nil || project.GetAssistantProjectLink() == nil {
			return
		}
		task, err := project.GetTask(taskID)
		if err != nil || task == nil || task.Status != TaskStatusCompleted {
			return
		}
		_, _, _ = NewAssistantProgramStore(store).RecordAcceptedCompletion(project.ID, "task:"+task.ID)
	})
}

func (service *AssistantProgramStore) SetPluginAvailable(stationID string, available bool) error {
	return service.store.Update(stationID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		if state == nil {
			return ErrAssistantStationNotFound
		}
		if state.PluginAvailable != available {
			state.PluginAvailable = available
			state.StateRevision++
			current.SetAssistantProgramState(state)
		}
		return nil
	})
}

func (service *AssistantProgramStore) LinkedProjects(stationID string) ([]*Workspace, error) {
	station, err := service.store.Get(stationID)
	if err != nil {
		return nil, err
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return nil, ErrAssistantStationNotFound
	}
	ids := NormalizeAssistantProjectIDs(state.LinkedProjectIDs)
	projects := make([]*Workspace, 0, len(ids))
	for _, id := range ids {
		project, getErr := service.store.Get(id)
		if getErr != nil || project == nil || project.Status == StatusTrashed || project.Status == StatusMissing {
			continue
		}
		link := project.GetAssistantProjectLink()
		if link == nil || link.StationWorkspaceID != stationID || link.Key.Normalize() != state.Key.Normalize() {
			continue
		}
		projects = append(projects, project)
	}
	return projects, nil
}
