package sessionhttp

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceroles"
)

const templateGroupRequirementPlanVersion = 1

const (
	templateGroupRoleVerificationAbsent        = "group_absent"
	templateGroupRoleVerificationVerified      = "verified"
	templateGroupRoleVerificationUnavailable   = "unavailable"
	templateGroupRoleVerificationNotApplicable = "not_applicable"
)

type templateGroupRequirementPlan struct {
	Version             int                                       `json:"version"`
	SourceRevision      string                                    `json:"source_revision,omitempty"`
	State               grouprequirements.State                   `json:"state"`
	Policy              projecttemplates.GroupPolicy              `json:"policy,omitempty"`
	SelectedComposition string                                    `json:"selected_composition,omitempty"`
	Summary             string                                    `json:"summary"`
	Home                *templateGroupRequirementHomePlan         `json:"home,omitempty"`
	RequiredHomeRoles   templateGroupRequirementRequiredRolesPlan `json:"required_home_roles"`
	Actions             []grouprequirements.Action                `json:"actions,omitempty"`
}

type templateGroupRequirementHomePlan struct {
	Exists       bool   `json:"exists"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	Name         string `json:"name,omitempty"`
	FolderSlug   string `json:"folder_slug,omitempty"`
	ProposedName string `json:"proposed_name,omitempty"`
}

type templateGroupRequirementRequiredRolesPlan struct {
	Verification string                                 `json:"verification"`
	Required     *int                                   `json:"required,omitempty"`
	Filled       *int                                   `json:"filled,omitempty"`
	Missing      *int                                   `json:"missing,omitempty"`
	Roles        []templateGroupRequirementRolePlanItem `json:"roles,omitempty"`
}

type templateGroupRequirementRolePlanItem struct {
	RoleID  string                        `json:"role_id"`
	Label   string                        `json:"label"`
	Primary bool                          `json:"primary"`
	State   string                        `json:"state,omitempty"`
	Agent   *workspaceroles.AgentIdentity `json:"agent,omitempty"`
}

type templateGroupRoleHolderState int

const (
	templateGroupRoleHolderEmpty templateGroupRoleHolderState = iota
	templateGroupRoleHolderVerified
	templateGroupRoleHolderUnavailable
)

func templateGroupSourceRevision(template projecttemplates.Template) string {
	skeletonDigest := ""
	if template.TemplateVariant != nil {
		skeletonDigest = template.TemplateVariant.Source.DefinitionDigest
	}
	definitionDigest := projecttemplates.TemplateDefinitionDigest(template, skeletonDigest)
	digest, err := grouprequirements.DigestInput(struct {
		TemplateID       string                            `json:"template_id"`
		TemplateRevision string                            `json:"template_revision,omitempty"`
		DefinitionDigest string                            `json:"definition_digest"`
		PluginOwner      *workspace.PluginTemplateOwner    `json:"plugin_owner,omitempty"`
		TemplateVariant  *projecttemplates.TemplateVariant `json:"template_variant,omitempty"`
		VariantRevision  string                            `json:"variant_revision,omitempty"`
		UserSetupQuest   *projecttemplates.UserSetupQuest  `json:"user_setup_quest,omitempty"`
	}{
		TemplateID: template.ID, TemplateRevision: template.Revision, DefinitionDigest: definitionDigest,
		PluginOwner: template.PluginOwner, TemplateVariant: template.TemplateVariant,
		VariantRevision: template.VariantRevision, UserSetupQuest: template.UserSetupQuest,
	})
	if err != nil {
		return ""
	}
	return digest
}

func templateAssistantProgramKey(ownerUserID string, template projecttemplates.Template) (workspace.AssistantProgramKey, bool) {
	if template.AssistantProgram == nil || strings.TrimSpace(ownerUserID) == "" {
		return workspace.AssistantProgramKey{}, false
	}
	key := workspace.AssistantProgramKey{
		OwnerUserID: strings.TrimSpace(ownerUserID), ProgramID: template.AssistantProgram.ID,
	}
	switch {
	case template.PluginOwner != nil:
		key.PluginID = template.PluginOwner.PluginID
	case template.TemplateVariant != nil:
		key.PluginID = template.TemplateVariant.Source.PluginID
	case template.UserSetupQuest != nil:
		key.TemplateID = template.ID
		key.AttachmentID = template.UserSetupQuest.AttachmentID
	default:
		return workspace.AssistantProgramKey{}, false
	}
	key = key.Normalize()
	return key, key.Valid()
}

func requiredHomeRoleSpecs(template projecttemplates.Template) []workspace.AssistantProgramRoleSpec {
	if template.AssistantProgram == nil {
		return nil
	}
	roles := make([]workspace.AssistantProgramRoleSpec, 0, len(template.AssistantProgram.Roles))
	for _, role := range template.AssistantProgram.Roles {
		if role.Scope == workspace.AssistantRoleScopeHome && role.Required {
			roles = append(roles, role)
		}
	}
	return roles
}

func unavailableRequiredHomeRoles() templateGroupRequirementRequiredRolesPlan {
	return templateGroupRequirementRequiredRolesPlan{Verification: templateGroupRoleVerificationUnavailable}
}

func notApplicableRequiredHomeRoles() templateGroupRequirementRequiredRolesPlan {
	return templateGroupRequirementRequiredRolesPlan{Verification: templateGroupRoleVerificationNotApplicable}
}

func absentRequiredHomeRoles(template projecttemplates.Template) templateGroupRequirementRequiredRolesPlan {
	roles := requiredHomeRoleSpecs(template)
	required, filled, missing := len(roles), 0, len(roles)
	items := make([]templateGroupRequirementRolePlanItem, 0, len(roles))
	for _, role := range roles {
		items = append(items, templateGroupRequirementRolePlanItem{
			RoleID: role.ID, Label: role.Label, Primary: role.Primary, State: workspaceroles.StateEmpty,
		})
	}
	return templateGroupRequirementRequiredRolesPlan{
		Verification: templateGroupRoleVerificationAbsent,
		Required:     &required, Filled: &filled, Missing: &missing, Roles: items,
	}
}

func (h *Handler) buildTemplateGroupRequirementPlan(
	template projecttemplates.Template,
	composition string,
	ownerUserID string,
	ownerErr error,
) *templateGroupRequirementPlan {
	if template.GroupRequirement == nil {
		return nil
	}
	plan := &templateGroupRequirementPlan{
		Version: templateGroupRequirementPlanVersion, SourceRevision: templateGroupSourceRevision(template),
		Policy: template.GroupRequirement.Policy,
	}
	if h == nil || h.groupRequirements == nil || h.workspaceTaskStore == nil || ownerErr != nil || strings.TrimSpace(ownerUserID) == "" {
		plan.State = grouprequirements.StateSourceUnavailable
		plan.Summary = "Template placement ownership is unavailable."
		plan.Actions = []grouprequirements.Action{grouprequirements.ActionRetry}
		plan.RequiredHomeRoles = unavailableRequiredHomeRoles()
		return plan
	}
	readiness := h.revalidateBlueprintReadiness(template)
	if blueprintCreationBlocked(template, readiness) {
		plan.State = grouprequirements.StateSourceUnavailable
		plan.Summary = strings.TrimSpace(readiness.Summary)
		if plan.Summary == "" {
			plan.Summary = "The selected blueprint source is unavailable."
		}
		plan.Actions = []grouprequirements.Action{grouprequirements.ActionRetry, grouprequirements.ActionManagePlugins, grouprequirements.ActionChangeTemplate}
		plan.RequiredHomeRoles = unavailableRequiredHomeRoles()
		return plan
	}

	evaluation := h.groupRequirements.Evaluate(grouprequirements.Input{
		OwnerUserID: strings.TrimSpace(ownerUserID), OperationKind: grouprequirements.OperationCreateWorkspace,
		Template: template, Composition: strings.TrimSpace(composition),
	})
	plan.State = evaluation.State
	plan.Policy = evaluation.Policy
	plan.SelectedComposition = evaluation.SelectedComposition
	plan.Summary = evaluation.Summary
	plan.Actions = append([]grouprequirements.Action(nil), evaluation.Actions...)
	switch evaluation.State {
	case grouprequirements.StateReadyStandalone:
		plan.RequiredHomeRoles = notApplicableRequiredHomeRoles()
		return plan
	case grouprequirements.StateChoiceRequired:
		plan.RequiredHomeRoles = unavailableRequiredHomeRoles()
		return plan
	case grouprequirements.StateHomeCreationReviewRequired:
		plan.Home = &templateGroupRequirementHomePlan{Exists: false, ProposedName: evaluation.HomeName}
		plan.RequiredHomeRoles = absentRequiredHomeRoles(template)
		return plan
	case grouprequirements.StateHomeRequired:
		plan.Home = &templateGroupRequirementHomePlan{Exists: false}
		plan.RequiredHomeRoles = absentRequiredHomeRoles(template)
		return plan
	case grouprequirements.StateReadyGrouped:
		// Continue below only when Evaluate resolved one exact existing Home.
	default:
		plan.RequiredHomeRoles = unavailableRequiredHomeRoles()
		return plan
	}

	if strings.TrimSpace(evaluation.HomeWorkspaceID) == "" {
		// Ready grouped without an existing ID belongs only to a separately
		// confirmed Prepare Home operation. Passive plan reads never request it.
		plan.State = grouprequirements.StateSourceUnavailable
		plan.Summary = "The canonical group could not be verified."
		plan.Actions = []grouprequirements.Action{grouprequirements.ActionRetry}
		plan.RequiredHomeRoles = unavailableRequiredHomeRoles()
		return plan
	}
	home, err := h.workspaceTaskStore.Get(evaluation.HomeWorkspaceID)
	if err != nil || home == nil || home.ID != evaluation.HomeWorkspaceID {
		plan.State = grouprequirements.StateSourceUnavailable
		plan.Summary = "The canonical group could not be read."
		plan.Actions = []grouprequirements.Action{grouprequirements.ActionRetry}
		plan.RequiredHomeRoles = unavailableRequiredHomeRoles()
		return plan
	}
	plan.Home = &templateGroupRequirementHomePlan{
		Exists: true, WorkspaceID: home.ID, Name: home.Name, FolderSlug: home.FolderSlug,
	}
	plan.RequiredHomeRoles = h.verifiedRequiredHomeRoles(template, evaluation.ProgramKey, home)
	if plan.RequiredHomeRoles.Verification == templateGroupRoleVerificationVerified &&
		plan.RequiredHomeRoles.Missing != nil && *plan.RequiredHomeRoles.Missing > 0 {
		plan.Actions = append(plan.Actions, grouprequirements.ActionOpenGroupRoles)
	}
	return plan
}

func (h *Handler) verifiedRequiredHomeRoles(
	template projecttemplates.Template,
	programKey *workspace.AssistantProgramKey,
	home *workspace.Workspace,
) templateGroupRequirementRequiredRolesPlan {
	roles := requiredHomeRoleSpecs(template)
	required, filled := len(roles), 0
	items := make([]templateGroupRequirementRolePlanItem, 0, len(roles))
	for _, role := range roles {
		item := templateGroupRequirementRolePlanItem{
			RoleID: role.ID, Label: role.Label, Primary: role.Primary, State: workspaceroles.StateEmpty,
		}
		holder, state := h.verifiedAssistantHomeRoleHolder(programKey, home, role.ID)
		if state == templateGroupRoleHolderUnavailable {
			return unavailableRequiredHomeRoles()
		}
		if state == templateGroupRoleHolderVerified {
			item.State = workspaceroles.StateFilled
			item.Agent = holder
			filled++
		}
		items = append(items, item)
	}
	missing := required - filled
	return templateGroupRequirementRequiredRolesPlan{
		Verification: templateGroupRoleVerificationVerified,
		Required:     &required, Filled: &filled, Missing: &missing, Roles: items,
	}
}

func (h *Handler) verifiedAssistantHomeRoleHolder(
	programKey *workspace.AssistantProgramKey,
	home *workspace.Workspace,
	roleID string,
) (*workspaceroles.AgentIdentity, templateGroupRoleHolderState) {
	if programKey == nil || home == nil || h == nil || h.workspaceTaskStore == nil {
		return nil, templateGroupRoleHolderUnavailable
	}
	key := programKey.Normalize()
	state := home.GetAssistantProgramState()
	if !key.Valid() || state == nil || state.Key.Normalize() != key || strings.TrimSpace(home.OwnerUserID) != key.OwnerUserID {
		return nil, templateGroupRoleHolderEmpty
	}
	var binding *workspace.AssistantRoleBinding
	for index := range state.HomeBindings.Bindings {
		candidate := &state.HomeBindings.Bindings[index]
		if candidate.RoleID != roleID {
			continue
		}
		if binding != nil {
			return nil, templateGroupRoleHolderEmpty
		}
		binding = candidate
	}
	if binding == nil {
		return nil, templateGroupRoleHolderEmpty
	}
	name := strings.TrimSpace(binding.AgentName)
	instanceID := strings.TrimSpace(binding.AgentInstanceID)
	if name == "" || instanceID == "" {
		return nil, templateGroupRoleHolderEmpty
	}
	instanceFound := false
	for _, instance := range home.GetAgentInstances() {
		if instance.ID == instanceID && instance.RoleID == roleID && strings.EqualFold(strings.TrimSpace(instance.Name), name) {
			instanceFound = true
			break
		}
	}
	if !instanceFound {
		return nil, templateGroupRoleHolderEmpty
	}
	if h.agentStore == nil {
		return nil, templateGroupRoleHolderUnavailable
	}
	canonical, err := h.validateAttachableWorkspaceAgent(name)
	if err != nil {
		return nil, templateGroupRoleHolderEmpty
	}
	snapshot, found, err := h.workspaceTaskStore.GetWorkspaceAgent(home.ID, canonical)
	if err != nil {
		return nil, templateGroupRoleHolderUnavailable
	}
	if !found || snapshot == nil {
		return nil, templateGroupRoleHolderEmpty
	}
	agent, found := h.lookupRoleAgentIdentity(canonical)
	if !found {
		return nil, templateGroupRoleHolderEmpty
	}
	return &agent, templateGroupRoleHolderVerified
}
