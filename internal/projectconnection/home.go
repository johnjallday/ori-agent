package projectconnection

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// HomePreparation is owner-read state, not a runtime grant or live verification.
// Whether the group exists is the only readiness it reports; live-control
// readiness belongs to the workspace setup wizard.
type HomePreparation struct {
	HomeID                string   `json:"group_id,omitempty"`
	Name                  string   `json:"name"`
	Exists                bool     `json:"exists"`
	TemplateID            string   `json:"template_id"`
	GroupPolicy           string   `json:"group_policy,omitempty"`
	AvailableCompositions []string `json:"available_compositions,omitempty"`
	// GroupTemplateID is the opaque, owner-free Group Template identity of this
	// program key. It is presentation-only: it selects which catalog entry the
	// shared creator describes and never authorizes this setup's own review.
	GroupTemplateID string `json:"group_template_id,omitempty"`
}

func homeKey(scope Scope) (workspace.AssistantProgramKey, error) {
	if scope.Template.AssistantProgram == nil {
		return workspace.AssistantProgramKey{}, ErrUnavailable
	}
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, ProgramID: scope.Template.AssistantProgram.ID}
	switch {
	case scope.Template.PluginOwner != nil:
		key.PluginID = scope.Template.PluginOwner.PluginID
	case scope.Template.UserSetupQuest != nil && scope.Template.UserSetupQuest.Declaration != nil:
		key.TemplateID = scope.Template.ID
		key.AttachmentID = scope.Template.UserSetupQuest.AttachmentID
	default:
		return workspace.AssistantProgramKey{}, ErrUnavailable
	}
	key = key.Normalize()
	if !key.Valid() {
		return key, ErrUnavailable
	}
	return key, nil
}

func (s *Service) HomePreparation(scope Scope) (HomePreparation, error) {
	if s == nil || s.store == nil || scope.Template.AssistantProgram == nil {
		return HomePreparation{}, ErrUnavailable
	}
	result := HomePreparation{Name: scope.Template.AssistantProgram.StationName, TemplateID: scope.Template.ID}
	if requirement := scope.Template.GroupRequirement; requirement != nil {
		result.GroupPolicy = string(requirement.Policy)
		switch requirement.Policy {
		case projecttemplates.GroupPolicyNone:
			result.AvailableCompositions = []string{"standalone"}
			return result, nil
		case projecttemplates.GroupPolicyRecommended:
			result.AvailableCompositions = []string{"grouped", "standalone"}
		case projecttemplates.GroupPolicyRequired:
			result.AvailableCompositions = []string{"grouped"}
		}
	}
	key, err := homeKey(scope)
	if err != nil {
		return HomePreparation{}, ErrUnavailable
	}
	result.GroupTemplateID = projecttemplates.GroupTemplateIDForKey(key)
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
	home, created, err := workspace.NewAssistantProgramStore(s.store).EnsureNamedStation(key, scope.Template.AssistantProgram, name)
	if err != nil {
		return HomePreparation{}, ErrUnavailable
	}
	if created && home != nil {
		s.recordCreatedWorkspace(home.ID)
	}
	return s.HomePreparation(scope)
}
