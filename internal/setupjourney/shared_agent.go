package setupjourney

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CreateSharedProjectAgent creates the user's agent for one shared project
// role before any project exists (assistant-reads-projects): the Home's shared
// assistant, hired when its folder is set up. It is the create a song's own
// staffing makes for the role (the role's system prompt, description and
// skills, the given model or the system default), stamped with marker and bound
// into no workspace: the Home holds it by its staffing consent alone, and each
// song binds it when opened.
//
// created says this call wrote the agent's profile. A failure after that keeps
// the profile (its marker lets the Home's sweep record it); a failure before it
// leaves nothing.
func (a *AssistantStaffingAdapter) CreateSharedProjectAgent(ctx context.Context, role workspace.AssistantProgramRoleSpec, name, provider, model string,
	marker *agent.AssistantSetupProvenance) (bool, error) {
	name = strings.TrimSpace(name)
	if a == nil || a.profiles == nil || marker == nil || name == "" || strings.TrimSpace(marker.ID) == "" {
		return false, ErrInvalid
	}
	if provider == "" && model == "" && a.defaults != nil {
		provider, model = a.defaults()
	}
	if provider == "" || model == "" || !staffingModelAvailable(a.validate, provider, model) {
		return false, ErrInvalid
	}
	if profileNameExists(a.profiles, name) {
		return false, ErrConflict
	}
	stamped := *marker
	created, err := a.createStaffingProfile(ctx, name, &store.CreateAgentConfig{
		Role: types.AgentRole(role.Role), Model: model, LLMProvider: provider, SystemPrompt: role.SystemPrompt,
		Description: role.Description, AssistantSetup: &stamped,
	})
	if err != nil {
		// A strict create reports the name it wrote; a legacy one wrote nothing
		// it can prove.
		return created.Name != "", ErrConflict
	}
	if len(role.Skills) == 0 {
		return true, nil
	}
	// A shared project role's skills come from the personal source, as in a
	// song of a Home whose projects have their own provider.
	if created.Witness != "" {
		preparer, ok := a.grants.(PreparedStaffingToolGrants)
		if !ok {
			return true, ErrConflict
		}
		apply, err := preparer.PrepareStaffingGrants(name, role.Skills, true)
		if err != nil || apply == nil {
			return true, ErrConflict
		}
		if err := store.WithStaffingProfiles(ctx, a.profiles, []store.StaffingSelection{created},
			func(map[string]*agent.Agent) error { return apply() }); err != nil {
			return true, err
		}
		return true, nil
	}
	personal, ok := a.grants.(PersonalStaffingToolGrants)
	if !ok {
		return true, ErrConflict
	}
	for _, skill := range role.Skills {
		if err := personal.GrantPersonal(name, skill); err != nil {
			return true, err
		}
	}
	return true, nil
}
