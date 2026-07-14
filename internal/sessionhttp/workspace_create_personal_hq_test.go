package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// TestCreateWorkspaceFromPersonalHQTemplate proves the renamed Personal HQ
// built-in (personal-ops folder) still goes through the same server-side
// workspace-creation path as any other template: entry-agent selection,
// starter-task seeding, and template provenance persistence all work
// identically to before the PRD FR119-FR122 rename (task 2.5: no duplicate
// constructor for HQ creation).
func TestCreateWorkspaceFromPersonalHQTemplate(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	libDir := handler.templatesRootResolver()
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	w, resp := postCreateWorkspace(t, handler, `{"name":"My HQ","template_id":"personal-ops"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if got, _ := resp["seeded_starter_tasks"].(float64); got != 3 {
		t.Fatalf("seeded_starter_tasks = %v, want 3", resp["seeded_starter_tasks"])
	}

	if _, ok := handler.agentStore.GetAgent("Personal Chief of Staff"); !ok {
		t.Fatal("expected Personal Chief of Staff seeded into the agent store")
	}

	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatal("expected folder in response")
	}
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatal("missing workspace id in response")
	}

	sessWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got := currentWorkspaceEntryAgentName(sessWS); got != "Personal Chief of Staff" {
		t.Fatalf("entry agent = %q, want Personal Chief of Staff", got)
	}

	tasks := workspaceTasksFromStore(t, handler, wsID)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 seeded tasks, got %d: %+v", len(tasks), tasks)
	}

	diskWS, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get: %v", err)
	}
	prov := diskWS.GetTemplateProvenance()
	if prov == nil {
		t.Fatal("expected template provenance to be recorded")
	}
	if prov.TemplateID != "personal-ops" {
		t.Fatalf("provenance TemplateID = %q, want personal-ops", prov.TemplateID)
	}
	if prov.TemplateName != "Personal HQ" {
		t.Fatalf("provenance TemplateName = %q, want Personal HQ", prov.TemplateName)
	}
	if prov.Version < 3 {
		t.Fatalf("provenance Version = %d, want at least 3", prov.Version)
	}

	// The workspace's own display name is whatever the user chose at
	// creation ("My HQ" here) — the template's display name never leaks
	// onto the workspace record.
	if sessWS.Name != "My HQ" {
		t.Fatalf("workspace name = %q, want My HQ (user-chosen, independent of template display name)", sessWS.Name)
	}
}

// TestCreateFromTemplateReusesTheProductionCreationPath proves the
// Personal HQ setup coordinator's workspace-creation hook produces an
// identical result to a normal POST /api/workspaces call — same entry
// agent, same starter-task seeding, same provenance — rather than a
// second, divergent constructor (PRD FR128 / task 4.6/2.5).
func TestCreateFromTemplateReusesTheProductionCreationPath(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	libDir := handler.templatesRootResolver()
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	wsID, err := handler.CreateFromTemplate(context.Background(), "My HQ", "personal-ops")
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if wsID == "" {
		t.Fatal("expected a non-empty workspace id")
	}

	sessWS, err := handler.store.GetWorkspace(context.Background(), wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if sessWS.Name != "My HQ" {
		t.Fatalf("name = %q, want My HQ", sessWS.Name)
	}
	if got := currentWorkspaceEntryAgentName(sessWS); got != "Personal Chief of Staff" {
		t.Fatalf("entry agent = %q, want Personal Chief of Staff", got)
	}
	if tasks := workspaceTasksFromStore(t, handler, wsID); len(tasks) != 3 {
		t.Fatalf("expected 3 seeded starter tasks, got %d", len(tasks))
	}
}

// TestCreateFromTemplateWithUnknownTemplateStillCreatesAWorkspace matches
// the existing POST /api/workspaces contract: an unresolvable template_id is
// non-fatal by design (the workspace is still created, with a
// generic auto-created manager entry agent, rather than failing outright).
// CreateFromTemplate must not layer a new hard failure on top of that.
func TestCreateFromTemplateWithUnknownTemplateStillCreatesAWorkspace(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()

	wsID, err := handler.CreateFromTemplate(context.Background(), "My HQ", "does-not-exist")
	if err != nil {
		t.Fatalf("CreateFromTemplate should not fail on an unresolvable template id, got %v", err)
	}
	if wsID == "" {
		t.Fatal("expected a workspace id even when the template did not resolve")
	}
}

// TestLibraryTemplateRefreshNeverRewritesAlreadyCreatedWorkspace covers PRD
// FR127/FR128 (task 2.4/2.7 migration safety): bumping the shipped Personal
// HQ manifest's builtin_version (as a later release will) must refresh only
// the *library's* template.json. A workspace already created from an older
// manifest keeps its own name and its provenance snapshot exactly as
// recorded at creation time — nothing about it is migrated, renamed, or
// rewritten in place.
func TestLibraryTemplateRefreshNeverRewritesAlreadyCreatedWorkspace(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	libDir := handler.templatesRootResolver()
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	_, resp := postCreateWorkspace(t, handler, `{"name":"My HQ","template_id":"personal-ops"}`)
	folder, _ := resp["folder"].(map[string]any)
	wsID, _ := folder["id"].(string)
	if wsID == "" {
		t.Fatalf("missing workspace id in response: %v", resp)
	}
	before, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get: %v", err)
	}
	beforeProv := before.GetTemplateProvenance()
	if beforeProv == nil {
		t.Fatal("expected provenance recorded on create")
	}

	// Simulate a future shipped release bumping the Personal HQ manifest
	// (mirrors what refreshBuiltinManifest does to the library copy only).
	manifestPath := filepath.Join(libDir, "personal-ops", "template.json")
	raw, err := os.ReadFile(manifestPath) // #nosec G304 -- test-owned temp library path
	if err != nil {
		t.Fatalf("read library manifest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal library manifest: %v", err)
	}
	m["name"] = "Personal HQ (future copy)"
	m["builtin_version"] = 99
	bumped, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal bumped manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, bumped, 0o640); err != nil { // #nosec G306 -- test-owned temp library path
		t.Fatalf("write bumped manifest: %v", err)
	}

	// The already-created workspace is a separate persisted entity: its
	// name and its frozen provenance snapshot must be unaffected by the
	// library manifest changing underneath it.
	after, err := handler.workspaceStore.Get(wsID)
	if err != nil {
		t.Fatalf("workspaceStore.Get after refresh: %v", err)
	}
	if after.Name != "My HQ" {
		t.Fatalf("workspace name changed after library refresh: got %q, want My HQ", after.Name)
	}
	afterProv := after.GetTemplateProvenance()
	if afterProv == nil {
		t.Fatal("provenance must survive a library manifest refresh")
	}
	if afterProv.TemplateName != beforeProv.TemplateName || afterProv.Version != beforeProv.Version {
		t.Fatalf("provenance snapshot changed after library refresh: before=%+v after=%+v", beforeProv, afterProv)
	}
	if afterProv.TemplateName != "Personal HQ" || afterProv.Version < 3 {
		t.Fatalf("provenance snapshot regressed: %+v", afterProv)
	}
}
