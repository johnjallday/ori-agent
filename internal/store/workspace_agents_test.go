package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// fakeWorkspaceSource stands in for internal/workspace's trusted source.
type fakeWorkspaceSource struct {
	entries []WorkspaceAgentEntry
	calls   int
}

func (f *fakeWorkspaceSource) WorkspaceAgents() []WorkspaceAgentEntry {
	f.calls++
	return f.entries
}

func workspaceCopy(workspaceID, workspaceName, name, prompt string) WorkspaceAgentEntry {
	return WorkspaceAgentEntry{
		WorkspaceID:   workspaceID,
		WorkspaceName: workspaceName,
		AgentName:     name,
		Agent:         &agent.Agent{Settings: types.Settings{Model: "gpt-4o-mini", SystemPrompt: prompt}},
	}
}

func compositeWithWorkspaces(t *testing.T, entries ...WorkspaceAgentEntry) (*CompositeStore, compositeFixture, *fakeWorkspaceSource) {
	t.Helper()
	f := newCompositeFixture(t)
	c := f.open(t)
	source := &fakeWorkspaceSource{entries: entries}
	c.SetWorkspaceAgentSource(source)
	return c, f, source
}

func TestRosterIsTheUnionWithEachNameOnce(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t,
		workspaceCopy("ws-b", "Studio", "scout", "studio's scout"), // a customisation, other case
		workspaceCopy("ws-b", "Studio", "Stranger", "only in a workspace"),
	)
	if err := c.CreateAgent(systemassistant.CanonicalName, &CreateAgentConfig{}); err != nil {
		t.Fatalf("assistant: %v", err)
	}
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "my scout"}); err != nil {
		t.Fatalf("Scout: %v", err)
	}

	names := c.ListAgents()
	if len(names) != 3 {
		t.Fatalf("ListAgents = %v, want the assistant, Scout, and Stranger once each", names)
	}
	got, ok := c.GetAgent("Scout")
	if !ok || got.Settings.SystemPrompt != "my scout" {
		t.Errorf("GetAgent(Scout) = %+v, want the root agent, not the workspace copy", got)
	}
	if got, ok := c.GetAgent("scout"); ok && got.Settings.SystemPrompt == "studio's scout" {
		t.Error("a differently-cased lookup resolved to the workspace copy of a root agent")
	}
	stranger, ok := c.GetAgent("Stranger")
	if !ok || stranger.Settings.SystemPrompt != "only in a workspace" {
		t.Errorf("GetAgent(Stranger) = %+v, want the workspace copy", stranger)
	}
}

func TestSameWorkspaceOnlyNameResolvesToTheLowestWorkspaceID(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t,
		workspaceCopy("ws-9", "Later", "Helper", "from ws-9"),
		workspaceCopy("ws-1", "Earlier", "Helper", "from ws-1"),
	)
	got, ok := c.GetAgent("Helper")
	if !ok || got.Settings.SystemPrompt != "from ws-1" {
		t.Fatalf("GetAgent(Helper) = %+v, want the copy from ws-1", got)
	}
	origin, _ := c.AgentOrigin("Helper")
	if origin.Source != SourceWorkspace || origin.WorkspaceID != "ws-1" || origin.WorkspaceName != "Earlier" {
		t.Errorf("AgentOrigin = %+v", origin)
	}
	if names := c.ListAgents(); len(names) != 1 {
		t.Errorf("ListAgents = %v, want Helper once", names)
	}
}

func TestWritesToAWorkspaceOnlyAgentAreRefused(t *testing.T) {
	c, f, _ := compositeWithWorkspaces(t, workspaceCopy("ws-1", "Studio", "Stranger", "only in a workspace"))
	ag, _ := c.GetAgent("Stranger")

	checks := map[string]error{
		"SetAgent":    c.SetAgent("Stranger", ag),
		"UpdateAgent": c.UpdateAgent("Stranger", func(*agent.Agent) error { return nil }),
		"DeleteAgent": c.DeleteAgent("Stranger"),
		"RenameAgent": c.RenameAgent("Stranger", "Renamed"),
	}
	for op, err := range checks {
		var owned *WorkspaceOwnedAgentError
		if !errors.As(err, &owned) || !errors.Is(err, ErrWorkspaceOwnedAgent) {
			t.Errorf("%s: err = %v, want ErrWorkspaceOwnedAgent", op, err)
			continue
		}
		if owned.WorkspaceName != "Studio" {
			t.Errorf("%s: owning workspace = %q", op, owned.WorkspaceName)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Stranger")); !os.IsNotExist(err) {
		t.Error("a refused write created a root agent")
	}
}

func TestOriginReportsCustomisationsOfARootAgent(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t,
		workspaceCopy("ws-1", "Studio", "Scout", "studio's scout"),
		workspaceCopy("ws-2", "Garage", "Scout", "garage's scout"),
	)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	origin, ok := c.AgentOrigin("Scout")
	if !ok || origin.Source != SourceRoster || len(origin.CustomisedIn) != 2 {
		t.Fatalf("AgentOrigin(Scout) = %+v, want roster customised in 2 workspaces", origin)
	}
	if origin.CustomisedIn[0].Name != "Studio" || origin.CustomisedIn[1].Name != "Garage" {
		t.Errorf("CustomisedIn = %+v", origin.CustomisedIn)
	}
	if err := c.CreateAgent(systemassistant.CanonicalName, &CreateAgentConfig{}); err != nil {
		t.Fatalf("assistant: %v", err)
	}
	if origin, _ := c.AgentOrigin(systemassistant.CanonicalName); origin.Source != SourceSystem {
		t.Errorf("the assistant's origin = %+v, want system", origin)
	}
}

func TestAddWorkspaceAgentCopiesItIntoTheRoot(t *testing.T) {
	c, f, _ := compositeWithWorkspaces(t, workspaceCopy("ws-1", "Studio", "Stranger", "only in a workspace"))
	name, err := c.AddWorkspaceAgent("ws-1", "stranger")
	if err != nil || name != "Stranger" {
		t.Fatalf("AddWorkspaceAgent = %q, %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Stranger", "agent_settings.json")); err != nil {
		t.Fatalf("no root agent was written: %v", err)
	}
	origin, _ := c.AgentOrigin("Stranger")
	if origin.Source != SourceRoster || len(origin.CustomisedIn) != 1 {
		t.Errorf("after adding, AgentOrigin = %+v, want a roster agent customised in Studio", origin)
	}
	got, _ := c.GetAgent("Stranger")
	if got.Settings.SystemPrompt != "only in a workspace" {
		t.Errorf("the added agent lost its definition: %+v", got.Settings)
	}
	if err := c.SetAgent("Stranger", got); err != nil {
		t.Errorf("the added agent is not editable: %v", err)
	}

	if _, err := c.AddWorkspaceAgent("ws-1", "Stranger"); !errors.Is(err, ErrAgentAlreadyExists) {
		t.Errorf("adding it twice: err = %v, want ErrAgentAlreadyExists", err)
	}
	if _, err := c.AddWorkspaceAgent("ws-404", "Nobody"); !errors.Is(err, ErrWorkspaceAgentNotFound) {
		t.Errorf("adding an unknown agent: err = %v, want ErrWorkspaceAgentNotFound", err)
	}
}

func TestWorkspaceViewIsCachedUntilInvalidated(t *testing.T) {
	c, _, source := compositeWithWorkspaces(t, workspaceCopy("ws-1", "Studio", "Stranger", "x"))
	for range 5 {
		c.GetAgent("Stranger")
		c.ListAgents()
	}
	if source.calls != 1 {
		t.Errorf("the source was read %d times, want once while the view is fresh", source.calls)
	}
	c.InvalidateWorkspaceAgents()
	c.ListAgents()
	if source.calls != 2 {
		t.Errorf("after invalidation the source was read %d times in total, want 2", source.calls)
	}
}
