package chathttp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type staticProtectionReader struct {
	fact  *dailybrief.ProtectionFact
	asked []string
}

func (r *staticProtectionReader) WorkspaceProtection(_ context.Context, userID, workspaceID string) (*dailybrief.ProtectionFact, error) {
	r.asked = append(r.asked, userID+"/"+workspaceID)
	return r.fact, nil
}

func protectionPromptFixture(t *testing.T) (*Handler, *staticProtectionReader, *workspace.Workspace) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Night Drive", Agents: []string{"REAPER Assistant", "Mix Engineer"}})
	ws.ID = "song"
	ws.AgentInstances = []workspace.AgentInstance{
		{ID: "i1", Name: "REAPER Assistant", InstanceNumber: 1, EntryPoint: true},
		{ID: "i2", Name: "Mix Engineer", InstanceNumber: 1},
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	reader := &staticProtectionReader{fact: &dailybrief.ProtectionFact{State: "not_covered", Sentence: "Not in any backup Ori observes",
		Route: "/workspaces/my-backups?panel=backup-recovery&tab=overview&section=coverage", RouteLabel: "Check protection", AsOf: at}}
	h := &Handler{workspaceStore: store, userProvider: userprofile.LocalUserProvider{}}
	h.SetWorkspaceProtectionReader(reader)
	return h, reader, ws
}

func TestRuntimePromptGivesOnlyTheEntryAgentTheStoredProtectionFact(t *testing.T) {
	h, reader, ws := protectionPromptFixture(t)
	route := normalizedChatRouteContext{Surface: "workspace_detail", PagePath: "/workspaces/" + ws.ID, WorkspaceID: ws.ID, AgentName: "REAPER Assistant"}
	prompt := h.buildRuntimeSystemPrompt(context.Background(), route)
	want := "Backup protection of this workspace's folder (stored evidence as of 2026-10-19T09:00:00Z, not a new check): Not in any backup Ori observes. Protection is set up in the Backup & Recovery workspace."
	if !strings.Contains(prompt, "## Backup Protection") || !strings.Contains(prompt, want) {
		t.Fatalf("entry agent did not receive the fact:\n%s", prompt)
	}
	section := prompt[strings.Index(prompt, "## Backup Protection"):]
	if end := strings.Index(section, "\n\n---\n"); end >= 0 {
		section = section[:end]
	}
	if !strings.HasSuffix(section, "Protection is set up in the Backup & Recovery workspace.") {
		t.Fatalf("the fact must end with the workspace pointer:\n%s", section)
	}
	for _, forbidden := range []string{"/workspaces/", "panel=", "Check protection", "backup_recovery_", "/Users/", "/Volumes/"} {
		if strings.Contains(section, forbidden) {
			t.Fatalf("the fact section offers a route, action or path %q:\n%s", forbidden, section)
		}
	}
	if len(reader.asked) != 1 || reader.asked[0] != "local/song" {
		t.Fatal(reader.asked)
	}

	route.AgentName = "Mix Engineer"
	if prompt := h.buildRuntimeSystemPrompt(context.Background(), route); strings.Contains(prompt, "Backup Protection") {
		t.Fatal("a non-entry agent received the fact")
	}
	route.AgentName, route.Surface = "REAPER Assistant", "home"
	if prompt := h.buildRuntimeSystemPrompt(context.Background(), route); strings.Contains(prompt, "Backup Protection") {
		t.Fatal("the fact left the workspace surfaces")
	}
}

func TestRuntimePromptOmitsProtectionWithoutAnAdoptedFolder(t *testing.T) {
	h, reader, ws := protectionPromptFixture(t)
	reader.fact = nil
	prompt := h.buildRuntimeSystemPrompt(context.Background(), normalizedChatRouteContext{Surface: "workspace_detail", WorkspaceID: ws.ID, AgentName: "REAPER Assistant"})
	if strings.Contains(prompt, "Backup Protection") || strings.Contains(prompt, "Backup & Recovery workspace") {
		t.Fatalf("a workspace without an adopted folder got a protection line:\n%s", prompt)
	}
	unwired := &Handler{workspaceStore: h.workspaceStore, userProvider: userprofile.LocalUserProvider{}}
	if prompt := unwired.buildWorkspaceProtectionPrompt(context.Background(), normalizedChatRouteContext{Surface: "workspace_detail", WorkspaceID: ws.ID, AgentName: "REAPER Assistant"}); prompt != "" {
		t.Fatal(prompt)
	}
}
