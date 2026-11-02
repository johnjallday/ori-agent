package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeStarterSource map[string]*workspace.Workspace

func (f fakeStarterSource) CachedWorkspaces() map[string]*workspace.Workspace { return f }

func (f fakeStarterSource) Get(id string) (*workspace.Workspace, error) { return f[id], nil }

// PRD 760 FR-28: a project's notes route to a workspace only when an active
// workspace's primary directory is exactly that project's folder.
func TestSecondBrainProjectTickets_WorkspaceForFolder(t *testing.T) {
	root := t.TempDir()
	album := filepath.Join(root, "Projects", "Album")
	nested := filepath.Join(album, "Stems")
	paused := filepath.Join(root, "Projects", "Paused")

	workspaces := map[string]*workspace.Workspace{}
	add := func(id, name, dir string, status workspace.WorkspaceStatus) {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
		ws.ID = id
		ws.Status = status
		ws.DirectoryReferences = []workspace.DirectoryReference{{ID: "d", Path: dir}}
		ws.SharedData = map[string]any{projecttemplates.PrimaryDirectoryIDKey: "d"}
		workspaces[id] = ws
	}
	add("album", "Album", album, workspace.StatusActive)
	add("stems", "Stems", nested, workspace.StatusActive)
	add("paused", "Paused", paused, workspace.StatusTrashed)

	tickets := secondBrainProjectTickets{source: fakeStarterSource(workspaces)}

	cases := []struct {
		name, path, want string
		ok               bool
	}{
		{"exact match", album, "album", true},
		{"a nested folder is its own workspace, not the parent's", nested, "stems", true},
		{"no workspace", filepath.Join(root, "Projects", "None"), "", false},
		{"inactive workspace", paused, "", false},
		{"outside any project", root, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, _, ok := tickets.WorkspaceForFolder(tc.path)
			if ok != tc.ok || id != tc.want {
				t.Errorf("WorkspaceForFolder(%q) = %q, %v, want %q, %v", tc.path, id, ok, tc.want, tc.ok)
			}
		})
	}
}

// PRD 760 FR-29: the key makes a later look find the same ticket, even after it
// moved on, and a finished line leaves the ticket's state alone.
func TestSecondBrainProjectTickets_CreateNoteTicketOnceAndNoteTheLine(t *testing.T) {
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album"})
	ws.Status = workspace.StatusActive
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}
	cached := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album"})
	cached.ID = ws.ID
	cached.Status = workspace.StatusActive
	tickets := secondBrainProjectTickets{
		b:      &ServerBuilder{workspaceStore: store},
		source: fakeStarterSource(map[string]*workspace.Workspace{ws.ID: cached}),
	}
	ctx := context.Background()

	note := secondbrain.NoteTicket{ItemID: "item-1", Title: "Mix the single", Detail: "Projects/Album/plan.md"}
	created, err := tickets.CreateNoteTicket(ctx, ws.ID, note)
	if err != nil || !created {
		t.Fatalf("first create = %v, %v", created, err)
	}
	again, err := tickets.CreateNoteTicket(ctx, ws.ID, note)
	if err != nil || again {
		t.Fatalf("second create = %v, %v, want a replay", again, err)
	}

	loaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(loaded.Tasks) != 1 {
		t.Fatalf("%d tickets, want 1", len(loaded.Tasks))
	}
	task := loaded.Tasks[0]
	if task.CanonicalState() != workspace.TicketStateBacklog || task.SourceType != workspace.TicketSourceNoteItem || task.SourceID != note.ItemID {
		t.Errorf("ticket = %q %q %q, want Backlog from the note item", task.CanonicalState(), task.SourceType, task.SourceID)
	}

	// The cache above holds the workspace as it was before the ticket existed.
	if err := tickets.MarkNoteLinesClosed(ctx, []string{note.ItemID}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	ticket, err := workspace.NewTicketService(store).Get(ws.ID, task.ID)
	if err != nil {
		t.Fatalf("get ticket: %v", err)
	}
	if ticket.SourceNote != workspace.NoteLineClosed {
		t.Errorf("source note = %q, want the closed line noted", ticket.SourceNote)
	}
	if ticket.State != workspace.TicketStateBacklog {
		t.Errorf("state = %q after the line closed, want it unchanged", ticket.State)
	}
}
