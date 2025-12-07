package main

//go:generate ../../bin/ori-plugin-gen -yaml=plugin.yaml -output=store_generated.go

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// storeTool implements pluginapi.Tool for file storage operations.
// Note: Compile-time interface check is in store_generated.go
type storeTool struct {
	pluginapi.BasePlugin
	store     agentstudio.Store
	agentName string
	agentDir  string
}

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml
// Note: Call() is auto-generated in store_generated.go from plugin.yaml
// Note: StoreParams is auto-generated in store_generated.go from plugin.yaml

// SetAgentContext implements AgentAwareTool interface
func (s *storeTool) SetAgentContext(ctx pluginapi.AgentContext) {
	s.agentName = ctx.Name
	s.agentDir = ctx.AgentDir

	// Initialize the workspace store
	var err error
	s.store, err = agentstudio.NewFileStore("workspaces")
	if err != nil {
		// Fallback - will fail later with better error message
		s.store = nil
	}
}

// extractWorkspaceID extracts the workspace ID from the agent directory path
// Expected path format: .../workspaces/{workspace-id}/agents/{agent-name}
func (s *storeTool) extractWorkspaceID(agentDir string) string {
	cleanPath := filepath.Clean(agentDir)
	parts := strings.Split(cleanPath, string(filepath.Separator))
	// Look for "workspaces/{id}/agents" pattern
	for i := 0; i < len(parts)-2; i++ {
		if parts[i] == "workspaces" && i+2 < len(parts) && parts[i+2] == "agents" {
			return parts[i+1]
		}
	}
	return ""
}

// Execute contains the business logic for the store_write operation
func (s *storeTool) Execute(ctx context.Context, params *StoreParams) (string, error) {
	// Note: Validation is already done in the generated Call() method

	// Extract workspace ID from agent directory path
	workspaceID := s.extractWorkspaceID(s.agentDir)
	if workspaceID == "" {
		return "", fmt.Errorf("workspace ID not found - plugin not properly initialized (agentDir: %s)", s.agentDir)
	}

	workspace, err := s.store.Get(workspaceID)
	if err != nil {
		return "", fmt.Errorf("failed to get workspace: %w", err)
	}

	// Find the store node by canvas node ID
	var storeNode *agentstudio.StoreNode
	for i := range workspace.StoreNodes {
		if workspace.StoreNodes[i].CanvasNodeID == params.StoreNodeId {
			storeNode = &workspace.StoreNodes[i]
			break
		}
	}

	if storeNode == nil {
		return "", fmt.Errorf("store node '%s' not found in workspace", params.StoreNodeId)
	}

	// Call WriteToStore from agentstudio package
	if err := agentstudio.WriteToStore(storeNode, params.FilePath, params.Data); err != nil {
		// Update error on store node
		storeNode.LastError = err.Error()
		storeNode.UpdatedAt = time.Now()

		// Save workspace with error
		if saveErr := s.store.Save(workspace); saveErr != nil {
			return "", fmt.Errorf("write failed: %v (also failed to save error state: %v)", err, saveErr)
		}

		return "", fmt.Errorf("failed to write to store: %w", err)
	}

	// Save updated workspace (WriteToStore already updated node stats)
	if err := s.store.Save(workspace); err != nil {
		return "", fmt.Errorf("data written successfully but failed to save workspace state: %w", err)
	}

	// Build success response
	response := map[string]interface{}{
		"success":         true,
		"store_node_id":   params.StoreNodeId,
		"file_path":       params.FilePath,
		"full_path":       fmt.Sprintf("%s%s", storeNode.BaseDir, params.FilePath),
		"write_count":     storeNode.WriteCount,
		"last_write_time": storeNode.LastWriteTime.Format(time.RFC3339),
	}

	// Include metadata if provided
	if params.Metadata != nil {
		response["metadata"] = params.Metadata
	}

	responseJSON, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		// Fallback to simple message if JSON encoding fails
		return fmt.Sprintf("Successfully wrote to %s%s (write #%d)", storeNode.BaseDir, params.FilePath, storeNode.WriteCount), nil
	}

	return string(responseJSON), nil
}

func main() {
	tool := &storeTool{}
	pluginapi.ServePlugin(tool, configYAML)
}
