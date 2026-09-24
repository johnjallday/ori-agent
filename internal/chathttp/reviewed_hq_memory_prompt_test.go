package chathttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type reviewedPromptSpy struct{ calls int }

func (s *reviewedPromptSpy) Section(_ context.Context, userID, workspaceID, instanceID string) (string, error) {
	s.calls++
	if userID != userprofile.LocalUserID || workspaceID != "hq" || instanceID != "entry" {
		return "", nil
	}
	return "## Personal HQ reviewed facts\n\n- user approved: \"trusted\"", nil
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
