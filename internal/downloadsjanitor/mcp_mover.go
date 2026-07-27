package downloadsjanitor

import (
	"context"
	"fmt"
	"strings"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// MCPMover performs an approved move through the workspace's root-scoped
// filesystem MCP binding.
//
// The division of labour is the whole point: filesystem MCP is the execution
// mechanism, never the authorization boundary. By the time Move is called, the
// Janitor service has already verified the approval, re-derived both paths from
// its own state, revalidated the source's fingerprint, and journaled the
// action. This type does one thing — issue `move_file` on the binding scoped to
// the approved root — and reports what the tool said. Whether the move actually
// happened is decided by the caller against the filesystem.
//
// The Downloads Curator agent never receives `move_file`; the binding's tool
// allowlist excludes it (see JanitorReadTools). The Janitor invokes the binding
// directly, which is what lets the agent stay read-only while approved moves
// still execute.
type MCPMover struct {
	workspaces WorkspaceStore
	resolver   BindingMaterializer
	caller     ToolCaller
	starter    ToolStarter
}

// isNotRunning reports whether an error means the connector process is not up.
func isNotRunning(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not running")
}

// isAlreadyRunning reports a start that failed because the connector was
// already up.
//
// This is not a failure. Two moves in one batch race for the same connector:
// the first sees "not running" and starts it, the second sees "not running"
// from the same window and then gets "server already running" back from its own
// start attempt. Treating that as fatal failed the second file of every batch
// while the first succeeded — a partial, silent-looking failure that only shows
// up with more than one file, which is why single-file testing never caught it.
func isAlreadyRunning(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already running")
}

// SetStarter wires lazy starting of the connector.
func (m *MCPMover) SetStarter(starter ToolStarter) {
	if m != nil {
		m.starter = starter
	}
}

// BindingMaterializer instantiates a workspace MCP binding as a runtime server
// and returns its name. *workspace.AgentRuntimeResolver satisfies it.
type BindingMaterializer interface {
	MaterializeRuntimeBinding(workspaceID string, binding workspace.MCPBinding) (string, error)
}

// ToolStarter starts a materialized runtime MCP server.
//
// A binding materialized for a workspace is registered but not started, so the
// first call against it finds nothing running. Chat and task paths already
// lazy-start on demand; the Janitor does the same rather than assuming someone
// else has warmed the connector up.
type ToolStarter interface {
	StartServer(serverName string) error
}

// ToolCaller invokes a tool on a named runtime MCP server and reports whether
// the tool itself signalled an error.
//
// It is deliberately narrower than the MCP registry's own signature: the
// Janitor needs "did the tool report failure", not the tool's payload, and
// keeping the result out of this package means a tool's response can never be
// mistaken here for evidence about the filesystem. The wiring layer adapts the
// registry to this shape.
type ToolCaller interface {
	CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (toolReportedError bool, err error)
}

// NewMCPMover builds the production mover.
func NewMCPMover(workspaces WorkspaceStore, resolver BindingMaterializer, caller ToolCaller) *MCPMover {
	return &MCPMover{workspaces: workspaces, resolver: resolver, caller: caller}
}

// Move issues `move_file` on the workspace's Janitor binding.
func (m *MCPMover) Move(ctx context.Context, workspaceID, sourcePath, destinationPath string) error {
	if m == nil || m.resolver == nil || m.caller == nil {
		return fmt.Errorf("%w: the filesystem connector is unavailable", ErrInvalidAction)
	}
	ws, err := readWorkspaceRecord(m.workspaces, workspaceID)
	if err != nil || ws == nil {
		return fmt.Errorf("%w: this workspace could not be loaded", ErrInvalidAction)
	}

	binding, found := janitorBinding(ws)
	if !found {
		return fmt.Errorf("%w: this workspace has no approved folder access", ErrInvalidAction)
	}
	runtimeName, err := m.resolver.MaterializeRuntimeBinding(workspaceID, binding)
	if err != nil {
		return fmt.Errorf("%w: the filesystem connector could not be started", ErrInvalidAction)
	}

	arguments := map[string]any{"source": sourcePath, "destination": destinationPath}
	toolReportedError, err := m.caller.CallTool(ctx, runtimeName, "move_file", arguments)
	if err != nil && isNotRunning(err) && m.starter != nil {
		// Lazy start, then one retry. The connector is a process that may not
		// be up yet; that is a reason to start it, not a reason to tell the
		// user their file could not be moved.
		if startErr := m.starter.StartServer(runtimeName); startErr != nil && !isAlreadyRunning(startErr) {
			return fmt.Errorf("the filesystem connector could not be started: %w", startErr)
		}
		toolReportedError, err = m.caller.CallTool(ctx, runtimeName, "move_file", arguments)
	}
	if err != nil {
		return err
	}
	if toolReportedError {
		return fmt.Errorf("the filesystem connector reported an error moving the file")
	}
	return nil
}

// janitorBinding returns the workspace's Janitor filesystem binding.
func janitorBinding(ws *workspace.Workspace) (workspace.MCPBinding, bool) {
	for _, binding := range ws.MCPBindings {
		if strings.EqualFold(strings.TrimSpace(binding.Alias), JanitorBindingAlias) {
			return binding, true
		}
	}
	return workspace.MCPBinding{}, false
}
