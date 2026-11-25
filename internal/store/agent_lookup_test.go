package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

func lookupStore(t *testing.T, names ...string) Store {
	t.Helper()
	st, err := NewFileStore(
		filepath.Join(t.TempDir(), "agents_index.json"),
		types.Settings{Model: "gpt-4o-mini", Temperature: 1.0},
	)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for _, name := range names {
		if err := st.CreateAgent(name, &CreateAgentConfig{}); err != nil {
			t.Fatalf("CreateAgent(%q): %v", name, err)
		}
	}
	return st
}

// markRetiredAssistant stamps the retired assistant's durable marker on a
// stored record, as an existing profile carries it.
func markRetiredAssistant(t *testing.T, st Store, name string) {
	t.Helper()
	fs, ok := st.(*fileStore)
	if !ok {
		t.Fatalf("store %T is not a file store", st)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ag := fs.agents[name]
	if ag == nil {
		t.Fatalf("no record %q to mark", name)
	}
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	ag.Metadata.Tags = append(ag.Metadata.Tags, systemassistant.ProtectedMarker)
}

// A stored reference resolves only to an agent the person has under exactly
// that name. The retired assistant's earlier names are no longer redirected.
func TestResolveAgentNeverRedirectsRetiredAssistantNames(t *testing.T) {
	st := lookupStore(t, "Research Buddy")

	for _, name := range []string{"Ask Ori", "Workspace Manager", "Ori", "__assistant__", "Deleted Agent", ""} {
		if _, _, ok := ResolveAgent(st, name); ok {
			t.Errorf("ResolveAgent(%q) resolved, want not found", name)
		}
	}
	if _, resolved, ok := ResolveAgent(st, "Research Buddy"); !ok || resolved != "Research Buddy" {
		t.Errorf("an ordinary agent must resolve to itself, got %q", resolved)
	}
}

// A person's own agent called "Ask Ori" is ordinary in every respect.
func TestResolveAgentReachesAUsersOwnAskOri(t *testing.T) {
	st := lookupStore(t, "Ask Ori")
	if _, resolved, ok := ResolveAgent(st, "Ask Ori"); !ok || resolved != "Ask Ori" {
		t.Fatalf("a user's own Ask Ori resolved to %q, ok=%v", resolved, ok)
	}
}

// The retired assistant's record is kept on disk but reached by nothing: not
// lookup, not listing, and not a create under its name.
func TestRetiredAssistantRecordIsHiddenNotDeleted(t *testing.T) {
	st := lookupStore(t, "Ask Ori", "Scout")
	markRetiredAssistant(t, st, "Ask Ori")

	if _, _, ok := ResolveAgent(st, "Ask Ori"); ok {
		t.Fatal("the retired assistant record must not resolve")
	}
	if _, ok := st.GetAgent("Ask Ori"); ok {
		t.Fatal("GetAgent returned the retired assistant record")
	}
	for _, name := range st.ListAgents() {
		if name == "Ask Ori" {
			t.Fatal("ListAgents listed the retired assistant record")
		}
	}
	if err := st.CreateAgent("Ask Ori", &CreateAgentConfig{}); !errors.Is(err, ErrRetiredAssistantRecord) {
		t.Fatalf("creating over the retired record = %v, want ErrRetiredAssistantRecord", err)
	}
	fs := st.(*fileStore)
	fs.mu.Lock()
	kept := fs.agents["Ask Ori"]
	fs.mu.Unlock()
	if kept == nil || !isMarkedSystemAgent(kept) {
		t.Fatal("the retired record was removed or rewritten; it must stay as it was")
	}
	if _, ok := st.GetAgent("Scout"); !ok {
		t.Fatal("an ordinary agent beside it must stay reachable")
	}
}

func TestResolveAgentHandlesNilStore(t *testing.T) {
	if _, _, ok := ResolveAgent(nil, "Scout"); ok {
		t.Fatal("a nil store must not resolve anything")
	}
	var getter AgentGetter = mapGetter{"Scout": {}}
	if !AgentExists(getter, " Scout ") {
		t.Fatal("AgentExists must trim the reference")
	}
}

type mapGetter map[string]*agent.Agent

func (m mapGetter) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := m[name]
	return ag, ok
}
