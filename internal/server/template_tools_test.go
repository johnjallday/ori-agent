package server

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// writePluginFixture writes an isolated installed.json under dir so the applier
// reads a temporary plugin registry, never the developer's ignored plugins/.
func writePluginFixture(t *testing.T, dir, installedJSON string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "installed.json"), []byte(installedJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

// applierBuilder wires a minimal ServerBuilder with an in-memory workspace store
// and a plugin handler pointed at an isolated fixture dir.
func applierBuilder(t *testing.T, pluginsDir string) (*ServerBuilder, workspace.Store) {
	t.Helper()
	b := &ServerBuilder{}
	store := workspace.NewInMemoryStore()
	b.workspaceStore = store
	b.pluginHandler = pluginhttp.NewHandler(nil, nil, t.TempDir(), pluginsDir)
	return b, store
}

func TestTemplateToolApplier_ConfiguredStoreAttachesEnabledPlugin(t *testing.T) {
	pluginsDir := t.TempDir()
	writePluginFixture(t, pluginsDir, `[
	  {"name":"reaper-plugin","enabled":true,"skills":["reaper-session-setup","reaper-web-remote"]}
	]`)
	b, store := applierBuilder(t, pluginsDir)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	apply := makeTemplateToolApplier(b)
	applied, missing := apply(ws.ID, projecttemplates.ToolDefaults{Plugins: []string{"reaper-plugin"}})

	sort.Strings(applied)
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied components, got %v", applied)
	}
	if len(missing) != 0 {
		t.Fatalf("expected nothing missing, got %v", missing)
	}
	got, _ := store.Get(ws.ID)
	if len(got.GetSkillBindings()) != 2 {
		t.Fatalf("expected 2 workspace skill bindings, got %d", len(got.GetSkillBindings()))
	}
}

func TestTemplateToolApplier_DisabledPluginReportedNotEnabled(t *testing.T) {
	pluginsDir := t.TempDir()
	writePluginFixture(t, pluginsDir, `[
	  {"name":"reaper-plugin","enabled":false,"skills":["reaper-session-setup"]}
	]`)
	b, store := applierBuilder(t, pluginsDir)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	apply := makeTemplateToolApplier(b)
	applied, missing := apply(ws.ID, projecttemplates.ToolDefaults{Plugins: []string{"reaper-plugin"}})

	if len(applied) != 0 {
		t.Fatalf("disabled plugin must not attach components, applied=%v", applied)
	}
	if len(missing) != 1 {
		t.Fatalf("expected one missing/disabled entry, got %v", missing)
	}
	got, _ := store.Get(ws.ID)
	if len(got.GetSkillBindings()) != 0 {
		t.Fatalf("disabled plugin must leave no bindings, got %d", len(got.GetSkillBindings()))
	}
}

func TestTemplateToolApplier_MissingPluginReported(t *testing.T) {
	pluginsDir := t.TempDir()
	writePluginFixture(t, pluginsDir, `[]`)
	b, store := applierBuilder(t, pluginsDir)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	apply := makeTemplateToolApplier(b)
	applied, missing := apply(ws.ID, projecttemplates.ToolDefaults{Plugins: []string{"reaper-plugin"}})

	if len(applied) != 0 {
		t.Fatalf("missing plugin must apply nothing, got %v", applied)
	}
	if len(missing) != 1 || missing[0] != "plugin:reaper-plugin" {
		t.Fatalf("expected missing plugin:reaper-plugin, got %v", missing)
	}
}

func TestTemplateToolApplier_IdempotentReapply(t *testing.T) {
	pluginsDir := t.TempDir()
	writePluginFixture(t, pluginsDir, `[
	  {"name":"reaper-plugin","enabled":true,"skills":["reaper-session-setup","reaper-web-remote"]}
	]`)
	b, store := applierBuilder(t, pluginsDir)
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	apply := makeTemplateToolApplier(b)
	tools := projecttemplates.ToolDefaults{
		Skills:  []string{"note-taking"},
		Plugins: []string{"reaper-plugin"},
	}
	apply(ws.ID, tools)
	applied2, _ := apply(ws.ID, tools)

	if len(applied2) != 0 {
		t.Fatalf("second application should be a no-op, got %v", applied2)
	}
	got, _ := store.Get(ws.ID)
	// note-taking + 2 plugin skills, no duplicates.
	if len(got.GetSkillBindings()) != 3 {
		t.Fatalf("expected 3 bindings after idempotent re-apply, got %d", len(got.GetSkillBindings()))
	}
}
