package plugin

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// IndependentHomeProviderEvidenceAvailable revalidates only the Home-owned
// declaration and packaged Home-role skills. Project-local behavior does not
// depend on this result.
func IndependentHomeProviderEvidenceAvailable(installed []InstalledPlugin, owner *workspace.AssistantProgramHomeOwner) bool {
	_, home := matchingIndependentHome(installed, owner)
	return home != nil
}

// IndependentProjectProviderEvidenceAvailable revalidates only the
// project-owned declaration and packaged project-role skills. Home management
// does not depend on this result.
func IndependentProjectProviderEvidenceAvailable(installed []InstalledPlugin, owner *workspace.AssistantProjectProviderOwner) bool {
	_, project := matchingIndependentProject(installed, owner)
	return project != nil
}

// IndependentProviderEvidenceAvailable additionally verifies the reciprocal
// attachment contract required by cross-provider coordination.
func IndependentProviderEvidenceAvailable(installed []InstalledPlugin, homeOwner *workspace.AssistantProgramHomeOwner, projectOwner *workspace.AssistantProjectProviderOwner) bool {
	homePlugin, home := matchingIndependentHome(installed, homeOwner)
	projectPlugin, project := matchingIndependentProject(installed, projectOwner)
	return homePlugin != nil && projectPlugin != nil && home != nil && project != nil &&
		project.Home.ProviderPluginID == homeOwner.PluginID && project.Home.ProgramID == homeOwner.ProgramID &&
		project.Home.HomeSchemaVersion == homeOwner.HomeSchemaVersion && home.Version >= project.Home.MinHomeVersion && home.Version <= project.Home.MaxHomeVersion &&
		independentHomeAllowsProject(*home, *projectOwner) && !independentRoleIDsOverlap(home.AssistantProgram().Roles, project.ProgramRoles())
}

func matchingIndependentHome(installed []InstalledPlugin, owner *workspace.AssistantProgramHomeOwner) (*InstalledPlugin, *projecttemplates.AssistantProgramHome) {
	if owner == nil || !owner.Valid() {
		return nil, nil
	}
	var matchedPlugin *InstalledPlugin
	var matchedHome *projecttemplates.AssistantProgramHome
	for index := range installed {
		candidate := &installed[index]
		if !independentContributionActive(*candidate) || !independentFeatureRequired(candidate.WorkspaceSurfaces) ||
			!strings.EqualFold(candidate.Name, owner.PluginID) || candidate.Version != owner.PluginVersion ||
			candidate.EvidenceGeneration() != owner.PluginGeneration || candidate.ComponentFingerprint != owner.ComponentFingerprint {
			continue
		}
		for homeIndex := range candidate.WorkspaceSurfaces.AssistantProgramHomes {
			declaration := &candidate.WorkspaceSurfaces.AssistantProgramHomes[homeIndex]
			if declaration.ID == owner.ProgramID && declaration.SchemaVersion == owner.HomeSchemaVersion && declaration.Version == owner.HomeVersion &&
				projecttemplates.AssistantProgramHomeDigest(*declaration) == owner.DeclarationDigest && independentRolesPackaged(candidate.Skills, declaration.AssistantProgram().Roles) {
				if matchedHome != nil {
					return nil, nil
				}
				matchedPlugin, matchedHome = candidate, declaration
			}
		}
	}
	return matchedPlugin, matchedHome
}

func matchingIndependentProject(installed []InstalledPlugin, owner *workspace.AssistantProjectProviderOwner) (*InstalledPlugin, *projecttemplates.AssistantProjectDeclaration) {
	if owner == nil || !owner.Valid() {
		return nil, nil
	}
	var matchedPlugin *InstalledPlugin
	var matchedProject *projecttemplates.AssistantProjectDeclaration
	for index := range installed {
		candidate := &installed[index]
		if !independentContributionActive(*candidate) || !independentFeatureRequired(candidate.WorkspaceSurfaces) ||
			!strings.EqualFold(candidate.Name, owner.PluginID) || candidate.Version != owner.PluginVersion ||
			candidate.EvidenceGeneration() != owner.PluginGeneration || candidate.ComponentFingerprint != owner.ComponentFingerprint {
			continue
		}
		for blueprintIndex := range candidate.ResolvedBlueprints {
			blueprint := &candidate.ResolvedBlueprints[blueprintIndex]
			declaration := blueprint.Template.AssistantProject
			if blueprint.ID == owner.BlueprintID && blueprint.Version == owner.BlueprintVersion && declaration != nil &&
				declaration.ID == owner.ProjectTeamID && declaration.SchemaVersion == owner.ProjectTeamSchema &&
				declaration.Version == owner.ProjectTeamVersion && projecttemplates.AssistantProjectDigest(declaration) == owner.ProjectTeamDigest &&
				independentRolesPackaged(candidate.Skills, declaration.ProgramRoles()) {
				if matchedProject != nil {
					return nil, nil
				}
				matchedPlugin, matchedProject = candidate, declaration
			}
		}
	}
	return matchedPlugin, matchedProject
}

func independentContributionActive(candidate InstalledPlugin) bool {
	if !candidate.Enabled || candidate.WorkspaceSurfaces == nil {
		return false
	}
	protocol := candidate.WorkspaceSurfaces.Protocol
	maximum := max(protocol.Max, protocol.Min)
	if protocol.Min > SurfaceProtocolVersion || maximum < SurfaceProtocolVersion {
		return false
	}
	for _, artifact := range candidate.ResolvedArtifacts {
		if !artifact.Available {
			return false
		}
	}
	return true
}

func independentFeatureRequired(contribution *SurfaceContribution) bool {
	if contribution == nil {
		return false
	}
	for _, feature := range contribution.RequiresHostFeatures {
		if feature == HostFeatureIndependentProgramHomesV1 {
			return true
		}
	}
	return false
}

func independentRolesPackaged(skills []string, roles []workspace.AssistantProgramRoleSpec) bool {
	packaged := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		packaged[strings.ToLower(strings.TrimSpace(skill))] = struct{}{}
	}
	for _, role := range roles {
		for _, skill := range role.Skills {
			if _, found := packaged[strings.ToLower(strings.TrimSpace(skill))]; !found {
				return false
			}
		}
	}
	return true
}

func independentHomeAllowsProject(home projecttemplates.AssistantProgramHome, project workspace.AssistantProjectProviderOwner) bool {
	matches := 0
	for _, allowed := range home.AllowedProjectAttachments {
		if allowed.ProviderPluginID == project.PluginID && allowed.BlueprintID == project.BlueprintID &&
			allowed.ProjectTeamID == project.ProjectTeamID && allowed.ProjectTeamSchemaVersion == project.ProjectTeamSchema &&
			project.ProjectTeamVersion >= allowed.MinProjectTeamVersion && project.ProjectTeamVersion <= allowed.MaxProjectTeamVersion {
			matches++
		}
	}
	return matches == 1
}

func independentRoleIDsOverlap(left, right []workspace.AssistantProgramRoleSpec) bool {
	seen := make(map[string]struct{}, len(left))
	for _, role := range left {
		seen[role.ID] = struct{}{}
	}
	for _, role := range right {
		if _, exists := seen[role.ID]; exists {
			return true
		}
	}
	return false
}
