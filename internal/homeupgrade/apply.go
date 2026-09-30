package homeupgrade

import (
	"context"
	"errors"
	"slices"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// errUnchanged ends a store update without writing: the record already holds
// the upgrade (a rerun) or an agent prompt was edited in the meantime.
var errUnchanged = errors.New("unchanged")

// drive advances an operation from its current status. target is the release
// a Commit resolved; recovery passes nil and never installs anything. An error
// with the operation still replaced is retried by the next recovery.
func (s *Service) drive(ctx context.Context, operation Operation, target *Target) (Operation, error) {
	if operation.Status == StatusClaimed {
		if err := s.replace(ctx, &operation, target); err != nil {
			return operation, err
		}
	}
	if operation.Status == StatusReplaced {
		if err := s.rebindAndStaff(ctx, &operation); err != nil {
			return operation, err
		}
	}
	return operation, nil
}

// replace is step 2: install exactly the target, then record the installed
// evidence it produced.
func (s *Service) replace(ctx context.Context, operation *Operation, target *Target) error {
	plan := operation.Plan
	installed, found, err := s.installedPackage(plan.PluginID)
	if err != nil {
		return err
	}
	if found && onFrom(installed, plan) {
		if target == nil {
			operation.Outcome.Reason = "The upgrade was interrupted before the new release was installed. Nothing changed; review the upgrade again."
			return s.transition(ctx, operation, StatusClaimed, StatusCancelled)
		}
		if replaceErr := s.plugins.Replace(ctx, plan.PluginID, *target); replaceErr != nil {
			after, afterFound, afterErr := s.installedPackage(plan.PluginID)
			if afterErr == nil && afterFound && onFrom(after, plan) {
				operation.Outcome.Reason = "The new release could not be installed. Nothing changed; review the upgrade again."
				if err := s.transition(ctx, operation, StatusClaimed, StatusCancelled); err != nil {
					return errors.Join(err, replaceErr)
				}
				return errors.Join(ErrUnavailable, replaceErr)
			}
			return s.reconcile(ctx, operation, StatusClaimed, "The plugin replacement stopped partway.", replaceErr)
		}
		if installed, found, err = s.installedPackage(plan.PluginID); err != nil {
			return err
		}
	}
	if !found || !onTarget(installed, plan) {
		return s.reconcile(ctx, operation, StatusClaimed, "The installed package is neither the reviewed release nor the one it replaces.", nil)
	}
	operation.TargetGeneration = installed.EvidenceGeneration()
	return s.transition(ctx, operation, StatusClaimed, StatusReplaced)
}

// rebindAndStaff is steps 3–5. Every write is idempotent, so a rerun after a
// crash repeats it safely.
func (s *Service) rebindAndStaff(ctx context.Context, operation *Operation) error {
	plan := operation.Plan
	live, err := s.liveWorkspaceIDs()
	if err != nil {
		return err
	}
	upgrades := make(map[string]workspace.HomeProviderUpgrade, len(plan.Homes))
	for _, planned := range plan.Homes {
		upgrade, ok := plan.upgrade(planned.ProgramID, operation.ID, operation.TargetGeneration, s.now())
		if !ok {
			return s.reconcile(ctx, operation, StatusReplaced, "The recorded plan names an unknown Home declaration.", nil)
		}
		if !live[planned.HomeID] {
			continue // A Home removed or trashed since the review keeps its old pin.
		}
		if err := s.update(planned.HomeID, func(current *workspace.Workspace) (bool, error) {
			return upgrade.RebindHome(current)
		}); err != nil {
			return s.stop(ctx, operation, "Home "+planned.HomeID+" is pinned to neither release.", err)
		}
		upgrades[planned.HomeID] = upgrade
		home, err := s.workspaces.Get(planned.HomeID)
		if err != nil {
			return errors.Join(ErrUnavailable, err)
		}
		declaration := home.GetAssistantProgramState().Declaration
		projectIDs := make([]string, 0, len(planned.Projects))
		for _, project := range planned.Projects {
			projectIDs = append(projectIDs, project.ID)
		}
		// A project linked after the review is moved too.
		linked, err := workspace.NewAssistantProgramStore(s.workspaces).LinkedProjects(planned.HomeID)
		if err != nil {
			return errors.Join(ErrUnavailable, err)
		}
		for _, project := range linked {
			if !slices.Contains(projectIDs, project.ID) {
				projectIDs = append(projectIDs, project.ID)
			}
		}
		for _, projectID := range projectIDs {
			if !live[projectID] {
				continue
			}
			if err := s.update(projectID, func(current *workspace.Workspace) (bool, error) {
				return upgrade.RebindProject(current, planned.HomeID, declaration)
			}); err != nil {
				return s.stop(ctx, operation, "Project "+projectID+" is pinned to neither release.", err)
			}
		}
	}

	var outcomes []AgentOutcome
	for _, planned := range plan.Homes {
		upgrade, moved := upgrades[planned.HomeID]
		program, _ := plan.program(planned.ProgramID)
		if !moved || len(program.RolePrompts) == 0 {
			continue
		}
		home, err := s.workspaces.Get(planned.HomeID)
		if err != nil {
			return errors.Join(ErrUnavailable, err)
		}
		state := home.GetAssistantProgramState()
		if state == nil || state.HomeProvider == nil || *state.HomeProvider != upgrade.To {
			continue
		}
		for _, change := range program.RolePrompts {
			for _, binding := range state.HomeBindings.Bindings {
				if binding.RoleID != change.RoleID {
					continue
				}
				profile, err := s.replaceProfilePrompt(binding.AgentName, change)
				if err != nil {
					return errors.Join(ErrUnavailable, err)
				}
				homeCopy, err := s.replaceHomeCopyPrompt(planned.HomeID, binding.AgentName, change)
				if err != nil {
					return errors.Join(ErrUnavailable, err)
				}
				outcomes = append(outcomes, AgentOutcome{HomeID: planned.HomeID, RoleID: change.RoleID, AgentName: binding.AgentName, Profile: profile, HomeCopy: homeCopy})
			}
		}
	}
	operation.Outcome.Agents = outcomes
	return s.transition(ctx, operation, StatusReplaced, StatusSucceeded)
}

// update applies one idempotent rebind as a fenced store write, writing
// nothing when the record already holds the upgrade.
func (s *Service) update(id string, rebind func(*workspace.Workspace) (bool, error)) error {
	err := s.workspaces.Update(id, func(current *workspace.Workspace) error {
		changed, err := rebind(current)
		if err != nil {
			return err
		}
		if !changed {
			return errUnchanged
		}
		return nil
	})
	if errors.Is(err, errUnchanged) {
		return nil
	}
	return err
}

// stop turns an unexpected pin into reconcile_required. Any other failure is
// left for the next recovery to retry.
func (s *Service) stop(ctx context.Context, operation *Operation, reason string, err error) error {
	if errors.Is(err, workspace.ErrHomeProviderPinUnexpected) {
		return s.reconcile(ctx, operation, StatusReplaced, reason, err)
	}
	return errors.Join(ErrUnavailable, err)
}

func (s *Service) reconcile(ctx context.Context, operation *Operation, from, reason string, cause error) error {
	operation.Outcome.Reason = reason
	if err := s.transition(ctx, operation, from, StatusReconcileRequired); err != nil {
		return errors.Join(ErrUnavailable, err, cause)
	}
	return errors.Join(ErrReconcileRequired, cause)
}

func (s *Service) replaceProfilePrompt(name string, change projecttemplates.HomeRolePromptChange) (string, error) {
	profile, found := s.profiles.GetAgent(name)
	if !found || profile == nil {
		return AgentMissing, nil
	}
	switch profile.Settings.SystemPrompt {
	case change.New:
		return AgentReplace, nil
	case change.Old:
	default:
		return AgentKeep, nil
	}
	err := s.profiles.UpdateAgent(name, func(current *agent.Agent) error {
		if current.Settings.SystemPrompt != change.Old {
			return errUnchanged // edited since it was read: keep the owner's text
		}
		current.Settings.SystemPrompt = change.New
		return nil
	})
	switch {
	case errors.Is(err, errUnchanged):
		return AgentKeep, nil
	case err != nil:
		return "", err
	}
	return AgentReplace, nil
}

func (s *Service) replaceHomeCopyPrompt(homeID, name string, change projecttemplates.HomeRolePromptChange) (string, error) {
	copyAgent, found, err := s.workspaces.GetWorkspaceAgent(homeID, name)
	if err != nil {
		return "", err
	}
	if !found || copyAgent == nil {
		return AgentMissing, nil
	}
	switch copyAgent.Settings.SystemPrompt {
	case change.New:
		return AgentReplace, nil
	case change.Old:
	default:
		return AgentKeep, nil
	}
	copyAgent.Settings.SystemPrompt = change.New
	if err := s.workspaces.SaveWorkspaceAgent(homeID, name, copyAgent); err != nil {
		return "", err
	}
	return AgentReplace, nil
}

// installedPackage reads the installed record; found is false once the package
// is uninstalled.
func (s *Service) installedPackage(pluginID string) (plugin.InstalledPlugin, bool, error) {
	installed, err := s.plugins.List()
	if err != nil {
		return plugin.InstalledPlugin{}, false, errors.Join(ErrUnavailable, err)
	}
	for _, candidate := range installed {
		if candidate.Name == pluginID {
			return candidate, true, nil
		}
	}
	return plugin.InstalledPlugin{}, false, nil
}

// liveWorkspaceIDs are the workspaces that still exist and are not in the
// Trash. A record that cannot be read is an error to retry, never a skip.
func (s *Service) liveWorkspaceIDs() (map[string]bool, error) {
	ids, err := s.workspaces.List()
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		current, err := s.workspaces.Get(id)
		if err != nil || current == nil {
			return nil, errors.Join(ErrUnavailable, err)
		}
		live[id] = current.Status != workspace.StatusTrashed && current.Status != workspace.StatusMissing
	}
	return live, nil
}

func onFrom(installed plugin.InstalledPlugin, plan Plan) bool {
	return installed.Version == plan.FromVersion && installed.ComponentFingerprint == plan.FromFingerprint &&
		installed.EvidenceGeneration() == plan.FromGeneration
}

// onTarget also requires every Home declaration to be exactly the reviewed one.
func onTarget(installed plugin.InstalledPlugin, plan Plan) bool {
	if installed.Version != plan.ToVersion || installed.ComponentFingerprint != plan.ToFingerprint ||
		installed.WorkspaceSurfaces == nil || len(installed.WorkspaceSurfaces.AssistantProgramHomes) != len(plan.Programs) {
		return false
	}
	for index, home := range installed.WorkspaceSurfaces.AssistantProgramHomes {
		program := plan.Programs[index]
		if home.ID != program.ProgramID || projecttemplates.AssistantProgramHomeDigest(home) != program.ToDeclarationDigest {
			return false
		}
	}
	return true
}
