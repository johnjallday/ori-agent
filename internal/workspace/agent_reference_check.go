package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// AgentReferenceReader is cleanup evidence, not owner-visible ranking data.
// Implementations must include restorable references and never import Trash.
type AgentReferenceReader interface {
	HasWorkspaceAgentReference(context.Context, string, string) (bool, error)
}

func HasWorkspaceAgentReference(ctx context.Context, st Store, name, excludingID string) (bool, error) {
	if reader, ok := st.(AgentReferenceReader); ok {
		return reader.HasWorkspaceAgentReference(ctx, name, excludingID)
	}
	return false, errors.New("workspace reference verification is unavailable")
}

func (s *AgentSnapshotStore) HasWorkspaceAgentReference(ctx context.Context, name, excludingID string) (bool, error) {
	return HasWorkspaceAgentReference(ctx, s.Store, name, excludingID)
}

func (s *SyncStore) HasWorkspaceAgentReference(ctx context.Context, name, excludingID string) (bool, error) {
	found, err := HasWorkspaceAgentReference(ctx, s.primary, name, excludingID)
	if err != nil || found {
		return found, err
	}
	if s.fileSync == nil {
		return false, nil
	}
	return s.fileSync.HasWorkspaceAgentReference(ctx, name, excludingID)
}

func (s *InMemoryStore) HasWorkspaceAgentReference(ctx context.Context, name, excludingID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := func(value string) bool { return strings.EqualFold(strings.TrimSpace(value), name) }
	for id, ws := range s.workspaces {
		if id == excludingID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if matches(ws.EntryAgentName()) {
			return true, nil
		}
		for _, instance := range ws.AgentInstances {
			if matches(instance.Name) {
				return true, nil
			}
		}
		if state := ws.AssistantProgramState; state != nil {
			for _, binding := range state.HomeBindings.Bindings {
				if matches(binding.AgentName) {
					return true, nil
				}
			}
			if state.ProjectStaffingConsent != nil {
				for _, role := range state.ProjectStaffingConsent.Roles {
					if matches(role.AgentName) || matches(role.PendingName) {
						return true, nil
					}
				}
			}
		}
		if link := ws.AssistantProjectLink; link != nil {
			for _, binding := range link.ProjectBindings.Bindings {
				if matches(binding.AgentName) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// HasWorkspaceAgentReference verifies the indexed, live folder metadata. The
// caller must also check the primary store, which retains restorable rows after
// folders leave this index. Missing/unreadable metadata is unknown, never empty.
// This does not rescan/import folders, hydrate runtime state, or repair files.
func (s *FileStore) HasWorkspaceAgentReference(ctx context.Context, name, excludingID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, path := range s.idToPath {
		if id == excludingID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		cached := s.cache[id]
		if cached == nil || string(cached.Status) == "trashed" {
			return false, errors.New("workspace reference metadata is unavailable")
		}
		folder := s.resolveFolder(path)
		var data []byte
		var err error
		if s.localConfig != nil && s.localConfig.native(ctx, id) != nil {
			data, err = workspacecontinuity.ReadCanonicalFile(ctx, folder, WorkspaceConfigFile, maxNativeWorkspaceBytes)
		} else {
			data, err = readNativeWorkspaceFile(folder)
		}
		if err != nil {
			return false, errors.New("workspace reference metadata is unavailable")
		}
		var refs workspaceAgentReferenceMetadata
		if err := json.Unmarshal(data, &refs); err != nil || refs.ID != id {
			return false, errors.New("workspace reference metadata is unreadable")
		}
		if refs.references(name) {
			return true, nil
		}
	}
	return false, nil
}

// This decoder deliberately has no Workspace embedding: even the general
// metadata decoder hydrates tickets and runs legacy migrations.
type workspaceAgentReferenceName struct {
	Name        string `json:"name"`
	AgentName   string `json:"agent_name"`
	PendingName string `json:"pending_name"`
}

type workspaceRoleReferences struct {
	Bindings []workspaceAgentReferenceName `json:"bindings"`
}

type workspaceAgentReferenceMetadata struct {
	ID             string                        `json:"id"`
	Agents         []string                      `json:"agents"`
	AgentInstances []workspaceAgentReferenceName `json:"agent_instances"`
	SharedData     struct {
		EntryAgentName string `json:"entry_agent_name"`
	} `json:"shared_data"`
	AssistantProgramState struct {
		HomeBindings           workspaceRoleReferences `json:"home_bindings"`
		ProjectStaffingConsent struct {
			Roles []workspaceAgentReferenceName `json:"roles"`
		} `json:"project_staffing_consent"`
	} `json:"assistant_program_state"`
	AssistantProjectLink struct {
		ProjectBindings workspaceRoleReferences `json:"project_bindings"`
	} `json:"assistant_project_link"`
}

func (refs workspaceAgentReferenceMetadata) references(name string) bool {
	matches := func(candidate string) bool { return strings.EqualFold(strings.TrimSpace(candidate), name) }
	if matches(refs.SharedData.EntryAgentName) {
		return true
	}
	for _, legacy := range refs.Agents {
		if matches(legacy) {
			return true
		}
	}
	for _, group := range [][]workspaceAgentReferenceName{refs.AgentInstances, refs.AssistantProgramState.HomeBindings.Bindings,
		refs.AssistantProgramState.ProjectStaffingConsent.Roles, refs.AssistantProjectLink.ProjectBindings.Bindings} {
		for _, ref := range group {
			if matches(ref.Name) || matches(ref.AgentName) || matches(ref.PendingName) {
				return true
			}
		}
	}
	return false
}
