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
	// A snapshot written by an older build still carries the retired
	// avatar/character fields. Migrating on read — through the exact same
	// contract the global store uses — is what stops a stale snapshot from
	// reintroducing the old schema after the global record has moved on
	// (PRD FR-69, risk 7.6).
	migrateSnapshotAppearance(workspaceFolder, agentName, &ag)
	return &ag, true, nil
}

// migrateSnapshotAppearance canonicalizes one snapshot's appearance in place.
//
// It touches nothing but Appearance: workspace snapshots own workspace-local
// settings such as toolbox, designation, and assignment, and an appearance
// migration is never a licence to rewrite those (FR-95).
func migrateSnapshotAppearance(workspaceFolder, agentName string, ag *agent.Agent) {
	result := ag.MigrateAppearance(agent.DefaultAppearanceEnvironment(agent.AppearanceUploadDir))
	if len(result.Reasons) == 0 {
		return
	}
	// The scope names the workspace by its folder base name, not its full path:
	// enough to identify which snapshot needs attention, without putting a
	// filesystem path into a message the user may see (FR-73).
	agent.RecordAppearanceMigrationNote(agent.AppearanceMigrationNote{
		Agent:   agentName,
		Scope:   "workspace:" + filepath.Base(workspaceFolder),
		Reasons: result.Reasons,
	})
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
	// Canonical on the way out too, so a snapshot can never be the one record
	// that keeps the retired schema alive (FR-77).
	ag.EnsureAppearance()
	data, err := json.MarshalIndent(ag, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace agent %q: %w", agentName, err)
	}
	path := filepath.Join(dir, WorkspaceAgentConfigFile)
	if err := atomicWriteFile(path, data); err != nil {
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
