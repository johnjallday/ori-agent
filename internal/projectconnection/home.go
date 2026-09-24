package projectconnection

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrHomeProviderMissing reports that a split blueprint's Home comes from a
// plugin that is not installed, switched off, or not compatible with this
// project. HomePreparation still returns the partial preparation with it, so
// a reader can name the group while it explains the provider.
var ErrHomeProviderMissing = errors.New("project connection Home provider is missing")

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

// homeTarget is the one Home a template's project joins: its key, the
// declaration a new Home is created from, and, for a split blueprint, the
// provider that contributed that declaration.
type homeTarget struct {
	key         workspace.AssistantProgramKey
	declaration *workspace.AssistantProgramDeclaration
	owner       *workspace.AssistantProgramHomeOwner
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

// resolveHomeTarget reads a combined blueprint's own declaration, or resolves
// a split blueprint's Home through the group-requirements service's
// independent-Home join. A provider the user can repair is ErrHomeProviderMissing.
func (s *Service) resolveHomeTarget(scope Scope) (homeTarget, error) {
	switch {
	case scope.Template.AssistantProgram != nil:
		key, err := homeKey(scope)
		if err != nil {
			return homeTarget{}, ErrUnavailable
		}
		return homeTarget{key: key, declaration: scope.Template.AssistantProgram}, nil
	case scope.Template.AssistantProject != nil:
		resolved, err := s.grouping.ResolveIndependentHome(scope.OwnerUserID, scope.Template)
		if grouprequirements.HomeProviderRepairable(err) {
			return homeTarget{}, ErrHomeProviderMissing
		}
		if err != nil {
			return homeTarget{}, ErrUnavailable
		}
		owner := resolved.Owner.Clone()
		return homeTarget{key: resolved.Key.Normalize(), declaration: resolved.Declaration, owner: &owner}, nil
	default:
		return homeTarget{}, ErrUnavailable
	}
}

// HomePreparation reads whether the template's Home exists. For a split
// blueprint whose Home provider is missing it returns the partial preparation
// (name, template, policy, compositions) together with ErrHomeProviderMissing.
func (s *Service) HomePreparation(scope Scope) (HomePreparation, error) {
	if s == nil || s.store == nil || (scope.Template.AssistantProgram == nil && scope.Template.AssistantProject == nil) {
		return HomePreparation{}, ErrUnavailable
	}
	result := HomePreparation{TemplateID: scope.Template.ID}
	if scope.Template.AssistantProgram != nil {
		result.Name = scope.Template.AssistantProgram.StationName
	}
	if requirement := scope.Template.GroupRequirement; requirement != nil {
		result.GroupPolicy = string(requirement.Policy)
		if result.Name == "" {
			result.Name = requirement.DefaultHomeName
		}
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
	target, err := s.resolveHomeTarget(scope)
	if errors.Is(err, ErrHomeProviderMissing) {
		return result, ErrHomeProviderMissing
	}
	if err != nil {
		return HomePreparation{}, ErrUnavailable
	}
	if result.Name == "" {
		result.Name = target.declaration.StationName
	}
	result.GroupTemplateID = projecttemplates.GroupTemplateIDForKey(target.key)
	home, err := workspace.NewAssistantProgramStore(s.store).FindStation(target.key)
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
	target, err := s.resolveHomeTarget(scope)
	if err != nil {
		return HomePreparation{}, err
	}
	programs := workspace.NewAssistantProgramStore(s.store)
	var home *workspace.Workspace
	var created bool
	if target.owner != nil {
		// A split Home records the provider it came from; group-requirement
		// evaluation later accepts only a Home with that exact provider.
		home, created, err = programs.EnsureNamedIndependentStation(target.key, target.declaration, name, *target.owner)
	} else {
		home, created, err = programs.EnsureNamedStation(target.key, target.declaration, name)
	}
	if err != nil {
		return HomePreparation{}, ErrUnavailable
	}
	if created && home != nil {
		s.recordCreatedWorkspace(home.ID)
	}
	return s.HomePreparation(scope)
}
