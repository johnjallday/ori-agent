package homeupgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// derivePlan inspects the installed package, the release the Plugins page would
// install, and every Home pinned to the package. It writes nothing.
func (s *Service) derivePlan(ctx context.Context, ownerID, pluginID string) (Plan, Target, error) {
	installed, err := s.installedRecord(pluginID)
	if err != nil {
		return Plan{}, Target{}, err
	}
	if installed.WorkspaceSurfaces == nil || len(installed.WorkspaceSurfaces.AssistantProgramHomes) == 0 {
		return Plan{}, Target{}, ErrNoHomes
	}
	homes, trashed, err := s.pinnedHomes(installed.Name)
	if err != nil {
		return Plan{}, Target{}, err
	}
	if len(homes) == 0 {
		return Plan{}, Target{}, ErrNoHomes
	}
	if len(homes) > maxHomes {
		return Plan{}, Target{}, ErrTooLarge
	}
	target, err := s.plugins.Target(ctx, installed)
	if err != nil {
		if errors.Is(err, ErrCurrent) {
			return Plan{}, Target{}, ErrCurrent
		}
		return Plan{}, Target{}, errors.Join(ErrUnavailable, err)
	}
	programs, err := guidanceOnlyPrograms(installed, target)
	if err != nil {
		return Plan{}, Target{}, err
	}
	plan := Plan{
		OwnerID: ownerID, PluginID: installed.Name,
		FromVersion: installed.Version, FromFingerprint: installed.ComponentFingerprint, FromGeneration: installed.EvidenceGeneration(),
		ToVersion: target.Inspection.Descriptor.Version, ToFingerprint: target.Inspection.Fingerprint,
		Programs: programs, TrashedHomes: trashed,
	}
	homeIDs := make([]string, 0, len(homes))
	for _, home := range homes {
		planned, err := s.planHome(plan, ownerID, home)
		if err != nil {
			return Plan{}, Target{}, err
		}
		plan.Homes = append(plan.Homes, planned)
		homeIDs = append(homeIDs, home.ID)
	}
	if busy, err := s.repairActive(ctx, homeIDs); err != nil {
		return Plan{}, Target{}, errors.Join(ErrUnavailable, err)
	} else if busy {
		return Plan{}, Target{}, ErrRepairActive
	}
	return plan, target, nil
}

func (s *Service) installedRecord(pluginID string) (plugin.InstalledPlugin, error) {
	installed, found, err := s.installedPackage(pluginID)
	if err == nil && !found {
		err = ErrNoHomes
	}
	return installed, err
}

// pinnedHomes returns the live Homes whose provider pin names the package,
// sorted by ID, and counts those in the Trash. It reads the same provenance
// the replacement guard does.
func (s *Service) pinnedHomes(pluginID string) ([]*workspace.Workspace, int, error) {
	ids, err := s.workspaces.List()
	if err != nil {
		return nil, 0, errors.Join(ErrUnavailable, err)
	}
	var homes []*workspace.Workspace
	trashed := 0
	for _, id := range ids {
		candidate, err := s.workspaces.Get(id)
		if err != nil || candidate == nil {
			return nil, 0, errors.Join(ErrUnavailable, err)
		}
		state := candidate.GetAssistantProgramState()
		if state == nil {
			continue
		}
		owner := state.HomeProvider
		if owner == nil && state.GroupTemplate != nil {
			owner = state.GroupTemplate.ProgramHomeOwner
		}
		if owner == nil || owner.PluginID != pluginID {
			continue
		}
		if candidate.Status == workspace.StatusTrashed || candidate.Status == workspace.StatusMissing {
			trashed++
			continue
		}
		homes = append(homes, candidate)
	}
	slices.SortFunc(homes, func(a, b *workspace.Workspace) int { return strings.Compare(a.ID, b.ID) })
	return homes, trashed, nil
}

// guidanceOnlyPrograms accepts a target that differs from the installed
// package only in Home role prompts and skill text, and describes each Home
// declaration's move.
func guidanceOnlyPrograms(installed plugin.InstalledPlugin, target Target) ([]PlanProgram, error) {
	next := target.Inspection.Descriptor
	if next.Name != installed.Name || next.WorkspaceSurfaces == nil || len(target.Inspection.Fingerprint) != 64 {
		return nil, ErrNotGuidanceOnly
	}
	if next.Version == installed.Version && target.Inspection.Fingerprint == installed.ComponentFingerprint {
		return nil, ErrCurrent
	}
	if order, comparable := reviewedintegration.CompareVersions(next.Version, installed.Version); comparable && order < 0 {
		return nil, ErrCurrent // Moving back to an older release is not offered.
	}
	// The package must still contribute nothing but its Homes: no services,
	// blueprints or MCP servers, and the same skill names.
	if len(next.MCPServers) != 0 || len(installed.MCPServers) != 0 || len(next.ResolvedBlueprints) != 0 || len(installed.ResolvedBlueprints) != 0 {
		return nil, ErrNotGuidanceOnly
	}
	nextSkills := make([]string, 0, len(next.Skills))
	for _, skill := range next.Skills {
		nextSkills = append(nextSkills, skill.Name)
	}
	installedSkills := append([]string(nil), installed.Skills...)
	slices.Sort(nextSkills)
	slices.Sort(installedSkills)
	if !slices.Equal(nextSkills, installedSkills) {
		return nil, ErrNotGuidanceOnly
	}
	currentHomes, nextHomes := installed.WorkspaceSurfaces.AssistantProgramHomes, next.WorkspaceSurfaces.AssistantProgramHomes
	if len(currentHomes) != len(nextHomes) {
		return nil, ErrNotGuidanceOnly
	}
	programs := make([]PlanProgram, 0, len(currentHomes))
	addsProfile := false
	for index := range currentHomes {
		change, err := projecttemplates.AcceptedHomeChange(currentHomes[index], nextHomes[index])
		if err != nil {
			return nil, ErrNotGuidanceOnly
		}
		addsProfile = addsProfile || change.AddsHomeProfile
		home := currentHomes[index]
		programs = append(programs, PlanProgram{
			ProgramID: home.ID,
			From: workspace.AssistantProgramHomeOwner{
				PluginID: installed.Name, PluginVersion: installed.Version, ProgramID: home.ID,
				HomeSchemaVersion: home.SchemaVersion, HomeVersion: home.Version,
				DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
				PluginGeneration:  installed.EvidenceGeneration(), ComponentFingerprint: installed.ComponentFingerprint,
			},
			ToDeclarationDigest: projecttemplates.AssistantProgramHomeDigest(nextHomes[index]),
			RolePrompts:         change.RolePrompts,
			AddsHomeProfile:     change.AddsHomeProfile, HomeProfileTitle: change.HomeProfileTitle,
		})
	}
	// Everything outside the Home declarations must be byte-identical, with one
	// exception that belongs to the additive profile class: a release that adds
	// a profile card must also start requiring the host feature that gates it.
	// That one feature is set aside, and only when a card was actually added
	// and the installed release did not already require it.
	withoutHomes := func(contribution plugin.SurfaceContribution, setAsideProfileFeature bool) ([]byte, error) {
		contribution.Name, contribution.Version, contribution.AssistantProgramHomes = "", "", nil
		if setAsideProfileFeature {
			contribution.RequiresHostFeatures = slices.DeleteFunc(slices.Clone(contribution.RequiresHostFeatures),
				func(feature string) bool { return feature == plugin.HostFeatureHomeProfileV1 })
		}
		return json.Marshal(contribution)
	}
	current, err := withoutHomes(*installed.WorkspaceSurfaces, false)
	if err != nil {
		return nil, ErrNotGuidanceOnly
	}
	setAside := addsProfile && !slices.Contains(installed.WorkspaceSurfaces.RequiresHostFeatures, plugin.HostFeatureHomeProfileV1)
	candidate, err := withoutHomes(*next.WorkspaceSurfaces, setAside)
	if err != nil || !bytes.Equal(current, candidate) {
		return nil, ErrNotGuidanceOnly
	}
	return programs, nil
}

// upgrade describes the move of one program's Homes. Before the replacement
// the target's content generation is not yet known; review checks use the
// generation the replacement will assign.
func (p Plan) upgrade(programID, operationID string, generation uint64, at time.Time) (workspace.HomeProviderUpgrade, bool) {
	for _, program := range p.Programs {
		if program.ProgramID != programID {
			continue
		}
		to := program.From
		to.PluginVersion, to.DeclarationDigest = p.ToVersion, program.ToDeclarationDigest
		to.PluginGeneration, to.ComponentFingerprint = generation, p.ToFingerprint
		prompts := make(map[string]workspace.HomeRolePromptChange, len(program.RolePrompts))
		for _, change := range program.RolePrompts {
			prompts[change.RoleID] = workspace.HomeRolePromptChange{Old: change.Old, New: change.New}
		}
		return workspace.HomeProviderUpgrade{OperationID: operationID, From: program.From, To: to, RolePrompts: prompts, UpgradedAt: at.UTC()}, true
	}
	return workspace.HomeProviderUpgrade{}, false
}

func (p Plan) program(programID string) (PlanProgram, bool) {
	for _, program := range p.Programs {
		if program.ProgramID == programID {
			return program, true
		}
	}
	return PlanProgram{}, false
}

func (s *Service) planHome(plan Plan, ownerID string, home *workspace.Workspace) (PlanHome, error) {
	state := home.GetAssistantProgramState()
	if home.OwnerUserID != ownerID || state.Key.Normalize().OwnerUserID != strings.TrimSpace(ownerID) {
		return PlanHome{}, ErrOtherOwner
	}
	if state.HomeProvider == nil {
		return PlanHome{}, ErrPinMismatch
	}
	program, found := plan.program(state.HomeProvider.ProgramID)
	upgrade, ok := plan.upgrade(state.HomeProvider.ProgramID, "review", plan.FromGeneration+1, s.now())
	if !found || !ok {
		return PlanHome{}, ErrPinMismatch
	}
	if pending, err := upgrade.CheckHome(home); err != nil || !pending {
		return PlanHome{}, ErrPinMismatch
	}
	if !homeMirrorsAgree(s.workspaces, home) {
		return PlanHome{}, ErrMirrorsDisagree
	}
	planned := PlanHome{HomeID: home.ID, Name: home.Name, ProgramID: program.ProgramID, StateRevision: state.StateRevision}
	projects, err := workspace.NewAssistantProgramStore(s.workspaces).LinkedProjects(home.ID)
	if err != nil {
		return PlanHome{}, errors.Join(ErrUnavailable, err)
	}
	if len(projects) > maxProjectsPerHome {
		return PlanHome{}, ErrTooLarge
	}
	for _, project := range projects {
		if _, err := upgrade.CheckProject(project, home.ID, state.Declaration); err != nil {
			return PlanHome{}, ErrPinMismatch
		}
		if !projectMirrorsAgree(s.workspaces, project) {
			return PlanHome{}, ErrMirrorsDisagree
		}
		planned.Projects = append(planned.Projects, PlanProject{ID: project.ID, Name: project.Name})
	}
	slices.SortFunc(planned.Projects, func(a, b PlanProject) int { return strings.Compare(a.ID, b.ID) })
	for _, change := range program.RolePrompts {
		for _, binding := range state.HomeBindings.Bindings {
			if binding.RoleID != change.RoleID {
				continue
			}
			profile, profileFound := s.profiles.GetAgent(binding.AgentName)
			copyAgent, copyFound, copyErr := s.workspaces.GetWorkspaceAgent(home.ID, binding.AgentName)
			if copyErr != nil {
				return PlanHome{}, errors.Join(ErrUnavailable, copyErr)
			}
			planned.Agents = append(planned.Agents, PlanAgent{
				RoleID: change.RoleID, RoleLabel: change.Label, AgentName: binding.AgentName,
				Profile:  promptAction(profileFound && profile != nil, promptOf(profile), change.Old),
				HomeCopy: promptAction(copyFound && copyAgent != nil, promptOf(copyAgent), change.Old),
			})
		}
	}
	return planned, nil
}

func promptAction(found bool, prompt, old string) string {
	switch {
	case !found:
		return AgentMissing
	case prompt == old:
		return AgentReplace
	default:
		return AgentKeep
	}
}

func homeMirrorsAgree(store workspace.Store, home *workspace.Workspace) bool {
	mirrors, ok := store.(workspace.MirrorWorkspaceProvider)
	if !ok {
		return true
	}
	folder, mirrored, err := mirrors.GetMirrorWorkspace(home.ID)
	if err != nil {
		return false
	}
	return !mirrored || (folder != nil && sameJSON(home.GetAssistantProgramState(), folder.GetAssistantProgramState()))
}

func projectMirrorsAgree(store workspace.Store, project *workspace.Workspace) bool {
	if !workspace.AssistantProjectLinkMirrorsAgree(store, project) {
		return false
	}
	mirrors, ok := store.(workspace.MirrorWorkspaceProvider)
	if !ok {
		return true
	}
	folder, mirrored, err := mirrors.GetMirrorWorkspace(project.ID)
	if err != nil {
		return false
	}
	// The lean SQLite projection has no provenance; only compare a primary copy that has one.
	primary := project.GetTemplateProvenance()
	return !mirrored || primary == nil || (folder != nil && sameJSON(primary, folder.GetTemplateProvenance()))
}

func planDigest(plan Plan) (string, error) {
	data, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sameJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
