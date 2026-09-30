package projectlibrary

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// InstalledPluginSource must be the host's live plugin manager, not a caller-
// supplied manifest or a browser-selected template. Eligibility is inert: it
// never issues a selection, installs a provider, creates a workspace or staffs
// an agent. A reviewed activation must perform its own fresh authority check.
type InstalledPluginSource interface {
	List() ([]plugin.InstalledPlugin, error)
}

type ActivationEligibility struct {
	EntryID           string   `json:"entry_id"`
	RootID            string   `json:"root_id,omitempty"` // Owner-only metadata, never a grant.
	RelativeFolder    string   `json:"relative_folder,omitempty"`
	State             string   `json:"state"`
	Reason            string   `json:"reason"`
	ProjectFiles      []string `json:"project_files,omitempty"`
	ProjectRoleLabels []string `json:"project_role_labels,omitempty"`
	BlueprintID       string   `json:"blueprint_id,omitempty"`
	WorkspaceID       string   `json:"workspace_id,omitempty"`
	// ObservedFormat is the catalog format of the first available observation,
	// set only with project_provider_unavailable. It is one of discovery's own
	// format identifiers, never a path, and lets the host decide whether a
	// reviewed integration could apply. It grants nothing.
	ObservedFormat string `json:"observed_format,omitempty"`
}

type ActivationInspector struct {
	library   *Store
	roots     *Roots
	installed InstalledPluginSource
	owners    projectconnection.FolderOwnerStore
}

func NewActivationInspector(library *Store, roots *Roots, installed InstalledPluginSource, owners projectconnection.FolderOwnerStore) *ActivationInspector {
	return &ActivationInspector{library: library, roots: roots, installed: installed, owners: owners}
}

// Eligibility never treats catalog status, a name, or a stale observation as
// project authority. It derives the compatible roster and existing-project
// mode only from a currently installed, reciprocally allowed blueprint. A
// positive result is guidance for a separate, freshly reviewed creator flow.
func (a *ActivationInspector) Eligibility(ctx context.Context, scope Scope, entryID string) (ActivationEligibility, error) {
	if a == nil || a.library == nil || a.roots == nil || a.installed == nil || a.owners == nil ||
		a.library.providerEvidence == nil || !validText(entryID, 160) || entryID == "" {
		return ActivationEligibility{}, ErrUnavailable
	}
	doc, state, err := a.library.readSnapshot(scope)
	if err != nil {
		return ActivationEligibility{}, err
	}
	var entry *Entry
	for i := range doc.Entries {
		if doc.Entries[i].ID == entryID {
			entry = &doc.Entries[i]
			break
		}
	}
	if entry == nil {
		return ActivationEligibility{}, ErrConflict
	}
	result := ActivationEligibility{EntryID: entryID, State: "unavailable", Reason: "A fresh, approved source is needed before project setup."}
	if entry.Link != nil {
		linked := make(map[string]bool, len(state.LinkedProjectIDs))
		for _, id := range state.LinkedProjectIDs {
			linked[id] = true
		}
		row := a.library.projectSearchRow(scope, *entry, nil, linked, nil)
		if row.Connection != "connected" {
			result.State, result.Reason = "link_needs_review", "The saved project link needs review; no new workspace will be created."
			return result, nil
		}
		result.State, result.Reason, result.WorkspaceID = "connected", "This exact project is already linked to this Home.", entry.Link.WorkspaceID
		return result, nil
	}
	// Revocation and last-observed unavailability are catalog facts, even
	// when the project integration is absent. Do not suggest an install will
	// restore a grant or a disconnected source.
	activeSource, revokedSource, availableObservation := false, false, false
	for _, observed := range entry.Observations {
		for _, root := range doc.Roots {
			if root.ID != observed.RootID {
				continue
			}
			inactive := root.RevokedAt != nil
			for _, id := range state.ProjectLibraryInactiveRoots {
				inactive = inactive || id == root.ID
			}
			if inactive {
				revokedSource = true
			} else {
				activeSource = true
				availableObservation = availableObservation || observed.Availability == "available" || observed.Availability == "ambiguous"
			}
			break
		}
	}
	if !activeSource && revokedSource {
		result.State, result.Reason = "revoked_source", "Discovery consent ended; reconnect through a new reviewed folder grant before setup."
		return result, nil
	}
	if activeSource && !availableObservation {
		result.State, result.Reason = "unavailable", "The last scan did not find an available project source; review a new scan before setup."
		return result, nil
	}
	home, err := a.library.workspaces.Get(scope.HomeID)
	var homeOwner *workspace.AssistantProgramHomeOwner
	if state != nil {
		homeOwner = state.HomeProvider
		if homeOwner == nil && state.GroupTemplate != nil {
			homeOwner = state.GroupTemplate.ProgramHomeOwner
		}
	}
	if err != nil || !a.library.providerWritable(scope, home) || homeOwner == nil {
		result.State, result.Reason = "home_provider_unavailable", "The Home provider is unavailable; saved catalog metadata remains readable."
		return result, nil
	}
	installed, err := a.installed.List()
	if err != nil || !plugin.IndependentHomeProviderEvidenceAvailable(installed, homeOwner) {
		result.State, result.Reason = "home_provider_unavailable", "The exact installed Home provider is required for project setup."
		return result, nil
	}
	chosen, selectedProject, providerAmbiguous := compatibleProjectBlueprint(installed, homeOwner, scope)
	if providerAmbiguous {
		result.State, result.Reason = "provider_ambiguous", "More than one compatible installed blueprint needs review."
		return result, nil
	}
	if chosen == nil {
		result.State, result.Reason = "project_provider_unavailable", "A compatible installed project integration with existing-project support is required; browsing never installs it."
		for _, observed := range entry.Observations {
			if observed.Availability == "available" || observed.Availability == "ambiguous" {
				result.Reason += " Last observed catalog format: " + observed.Format + ". This does not imply that any integration supports it."
				result.ObservedFormat = observed.Format
				break
			}
		}
		return result, nil
	}
	result.BlueprintID = chosen.QualifiedID
	for _, role := range selectedProject.Roles {
		result.ProjectRoleLabels = append(result.ProjectRoleLabels, role.Label)
	}
	for _, observed := range entry.Observations {
		if observed.Availability != "available" && observed.Availability != "ambiguous" {
			continue
		}
		var approved bool
		for _, root := range doc.Roots {
			if root.ID == observed.RootID && root.RevokedAt == nil {
				approved = true
				break
			}
		}
		if !approved {
			continue
		}
		usable := make([]string, 0, len(observed.Alternates))
		for _, name := range observed.Alternates {
			for _, ext := range chosen.Template.ProjectConnection.AttachExisting.EntryExtensions {
				if strings.EqualFold(filepath.Ext(name), ext) {
					usable = append(usable, name)
					break
				}
			}
		}
		if len(usable) == 0 {
			result.State, result.Reason = "unsupported_format", "No observed project file uses the installed blueprint's supported format."
			continue
		}
		root, verifyErr := a.roots.VerifyConnectedRoot(scope, observed.RootID)
		if verifyErr != nil {
			continue
		}
		rows, partial, readErr := a.roots.readDirectoryWithIdentity(ctx, scope, root.ID, observed.RelativeFolder, observed.FileIdentity, 5000)
		if readErr != nil || partial {
			continue
		}
		verified := make([]string, 0, len(usable))
		for _, name := range usable {
			for _, row := range rows {
				if row.Name == name && !row.IsDir && !row.IsLink && !row.Unreadable {
					verified = append(verified, name)
					break
				}
			}
		}
		if len(verified) != len(usable) {
			continue // An observed entry disappeared or changed; do not guess a replacement.
		}
		// This path is derived only from the pinned approved root and exact
		// observed relative folder. It is an inert owner check, not a grant or
		// a selection to pass to the project creator.
		folder := filepath.Join(root.Path, observed.RelativeFolder)
		owner, ownerErr := projectconnection.FindFolderOwner(a.owners, folder, "")
		if ownerErr != nil {
			continue
		}
		if owner != nil {
			result.State, result.Reason = "folder_owned", "This folder already belongs to a workspace; review its exact ownership before linking."
			result.WorkspaceID = "" // Never reveal a foreign workspace in this projection.
			return result, nil
		}
		result.ProjectFiles = verified
		result.RootID = root.ID
		result.RelativeFolder = observed.RelativeFolder
		if len(verified) != 1 || observed.Availability == "ambiguous" {
			result.State, result.Reason = "file_choice_required", "Choose the authoritative project file in a separate reviewed setup."
		} else {
			result.State, result.Reason = "review_available", "Review this one project's existing-file setup; no workspace or grant was created."
		}
		return result, nil
	}
	return result, nil
}

// compatibleProjectBlueprint is shared by the inert read and the reviewed
// creator boundary. Only the host's live enabled provider declarations count;
// a browser-supplied blueprint ID can never select or weaken this roster.
func compatibleProjectBlueprint(installed []plugin.InstalledPlugin, homeOwner *workspace.AssistantProgramHomeOwner, scope Scope) (*plugin.ResolvedBlueprint, *projecttemplates.AssistantProjectDeclaration, bool) {
	var chosen *plugin.ResolvedBlueprint
	var selectedProject *projecttemplates.AssistantProjectDeclaration
	for _, candidate := range installed {
		if !candidate.Enabled || candidate.WorkspaceSurfaces == nil || candidate.EvidenceGeneration() == 0 || candidate.ComponentFingerprint == "" {
			continue
		}
		for i := range candidate.ResolvedBlueprints {
			blueprint := &candidate.ResolvedBlueprints[i]
			template := blueprint.Template
			project := template.AssistantProject
			if template.GroupRequirement == nil || template.GroupRequirement.Policy != projecttemplates.GroupPolicyRequired ||
				template.GroupRequirement.SchemaVersion != projecttemplates.SplitGroupRequirementSchemaVersion ||
				project == nil || template.GroupRequirement.AssistantProjectID != project.ID ||
				template.PluginOwner == nil || template.PluginOwner.PluginID != candidate.Name ||
				template.PluginOwner.PluginVersion != candidate.Version || template.PluginOwner.BlueprintID != blueprint.ID ||
				template.PluginOwner.BlueprintVersion != blueprint.Version ||
				project.Home.ProviderPluginID != scope.ProviderID || project.Home.ProgramID != scope.ProgramID ||
				template.ProjectConnection == nil || !template.ProjectConnection.Supports(projecttemplates.ProjectConnectionExistingProject) ||
				template.ProjectConnection.AttachExisting == nil {
				continue
			}
			owner := &workspace.AssistantProjectProviderOwner{
				PluginID: candidate.Name, PluginVersion: candidate.Version,
				BlueprintID: blueprint.ID, BlueprintVersion: blueprint.Version,
				ProjectTeamID: project.ID, ProjectTeamSchema: project.SchemaVersion, ProjectTeamVersion: project.Version,
				ProjectTeamDigest: projecttemplates.AssistantProjectDigest(project),
				PluginGeneration:  candidate.EvidenceGeneration(), ComponentFingerprint: candidate.ComponentFingerprint,
			}
			if !plugin.IndependentProviderEvidenceAvailable(installed, homeOwner, owner) {
				continue
			}
			if chosen != nil {
				return nil, nil, true
			}
			chosen, selectedProject = blueprint, project
		}
	}
	return chosen, selectedProject, false
}
