package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestMutateDefinitionRefusalChangesNothing(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	path := definitionPath(dir, "Scout")
	before, _ := os.ReadFile(path)
	refused := errors.New("refused")

	_, err := fs.MutateAgentDefinition("Scout", func(draft *agent.Agent) error {
		draft.Settings.SystemPrompt = "half applied"
		draft.Metadata = &types.AgentMetadata{Tags: []string{"leak"}}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the callback's refusal", err)
	}
	live, _ := fs.GetAgent("Scout")
	if live.Settings.SystemPrompt != "original" || (live.Metadata != nil && len(live.Metadata.Tags) != 0) {
		t.Fatalf("refused mutation leaked into the live record: %+v %+v", live.Settings, live.Metadata)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("refused mutation rewrote the definition file")
	}
}

func TestMutateDefinitionAdoptsInPlaceAndKeepsRuntimeState(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	held, _ := fs.GetAgent("Scout")
	held.Statistics.RecordMessage(42, 0.5)
	stats := held.Statistics

	committed, err := fs.MutateAgentDefinition("Scout", func(draft *agent.Agent) error {
		draft.Settings.SystemPrompt = "edited"
		return nil
	})
	if err != nil {
		t.Fatalf("MutateAgentDefinition: %v", err)
	}
	if committed.Settings.SystemPrompt != "edited" {
		t.Fatalf("committed copy = %q", committed.Settings.SystemPrompt)
	}
	// A caller that held the record before the edit sees the edit, so writing
	// that record back later cannot restore the old definition.
	if held.Settings.SystemPrompt != "edited" {
		t.Fatalf("held record did not adopt the edit: %q", held.Settings.SystemPrompt)
	}
	if held.Statistics != stats || held.Statistics.MessageCount != 1 {
		t.Fatalf("runtime statistics were replaced or lost: %+v", held.Statistics)
	}
	// The committed copy is private: changing it reaches neither memory nor disk.
	committed.Settings.SystemPrompt = "tampered"
	if held.Settings.SystemPrompt != "edited" {
		t.Fatal("committed copy aliases the live record")
	}
	reopened := writesStore(t, dir)
	got, _ := reopened.GetAgent("Scout")
	if got.Settings.SystemPrompt != "edited" {
		t.Fatalf("edit not durable: %q", got.Settings.SystemPrompt)
	}
}

func TestMutateDefinitionAfterAnEditOnDiskRefusesAndReloads(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	path := definitionPath(dir, "Scout")
	editOnDisk(t, path, setEditedPrompt)
	onDisk, _ := os.ReadFile(path)

	called := false
	_, err := fs.MutateAgentDefinition("Scout", func(draft *agent.Agent) error {
		called = true
		draft.Settings.Temperature = 0.25
		return nil
	})
	if !errors.Is(err, ErrAgentChangedOnDisk) {
		t.Fatalf("err = %v, want ErrAgentChangedOnDisk", err)
	}
	if called {
		t.Fatal("the callback ran against a definition that no longer matched the file")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(onDisk) {
		t.Fatal("the outside edit was overwritten")
	}
	live, _ := fs.GetAgent("Scout")
	if live.Settings.SystemPrompt != "edited in a text editor" {
		t.Fatalf("memory was not reloaded from disk: %q", live.Settings.SystemPrompt)
	}
}

func TestMutateDefinitionWriteFailureLeavesTheLiveRecord(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions enforced for this user")
	}
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	folder := filepath.Dir(definitionPath(dir, "Scout"))
	// The atomic write needs a temporary file beside the definition; a folder
	// it cannot write to fails the write before the commit point.
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o750) })

	_, err := fs.MutateAgentDefinition("Scout", func(draft *agent.Agent) error {
		draft.Settings.SystemPrompt = "never written"
		return nil
	})
	if err == nil {
		t.Fatal("expected the write to fail")
	}
	live, _ := fs.GetAgent("Scout")
	if live.Settings.SystemPrompt != "original" {
		t.Fatalf("a failed write still changed the live record: %q", live.Settings.SystemPrompt)
	}
	_ = os.Chmod(folder, 0o750)
	if _, err := fs.MutateAgentDefinition("Scout", func(draft *agent.Agent) error {
		draft.Settings.SystemPrompt = "retried"
		return nil
	}); err != nil {
		t.Fatalf("retry after the folder became writable: %v", err)
	}
}

func TestCompositeMutateDefinitionRoutesToTheOwningStore(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	var _ DefinitionMutator = c
	if err := c.CreateAgent(systemassistant.CanonicalName, &CreateAgentConfig{}); err != nil {
		t.Fatalf("create assistant: %v", err)
	}
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("create Scout: %v", err)
	}
	for _, name := range []string{systemassistant.CanonicalName, "Scout"} {
		if _, err := MutateDefinition(c, name, func(draft *agent.Agent) error {
			draft.Settings.SystemPrompt = "routed " + name
			return nil
		}); err != nil {
			t.Fatalf("mutate %s: %v", name, err)
		}
	}
	reopened := f.open(t)
	for _, name := range []string{systemassistant.CanonicalName, "Scout"} {
		got, _ := reopened.GetAgent(name)
		if got == nil || got.Settings.SystemPrompt != "routed "+name {
			t.Fatalf("%s edit not durable in its own store: %+v", name, got)
		}
	}

	// A root that disappears mid-session refuses the write rather than
	// recreating the folder.
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatalf("remove root: %v", err)
	}
	if _, err := MutateDefinition(c, "Scout", func(*agent.Agent) error { return nil }); !errors.Is(err, ErrAgentRootUnavailable) {
		t.Fatalf("err = %v, want ErrAgentRootUnavailable", err)
	}
}

func TestMutateDefinitionUnknownAgent(t *testing.T) {
	fs := writesStore(t, t.TempDir())
	_, err := fs.MutateAgentDefinition("Nobody", func(*agent.Agent) error { return nil })
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("err = %v, want ErrAgentNotFound", err)
	}
}
