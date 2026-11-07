package setupjourney

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Fingerprint only target ownership/declarations, never history or runtime
// state. The target's canonical Update rechecks it before adding any binding.
func staffingTargetIdentity(target *workspace.Workspace) string {
	material := map[string]any{"owner": target.OwnerUserID, "link": target.GetAssistantProjectLink()}
	if state := target.GetAssistantProgramState(); state != nil {
		material["key"], material["declaration"], material["provider"], material["available"] = state.Key, state.Declaration, state.HomeProvider, state.PluginAvailable
	}
	data, err := json.Marshal(material)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type staffingSourceContextKey struct{}

func withStaffingRoleSources(ctx context.Context, input staffingInput) context.Context {
	sources := make(map[string]staffingRoleInput)
	for _, role := range input.Roles {
		if role.SourceWitness != "" {
			sources[role.RoleID] = role
		}
	}
	return context.WithValue(ctx, staffingSourceContextKey{}, sources)
}

func applyStaffingRoleSources(ctx context.Context, input *staffingInput) error {
	sources, _ := ctx.Value(staffingSourceContextKey{}).(map[string]staffingRoleInput)
	for i := range input.Roles {
		role := &input.Roles[i]
		if source, ok := sources[role.RoleID]; ok {
			if !role.binds() || source.Name != role.Name {
				return ErrConflict
			}
			role.SourceWitness = source.SourceWitness
		}
	}
	return nil
}

// Pin canonical bind sources before any effects. A trusted caller can require
// a previously reviewed witness; legacy manual calls pin their current source
// to close cleanup/binding races without acquiring recommendation authority.
// Private-only manual sources retain their separate existing contract.
func (a *AssistantStaffingAdapter) staffingSources(ctx context.Context, input staffingInput) ([]store.StaffingSelection, error) {
	reviewed, creates := false, false
	for _, role := range input.Roles {
		reviewed = reviewed || role.SourceWitness != ""
		creates = creates || !role.binds()
	}
	if _, strict := a.profiles.(store.StaffingProfileCreator); reviewed && creates && !strict {
		return nil, ErrConflict
	}
	snapshot := store.ReadStaffingSnapshot(ctx, a.profiles)
	_, guarded := a.profiles.(store.StaffingSourceGuard)
	var selected []store.StaffingSelection
	for _, role := range input.Roles {
		if role.SourceWitness != "" && (!role.binds() || !guarded) {
			return nil, ErrConflict
		}
		if !role.binds() {
			continue
		}
		matched := false
		for _, profile := range snapshot.Profiles {
			if profile.Name != role.Name || !guarded {
				continue
			}
			if role.SourceWitness != "" && role.SourceWitness != profile.Witness {
				return nil, ErrConflict
			}
			selected = append(selected, store.StaffingSelection{Name: role.Name, Witness: profile.Witness})
			matched = true
			break
		}
		if !matched && role.SourceWitness != "" {
			return nil, ErrConflict
		}
	}
	return selected, nil
}

func (a *AssistantStaffingAdapter) updateStaffingBindings(ctx context.Context, targetID string, selected []store.StaffingSelection, mutate func(*workspace.Workspace) error) error {
	if len(selected) == 0 {
		return a.workspaces.Update(targetID, mutate)
	}
	return store.WithStaffingProfiles(ctx, a.profiles, selected, func(profiles map[string]*agent.Agent) error {
		return workspace.UpdateStaffingBindings(a.workspaces, targetID, func(current *workspace.Workspace) error {
			if current.Status == workspace.StatusTrashed || current.Status == workspace.StatusMissing {
				return ErrConflict
			}
			if err := mutate(current); err != nil {
				return err
			}
			// Copies contain the exact fenced definition, not a post-check name
			// lookup; do not run the generic snapshot decorator under this lock.
			for name, profile := range profiles {
				if err := a.workspaces.SaveWorkspaceAgent(targetID, name, profile); err != nil {
					return err
				}
			}
			return nil
		})
	})
}
