package workspace

import (
	"errors"
	"fmt"
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

	now := service.now()
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		if state == nil || state.Key.Normalize() != key {
			return ErrAssistantStationNotFound
		}
		state.LinkedProjectIDs = NormalizeAssistantProjectIDs(append(state.LinkedProjectIDs, project.ID))
		state.PluginAvailable = true
		state.StateRevision++
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		if created {
			_ = service.store.Delete(station.ID)
		}
		return nil, false, err
	}
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
