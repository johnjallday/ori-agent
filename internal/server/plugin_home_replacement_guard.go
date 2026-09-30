package server

import (
	"fmt"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// An installed content change cannot silently move an existing independent
// Home (or its child snapshots) onto a new package's role guidance. The
// replacement is refused BEFORE changing any artifact, surface or plugin
// registry entry, unless it is exactly the one a claimed, owner-reviewed Home
// package upgrade is performing (reviewed, recoverable rebind:
// independent-program-homes.md §6.1). This also covers a local Install over an
// existing installed name, not only the Plugins Update route.
func refuseUnreviewedHomeReplacement(store workspace.Store, current plugin.InstalledPlugin, nextVersion, nextFingerprint string, reviewedUpgrade func(plugin.InstalledPlugin, string, string) bool) error {
	if current.WorkspaceSurfaces == nil || len(current.WorkspaceSurfaces.AssistantProgramHomes) == 0 {
		return nil
	}
	if reviewedUpgrade != nil && reviewedUpgrade(current, nextVersion, nextFingerprint) {
		return nil
	}
	if store == nil {
		return fmt.Errorf("cannot verify existing program Homes before plugin replacement")
	}
	ids, err := store.List()
	if err != nil {
		return fmt.Errorf("cannot list program Homes before plugin replacement: %w", err)
	}
	for _, id := range ids {
		candidate, err := store.Get(id)
		if err != nil || candidate == nil {
			return fmt.Errorf("cannot verify workspace before plugin replacement: %w", err)
		}
		state := candidate.GetAssistantProgramState()
		if state == nil {
			continue
		}
		owner := state.HomeProvider
		if owner == nil && state.GroupTemplate != nil {
			owner = state.GroupTemplate.ProgramHomeOwner
		}
		if owner == nil || owner.PluginID != current.Name {
			continue
		}
		if owner.PluginVersion != nextVersion || owner.ComponentFingerprint != nextFingerprint {
			return fmt.Errorf("plugin replacement would strand existing Home %s; reviewed Home/child upgrade is required first", candidate.ID)
		}
	}
	return nil
}
