package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// A folder attached to the conversation that Ori has nothing to set up for
// leaves the request's hire offer available, like a link does
// (assistant-hires-from-a-request, requirement 9). Since an empty or loose
// folder is an inbox folder with a hire of its own (folder-inbox-hire, 1A),
// such a folder is one of a kind Ori knows but sets up only as a blank
// workspace: here an Obsidian vault. A folder Ori can set up belongs to the
// folder's own card instead
// (TestAssistantSoftwareFolder_DiscussionAndSetupWordingSuggestAndCreateNothing).
func TestAssistantHireOffer_AFolderOriHasNoSetupForDoesNotWithholdTheHireTool(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	home := os.Getenv("HOME") // newDraftServerFixture sandboxes HOME.
	if err := os.MkdirAll(filepath.Join(home, "Documents", ".obsidian"), 0750); err != nil {
		t.Fatal(err)
	}
	draft := uuid.NewString()
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, selected)
	}
	if selected["hire_candidate"] != nil || selected["structure"] != nil {
		t.Fatalf("a folder with no setup was answered with a card: %v", selected)
	}
	observation := selected["observation"].(map[string]any)
	f.provider.script = []llm.ChatResponse{readerCall("assistant_propose_hire", map[string]any{
		"role": "researcher", "name": "Ruth", "brief": "Compare the three CRMs we're evaluating.", "workspace_name": "CRM comparison",
	})}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "help me compare the three CRMs we're evaluating", "intent": "assistant_conversation",
		"context": placementContext(f.project), "conversation": map[string]string{},
		"folder_context": map[string]any{"selection_id": observation["id"], "draft_id": draft},
	})
	if status != http.StatusOK {
		t.Fatalf("ask: %d %v", status, reply)
	}
	if len(f.provider.requests) != 1 || !hasTool(f.provider.requests[0], "assistant_propose_hire") {
		t.Fatalf("the folder turn did not offer the hire tool: %v", toolNames(f.provider.requests[len(f.provider.requests)-1]))
	}
	call := f.provider.requests[0]
	if rules := call.SystemPrompt + call.Messages[0].Content; strings.Contains(rules, "folder's own setup card is the offer") || !strings.Contains(rules, "assistant_propose_hire") {
		t.Fatal("an unrecognized folder was treated as the offer")
	}
	confirmation, _ := reply["confirmation"].(map[string]any)
	if confirmation["action_type"] != "hire_agent" || reply["conversation"].(map[string]any)["stored"] != true {
		t.Fatalf("no hire card on the folder turn: %v", reply)
	}
	// The folder stays attached to the turn; the folder path's own plain
	// "blank workspace" suggestion for such a folder is its business.
	if reply["folder_context"] == nil {
		t.Fatalf("the folder context was lost: %v", reply)
	}
	brief := confirmation["arguments"].(map[string]any)["brief"].(string)
	if strings.Contains(brief, home) || strings.Contains(brief, observation["id"].(string)) {
		t.Fatalf("the card carried a path or a selection id: %q", brief)
	}
}

// An inbox folder (here an empty one) is answered on attach by its own hire
// card, with no model configured for it: the folder owns the offer, so the
// request's hire tool is withheld on the turn (folder-inbox-hire, FR6, FR23).
func TestAssistantHireOffer_AnInboxFolderIsAnsweredByItsOwnHireCard(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	home := os.Getenv("HOME") // newDraftServerFixture sandboxes HOME.
	if err := os.MkdirAll(filepath.Join(home, "Documents", "Scratch"), 0750); err != nil {
		t.Fatal(err)
	}
	draft := uuid.NewString()
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, selected)
	}
	if selected["hire_candidate"] != "candidate-0" || selected["structure"] != nil {
		t.Fatalf("an inbox folder was not answered with its hire: %v", selected)
	}
	observation := selected["observation"].(map[string]any)
	f.provider.script = []llm.ChatResponse{{Content: "The card above sets this up."}}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": "what should I do with this folder", "intent": "assistant_conversation",
		"context": placementContext(f.project), "conversation": map[string]string{},
		"folder_context": map[string]any{"selection_id": observation["id"], "draft_id": draft},
	})
	if status != http.StatusOK {
		t.Fatalf("ask: %d %v", status, reply)
	}
	if len(f.provider.requests) != 1 || hasTool(f.provider.requests[0], "assistant_propose_hire") {
		t.Fatalf("the inbox folder's turn offered the request's hire tool: %v", toolNames(f.provider.requests[len(f.provider.requests)-1]))
	}
}
