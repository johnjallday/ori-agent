package projectconnection

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const preparationAcknowledgementKey = "setup_preparation_acknowledgement"

// HomePreparation is owner-read state, not a runtime grant or live verification.
type HomePreparation struct {
	HomeID       string `json:"group_id,omitempty"`
	Name         string `json:"name"`
	Exists       bool   `json:"exists"`
	Acknowledged bool   `json:"acknowledged"`
	TemplateID   string `json:"template_id"`
}

func homeKey(scope Scope) (workspace.AssistantProgramKey, error) {
	if scope.Template.PluginOwner == nil || scope.Template.AssistantProgram == nil {
		return workspace.AssistantProgramKey{}, ErrUnavailable
	}
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.Template.PluginOwner.PluginID, ProgramID: scope.Template.AssistantProgram.ID}.Normalize()
	if !key.Valid() {
		return key, ErrUnavailable
	}
	return key, nil
}

func (s *Service) HomePreparation(scope Scope) (HomePreparation, error) {
	key, err := homeKey(scope)
	if err != nil || s == nil || s.store == nil {
		return HomePreparation{}, ErrUnavailable
	}
	result := HomePreparation{Name: scope.Template.AssistantProgram.StationName, TemplateID: scope.Template.ID}
	home, err := workspace.NewAssistantProgramStore(s.store).FindStation(key)
	if errors.Is(err, workspace.ErrAssistantStationNotFound) {
		return result, nil
	}
	if err != nil || home == nil || home.Kind != "group" || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return HomePreparation{}, ErrUnavailable
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.SchemaVersion != workspace.AssistantProgramStateSchemaVersion {
		return HomePreparation{}, ErrUnavailable
	}
	result.HomeID, result.Name, result.Exists = home.ID, home.Name, true
	result.Acknowledged = home.SharedData[preparationAcknowledgementKey] == templateIdentity(scope.Template)
	return result, nil
}

func (s *Service) CreateHome(scope Scope, name string) (HomePreparation, error) {
	name = strings.TrimSpace(name)
	if !validDisplayName(name) {
		return HomePreparation{}, ErrInvalid
	}
	before, err := s.HomePreparation(scope)
	if err != nil || before.Exists {
		return before, err
	}
	key, err := homeKey(scope)
	if err != nil {
		return HomePreparation{}, err
	}
	if _, _, err := workspace.NewAssistantProgramStore(s.store).EnsureNamedStation(key, scope.Template.AssistantProgram, name); err != nil {
		return HomePreparation{}, ErrUnavailable
	}
	return s.HomePreparation(scope)
}

// AcknowledgePreparation records only the decision to proceed. It does not
// select an operating mode, test a project, or authorize any runtime access.
func (s *Service) AcknowledgePreparation(scope Scope) (HomePreparation, error) {
	home, err := s.HomePreparation(scope)
	if err != nil || !home.Exists {
		return HomePreparation{}, ErrUnavailable
	}
	key, keyErr := homeKey(scope)
	if keyErr != nil {
		return HomePreparation{}, keyErr
	}
	err = s.store.Update(home.HomeID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		if current.Kind != "group" || state == nil || state.Key.Normalize() != key || current.Status == workspace.StatusTrashed || current.Status == workspace.StatusMissing {
			return ErrChanged
		}
		if current.SharedData == nil {
			current.SharedData = map[string]interface{}{}
		}
		current.SharedData[preparationAcknowledgementKey] = templateIdentity(scope.Template)
		return nil
	})
	if err != nil {
		return HomePreparation{}, ErrUnavailable
	}
	return s.HomePreparation(scope)
}
