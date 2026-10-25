package workspace

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func (r *AgentRuntimeResolver) SetCuratorProtectionReader(reader filecuratorguidance.ProtectionReader) {
	if r != nil {
		r.curatorProtection = reader
	}
}

// HostContext is product-owned advice, separate from selectable user skills.
// Resolve it at prompt composition time: a cached agent runtime is not evidence
// that its capability/attachment still exists. It grants no tool authority.
type HostContext func() string

func AppendHostContext(base string, context HostContext) string {
	if context == nil {
		return base
	}
	return base + context()
}

func (r *AgentRuntimeResolver) fileCuratorHostContext(ws *Workspace, agentName, nodeID string) HostContext {
	instance, ok := ws.FindUniqueAgentInstance(agentName, nodeID)
	curator, associated := ws.FileCurator()
	if !ok || !associated || instance.ID != curator.ID {
		return nil
	}
	workspaceID, owner, instanceID := ws.ID, ws.OwnerUserID, instance.ID
	return func() string {
		valid := func() bool {
			current, err := r.workspaceStore.Get(workspaceID)
			if err != nil || current == nil || current.OwnerUserID != owner {
				return false
			}
			bound, unique := current.FindUniqueAgentInstance(agentName, nodeID)
			companion, present := current.FileCurator()
			return unique && present && bound.ID == instanceID && companion.ID == instanceID
		}
		if !valid() {
			return ""
		}
		facts := ""
		if r.curatorProtection != nil {
			userID := owner
			if userID == "" {
				userID = userprofile.LocalUserID
			}
			if fact, err := r.curatorProtection.ReadProtection(context.Background(), userID, workspaceID); err == nil && fact.WorkspaceID == workspaceID {
				facts = fact.Prompt()
			}
		}
		if !valid() {
			return ""
		}
		return filecuratorguidance.Prompt() + facts
	}
}
