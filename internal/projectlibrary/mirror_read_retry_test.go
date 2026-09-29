package projectlibrary

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// splitMirror reports a folder mirror whose library document is one revision
// ahead for its first `splits` reads, the way a read landing between a fenced
// write's folder rename and its primary update sees the Home.
type splitMirror struct {
	workspace.Store
	splits atomic.Int32
	reads  atomic.Int32
}

func (m *splitMirror) GetMirrorWorkspace(id string) (*workspace.Workspace, bool, error) {
	m.reads.Add(1)
	home, err := m.Get(id)
	if err != nil || home == nil {
		return nil, true, err
	}
	if m.splits.Add(-1) < 0 {
		return home, true, nil
	}
	state := *home.GetAssistantProgramState()
	var doc Document
	if err := json.Unmarshal(state.ProjectLibrary, &doc); err != nil {
		return nil, true, err
	}
	doc.Revision++
	ahead, err := json.Marshal(doc)
	if err != nil {
		return nil, true, err
	}
	state.ProjectLibrary = ahead
	// A JSON round trip clones the record without copying its mutex or
	// touching the store's own copy.
	encoded, err := json.Marshal(home)
	if err != nil {
		return nil, true, err
	}
	var clone workspace.Workspace
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, true, err
	}
	clone.SetAssistantProgramState(&state)
	return &clone, true, nil
}

func TestReadSnapshot_WaitsOutAnInFlightWriteButStillRefusesAPersistentSplit(t *testing.T) {
	file, scope := libraryHome(t)
	initializeLibrary(t, NewStore(file), scope)
	transient := &splitMirror{Store: file}
	transient.splits.Store(2)
	if _, err := NewStore(transient).Read(scope); err != nil {
		t.Fatalf("a split lasting two reads was not waited out: %v", err)
	}
	if got := transient.reads.Load(); got != 3 {
		t.Fatalf("want 3 mirror reads (two split, one agreeing), got %d", got)
	}
	persistent := &splitMirror{Store: file}
	persistent.splits.Store(1000)
	if _, err := NewStore(persistent).Read(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("a persistent split must still fail closed: %v", err)
	}
	if got := persistent.reads.Load(); got != mirrorReadAttempts {
		t.Fatalf("want %d attempts before refusing, got %d", mirrorReadAttempts, got)
	}
}
