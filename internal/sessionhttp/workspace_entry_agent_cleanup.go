package sessionhttp

import (
	"context"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// RoleSource alone is display provenance, not deletion authority. Legacy and
// assigned entries without a matching host creation operation are retained.
// This is deliberately not a duplicate-agent garbage collector.
type entryAgentCleanup struct {
	selection   agentstore.StaffingSelection
	operationID string
	// managerOf is set instead of operationID when the entry agent is a Blank
	// workspace's own manager: its provenance, not an assistant-setup run, is
	// the deletion authority.
	managerOf  string
	folderPath string // internal location evidence; never projected or logged
}

func (h *Handler) prepareEntryAgentCleanup(ctx context.Context, workspaceID string) *entryAgentCleanup {
	if h.workspaceStore == nil || h.agentStore == nil {
		return nil
	}
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return nil
	}
	name := strings.TrimSpace(ws.EntryAgentName())
	if name == "" {
		return nil
	}
	created := false
	for _, instance := range ws.AgentInstances {
		if !strings.EqualFold(strings.TrimSpace(instance.Name), name) {
			continue
		}
		if instance.RoleSource != workspace.RoleSourceCreated {
			return nil
		}
		created = true
	}
	if !created {
		return nil
	}
	folder, err := h.workspaceStore.GetFolderPath(workspaceID)
	if err != nil || folder == "" {
		return nil
	}
	// A workspace's own manager is removed with the workspace that made it.
	// Only the profile's provenance decides this — never the agent's name.
	if profile, ok := h.agentStore.GetAgent(name); ok && profile != nil && profile.WorkspaceManager.ManagesWorkspace(workspaceID) {
		for _, snapshot := range agentstore.ReadStaffingSnapshot(ctx, h.agentStore).Profiles {
			if snapshot.Name == name {
				return &entryAgentCleanup{selection: agentstore.StaffingSelection{Name: name, Witness: snapshot.Witness}, managerOf: workspaceID, folderPath: folder}
			}
		}
		return nil
	}
	provenance := ws.GetTemplateProvenance()
	if provenance == nil || provenance.AssistantSetup == nil ||
		provenance.AssistantSetup.OperationID == "" || provenance.AssistantSetup.ReviewDigest == "" || provenance.AssistantSetup.RunID == "" {
		return nil
	}
	for _, profile := range agentstore.ReadStaffingSnapshot(ctx, h.agentStore).Profiles {
		if profile.Name == name {
			return &entryAgentCleanup{selection: agentstore.StaffingSelection{Name: name, Witness: profile.Witness}, operationID: provenance.AssistantSetup.OperationID, folderPath: folder}
		}
	}
	return nil
}

// cleanupEntryAgent runs only after this workspace has been removed. Every
// other assignment, including a restorable row, protects its saved profile and
// unrelated sessions. Unknown ownership/reference coverage also protects it.
func (h *Handler) cleanupEntryAgent(ctx context.Context, candidate *entryAgentCleanup, workspaceID string) {
	if candidate == nil {
		return
	}
	// External folders are only unregistered; they retain their assignment.
	// An incomplete group removal also must not orphan a surviving folder.
	if _, err := os.Lstat(candidate.folderPath); !os.IsNotExist(err) {
		return
	}
	guard, guarded := h.agentStore.(agentstore.StaffingSourceGuard)
	refs, verified := h.store.(session.AgentReferenceReader)
	if !guarded || !verified || h.workspaceStore == nil {
		return
	}
	_, err := guard.DeleteStaffingProfile(ctx, candidate.selection, func(profile *agent.Agent) bool {
		switch {
		case candidate.managerOf != "":
			if !profile.WorkspaceManager.ManagesWorkspace(candidate.managerOf) {
				return false
			}
		case profile.AssistantSetup == nil || profile.AssistantSetup.ID == "" || profile.AssistantSetup.ConfigDigest == "" || profile.AssistantSetup.OperationID != candidate.operationID:
			return false
		}
		referenced, err := refs.HasWorkspaceAgentReference(ctx, candidate.selection.Name, workspaceID)
		if err != nil || referenced {
			return false
		}
		referenced, err = h.workspaceStore.HasWorkspaceAgentReference(ctx, candidate.selection.Name, workspaceID)
		return err == nil && !referenced
	}, func() {
		if _, err := h.store.DeleteSessionsByAgent(ctx, candidate.selection.Name); err != nil {
			logger.Warn("Could not purge sessions for deleted workspace-owned entry agent", logger.Fields{"workspace_id": workspaceID})
		}
	})
	if err != nil {
		// Source/reference contents and errors are not logged; retaining a
		// profile is safe, and failure here never rolls back workspace deletion.
		logger.Warn("Retained workspace entry agent: cleanup could not be verified", logger.Fields{"workspace_id": workspaceID})
		return
	}
}
