package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
)

// A ticket's prompt used to carry the contents of any attachment that a stored
// layout wired to it: the text of a file, and the title and body of a note or a
// link. Only the retired workspace canvas could draw those wires. They are
// stored data now and nothing reads them, so a workspace that still holds some
// must prepare and build its ticket exactly as one without them would.
func TestWiredAttachmentsAreNoLongerInjectedIntoATicketPrompt(t *testing.T) {
	const fileMarker = "FILE-CONTENT-MARKER-4f2a9c"
	const noteMarker = "NOTE-BODY-MARKER-91bd07"

	filePath := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(filePath, []byte("The spec says "+fileMarker), 0o600); err != nil {
		t.Fatal(err)
	}

	wsStore := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "wired", Agents: []string{"Ori"}})
	ws.ID = "workspace-wired"
	ws.Tasks = []Task{{ID: "task-wired", Description: "summarize the spec", To: "Ori", Status: TaskStatusPending}}
	ws.Attachments = []Attachment{
		{
			ID: "att-file", Title: "spec.md", Type: AttachmentTypeDoc,
			File: &AttachmentFileMeta{Name: "spec.md", Mime: "text/markdown", URL: filePath},
		},
		// A note or link: a title and a body, no file behind it.
		{ID: "att-note", Title: "Meeting note", Body: noteMarker, Type: AttachmentTypeOther},
	}
	ws.Layout = &CanvasLayout{WorkflowConnections: []WorkflowConnectionLayout{
		{From: "att-file", To: "task-wired"},
		{From: "att-note", To: "task-wired"},
	}}
	if err := wsStore.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	handler := &LLMTaskHandler{
		agentStore:     &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Ori": {}}},
		workspaceStore: wsStore,
		contextStore:   &mockTaskPromptContextStore{},
	}
	task := Task{ID: "task-wired", WorkspaceID: ws.ID, To: "Ori", Description: "summarize the spec"}

	prompt := handler.buildTaskPrompt(context.Background(), task)
	if !strings.Contains(prompt, "summarize the spec") {
		t.Fatalf("the ticket prompt was not built: %q", prompt)
	}
	for _, injected := range []string{fileMarker, noteMarker, "## Attached Files"} {
		if strings.Contains(prompt, injected) {
			t.Fatalf("wired attachment content reached the prompt (%q): %q", injected, prompt)
		}
	}

	prepared, err := handler.PrepareTaskContext(context.Background(), "Ori", task)
	if err != nil {
		t.Fatalf("a stored attachment wire must not break preparing the ticket: %v", err)
	}
	for _, item := range prepared.Items {
		if item.Kind == "attached_file" || item.Access == "injected" && strings.Contains(item.Detail, "Attached content") {
			t.Fatalf("a wired attachment is still listed as injected context: %+v", prepared.Items)
		}
	}

	// The stored wires are left alone: they are inert data, not something this
	// path rewrites or removes.
	saved, err := wsStore.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Layout == nil || len(saved.Layout.WorkflowConnections) != 2 {
		t.Fatalf("the stored layout was altered: %+v", saved.Layout)
	}
}
