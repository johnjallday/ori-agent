package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestLibraryDefinitionNeverAnswersWithAWorkspaceCopy(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t,
		workspaceCopy("ws-1", "Earlier", "Helper", "from ws-1"),
		workspaceCopy("ws-2", "Studio", "Scout", "studio's scout"),
	)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "my scout"}); err != nil {
		t.Fatal(err)
	}

	// A workspace-only name has no library profile, even though GetAgent
	// answers with the lowest workspace ID's copy.
	if got, ok := c.GetAgent("Helper"); !ok || got.Settings.SystemPrompt != "from ws-1" {
		t.Fatalf("precondition: GetAgent(Helper) = %+v", got)
	}
	if rec := LibraryDefinition(c, "Helper"); rec.State != LibraryAbsent || rec.Definition != nil {
		t.Fatalf("LibraryDefinition(Helper) = %+v, want absent", rec)
	}

	rec := LibraryDefinition(c, "Scout")
	if rec.State != LibraryPresent || rec.Owner != LibraryOwnerLibrary || rec.Definition.Settings.SystemPrompt != "my scout" {
		t.Fatalf("LibraryDefinition(Scout) = %+v", rec)
	}
	// Exact name: a different case is not this agent's profile.
	if rec := LibraryDefinition(c, "scout"); rec.State != LibraryAbsent {
		t.Fatalf("LibraryDefinition(scout) = %+v, want absent", rec)
	}
}

func TestLibraryDefinitionIsADetachedCopy(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatal(err)
	}
	rec := LibraryDefinition(c, "Scout")
	rec.Definition.Settings.SystemPrompt = "changed by a reader"
	live, _ := c.GetAgent("Scout")
	if live.Settings.SystemPrompt != "original" {
		t.Fatalf("a reader's change reached the live record: %q", live.Settings.SystemPrompt)
	}
}

func TestLibraryDefinitionLabelsTheBuiltInAssistantAsSystem(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent(systemassistant.CanonicalName, &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	rec := LibraryDefinition(c, systemassistant.CanonicalName)
	if rec.State != LibraryPresent || rec.Owner != LibraryOwnerSystem {
		t.Fatalf("LibraryDefinition(assistant) = %+v, want a present system record", rec)
	}
}

func TestLibraryDefinitionReportsAnUnreadableDefinition(t *testing.T) {
	f := newCompositeFixture(t)
	dir := filepath.Join(f.root, "Agents", "Broken")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent_settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := f.open(t)
	if len(c.UnreadableAgents()) == 0 {
		t.Skip("store did not classify the fixture as unreadable")
	}
	rec := LibraryDefinition(c, "Broken")
	if rec.State != LibraryUnreadable || rec.Definition != nil {
		t.Fatalf("LibraryDefinition(Broken) = %+v, want unreadable", rec)
	}
}

func TestLibraryDefinitionWritesNothing(t *testing.T) {
	c, f, _ := compositeWithWorkspaces(t, workspaceCopy("ws-1", "Earlier", "Helper", "x"))
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "p"}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, filepath.Dir(f.dataDir))
	for _, name := range []string{"Scout", "Helper", "Missing", "scout"} {
		_ = LibraryDefinition(c, name)
	}
	if after := snapshotTree(t, filepath.Dir(f.dataDir)); !equalTrees(before, after) {
		t.Fatalf("LibraryDefinition changed files on disk:\nbefore %v\nafter  %v", before, after)
	}
}

// The inspection read and #780's protected edit share the store lock, so a
// concurrent read can never observe (or race with) a half-adopted definition.
func TestLibraryDefinitionIsRaceFreeAgainstDefinitionMutations(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "p0"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = MutateDefinition(c, "Scout", func(draft *agent.Agent) error {
				draft.Settings.SystemPrompt = "p" + string(rune('a'+i%26))
				draft.Settings.Model = "m" + string(rune('a'+i%26))
				return nil
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			rec := LibraryDefinition(c, "Scout")
			if rec.Definition == nil {
				t.Error("definition disappeared during a mutation")
				return
			}
			_ = rec.Definition.Settings.SystemPrompt + rec.Definition.Settings.Model
		}
	}()
	wg.Wait()
}

// The Default Toolbox editor updates the record in place through UpdateAgent;
// an inspection read must own its copy of the selection.
func TestLibraryDefinitionDefaultToolboxIsPrivate(t *testing.T) {
	c, _, _ := compositeWithWorkspaces(t)
	if err := c.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateAgent("Scout", func(ag *agent.Agent) error {
		ag.InitializeDefaultToolbox()
		ag.DefaultToolbox.Skills = []types.DefaultToolboxSkillRef{{CapabilityID: "a", DisplayName: "A"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := LibraryDefinition(c, "Scout")
	live, _ := c.GetAgent("Scout")
	if rec.Definition.DefaultToolbox == live.DefaultToolbox {
		t.Fatal("inspection shares the live Default Toolbox pointer")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = c.UpdateAgent("Scout", func(ag *agent.Agent) error {
				ag.DefaultToolbox.Skills = append(ag.DefaultToolbox.Skills[:0], types.DefaultToolboxSkillRef{CapabilityID: "b"})
				ag.DefaultToolbox.Version++
				return nil
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if tb := LibraryDefinition(c, "Scout").Definition.DefaultToolbox; tb != nil {
				_ = len(tb.Skills) + int(tb.Version)
			}
		}
	}()
	wg.Wait()
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- test temp dir
		if readErr != nil {
			return nil
		}
		out[path] = info.ModTime().String() + "|" + string(data)
		return nil
	})
	return out
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
