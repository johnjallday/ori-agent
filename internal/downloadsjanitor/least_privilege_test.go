package downloadsjanitor

import (
	"context"
	"slices"
	"strings"
	"testing"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// The Downloads Curator must never hold a tool that can change a file. Approved
// moves are issued by the Janitor service against the same binding — that is
// what lets the agent stay read-only while filing still happens.
//
// These tests assert the property at the binding, which is the single place
// every agent-facing path (chat, tasks, missions, delegated work, native CLI)
// derives its tool list from: internal/workspace/agent_runtime_resolver.go
// copies MCPBinding.AllowedTools into ResolvedAgentRuntime.MCPToolAllowlist,
// and every consumer of that runtime honours it.

// mutationTools are the filesystem tools that can change or read out a file.
// None of them may ever appear on the Janitor binding.
var mutationTools = []string{
	"move_file", "delete_file", "copy_file", "write_file",
	"edit_file", "create_directory", "remove_directory",
}

func TestJanitorBinding_ExposesNoMutationOrContentTools(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	ws := workspaces.workspaces["ws-1"]
	binding, found := janitorBinding(ws)
	if !found {
		t.Fatal("expected a Janitor binding after setup")
	}

	// A binding with no allowlist means every tool: that must never be the
	// case here.
	if binding.AllowsAllTools() {
		t.Fatal("the Janitor binding must restrict its tools, not expose all of them")
	}
	for _, tool := range mutationTools {
		if binding.ToolAllowed(tool) {
			t.Errorf("the agent must never be able to call %q against the Downloads folder", tool)
		}
	}
	// Content reading is a separate opt-in and is not granted by folder access.
	for _, tool := range []string{"read_file", "read_text_file", "read_media_file"} {
		if binding.ToolAllowed(tool) {
			t.Errorf("metadata-only mode must not expose %q", tool)
		}
	}
	// The four listing/metadata tools are exactly what is granted.
	for _, tool := range JanitorReadTools {
		if !binding.ToolAllowed(tool) {
			t.Errorf("the agent needs %q to list and inspect the folder", tool)
		}
	}
	if len(binding.AllowedTools) != len(JanitorReadTools) {
		t.Fatalf("allowed tools = %v, want exactly %v", binding.AllowedTools, JanitorReadTools)
	}
}

// The resolved runtime is what chat, tasks, missions, and delegated work all
// read their tool list from. Whatever path an agent takes, it arrives at this
// allowlist.
func TestResolvedRuntime_CarriesTheReadOnlyAllowlistToEveryAgentPath(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	ws := workspaces.workspaces["ws-1"]
	binding, _ := janitorBinding(ws)

	// This mirrors what AgentRuntimeResolver does with a restricted binding:
	// the binding's allowed tools become the runtime server's allowlist.
	runtimeName := workspace.RuntimeMCPServerName(ws.ID, binding.ServerName, binding.ID)
	allowlist := map[string][]string{}
	if !binding.AllowsAllTools() {
		allowlist[runtimeName] = append([]string(nil), binding.AllowedTools...)
	}

	tools, present := allowlist[runtimeName]
	if !present {
		t.Fatal("a restricted binding must contribute an allowlist entry; a missing key means unrestricted")
	}
	for _, tool := range mutationTools {
		if slices.Contains(tools, tool) {
			t.Fatalf("the runtime allowlist exposes %q", tool)
		}
	}
	for _, tool := range tools {
		if !slices.Contains(JanitorReadTools, tool) {
			t.Fatalf("unexpected tool %q reached the agent runtime", tool)
		}
	}
}

// Setup repairs a binding that has drifted, so a hand-edited or imported
// workspace cannot leave the agent holding a mutation tool.
func TestConfirmSetup_RepairsABindingThatGainedMutationTools(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	ws := workspaces.workspaces["ws-1"]
	for i := range ws.MCPBindings {
		if strings.EqualFold(ws.MCPBindings[i].Alias, JanitorBindingAlias) {
			ws.MCPBindings[i].AllowedTools = append(ws.MCPBindings[i].AllowedTools, "move_file", "delete_file")
		}
	}

	// Readiness notices before setup is re-run.
	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Readiness.State == ReadinessReady {
		t.Fatal("a widened binding must not be Ready")
	}

	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup (repair): %v", err)
	}
	binding, _ := janitorBinding(workspaces.workspaces["ws-1"])
	for _, tool := range mutationTools {
		if binding.ToolAllowed(tool) {
			t.Fatalf("re-running setup must strip %q", tool)
		}
	}
}

// The Janitor's own mover reaches the binding directly, which is how an
// approved move executes without the agent ever holding move_file.
func TestMCPMover_CallsMoveFileOnTheJanitorBindingOnly(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	materializer := &fakeMaterializer{}
	caller := &fakeToolCaller{}
	mover := NewMCPMover(workspaces, materializer, caller)

	if err := mover.Move(t.Context(), "ws-1", root+"/a.pdf", root+"/Filed/Documents/a.pdf"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if caller.tool != "move_file" {
		t.Fatalf("tool = %q, want move_file", caller.tool)
	}
	if materializer.binding.Alias != JanitorBindingAlias {
		t.Fatalf("the move must go through the Janitor binding, got %q", materializer.binding.Alias)
	}
	// The binding it runs on is still the read-only one: the Janitor calling
	// move_file does not widen what the agent can see.
	for _, tool := range mutationTools {
		if materializer.binding.ToolAllowed(tool) {
			t.Fatalf("the binding must stay read-only for the agent, but allows %q", tool)
		}
	}
	if caller.arguments["source"] == nil || caller.arguments["destination"] == nil {
		t.Fatalf("move_file needs both paths: %+v", caller.arguments)
	}
}

func TestMCPMover_ReportsAToolErrorRatherThanSwallowingIt(t *testing.T) {
	service, workspaces := newTestService(t)
	root := inboxFixture(t)
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	mover := NewMCPMover(workspaces, &fakeMaterializer{}, &fakeToolCaller{reportsError: true})
	if err := mover.Move(t.Context(), "ws-1", "/a", "/b"); err == nil {
		t.Fatal("a tool-reported error must surface")
	}

	// A workspace with no Janitor binding cannot move anything.
	plain := newFakeWorkspaceStore("ws-plain")
	if err := NewMCPMover(plain, &fakeMaterializer{}, &fakeToolCaller{}).Move(t.Context(), "ws-plain", "/a", "/b"); err == nil {
		t.Fatal("a workspace with no approved folder access must not move files")
	}
}

type fakeMaterializer struct{ binding workspace.MCPBinding }

func (f *fakeMaterializer) MaterializeRuntimeBinding(workspaceID string, binding workspace.MCPBinding) (string, error) {
	f.binding = binding
	return workspace.RuntimeMCPServerName(workspaceID, binding.ServerName, binding.ID), nil
}

type fakeToolCaller struct {
	tool         string
	arguments    map[string]any
	reportsError bool
}

func (f *fakeToolCaller) CallTool(_ context.Context, _, toolName string, arguments map[string]any) (bool, error) {
	f.tool = toolName
	f.arguments = arguments
	return f.reportsError, nil
}
