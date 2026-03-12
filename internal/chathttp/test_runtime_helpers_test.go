package chathttp

import "github.com/johnjallday/ori-agent/internal/agent"

func runtimeTestAgent(ag *agent.Agent, servers ...string) *resolvedChatAgent {
	return &resolvedChatAgent{
		Agent:      ag,
		MCPServers: append([]string{}, servers...),
	}
}
