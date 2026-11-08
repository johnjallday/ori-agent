package sessionhttp

import (
	"context"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The strict store result, not a successful idempotent CreateAgent call, proves
// which definition this request owns. Existing manual test/extension stores can
// still create, but missing source evidence never authorizes deleting by name.
// The built-in Blank blueprint retains its existing system-assistant bootstrap
// contract. It is not a roster candidate or an operation-owned user profile;
// even a successful bootstrap must never authorize deleting it on rollback.
func (h *Handler) createTemplateSeededProfile(ctx context.Context, seed *seedAgentsResult, tpl projecttemplates.Template, name string, config *store.CreateAgentConfig) error {
	if tpl.Builtin && tpl.ID == blankWorkspaceTemplateID && name == systemassistant.CanonicalName {
		return h.agentStore.CreateAgent(name, config)
	}
	return h.createSeededProfile(ctx, seed, name, config)
}

func (h *Handler) createSeededProfile(ctx context.Context, seed *seedAgentsResult, name string, config *store.CreateAgentConfig) error {
	creator, ok := h.agentStore.(store.StaffingProfileCreator)
	if !ok {
		if err := h.agentStore.CreateAgent(name, config); err != nil {
			return err
		}
		seed.OwnedNames = append(seed.OwnedNames, name)
		return nil
	}
	selected, err := creator.CreateStaffingProfile(ctx, name, config)
	if selected.Name != "" {
		seed.OwnedNames = append(seed.OwnedNames, selected.Name)
		seed.OwnedSources = append(seed.OwnedSources, selected)
	}
	return err
}

func (h *Handler) updateOrdinaryRoleWithSources(ctx context.Context, id string, sources []store.StaffingSelection, mutate func(*workspace.Workspace) error) error {
	if len(sources) == 0 {
		return h.workspaceTaskStore.Update(id, mutate)
	}
	return store.WithStaffingProfiles(ctx, h.agentStore, sources, func(profiles map[string]*agent.Agent) error {
		return workspace.UpdateStaffingBindings(h.workspaceTaskStore, id, func(ws *workspace.Workspace) error {
			if err := mutate(ws); err != nil {
				return err
			}
			for name, profile := range profiles {
				if err := h.workspaceTaskStore.SaveWorkspaceAgent(id, name, profile); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func (h *Handler) rollbackOwnedProfile(ctx context.Context, seed seedAgentsResult, name string) error {
	guard, ok := h.agentStore.(store.StaffingSourceGuard)
	refs, known := h.store.(session.AgentReferenceReader)
	if !ok || !known {
		return errors.New("profile retained: cleanup reference/source verification unavailable")
	}
	var selected store.StaffingSelection
	for _, source := range seed.OwnedSources {
		if source.Name == name {
			selected = source
			break
		}
	}
	if selected.Witness == "" {
		return errors.New("profile retained: creation source could not be verified")
	}
	deleted, err := guard.DeleteStaffingProfile(ctx, selected, func(*agent.Agent) bool {
		used, err := refs.HasWorkspaceAgentReference(ctx, name, "")
		if err != nil || used {
			return false
		}
		if h.workspaceTaskStore != nil {
			used, err = workspace.HasWorkspaceAgentReference(ctx, h.workspaceTaskStore, name, "")
			if err != nil || used {
				return false
			}
		}
		// Without a configured folder store this handler cannot have created
		// folder assignments; the primary rows remain the reference authority.
		if h.workspaceStore == nil {
			return true
		}
		used, err = h.workspaceStore.HasWorkspaceAgentReference(ctx, name, "")
		return err == nil && !used
	}, nil)
	if err != nil {
		return fmt.Errorf("profile retained: %w", err)
	}
	if !deleted {
		return errors.New("profile retained: another assignment exists or references could not be verified")
	}
	return nil
}
