package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func sampleState(messages int64) RuntimeState {
	stats := types.NewAgentStatistics()
	stats.MessageCount = messages
	return RuntimeState{Status: types.AgentStatusActive, Statistics: stats, Evolution: types.NewAgentEvolution()}
}

func TestRuntimeStateRoundTrip(t *testing.T) {
	states := NewRuntimeStateStore(t.TempDir(), "/roots/one")
	if _, ok, err := states.Load("Scout"); err != nil || ok {
		t.Fatalf("Load before Save = ok %v err %v, want nothing", ok, err)
	}
	if err := states.Save("Scout", sampleState(7)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := states.Load("Scout")
	if err != nil || !ok {
		t.Fatalf("Load: ok %v err %v", ok, err)
	}
	if got.Status != types.AgentStatusActive || got.Statistics.MessageCount != 7 || got.Evolution == nil {
		t.Errorf("round trip lost data: %+v", got)
	}
}

func TestRuntimeStateUnchangedIsNotRewritten(t *testing.T) {
	states := NewRuntimeStateStore(t.TempDir(), "/roots/one")
	state := sampleState(3)
	if err := states.Save("Scout", state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := states.path("Scout")
	backdate(t, path)
	if err := states.Save("Scout", state); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	assertUntouched(t, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("state file must end with one newline: %q", data)
	}
}

func TestRuntimeStateNeverStoresDisabled(t *testing.T) {
	states := NewRuntimeStateStore(t.TempDir(), "/roots/one")
	state := sampleState(1)
	state.Status = types.AgentStatusDisabled
	if err := states.Save("Scout", state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(states.path("Scout"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "disabled") {
		t.Errorf("paused is a definition fact, but the state file says: %s", data)
	}
}

func TestRuntimeStateRenameAndDelete(t *testing.T) {
	states := NewRuntimeStateStore(t.TempDir(), "/roots/one")
	if err := states.Save("Scout", sampleState(5)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := states.Rename("Scout", "Ranger"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, ok, _ := states.Load("Scout"); ok {
		t.Error("state still under the old name")
	}
	got, ok, err := states.Load("Ranger")
	if err != nil || !ok || got.Statistics.MessageCount != 5 {
		t.Fatalf("state did not move: ok %v err %v %+v", ok, err, got)
	}
	if err := states.Rename("Nobody", "Someone"); err != nil {
		t.Errorf("renaming missing state must be a no-op, got %v", err)
	}
	if err := states.Delete("Ranger"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := states.Load("Ranger"); ok {
		t.Error("state survived Delete")
	}
	if err := states.Delete("Ranger"); err != nil {
		t.Errorf("deleting missing state must be a no-op, got %v", err)
	}
}

func TestRuntimeStateRootsDoNotShareFiles(t *testing.T) {
	stateRoot := t.TempDir()
	one := NewRuntimeStateStore(stateRoot, "/roots/one")
	two := NewRuntimeStateStore(stateRoot, "/roots/two")
	if one.Dir() == two.Dir() {
		t.Fatalf("two roots share a state folder: %s", one.Dir())
	}
	if err := one.Save("Scout", sampleState(11)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok, _ := two.Load("Scout"); ok {
		t.Error("a same-named agent in another root sees this root's statistics")
	}
	if got := filepath.Base(one.Dir()); len(got) != rootKeyLength {
		t.Errorf("root key %q should be %d characters", got, rootKeyLength)
	}
	if RootKey("/roots/one/") != RootKey("/roots/one") {
		t.Error("a trailing separator must not change the root key")
	}
}
