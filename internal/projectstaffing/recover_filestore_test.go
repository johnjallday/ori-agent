package projectstaffing

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// library-setup-staffing: restart recovery over the real on-disk store. The
// reservation (with its time) and the created binding are written to disk by
// one process; a second process, with a new store and a new Service that
// remember nothing, recovers from what is stored alone.
func TestRecoverAfterARestartOverTheRealFileStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspaces")
	open := func() workspace.Store {
		t.Helper()
		store, err := workspace.NewFileStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	first := open()
	seedHome(t, first)
	agents := fakeAgents{}
	before := New(first, agents)
	grantHome(t, before)
	fills, err := before.Fill("song-a", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("fill = %+v %v", fills, err)
	}
	// The agent was created and bound in song-a; the process stopped before
	// Settle recorded it.
	agents[fills[0].Name] = true
	bindInSong(t, first, "song-a", fills[0].Name, workspace.RoleSourceCreated)

	// "Restart": nothing but the files on disk carries over.
	second := open()
	after := New(second, agents)
	consent, err := after.Consents().Read("home")
	if err != nil || consent == nil {
		t.Fatalf("consent after restart = %v %v", consent, err)
	}
	role := consent.Roles[0]
	if role.AgentName != "" || role.PendingName != "REAPER Assistant" || role.ReservedAt == nil || role.ReservedAt.IsZero() {
		t.Fatalf("the stored reservation lost its time: %+v", role)
	}
	// Before recovery the held name stops any second create.
	if _, err := after.Fill("song-b", []string{"reaper-assistant"}, nil); !errors.Is(err, ErrReservationHeld) {
		t.Fatalf("before recovery: %v", err)
	}
	if err := after.Recover("home"); err != nil {
		t.Fatal(err)
	}
	recovered, _ := after.Consents().Read("home")
	if got := recovered.Roles[0]; got.AgentName != "REAPER Assistant" || got.PendingName != "" || got.ReservedAt != nil {
		t.Fatalf("after recovery: %+v", got)
	}
	fills, err = after.Fill("song-b", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("next song = %+v %v", fills, err)
	}

	// A third process sees the same recorded agent.
	third := New(open(), agents)
	final, err := third.Consents().Read("home")
	if err != nil || final.Roles[0].AgentName != "REAPER Assistant" || final.Roles[0].PendingName != "" {
		t.Fatalf("durable record = %+v %v", final, err)
	}
}

// A binding made before the reservation (an older song's own agent that only
// shares the name) is still never recorded after a restart.
func TestRecoverAfterARestartStillIgnoresAnOlderBinding(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspaces")
	store, err := workspace.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedHome(t, store)
	agents := fakeAgents{"REAPER Assistant": true}
	if err := store.Update("song-a", func(song *workspace.Workspace) error {
		song.AgentInstances = append(song.AgentInstances, workspace.AgentInstance{ID: "i-old", Name: "REAPER Assistant",
			RoleSource: workspace.RoleSourceCreated, CreatedAt: time.Now().Add(-time.Hour)})
		link := song.GetAssistantProjectLink()
		link.ProjectBindings.Bindings = append(link.ProjectBindings.Bindings, workspace.AssistantRoleBinding{
			RoleID: "reaper-assistant", AgentInstanceID: "i-old", AgentName: "REAPER Assistant"})
		song.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := New(store, agents)
	grantHome(t, service)
	if err := service.Consents().ReserveAgent("home", "ori-reaper", "reaper-song", digest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(reopened, agents)
	if err := restarted.Recover("home"); err != nil {
		t.Fatal(err)
	}
	consent, _ := restarted.Consents().Read("home")
	if got := consent.Roles[0]; got.AgentName != "" || got.PendingName != "REAPER Assistant" {
		t.Fatalf("an older binding was recorded after a restart: %+v", got)
	}
}
