package setupjourney

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var ErrProjectRoleRepairUnavailable = errors.New("project role repair evidence is unavailable")

// ProjectRoleRepairInspection is an inert disclosure candidate, NOT a review
// token, permission to write, or evidence that the child is staffed. It omits
// prompts and skills; RoleDigest binds their exact installed values too.
type ProjectRoleRepairInspection struct {
	HomeID            string                  `json:"home_id"`
	HomeVersion       int64                   `json:"home_version"`
	HomeStateRevision int64                   `json:"home_state_revision"`
	ProjectID         string                  `json:"project_id"`
	ProjectVersion    int64                   `json:"project_version"`
	LinkID            string                  `json:"link_id"`
	LinkRevision      int64                   `json:"link_revision"`
	RoleDigest        string                  `json:"role_digest"`
	ProvenanceDigest  string                  `json:"provenance_digest"`
	Roles             []ProjectRoleRepairRole `json:"roles"`
}

type ProjectRoleRepairRole struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Primary  bool   `json:"primary"`
}

// InspectMissingSplitProjectRoles reads one exact, owner-linked child using a
// host-supplied installed-provider snapshot, without selecting a mirror winner
// or mutating an old child. A future repair must separately persist a reviewed
// consent receipt and re-run every check at commit under a recoverable write.
func InspectMissingSplitProjectRoles(store workspace.Store, installed []plugin.InstalledPlugin, ownerID, homeID, projectID string) (ProjectRoleRepairInspection, error) {
	refuse := func() (ProjectRoleRepairInspection, error) {
		return ProjectRoleRepairInspection{}, ErrProjectRoleRepairUnavailable
	}
	if store == nil || ownerID == "" || homeID == "" || projectID == "" || homeID == projectID {
		return refuse()
	}
	home, homeErr := store.Get(homeID)
	project, projectErr := store.Get(projectID)
	if homeErr != nil || projectErr != nil || home == nil || project == nil || home.ID != homeID || project.ID != projectID ||
		home.OwnerUserID != ownerID || project.OwnerUserID != ownerID || home.Kind != "group" ||
		home.Status != workspace.StatusActive || project.Status != workspace.StatusActive || project.ParentID != homeID {
		return refuse()
	}
	state, link := home.GetAssistantProgramState(), project.GetAssistantProjectLink()
	if state == nil || state.Declaration == nil || state.HomeProvider == nil || link == nil || link.HomeProvider == nil || link.ProjectProvider == nil ||
		state.SchemaVersion != workspace.AssistantProgramStateSchemaVersion || link.SchemaVersion != workspace.AssistantProjectLinkSchemaVersion ||
		link.DeclarationVersion != state.Declaration.SchemaVersion ||
		!state.HomeProvider.Valid() || !link.ProjectProvider.Valid() || state.Key.Normalize().OwnerUserID != ownerID ||
		state.Key.Normalize().PluginID != state.HomeProvider.PluginID || state.Key.Normalize().ProgramID != state.HomeProvider.ProgramID ||
		link.Key.Normalize() != state.Key.Normalize() || *link.HomeProvider != *state.HomeProvider ||
		link.StationWorkspaceID != homeID || link.ID != workspace.AssistantProjectLinkID(homeID, projectID) || link.StateRevision < 1 ||
		len(link.ProjectRoles) != 0 || link.ProjectBindings.StateRevision != 0 || len(link.ProjectBindings.Bindings) != 0 || len(project.GetAgentInstances()) != 0 ||
		!workspace.AssistantProjectLinkMirrorsAgree(store, project) {
		return refuse()
	}
	found := false
	for _, id := range state.LinkedProjectIDs {
		if id == projectID {
			if found {
				return refuse()
			}
			found = true
		}
	}
	if !found {
		return refuse()
	}
	portable := project
	if mirrors, ok := store.(workspace.MirrorWorkspaceProvider); ok {
		folderHome, mirrored, err := mirrors.GetMirrorWorkspace(homeID)
		if err != nil || (mirrored && (folderHome == nil || folderHome.ID != homeID || folderHome.OwnerUserID != ownerID ||
			folderHome.Kind != home.Kind || folderHome.ParentID != home.ParentID || folderHome.Status != home.Status)) {
			return refuse()
		}
		if mirrored {
			primaryJSON, primaryErr := json.Marshal(state)
			folderJSON, folderErr := json.Marshal(folderHome.GetAssistantProgramState())
			if primaryErr != nil || folderErr != nil || !bytes.Equal(primaryJSON, folderJSON) {
				return refuse()
			}
		}
		folder, mirrored, err := mirrors.GetMirrorWorkspace(projectID)
		if err != nil || (mirrored && folder == nil) {
			return refuse()
		}
		if mirrored {
			if primaryProvenance := project.GetTemplateProvenance(); primaryProvenance != nil {
				primaryJSON, primaryErr := json.Marshal(primaryProvenance)
				folderJSON, folderErr := json.Marshal(folder.GetTemplateProvenance())
				if primaryErr != nil || folderErr != nil || !bytes.Equal(primaryJSON, folderJSON) {
					return refuse()
				}
			}
			portable = folder
		}
	}
	provenance := portable.GetTemplateProvenance()
	if provenance == nil || provenance.GroupRequirement == nil || provenance.PluginOwner == nil || provenance.AssistantProgram == nil ||
		len(provenance.AssistantProjectRoles) != 0 || provenance.GroupRequirement.SelectedComposition != workspace.GroupRequirementCompositionGrouped ||
		(provenance.GroupRequirement.SourcePlugin != nil && *provenance.GroupRequirement.SourcePlugin != *provenance.PluginOwner) ||
		provenance.TemplateID != provenance.GroupRequirement.TemplateID ||
		provenance.PluginOwner.PluginID != link.ProjectProvider.PluginID || provenance.PluginOwner.PluginVersion != link.ProjectProvider.PluginVersion ||
		provenance.PluginOwner.BlueprintID != link.ProjectProvider.BlueprintID || provenance.PluginOwner.BlueprintVersion != link.ProjectProvider.BlueprintVersion ||
		provenance.GroupRequirement.ProjectProvider == nil || *provenance.GroupRequirement.ProjectProvider != *link.ProjectProvider ||
		provenance.GroupRequirement.HomeProvider == nil || *provenance.GroupRequirement.HomeProvider != *state.HomeProvider {
		return refuse()
	}
	for _, role := range state.Declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome {
			return refuse()
		}
	}
	originalHome, originalErr := json.Marshal(provenance.AssistantProgram)
	currentHome, currentErr := json.Marshal(state.Declaration)
	if originalErr != nil || currentErr != nil || !bytes.Equal(originalHome, currentHome) {
		return refuse()
	}
	lifecycle := workspace.EvaluateGroupRequirementLifecycle(portable, store.Get)
	if lifecycle == nil || lifecycle.State != workspace.GroupRequirementStatusReadyGrouped {
		return refuse()
	}
	roles, ok := plugin.ExactIndependentProjectRoles(installed, state.HomeProvider, link.ProjectProvider)
	if !ok {
		return refuse()
	}
	encoded, err := json.Marshal(roles)
	if err != nil {
		return refuse()
	}
	portableEvidence, err := json.Marshal(provenance)
	if err != nil {
		return refuse()
	}
	sum, portableSum := sha256.Sum256(encoded), sha256.Sum256(portableEvidence)
	result := ProjectRoleRepairInspection{HomeID: homeID, HomeVersion: home.Version, HomeStateRevision: state.StateRevision,
		ProjectID: projectID, ProjectVersion: project.Version, LinkID: link.ID,
		LinkRevision: link.StateRevision, RoleDigest: hex.EncodeToString(sum[:]), ProvenanceDigest: hex.EncodeToString(portableSum[:]),
		Roles: make([]ProjectRoleRepairRole, 0, len(roles))}
	for _, role := range roles {
		if role.Scope != workspace.AssistantRoleScopeProject || role.ID == "" || role.Label == "" {
			return refuse()
		}
		result.Roles = append(result.Roles, ProjectRoleRepairRole{ID: role.ID, Label: role.Label, Required: role.Required, Primary: role.Primary})
	}
	return result, nil
}
