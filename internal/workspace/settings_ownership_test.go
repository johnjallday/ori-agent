package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
)

func field(t *testing.T, def OwnershipDefinition, key string) OwnershipField {
	t.Helper()
	for _, f := range def.Fields {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("field %q missing from %+v", key, def.Fields)
	return OwnershipField{}
}

func project(t *testing.T, f *ownershipFixture, req SettingsOwnershipRequest) SettingsOwnership {
	t.Helper()
	out, err := ProjectSettingsOwnership(f.sources(), req)
	if err != nil {
		t.Fatalf("ProjectSettingsOwnership(%+v): %v", req, err)
	}
	return out
}

func TestSettingsOwnershipSourcesAndComparisons(t *testing.T) {
	f := newOwnershipFixture(t)
	cases := []struct {
		name, workspace        string
		state, source          string
		model, modelComparison string
		tracking, contextState string
		promptComparison       string
	}{
		{"library", "", OwnershipStateOK, OwnershipSourceLibrary, "library-model", OwnershipNotCompared, OwnershipTrackingNotApplicable, OwnershipStateOK, OwnershipNotCompared},
		{"tracked copy", "ws-tracked", OwnershipStateOK, OwnershipSourceWorkspaceCopy, "library-model", OwnershipSame, "in_step", OwnershipStateOK, OwnershipSame},
		{"customised copy", "ws-custom", OwnershipStateOK, OwnershipSourceWorkspaceCopy, "custom-model", OwnershipDifferent, "customized", OwnershipStateOK, OwnershipSame},
		// Equal values do not make a copy tracked or inherited.
		{"untracked equal copy", "ws-equal", OwnershipStateOK, OwnershipSourceWorkspaceCopy, "library-model", OwnershipSame, "untracked", OwnershipStateOK, OwnershipSame},
		{"no copy reads library", "ws-fallback", OwnershipStateOK, OwnershipSourceLibraryFallback, "library-model", OwnershipNotCompared, OwnershipTrackingNotApplicable, OwnershipStateOK, OwnershipNotCompared},
		{"unreadable copy", "ws-broken", OwnershipStateDegraded, OwnershipSourceUnknown, "", "", OwnershipTrackingUnknown, OwnershipStateOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: tc.workspace})
			if out.Definition.State != tc.state || out.Definition.Source != tc.source {
				t.Fatalf("definition = %s/%s, want %s/%s", out.Definition.State, out.Definition.Source, tc.state, tc.source)
			}
			if out.Tracking.State != tc.tracking || out.Context.State != tc.contextState {
				t.Fatalf("tracking/context = %s/%s", out.Tracking.State, out.Context.State)
			}
			if tc.model == "" {
				if len(out.Definition.Fields) != 0 {
					t.Fatalf("a degraded context showed values: %+v", out.Definition.Fields)
				}
				return
			}
			model := field(t, out.Definition, "model")
			if model.Value != tc.model || model.Comparison != tc.modelComparison {
				t.Fatalf("model = %+v", model)
			}
			if prompt := field(t, out.Definition, "system_prompt"); prompt.Comparison != tc.promptComparison {
				t.Fatalf("prompt comparison = %s", prompt.Comparison)
			}
		})
	}
}

func TestSettingsOwnershipWorkspaceOnlyNamesakesNeverBorrowEachOther(t *testing.T) {
	f := newOwnershipFixture(t)
	lib := project(t, f, SettingsOwnershipRequest{Agent: "Scout"})
	if lib.Context.State != OwnershipStateNoLibrary || lib.Library.State != store.LibraryAbsent || len(lib.Definition.Fields) != 0 {
		t.Fatalf("Scout library context = %+v / %+v", lib.Context, lib.Definition)
	}
	if len(lib.Contexts.Workspaces) != 2 {
		t.Fatalf("Scout contexts = %+v, want both workspaces", lib.Contexts.Workspaces)
	}
	for ws, want := range map[string]string{"ws-scout-a": "scout-a-model", "ws-scout-b": "scout-b-model"} {
		out := project(t, f, SettingsOwnershipRequest{Agent: "Scout", WorkspaceID: ws})
		model := field(t, out.Definition, "model")
		if model.Value != want || model.Comparison != OwnershipNotCompared {
			t.Fatalf("%s model = %+v, want %s and no comparison", ws, model, want)
		}
	}
	// A workspace that names Writer but not Scout is not a Scout context.
	out := project(t, f, SettingsOwnershipRequest{Agent: "Scout", WorkspaceID: "ws-tracked"})
	if out.Context.State != OwnershipStateNotMember || len(out.Definition.Fields) != 0 {
		t.Fatalf("non-member context = %+v %+v", out.Context, out.Definition)
	}
}

func TestSettingsOwnershipUnavailableContextsAreRecoverable(t *testing.T) {
	f := newOwnershipFixture(t)
	for _, ws := range []string{"ws-trashed", "no-such-workspace"} {
		out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: ws})
		if out.Context.State != OwnershipStateNotFound || out.Context.Workspace != nil || len(out.Definition.Fields) != 0 {
			t.Fatalf("%s = %+v %+v", ws, out.Context, out.Definition)
		}
		if out.Context.Message == "" {
			t.Fatalf("%s has no recovery message", ws)
		}
	}
	// The trashed workspace is not offered as a context.
	lib := project(t, f, SettingsOwnershipRequest{Agent: "Writer"})
	for _, entry := range lib.Contexts.Workspaces {
		if entry.ID == "ws-trashed" {
			t.Fatal("trashed workspace offered as a context")
		}
	}
	if _, err := ProjectSettingsOwnership(f.sources(), SettingsOwnershipRequest{Agent: "Nobody"}); !errors.Is(err, ErrSettingsOwnershipAgentNotFound) {
		t.Fatalf("unknown agent err = %v", err)
	}
	if _, err := ProjectSettingsOwnership(f.sources(), SettingsOwnershipRequest{Agent: "Nobody", WorkspaceID: "ws-tracked"}); !errors.Is(err, ErrSettingsOwnershipAgentNotFound) {
		t.Fatalf("unknown agent in a workspace err = %v", err)
	}
	broken := project(t, f, SettingsOwnershipRequest{Agent: "Broken"})
	if broken.Context.State != OwnershipStateUnavailable || broken.Definition.State != OwnershipStateUnavailable {
		t.Fatalf("unreadable library = %+v %+v", broken.Context, broken.Definition)
	}
}

func TestSettingsOwnershipInstanceSelectionIsExact(t *testing.T) {
	f := newOwnershipFixture(t)
	amb := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-dup"})
	if amb.Instances.State != OwnershipInstanceAmbiguous || amb.Instances.SelectedID != "" || len(amb.Instances.Items) != 2 {
		t.Fatalf("duplicate instances = %+v", amb.Instances)
	}
	sel := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-dup", InstanceID: "i-dup-2"})
	if sel.Instances.State != OwnershipInstanceSelected || sel.Instances.SelectedID != "i-dup-2" {
		t.Fatalf("explicit instance = %+v", sel.Instances)
	}
	// Another workspace's instance, or another agent's, is never accepted.
	for _, id := range []string{"i-tracked", "i-scout-a", "nope"} {
		out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-dup", InstanceID: id})
		if out.Instances.State != OwnershipInstanceNotFound || out.Instances.SelectedID != "" {
			t.Fatalf("instance %s = %+v", id, out.Instances)
		}
	}
	single := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-tracked"})
	if single.Instances.State != OwnershipInstanceSelected || single.Instances.SelectedID != "i-tracked" {
		t.Fatalf("single instance = %+v", single.Instances)
	}
}

func TestSettingsOwnershipDistinguishesEmptyFromZero(t *testing.T) {
	f := newOwnershipFixture(t)
	def := f.library["Writer"].Definition
	def.Settings.Temperature = 0
	def.Settings.ReasoningEffort = ""
	def.Settings.MaxOutputTokens = 0
	out := project(t, f, SettingsOwnershipRequest{Agent: "Writer"})
	if temp := field(t, out.Definition, "temperature"); temp.Empty || temp.Value != float64(0) {
		t.Fatalf("explicit zero temperature = %+v", temp)
	}
	if r := field(t, out.Definition, "reasoning_effort"); !r.Empty {
		t.Fatalf("unset reasoning = %+v", r)
	}
	if m := field(t, out.Definition, "max_output_tokens"); !m.Empty {
		t.Fatalf("unset output limit = %+v", m)
	}
	paused := project(t, f, SettingsOwnershipRequest{Agent: "Paused"})
	if !paused.Definition.Disabled || paused.Definition.State != OwnershipStateOK {
		t.Fatalf("disabled definition = %+v", paused.Definition)
	}
}

func TestSettingsOwnershipNeverSerializesCredentialsOrGrants(t *testing.T) {
	f := newOwnershipFixture(t)
	for _, ws := range []string{"", "ws-tracked", "ws-custom", "ws-fallback", "ws-dup", "ws-broken"} {
		out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: ws})
		data, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, leaked := range []string{ownershipSecret, "api_key", "allow_web_search", "allow_native_mcp_tools", "fallback_", "fallback-secret-model", "ollama", "ori_local_config_id", "Settings"} {
			if strings.Contains(string(data), leaked) {
				t.Fatalf("context %q leaked %q: %s", ws, leaked, data)
			}
		}
	}
}

func TestSettingsOwnershipWritesNothingAcrossEveryContext(t *testing.T) {
	f := newOwnershipFixture(t)
	for _, name := range []string{"Writer", "Scout", "Broken", "Paused", "Nobody"} {
		for _, ws := range []string{"", "ws-tracked", "ws-custom", "ws-equal", "ws-fallback", "ws-dup", "ws-broken", "ws-scout-a", "ws-trashed", "missing"} {
			_, _ = ProjectSettingsOwnership(f.sources(), SettingsOwnershipRequest{Agent: name, WorkspaceID: ws, InstanceID: "i-dup-1"})
		}
	}
	if f.store.writes != 0 {
		t.Fatalf("%d writes during inspection", f.store.writes)
	}
}

// Real FileStore: before/after bytes and modification times must match.
func TestSettingsOwnershipLeavesAFileStoreUntouched(t *testing.T) {
	root := t.TempDir()
	files, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{ID: "ws-real", Name: "Real", Status: StatusActive, AgentInstances: []AgentInstance{{ID: "i-1", Name: "Writer", InstanceNumber: 1}}}
	if err := files.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := files.SaveWorkspaceAgent("ws-real", "Writer", writerDefinition("disk-model", "Disk prompt.")); err != nil {
		t.Fatal(err)
	}
	// A legacy-shaped copy with retired appearance fields: the read normalizes
	// in memory only.
	before := treeSnapshot(t, root)
	src := SettingsOwnershipSources{
		Workspaces: files,
		Library: func(string) store.LibraryRecord {
			return store.LibraryRecord{Definition: writerDefinition("library-model", "Library prompt."), State: store.LibraryPresent, Owner: store.LibraryOwnerLibrary}
		},
	}
	for i := 0; i < 3; i++ {
		out, err := ProjectSettingsOwnership(src, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-real"})
		if err != nil || field(t, out.Definition, "model").Value != "disk-model" {
			t.Fatalf("projection = %+v %v", out.Definition, err)
		}
		_, _ = ProjectSettingsOwnership(src, SettingsOwnershipRequest{Agent: "Writer"})
	}
	if after := treeSnapshot(t, root); len(after) != len(before) {
		t.Fatalf("file set changed: %d -> %d", len(before), len(after))
	} else {
		for path, sig := range before {
			if after[path] != sig {
				t.Fatalf("%s changed during inspection", path)
			}
		}
	}
}

func TestSettingsOwnershipConcurrentReadsAreRaceFree(t *testing.T) {
	f := newOwnershipFixture(t)
	src := f.sources()
	src.Library = func(name string) store.LibraryRecord { return f.library[name] }
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, ws := range []string{"", "ws-custom", "ws-dup", "ws-fallback"} {
				_, _ = ProjectSettingsOwnership(src, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: ws})
			}
		}()
	}
	wg.Wait()
}

// For the ordinary cases the projection names the same saved definition the
// runner's snapshot-first selector uses.
func TestSettingsOwnershipAgreesWithTheRuntimeDefinitionSelector(t *testing.T) {
	f := newOwnershipFixture(t)
	lib := memLibrary{"Writer": writerDefinition("library-model", "Library prompt.")}
	resolver := NewAgentRuntimeResolver(lib, f.store, nil, nil)
	for _, ws := range []string{"ws-custom", "ws-fallback", "ws-dup"} {
		resolved, err := resolver.ResolveAgentDefinition("Writer", ws)
		if err != nil {
			t.Fatal(err)
		}
		out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: ws})
		if got := field(t, out.Definition, "model").Value; got != resolved.Agent.Settings.Model {
			t.Fatalf("%s: projection %v, runtime %s", ws, got, resolved.Agent.Settings.Model)
		}
		if (out.Definition.Source == OwnershipSourceWorkspaceCopy) != resolved.FromWorkspaceCopy {
			t.Fatalf("%s: source %s, runtime FromWorkspaceCopy=%v", ws, out.Definition.Source, resolved.FromWorkspaceCopy)
		}
	}
}

// Copies are stored by slug: "Dev Ops" and "dev-ops" share one file, so the
// projection refuses to attribute it rather than show another agent's copy.
func TestSettingsOwnershipRefusesASlugSharedCopyFile(t *testing.T) {
	f := newOwnershipFixture(t)
	mem := f.store.Store
	ws := &Workspace{ID: "ws-slug", Name: "Slug Studio", Status: StatusActive, AgentInstances: []AgentInstance{
		{ID: "i-a", Name: "Dev Ops", InstanceNumber: 1}, {ID: "i-b", Name: "dev-ops", InstanceNumber: 1},
	}}
	if err := mem.Save(ws); err != nil {
		t.Fatal(err)
	}
	if err := mem.SaveWorkspaceAgent("ws-slug", "dev-ops", writerDefinition("other-model", "x")); err != nil {
		t.Fatal(err)
	}
	f.library["Dev Ops"] = store.LibraryRecord{Definition: writerDefinition("library-model", "y"), State: store.LibraryPresent, Owner: store.LibraryOwnerLibrary}
	out := project(t, f, SettingsOwnershipRequest{Agent: "Dev Ops", WorkspaceID: "ws-slug"})
	if out.Definition.State != OwnershipStateDegraded || len(out.Definition.Fields) != 0 || !strings.Contains(out.Definition.Note, "dev-ops") {
		t.Fatalf("slug-shared copy = %+v", out.Definition)
	}
	if out.Instances.State != OwnershipInstanceSelected || out.Instances.SelectedID != "i-a" {
		t.Fatalf("instances = %+v, want only Dev Ops's own", out.Instances)
	}
}

// A copy file in a workspace that never names the agent is not a context.
func TestSettingsOwnershipIgnoresAnUnreferencedCopyFile(t *testing.T) {
	f := newOwnershipFixture(t)
	if err := f.store.Store.SaveWorkspaceAgent("ws-scout-a", "Writer", writerDefinition("stray", "stray")); err != nil {
		t.Fatal(err)
	}
	out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-scout-a"})
	if out.Context.State != OwnershipStateNotMember || len(out.Definition.Fields) != 0 {
		t.Fatalf("stray copy = %+v %+v", out.Context, out.Definition)
	}
}

func TestSettingsOwnershipLegacyInstanceWithoutIDIsNotSelectable(t *testing.T) {
	f := newOwnershipFixture(t)
	ws := &Workspace{ID: "ws-legacy", Name: "Legacy", Status: StatusActive, AgentInstances: []AgentInstance{{Name: "Writer"}}}
	if err := f.store.Store.Save(ws); err != nil {
		t.Fatal(err)
	}
	out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-legacy"})
	if out.Instances.State != OwnershipInstanceUnidentified || out.Instances.SelectedID != "" {
		t.Fatalf("legacy instance = %+v", out.Instances)
	}
	if out.Definition.Source != OwnershipSourceLibraryFallback {
		t.Fatalf("legacy definition = %+v", out.Definition)
	}
}

func TestSettingsOwnershipNotesRetiredAssistantReferences(t *testing.T) {
	f := newOwnershipFixture(t)
	f.library["Ask Ori"] = store.LibraryRecord{Definition: writerDefinition("m", "p"), State: store.LibraryPresent, Owner: store.LibraryOwnerSystem}
	out := project(t, f, SettingsOwnershipRequest{Agent: "Ask Ori"})
	if out.Definition.Source != OwnershipSourceSystem || out.Contexts.Note == "" {
		t.Fatalf("assistant = %+v / %+v", out.Definition, out.Contexts)
	}
}

func TestSettingsOwnershipPromptUsesTheSelectedSavedBaseAndExactInstance(t *testing.T) {
	f := newOwnershipFixture(t)

	lib := project(t, f, SettingsOwnershipRequest{Agent: "Writer"})
	if lib.Prompt.State != OwnershipPromptOK || lib.Prompt.Preview != "Library prompt." || len(lib.Prompt.Layers) != 0 {
		t.Fatalf("library prompt = %+v", lib.Prompt)
	}
	if !strings.Contains(lib.Prompt.Note, "not the complete prompt") {
		t.Fatalf("missing run-time exclusion: %q", lib.Prompt.Note)
	}

	// The workspace's own base, never the library's or a namesake's.
	dup := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-dup", InstanceID: "i-dup-2"})
	want := "Dup base prompt.\n\n---\n" + RenderAgentRefinement(AgentInstance{Role: "Edits", CustomInstructions: "Edit carefully."})
	if dup.Prompt.State != OwnershipPromptOK || dup.Prompt.Base != "Dup base prompt." || dup.Prompt.Preview != want {
		t.Fatalf("dup instance 2 prompt = %+v", dup.Prompt)
	}
	if dup.Prompt.BaseSource != OwnershipSourceWorkspaceCopy || len(dup.Prompt.Layers) != 2 {
		t.Fatalf("dup layers/source = %+v", dup.Prompt)
	}
	other := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-dup", InstanceID: "i-dup-1"})
	if !strings.Contains(other.Prompt.Preview, "Draft quickly.") || strings.Contains(other.Prompt.Preview, "Edit carefully.") {
		t.Fatalf("instance 1 preview mixed in instance 2: %q", other.Prompt.Preview)
	}

	// Ambiguous: base only, no guidance chosen for the reader.
	amb := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-dup"})
	if amb.Prompt.State != OwnershipPromptNeedsInstance || amb.Prompt.Preview != "" || amb.Prompt.Base != "Dup base prompt." {
		t.Fatalf("ambiguous prompt = %+v", amb.Prompt)
	}

	// No copy: the library base, read directly, with the instance's guidance.
	fb := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-tracked"})
	if fb.Prompt.BaseSource != OwnershipSourceWorkspaceCopy || !strings.HasPrefix(fb.Prompt.Preview, "Library prompt.\n\n---\n") {
		t.Fatalf("tracked copy prompt = %+v", fb.Prompt)
	}
	direct := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-fallback"})
	if direct.Prompt.BaseSource != OwnershipSourceLibraryFallback || direct.Prompt.Preview != "Library prompt." {
		t.Fatalf("fallback prompt = %+v", direct.Prompt)
	}

	// Unverified definition: no base at all.
	broken := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-broken"})
	if broken.Prompt.State != OwnershipPromptUnavailable || broken.Prompt.Base != "" || broken.Prompt.Preview != "" {
		t.Fatalf("degraded prompt = %+v", broken.Prompt)
	}
}

func TestSettingsOwnershipPromptHandlesEmptyBasesAndVariables(t *testing.T) {
	f := newOwnershipFixture(t)
	mem := f.store.Store
	empty := writerDefinition("m", "")
	if err := mem.SaveWorkspaceAgent("ws-custom", "Writer", empty); err != nil {
		t.Fatal(err)
	}
	if err := mem.Update("ws-custom", func(ws *Workspace) error {
		ws.AgentInstances[0].CustomInstructions = "<script>alert(1)</script> only here"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out := project(t, f, SettingsOwnershipRequest{Agent: "Writer", WorkspaceID: "ws-custom"})
	if out.Prompt.Base != "" || !strings.HasPrefix(out.Prompt.Preview, "Workspace-specific guidance") {
		t.Fatalf("empty base prompt = %+v", out.Prompt)
	}
	// Saved text is preserved verbatim; the page renders it as text.
	if !strings.Contains(out.Prompt.Preview, "<script>alert(1)</script> only here") {
		t.Fatalf("saved text altered: %q", out.Prompt.Preview)
	}
	f.library["Writer"].Definition.Settings.SystemPrompt = "You help in {{workspace.name}}."
	lib := project(t, f, SettingsOwnershipRequest{Agent: "Writer"})
	if !lib.Prompt.HasVariables || lib.Prompt.Preview != "You help in {{workspace.name}}." {
		t.Fatalf("variables = %+v", lib.Prompt)
	}
}

func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path) // #nosec G304 -- test temp dir
		out[path] = info.ModTime().String() + "|" + string(data)
		return nil
	})
	return out
}

// memLibrary is a minimal store.Store for the parity test.
type memLibrary map[string]*agent.Agent

func (m memLibrary) ListAgents() []string { return nil }
func (m memLibrary) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := m[name]
	return ag, ok
}
func (m memLibrary) CreateAgent(string, *store.CreateAgentConfig) error { return nil }
func (m memLibrary) SetAgent(string, *agent.Agent) error                { return nil }
func (m memLibrary) UpdateAgent(string, func(*agent.Agent) error) error { return nil }
func (m memLibrary) DeleteAgent(string) error                           { return nil }
func (m memLibrary) ClearAgents() error                                 { return nil }
func (m memLibrary) Save() error                                        { return nil }
