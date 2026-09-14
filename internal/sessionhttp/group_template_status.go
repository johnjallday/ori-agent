package sessionhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	groupTemplateTeamReady             = "ready"
	groupTemplateTeamIncomplete        = "incomplete"
	groupTemplateTeamUnverified        = "unverified"
	groupTemplateTeamMigrationRequired = "migration_required"

	groupTemplateIntegrationAvailable   = "available"
	groupTemplateIntegrationUnavailable = "unavailable"
	groupTemplateIntegrationUnknown     = "unknown"
)

// groupTemplateStatus reports three independent facts about one group:
// it exists, whether its required Home roles are verifiably filled, and
// whether its source integration is currently available. Staffing never
// implies integration readiness, and neither is inferred from names.
type groupTemplateStatus struct {
	WorkspaceID string                                  `json:"workspace_id"`
	Name        string                                  `json:"name"`
	Kind        projecttemplates.GroupTemplateKind      `json:"kind"`
	Template    *groupTemplateStatusTemplate            `json:"template,omitempty"`
	Provider    *projecttemplates.GroupTemplateProvider `json:"provider,omitempty"`
	Group       groupTemplateStatusFact                 `json:"group"`
	Team        *groupTemplateStatusTeam                `json:"team,omitempty"`
	Integration *groupTemplateStatusFact                `json:"integration,omitempty"`
}

type groupTemplateStatusTemplate struct {
	Name                string `json:"name"`
	Description         string `json:"description,omitempty"`
	CreatedFromTemplate bool   `json:"created_from_template"`
	GroupTemplateID     string `json:"group_template_id,omitempty"`
}

type groupTemplateStatusFact struct {
	State   string   `json:"state"`
	Reason  string   `json:"reason,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

type groupTemplateStatusTeam struct {
	State             string                                    `json:"state"`
	RequiredHomeRoles templateGroupRequirementRequiredRolesPlan `json:"required_home_roles"`
	OptionalHomeRoles []projecttemplates.GroupTemplateRole      `json:"optional_home_roles,omitempty"`
	ProjectRoles      []string                                  `json:"project_roles_note,omitempty"`
	Actions           []string                                  `json:"actions,omitempty"`
}

// groupTemplateSummary derives the bounded list/Map summary for a program
// Home from its own persisted state. It returns nil for anything else.
func groupTemplateSummary(current *workspace.Workspace) *session.WorkspaceGroupTemplateSummary {
	if current == nil || !strings.EqualFold(strings.TrimSpace(current.Kind), "group") {
		return nil
	}
	state := current.GetAssistantProgramState()
	if state == nil || state.Declaration == nil || strings.TrimSpace(state.Declaration.StationName) == "" {
		return nil
	}
	summary := &session.WorkspaceGroupTemplateSummary{
		Kind: string(projecttemplates.GroupTemplateKindManaged),
		Name: state.Declaration.StationName,
	}
	key := state.Key.Normalize()
	switch {
	case key.PluginID != "":
		summary.ProviderKind, summary.PluginID = string(projecttemplates.GroupTemplateSourcePlugin), key.PluginID
	case key.TemplateID != "":
		summary.ProviderKind = string(projecttemplates.GroupTemplateSourceUserTemplate)
	}
	return summary
}

// GetGroupTemplateStatus handles GET /api/workspaces/{workspaceID}/group-template.
// It is read-only: no backfill, availability write, or provenance repair.
func (h *Handler) GetGroupTemplateStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" || h == nil || h.workspaceTaskStore == nil {
		_ = orihttp.RespondBadRequest(w, "workspace id is required")
		return
	}
	current, err := h.workspaceTaskStore.Get(workspaceID)
	if err != nil || current == nil {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(current.Kind), "group") {
		_ = orihttp.RespondBadRequest(w, "group template status applies only to groups")
		return
	}

	status := groupTemplateStatus{
		WorkspaceID: current.ID, Name: current.Name,
		Kind:  projecttemplates.GroupTemplateKindOrdinary,
		Group: groupTemplateStatusFact{State: "created"},
	}
	state := current.GetAssistantProgramState()
	if state == nil || state.Declaration == nil {
		// Ordinary and unknown groups stay General; nothing is inferred from
		// names, tags, parentage, agents, or provenance.
		general := projecttemplates.GeneralGroupTemplate()
		status.Template = &groupTemplateStatusTemplate{Name: general.Name, Description: general.Description}
		_ = orihttp.RespondSuccess(w, map[string]any{"group_template": status})
		return
	}

	status.Kind = projecttemplates.GroupTemplateKindManaged
	declaration := state.Declaration
	key := state.Key.Normalize()
	status.Template = &groupTemplateStatusTemplate{Name: declaration.StationName, Description: declaration.StationDescription}
	if provenance := state.GroupTemplate; provenance != nil {
		status.Template.CreatedFromTemplate = true
		status.Template.GroupTemplateID = provenance.GroupTemplateID
	}
	status.Provider = groupTemplateStatusProvider(key, state.GroupTemplate)
	status.Team = h.groupTemplateTeam(current, state)
	status.Integration = h.groupTemplateIntegration(r, current, key)
	_ = orihttp.RespondSuccess(w, map[string]any{"group_template": status})
}

func groupTemplateStatusProvider(key workspace.AssistantProgramKey, provenance *workspace.AssistantGroupTemplateProvenance) *projecttemplates.GroupTemplateProvider {
	switch {
	case key.PluginID != "":
		provider := &projecttemplates.GroupTemplateProvider{Kind: projecttemplates.GroupTemplateSourcePlugin, PluginID: key.PluginID}
		if provenance != nil && provenance.PluginOwner != nil {
			provider.PluginVersion = provenance.PluginOwner.PluginVersion
		}
		return provider
	case key.TemplateID != "":
		return &projecttemplates.GroupTemplateProvider{Kind: projecttemplates.GroupTemplateSourceUserTemplate}
	default:
		return nil
	}
}

// groupTemplateTeam verifies required Home roles against the Home's own
// declaration snapshot, so staffing stays readable when its source is gone.
func (h *Handler) groupTemplateTeam(home *workspace.Workspace, state *workspace.AssistantProgramState) *groupTemplateStatusTeam {
	team := &groupTemplateStatusTeam{}
	for _, role := range state.Declaration.Roles {
		switch role.Scope {
		case workspace.AssistantRoleScopeHome:
			if !role.Required {
				team.OptionalHomeRoles = append(team.OptionalHomeRoles, projecttemplates.GroupTemplateRole{
					RoleID: role.ID, Label: role.Label, Description: role.Description,
				})
			}
		case workspace.AssistantRoleScopeProject:
			team.ProjectRoles = append(team.ProjectRoles, role.Label)
		}
	}
	if state.SchemaVersion != workspace.AssistantProgramStateSchemaVersion {
		team.State = groupTemplateTeamMigrationRequired
		team.RequiredHomeRoles = unavailableRequiredHomeRoles()
		return team
	}
	key := state.Key.Normalize()
	roles := h.verifiedRequiredHomeRoles(projecttemplates.Template{AssistantProgram: state.Declaration}, &key, home)
	team.RequiredHomeRoles = roles
	switch {
	case roles.Verification != templateGroupRoleVerificationVerified || roles.Missing == nil:
		team.State = groupTemplateTeamUnverified
	case *roles.Missing > 0:
		team.State = groupTemplateTeamIncomplete
		team.Actions = []string{groupTemplateActionOpenRoles}
	default:
		team.State = groupTemplateTeamReady
	}
	return team
}

// groupTemplateIntegration reports whether the Home's source can currently be
// used. It never changes stored availability and never restores grants.
func (h *Handler) groupTemplateIntegration(r *http.Request, home *workspace.Workspace, key workspace.AssistantProgramKey) *groupTemplateStatusFact {
	if key.PluginID != "" {
		if h.installedPluginLister == nil {
			return &groupTemplateStatusFact{State: groupTemplateIntegrationUnknown, Reason: "dependency_state_unknown", Actions: []string{groupTemplateActionRetry}}
		}
		installed, err := h.installedPluginLister.List()
		if err != nil {
			return &groupTemplateStatusFact{State: groupTemplateIntegrationUnknown, Reason: "dependency_state_unknown", Actions: []string{groupTemplateActionRetry}}
		}
		found := false
		for _, candidate := range installed {
			if strings.EqualFold(strings.TrimSpace(candidate.Name), key.PluginID) {
				found = true
				if !candidate.Enabled {
					return &groupTemplateStatusFact{State: groupTemplateIntegrationUnavailable, Reason: "plugin_enable_required", Actions: []string{groupTemplateActionManage}}
				}
				break
			}
		}
		if !found {
			return &groupTemplateStatusFact{State: groupTemplateIntegrationUnavailable, Reason: "plugin_install_required", Actions: []string{groupTemplateActionManage}}
		}
	}

	listing := h.groupTemplateListing(r.Context())
	if listing.CatalogUnavailable {
		return &groupTemplateStatusFact{State: groupTemplateIntegrationUnknown, Reason: "dependency_state_unknown", Actions: []string{groupTemplateActionRetry}}
	}
	selectionID := projecttemplates.GroupTemplateIDForKey(key)
	for _, entry := range listing.Templates {
		if selectionID == "" || entry.ID != selectionID || entry.Availability == nil {
			continue
		}
		switch entry.Availability.State {
		case groupTemplateAvailabilityReusable:
			if entry.Home == nil || entry.Home.WorkspaceID != home.ID {
				// Another Home currently answers for this key; this one is not
				// the canonical owner and must not report its source as usable.
				return &groupTemplateStatusFact{State: groupTemplateIntegrationUnavailable, Reason: "home_ambiguous"}
			}
			return &groupTemplateStatusFact{State: groupTemplateIntegrationAvailable}
		case groupTemplateAvailabilityIncompatible:
			return &groupTemplateStatusFact{State: groupTemplateIntegrationUnavailable, Reason: "home_incompatible"}
		default:
			fact := &groupTemplateStatusFact{State: groupTemplateIntegrationUnavailable, Reason: entry.Availability.Reason}
			for _, action := range entry.Availability.Actions {
				if action == groupTemplateActionManage || action == groupTemplateActionRetry {
					fact.Actions = append(fact.Actions, action)
				}
			}
			return fact
		}
	}
	if key.PluginID != "" {
		// Installed and enabled, but its current declaration does not offer
		// this Home as a group template (for example, a legacy source).
		return &groupTemplateStatusFact{State: groupTemplateIntegrationAvailable, Reason: "not_offered_as_group_template"}
	}
	return &groupTemplateStatusFact{State: groupTemplateIntegrationUnavailable, Reason: "template_unavailable"}
}
