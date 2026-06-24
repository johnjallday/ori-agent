package agenthttp

import (
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/types"
)

var cliAgentStatusState = struct {
	sync.RWMutex
	statuses map[string]types.AgentStatus
}{
	statuses: make(map[string]types.AgentStatus),
}

func getCLIAgentOperationalStatus(backend string) types.AgentStatus {
	key := strings.ToLower(strings.TrimSpace(backend))
	if key == "" {
		return types.AgentStatusActive
	}

	cliAgentStatusState.RLock()
	status := cliAgentStatusState.statuses[key]
	cliAgentStatusState.RUnlock()
	if status == "" {
		return types.AgentStatusActive
	}
	return status
}

func setCLIAgentOperationalStatus(backend string, status types.AgentStatus) {
	key := strings.ToLower(strings.TrimSpace(backend))
	if key == "" {
		return
	}
	if status == "" {
		status = types.AgentStatusActive
	}

	cliAgentStatusState.Lock()
	cliAgentStatusState.statuses[key] = status
	cliAgentStatusState.Unlock()
}
