package agenthttp

import (
	"errors"
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
)

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
