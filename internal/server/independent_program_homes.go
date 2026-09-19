package server

import (
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var errIndependentProgramHomeUnavailable = errors.New("independent Assistant Program Home is unavailable")

// resolveIndependentProgramHome performs the reciprocal, two-provider trust
// join for one already-trusted project template. It is read-only and never
// installs, enables, creates, links, staffs, or grants anything.
func resolveIndependentProgramHome(installed []plugin.InstalledPlugin, ownerUserID string, template projecttemplates.Template, requireHome bool) (grouprequirements.IndependentHomeResolution, error) {
	project := template.AssistantProject
	if project == nil || project.SchemaVersion != projecttemplates.AssistantProjectSchemaVersion || strings.TrimSpace(ownerUserID) == "" {
		return grouprequirements.IndependentHomeResolution{}, errIndependentProgramHomeUnavailable
	}
	pluginID, pluginVersion, blueprintID, blueprintVersion, ok := splitProjectSource(template)
	if !ok {
		return grouprequirements.IndependentHomeResolution{}, errIndependentProgramHomeUnavailable
	}
	var projectInstall *plugin.InstalledPlugin
	var projectBlueprint *plugin.ResolvedBlueprint
	for index := range installed {
		candidate := &installed[index]
		if !strings.EqualFold(candidate.Name, pluginID) || !pluginBlueprintsActive(*candidate) || candidate.Version != pluginVersion || candidate.Generation == 0 || !lowerDigest(candidate.ComponentFingerprint) {
			continue
		}
		for blueprintIndex := range candidate.ResolvedBlueprints {
			blueprint := &candidate.ResolvedBlueprints[blueprintIndex]
			if blueprint.ID == blueprintID && blueprint.Version == blueprintVersion && blueprint.Template.AssistantProject != nil &&
				projecttemplates.AssistantProjectDigest(blueprint.Template.AssistantProject) == projecttemplates.AssistantProjectDigest(project) {
				if projectInstall != nil {
					return grouprequirements.IndependentHomeResolution{}, errIndependentProgramHomeUnavailable
				}
				projectInstall, projectBlueprint = candidate, blueprint
			}
		}
	}
	if projectInstall == nil || projectBlueprint == nil || !requiresFeature(projectInstall.WorkspaceSurfaces, plugin.HostFeatureIndependentProgramHomesV1) ||
		!assistantProjectSkillsPackaged(*projectInstall, project) {
		return grouprequirements.IndependentHomeResolution{}, errIndependentProgramHomeUnavailable
	}
	projectOwner := &workspace.AssistantProjectProviderOwner{
		PluginID: projectInstall.Name, PluginVersion: projectInstall.Version,
		BlueprintID: projectBlueprint.ID, BlueprintVersion: projectBlueprint.Version,
		ProjectTeamID: project.ID, ProjectTeamSchema: project.SchemaVersion, ProjectTeamVersion: project.Version,
		ProjectTeamDigest: projecttemplates.AssistantProjectDigest(project),
		PluginGeneration:  projectInstall.EvidenceGeneration(), ComponentFingerprint: projectInstall.ComponentFingerprint,
	}
	resolution := grouprequirements.IndependentHomeResolution{ProjectOwner: projectOwner}
	if !requireHome {
		return resolution, nil
	}

	var homeInstall *plugin.InstalledPlugin
	var homeDeclaration *projecttemplates.AssistantProgramHome
	for index := range installed {
		candidate := &installed[index]
		if !strings.EqualFold(candidate.Name, project.Home.ProviderPluginID) || !pluginBlueprintsActive(*candidate) ||
			candidate.Generation == 0 || !lowerDigest(candidate.ComponentFingerprint) || !requiresFeature(candidate.WorkspaceSurfaces, plugin.HostFeatureIndependentProgramHomesV1) {
			continue
		}
		for homeIndex := range candidate.WorkspaceSurfaces.AssistantProgramHomes {
			home := &candidate.WorkspaceSurfaces.AssistantProgramHomes[homeIndex]
			if home.ID != project.Home.ProgramID || home.SchemaVersion != project.Home.HomeSchemaVersion ||
				home.Version < project.Home.MinHomeVersion || home.Version > project.Home.MaxHomeVersion ||
				!assistantProgramHomeSkillsPackaged(*candidate, *home) || !homeAllowsProject(*home, *projectOwner) {
				continue
			}
			if homeInstall != nil {
				return grouprequirements.IndependentHomeResolution{}, errIndependentProgramHomeUnavailable
			}
			homeInstall, homeDeclaration = candidate, home
		}
	}
	if homeInstall == nil || homeDeclaration == nil || roleIDsOverlap(homeDeclaration.AssistantProgram().Roles, project.ProgramRoles()) {
		return grouprequirements.IndependentHomeResolution{}, errIndependentProgramHomeUnavailable
	}
	homeOwner := &workspace.AssistantProgramHomeOwner{
		PluginID: homeInstall.Name, PluginVersion: homeInstall.Version, ProgramID: homeDeclaration.ID,
		HomeSchemaVersion: homeDeclaration.SchemaVersion, HomeVersion: homeDeclaration.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(*homeDeclaration),
		PluginGeneration:  homeInstall.EvidenceGeneration(), ComponentFingerprint: homeInstall.ComponentFingerprint,
	}
	resolution.Key = workspace.AssistantProgramKey{OwnerUserID: strings.TrimSpace(ownerUserID), PluginID: homeOwner.PluginID, ProgramID: homeOwner.ProgramID}.Normalize()
	resolution.Declaration = homeDeclaration.AssistantProgram()
	resolution.Owner = homeOwner
	return resolution, nil
}

func splitProjectSource(template projecttemplates.Template) (pluginID, pluginVersion, blueprintID string, blueprintVersion int, ok bool) {
	switch {
	case template.PluginOwner != nil:
		owner := template.PluginOwner
		return owner.PluginID, owner.PluginVersion, owner.BlueprintID, owner.BlueprintVersion, owner.PluginID != "" && owner.BlueprintID != ""
	case template.TemplateVariant != nil:
		source := template.TemplateVariant.Source
		return source.PluginID, source.PluginVersion, source.BlueprintID, source.BlueprintVersion, source.PluginID != "" && source.BlueprintID != ""
	default:
		return "", "", "", 0, false
	}
}

func assistantProjectSkillsPackaged(installed plugin.InstalledPlugin, project *projecttemplates.AssistantProjectDeclaration) bool {
	packaged := make(map[string]struct{}, len(installed.Skills))
	for _, skill := range installed.Skills {
		packaged[strings.ToLower(strings.TrimSpace(skill))] = struct{}{}
	}
	for _, role := range project.Roles {
		for _, skill := range role.Skills {
			if _, found := packaged[strings.ToLower(strings.TrimSpace(skill))]; !found {
				return false
			}
		}
	}
	return true
}

func homeAllowsProject(home projecttemplates.AssistantProgramHome, project workspace.AssistantProjectProviderOwner) bool {
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

func roleIDsOverlap(left, right []workspace.AssistantProgramRoleSpec) bool {
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

func requiresFeature(contribution *plugin.SurfaceContribution, feature string) bool {
	if contribution == nil {
		return false
	}
	for _, candidate := range contribution.RequiresHostFeatures {
		if candidate == feature {
			return true
		}
	}
	return false
}

func lowerDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
