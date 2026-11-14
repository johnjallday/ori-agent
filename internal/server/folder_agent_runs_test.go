package server

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFolderAgentHiring_ListsWhatAgentsRunAndOffersIdleAgentsFirst(t *testing.T) {
	agents, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Zebra", "beta", "Alpha", "Idle"} {
		if err := agents.CreateAgent(name, nil); err != nil {
			t.Fatal(err)
		}
	}
	workspaces, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, name, entry, owner string
		status                 workspace.WorkspaceStatus
	}{
		{"1", "Z Songs", "Alpha", "u1", workspace.StatusActive},
		{"2", "Albums", "Alpha", "u1", workspace.StatusActive},
		{"3", "Drafts", "Alpha", "u1", workspace.StatusActive},
		{"4", "Inbox", "beta", "u1", workspace.StatusActive},
		{"5", "Discarded", "Zebra", "u1", workspace.StatusTrashed},
		{"6", "Someone else's", "Zebra", "u2", workspace.StatusActive},
	} {
		ws := &workspace.Workspace{ID: row.id, Name: row.name, Status: row.status, OwnerUserID: row.owner,
			AgentInstances: []workspace.AgentInstance{{ID: "lead", Name: row.entry, EntryPoint: true}, {ID: "teammate", Name: "Idle"}},
		}
		if err := workspaces.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	got := folderAgentHiring{builder: &ServerBuilder{st: agents, workspaceStore: workspaces}}.Agents(context.Background(), "u1")
	var names, runs []string
	for _, item := range got {
		names = append(names, item.Name)
		runs = append(runs, item.Runs)
	}
	if !reflect.DeepEqual(names, []string{"Idle", "Zebra", "Alpha", "beta"}) {
		t.Fatalf("order = %v", names)
	}
	if !reflect.DeepEqual(runs, []string{"", "", "Albums and 2 more", "Inbox"}) {
		t.Fatalf("runs = %v", runs)
	}
}
