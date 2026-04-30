package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// workspaceAgentDir returns <workspace>/agents/<slug> for a given agent name.
// The agent name is slugified to keep filesystem paths safe.
func workspaceAgentDir(workspaceFolder, agentName string) (string, error) {
	trimmed := strings.TrimSpace(agentName)
	if trimmed == "" {
		return "", errors.New("agent name is empty")
	}
	slug := Slugify(trimmed)
	if slug == "" || slug == "untitled" && trimmed != "untitled" {
		return "", fmt.Errorf("agent name %q has no usable slug", agentName)
	}
	return filepath.Join(workspaceFolder, WorkspaceAgentsDir, slug), nil
}

// readWorkspaceAgent loads a workspace-local agent snapshot if present.
// Returns (nil, false, nil) when the snapshot does not exist.
func readWorkspaceAgent(workspaceFolder, agentName string) (*agent.Agent, bool, error) {
	dir, err := workspaceAgentDir(workspaceFolder, agentName)
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(dir, WorkspaceAgentConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read workspace agent %q: %w", agentName, err)
	}
	var ag agent.Agent
	if err := json.Unmarshal(data, &ag); err != nil {
		return nil, false, fmt.Errorf("decode workspace agent %q: %w", agentName, err)
	}
	return &ag, true, nil
}

// writeWorkspaceAgent persists an agent snapshot under <workspace>/agents/<slug>/config.json.
func writeWorkspaceAgent(workspaceFolder, agentName string, ag *agent.Agent) error {
	if ag == nil {
		return errors.New("nil agent")
	}
	dir, err := workspaceAgentDir(workspaceFolder, agentName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create workspace agent dir: %w", err)
	}
	data, err := json.MarshalIndent(ag, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace agent %q: %w", agentName, err)
	}
	path := filepath.Join(dir, WorkspaceAgentConfigFile)
	if err := atomicWriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write workspace agent %q: %w", agentName, err)
	}
	return nil
}

// ReadWorkspaceAgentFromFolder is the exported form of readWorkspaceAgent for
// callers (e.g. session adapter) that already know the workspace folder path.
func ReadWorkspaceAgentFromFolder(workspaceFolder, agentName string) (*agent.Agent, bool, error) {
	return readWorkspaceAgent(workspaceFolder, agentName)
}

// WriteWorkspaceAgentToFolder is the exported form of writeWorkspaceAgent.
func WriteWorkspaceAgentToFolder(workspaceFolder, agentName string, ag *agent.Agent) error {
	return writeWorkspaceAgent(workspaceFolder, agentName, ag)
}
