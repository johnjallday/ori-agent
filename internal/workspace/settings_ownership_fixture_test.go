package workspace

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Reusable synthetic fixture for settings-ownership inspection (task 1.3).
//
//   - Library "Writer" (model library-model, prompt "Library prompt.")
//   - ws-tracked: tracked copy equal to the library, one instance
//   - ws-custom: customised copy (model custom-model), one instance
//   - ws-equal: untracked copy equal to the library, one instance
//   - ws-fallback: one instance and no copy of its own
//   - ws-dup: own copy and two Writer instances with different refinements
//   - ws-broken: one instance whose copy cannot be read
//   - ws-scout-a / ws-scout-b: workspace-only "Scout" namesakes, no library
//   - ws-trashed: a trashed workspace that still names Writer
//
// A copy carries an API key and permission grants that must never surface.

const ownershipSecret = "sk-ownership-fixture-secret"

type ownershipFixture struct {
	store    *ownershipSpyStore
	library  map[string]store.LibraryRecord
	tracking map[string]string
	// libraryCalls counts library reads, to show a context switch reads one.
	libraryCalls int
}

func writerDefinition(model, prompt string) *agent.Agent {
	allow := true
	return &agent.Agent{
		Role: types.RoleGeneral,
		Settings: types.Settings{
			Provider: "openai", Model: model, Temperature: 0.4, SystemPrompt: prompt,
			APIKey: ownershipSecret, AllowWebSearch: &allow, AllowNativeMCPTools: &allow,
			FallbackProvider: "ollama", FallbackModel: "fallback-secret-model",
		},
		Metadata: &types.AgentMetadata{Description: "Writes things."},
	}
}

func newOwnershipFixture(t *testing.T) *ownershipFixture {
	t.Helper()
	mem := NewInMemoryStore()
	now := time.Now()
	add := func(id, name string, status WorkspaceStatus, instances ...AgentInstance) {
		ws := &Workspace{ID: id, Name: name, FolderSlug: id, Status: status, CreatedAt: now, UpdatedAt: now, AgentInstances: instances}
		if err := mem.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	inst := func(id, name string, n int, role, custom string) AgentInstance {
		return AgentInstance{ID: id, Name: name, InstanceNumber: n, NodeID: id + "-node", Role: role, CustomInstructions: custom}
	}
	add("ws-tracked", "Tracked Studio", StatusActive, inst("i-tracked", "Writer", 1, "Lead", ""))
	add("ws-custom", "Custom Studio", StatusActive, inst("i-custom", "Writer", 1, "", ""))
	add("ws-equal", "Equal Studio", StatusActive, inst("i-equal", "Writer", 1, "", ""))
	add("ws-fallback", "Fallback Studio", StatusActive, inst("i-fallback", "Writer", 1, "", ""))
	add("ws-dup", "Duplicate Studio", StatusActive,
		inst("i-dup-1", "Writer", 1, "Drafts", "Draft quickly."),
		inst("i-dup-2", "Writer", 2, "Edits", "Edit carefully."))
	add("ws-broken", "Broken Studio", StatusActive, inst("i-broken", "Writer", 1, "", ""))
	add("ws-scout-a", "Scout A", StatusActive, inst("i-scout-a", "Scout", 1, "", ""))
	add("ws-scout-b", "Scout B", StatusActive, inst("i-scout-b", "Scout", 1, "", ""))
	add("ws-trashed", "Old Studio", StatusTrashed, inst("i-old", "Writer", 1, "", ""))

	save := func(ws, name string, ag *agent.Agent) {
		if err := mem.SaveWorkspaceAgent(ws, name, ag); err != nil {
			t.Fatal(err)
		}
	}
	save("ws-tracked", "Writer", writerDefinition("library-model", "Library prompt."))
	save("ws-custom", "Writer", writerDefinition("custom-model", "Library prompt."))
	save("ws-equal", "Writer", writerDefinition("library-model", "Library prompt."))
	save("ws-dup", "Writer", writerDefinition("dup-model", "Dup base prompt."))
	save("ws-scout-a", "Scout", writerDefinition("scout-a-model", "Scout A prompt."))
	save("ws-scout-b", "Scout", writerDefinition("scout-b-model", "Scout B prompt."))

	disabled := writerDefinition("library-model", "Library prompt.")
	disabled.Status = types.AgentStatusDisabled
	f := &ownershipFixture{
		store: &ownershipSpyStore{Store: mem, failCopy: map[string]bool{"ws-broken": true}},
		library: map[string]store.LibraryRecord{
			"Writer": {Definition: writerDefinition("library-model", "Library prompt."), State: store.LibraryPresent, Owner: store.LibraryOwnerLibrary},
			"Paused": {Definition: disabled, State: store.LibraryPresent, Owner: store.LibraryOwnerLibrary},
			"Broken": {State: store.LibraryUnreadable, Owner: store.LibraryOwnerLibrary},
		},
		tracking: map[string]string{"ws-tracked": "in_step", "ws-custom": "customized", "ws-equal": "untracked", "ws-dup": "untracked"},
	}
	f.store.t = t
	return f
}

func (f *ownershipFixture) sources() SettingsOwnershipSources {
	return SettingsOwnershipSources{
		Workspaces: f.store,
		Library: func(name string) store.LibraryRecord {
			f.libraryCalls++
			if rec, ok := f.library[name]; ok {
				return rec
			}
			return store.LibraryRecord{State: store.LibraryAbsent}
		},
		Tracking: func(_, workspaceID string, _, _ *agent.Agent) (string, error) {
			if state, ok := f.tracking[workspaceID]; ok {
				return state, nil
			}
			return "unknown", errors.New("no tracking fixture")
		},
	}
}

// ownershipSpyStore fails the test on any write and can fail chosen copy
// reads, standing in for an unreadable or locked workspace copy.
type ownershipSpyStore struct {
	Store
	t        *testing.T
	failCopy map[string]bool
	writes   int
}

func (s *ownershipSpyStore) write(what string) {
	s.writes++
	s.t.Errorf("settings ownership wrote: %s", what)
}

func (s *ownershipSpyStore) Save(ws *Workspace) error { s.write("Save " + ws.ID); return nil }
func (s *ownershipSpyStore) Delete(id string) error   { s.write("Delete " + id); return nil }
func (s *ownershipSpyStore) Update(id string, _ func(*Workspace) error) error {
	s.write("Update " + id)
	return nil
}
func (s *ownershipSpyStore) SaveWorkspaceAgent(id, name string, _ *agent.Agent) error {
	s.write("SaveWorkspaceAgent " + id + "/" + name)
	return nil
}
func (s *ownershipSpyStore) GetWorkspaceAgent(id, name string) (*agent.Agent, bool, error) {
	if s.failCopy[id] {
		return nil, false, ErrLocalConfigUnavailable
	}
	return s.Store.GetWorkspaceAgent(id, name)
}
