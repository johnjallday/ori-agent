package agenthttp

import (
	"errors"
	"fmt"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
)

const (
	agentChangedOnDiskCode    = "agent_changed_on_disk"
	agentChangedOnDiskMessage = store.AgentChangedOnDiskMessage

	agentRootUnavailableCode    = "agent_root_unavailable"
	agentRootUnavailableMessage = "Your Workspace Directory was not found, so your agents are unavailable."

	workspaceOwnedAgentCode = "workspace_owned_agent"
)

// agentOriginReader is the composite store's view of where an agent comes from.
type agentOriginReader interface {
	AgentOrigin(name string) (store.AgentOrigin, bool)
}

// agentOrigin reports where a roster entry comes from, or false when the store
// does not know (a plain single store: everything is the user's own agent).
func agentOrigin(st store.Store, name string) (store.AgentOrigin, bool) {
	if reader, ok := st.(agentOriginReader); ok {
		return reader.AgentOrigin(name)
	}
	return store.AgentOrigin{}, false
}

// workspaceOwnedAgent returns the owning workspace of an agent that only a
// workspace holds, or nil.
func workspaceOwnedAgent(st store.Store, name string) *store.WorkspaceOwnedAgentError {
	origin, ok := agentOrigin(st, name)
	if !ok || origin.Source != store.SourceWorkspace {
		return nil
	}
	return &store.WorkspaceOwnedAgentError{Agent: name, WorkspaceID: origin.WorkspaceID, WorkspaceName: origin.WorkspaceName}
}

// workspaceOwnedMessage tells the user where a workspace-only agent is edited.
func workspaceOwnedMessage(owned *store.WorkspaceOwnedAgentError) string {
	return fmt.Sprintf("This agent belongs to the workspace %s. Edit it there, or add it to your agents first.", owned.WorkspaceName)
}

// WriteAgentStoreError answers a failed agent-store write.
//
// A conflict the user can resolve gets 409 and a message that says what to do;
// the Agents page refetches the agent when it sees the code. Anything else is a
// 500 carrying fallback. Every SetAgent call site in this package answers
// through here, so the mapping cannot drift between endpoints.
func WriteAgentStoreError(w http.ResponseWriter, fallback string, err error) {
	if errors.Is(err, store.ErrAgentChangedOnDisk) {
		if respErr := orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"success": false,
			"code":    agentChangedOnDiskCode,
			"error":   agentChangedOnDiskMessage,
			"message": agentChangedOnDiskMessage,
		}); respErr != nil {
			logger.Error("Failed to write agent conflict response", logger.Fields{"error": respErr})
		}
		return
	}
	var owned *store.WorkspaceOwnedAgentError
	if errors.As(err, &owned) {
		message := workspaceOwnedMessage(owned)
		if respErr := orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
			"success":        false,
			"code":           workspaceOwnedAgentCode,
			"error":          message,
			"message":        message,
			"workspace_id":   owned.WorkspaceID,
			"workspace_name": owned.WorkspaceName,
		}); respErr != nil {
			logger.Error("Failed to write workspace-owned agent response", logger.Fields{"error": respErr})
		}
		return
	}
	if errors.Is(err, store.ErrAgentRootUnavailable) {
		// The folder may come back (a drive plugged in again), so this is a
		// temporary condition rather than a bad request.
		if respErr := orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"code":    agentRootUnavailableCode,
			"error":   agentRootUnavailableMessage,
			"message": agentRootUnavailableMessage,
		}); respErr != nil {
			logger.Error("Failed to write agent root response", logger.Fields{"error": respErr})
		}
		return
	}
	orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, fallback, err)
}
