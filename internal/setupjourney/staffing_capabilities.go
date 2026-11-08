package setupjourney

import "github.com/johnjallday/ori-agent/internal/workspace"

// StaffingCapabilityGuard wraps a short bind while the already-resolved profile
// skill state remains unchanged. Neither it nor apply may look up profiles or
// re-enter the skills manager. Source fencing is owned separately by the adapter.
type StaffingCapabilityGuard func(apply func() error) error

// PreparedStaffingCapabilities checks actual existing collection membership;
// unlike StaffingToolGrants it never grants anything. All directory/catalog
// resolution happens during preparation, before the profile source fence.
type PreparedStaffingCapabilities interface {
	PrepareStaffingCapabilities(agentName string, names []string, personal bool, witness string) (StaffingCapabilityGuard, error)
}

func (a *AssistantStaffingAdapter) staffingCapabilities(target *workspace.Workspace, scope workspace.AssistantRoleScope, input staffingInput, roles map[string]workspace.AssistantProgramRoleSpec) (StaffingCapabilityGuard, error) {
	var guard StaffingCapabilityGuard
	for _, requested := range input.Roles {
		role := roles[requested.RoleID]
		if requested.ChoiceDigest != "" && requested.binds() && len(role.Skills) > 0 && requested.CapabilityWitness == "" {
			return nil, ErrConflict
		}
		if requested.CapabilityWitness == "" {
			continue
		}
		if len(input.Roles) != 1 || scope != workspace.AssistantRoleScopeHome || !requested.binds() || requested.SourceWitness == "" {
			return nil, ErrInvalid
		}
		preparer, ok := a.grants.(PreparedStaffingCapabilities)
		if !ok {
			return nil, ErrConflict
		}
		var err error
		guard, err = preparer.PrepareStaffingCapabilities(requested.Name, role.Skills, splitRoleRequiresPersonalSource(target, scope), requested.CapabilityWitness)
		if err != nil || guard == nil {
			return nil, ErrConflict
		}
	}
	return guard, nil
}
