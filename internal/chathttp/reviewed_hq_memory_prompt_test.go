package chathttp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type reviewedPromptSpy struct {
	calls      int
	workspaces []string
}

func (s *reviewedPromptSpy) Section(_ context.Context, userID, workspaceID, instanceID string) (string, error) {
	s.calls++
	s.workspaces = append(s.workspaces, workspaceID)
	if userID != userprofile.LocalUserID || workspaceID != "hq" || instanceID != "entry" {
		return "", nil
	}
	return "## Global memory (Personal HQ, user-approved)\n\n- user approved: \"trusted\"", nil
}

func TestReviewedHQChatPromptRequiresUniqueRespondingInstanceForStructuredAndRawVariables(t *testing.T) {
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Home"})
	ws.ID = "hq"
	ws.OwnerUserID = userprofile.LocalUserID
	ws.AgentInstances = []workspace.AgentInstance{{ID: "entry", Name: "Atlas", EntryPoint: true}, {ID: "other", Name: "Helper"}}
	if err := folders.Save(ws); err != nil {
		t.Fatal(err)
	}
	spy := &reviewedPromptSpy{}
	h := &Handler{fileStore: folders, workspaceStore: folders, userProvider: userprofile.LocalUserProvider{}}
	h.SetReviewedMemoryReader(spy)
	route := normalizedChatRouteContext{Surface: "workspace_detail", WorkspaceID: ws.ID, AgentName: "Atlas", PagePath: "/workspaces/hq"}
	structured := h.buildWorkspaceMemoryPrompt(context.Background(), route, false)
	if strings.Count(structured, `user approved: "trusted"`) != 1 {
		t.Fatalf("structured reviewed section missing or duplicated: %q", structured)
	}
	prompt := &resolvedChatAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Context {{workspace.memory}}"}}}
	if !h.resolveAgentBasePromptVars(context.Background(), route, prompt) || strings.Count(prompt.Settings.SystemPrompt, `user approved: "trusted"`) != 1 {
		t.Fatalf("raw variable missed reviewed section: %q", prompt.Settings.SystemPrompt)
	}
	other := route
	other.AgentName = "Helper"
	if section := h.buildWorkspaceMemoryPrompt(context.Background(), other, false); strings.Contains(section, "trusted") {
		t.Fatalf("other HQ agent received reviewed memory: %q", section)
	}
	noAgent := route
	noAgent.AgentName = ""
	before := spy.calls
	if section := h.buildWorkspaceMemoryPrompt(context.Background(), noAgent, false); strings.Contains(section, "trusted") || spy.calls != before {
		t.Fatalf("unresolved agent was authorized: %q calls=%d", section, spy.calls-before)
	}
}

// copyManagedLineInto appends a reviewed HQ line by hand, the way a user might
// paste one from the HQ's MEMORY.md; Append itself refuses managed markers.
func copyManagedLineInto(t *testing.T, folders *workspace.FileStore, workspaceID string) {
	t.Helper()
	folder, err := folders.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, workspace.MemoryFileName)
	existing, err := os.ReadFile(path) // #nosec G304 -- disposable test workspace
	if err != nil {
		t.Fatal(err)
	}
	line := "- [fact, 2026-10-17, ori-hq:item-1:rev-1] COPIED-MANAGED-LINE\n"
	if err := os.WriteFile(path, append(existing, line...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Global memory is read by the personal assistant only (global-memory FR 3).
// A project workspace's chat prompt never asks for the HQ's reviewed facts and
// never renders a managed ori-hq line, even one copied into its own MEMORY.md.
func TestProjectWorkspaceChatPromptNeverCarriesGlobalMemory(t *testing.T) {
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Home"})
	hq.ID = "hq"
	hq.OwnerUserID = userprofile.LocalUserID
	hq.AgentInstances = []workspace.AgentInstance{{ID: "entry", Name: "Atlas", EntryPoint: true}}
	album := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album 3"})
	album.ID = "album"
	album.OwnerUserID = userprofile.LocalUserID
	album.AgentInstances = []workspace.AgentInstance{{ID: "scout", Name: "Scout", EntryPoint: true}, {ID: "atlas-copy", Name: "Atlas"}}
	for _, ws := range []*workspace.Workspace{hq, album} {
		if err := folders.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	if err := workspace.NewMemoryStore(folders).Append(album.ID, workspace.MemoryEntry{
		Type: workspace.MemoryTypeFact, Date: "2026-10-17", Provenance: "agent:Scout", Text: "Album masters at -14 LUFS.",
	}); err != nil {
		t.Fatal(err)
	}
	copyManagedLineInto(t, folders, album.ID)
	spy := &reviewedPromptSpy{}
	h := &Handler{fileStore: folders, workspaceStore: folders, userProvider: userprofile.LocalUserProvider{}}
	h.SetReviewedMemoryReader(spy)
	for _, agentName := range []string{"Scout", "Atlas"} {
		route := normalizedChatRouteContext{Surface: "workspace_detail", WorkspaceID: album.ID, AgentName: agentName, PagePath: "/workspaces/album"}
		structured := h.buildWorkspaceMemoryPrompt(context.Background(), route, true)
		prompt := &resolvedChatAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Context {{workspace.memory}}"}}}
		h.resolveAgentBasePromptVars(context.Background(), route, prompt)
		for _, text := range []string{structured, prompt.Settings.SystemPrompt} {
			if !strings.Contains(text, "-14 LUFS") {
				t.Fatalf("%s lost its own workspace memory: %q", agentName, text)
			}
			for _, leaked := range []string{"trusted", "reviewed facts", "Global memory", "ori-hq:", "COPIED-MANAGED-LINE"} {
				if strings.Contains(text, leaked) {
					t.Fatalf("%s in a project workspace received %q: %q", agentName, leaked, text)
				}
			}
		}
	}
	for _, asked := range spy.workspaces {
		if asked == hq.ID {
			t.Fatalf("a project workspace prompt asked for the HQ's reviewed facts: %v", spy.workspaces)
		}
	}
}
