package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// personalHQImportHarness wires the real Personal HQ service against the same
// in-memory database and folder store the handler uses, so import tests
// exercise the production designation path end to end rather than a fake.
type personalHQImportHarness struct {
	handler   *Handler
	fileStore *agentworkspace.FileStore
	service   *personalhq.Service
}

func newPersonalHQImportHarness(t *testing.T) (*personalHQImportHarness, func()) {
	t.Helper()

	handler, cleanup := createTestHandler(t)
	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		cleanup()
		t.Fatalf("NewFileStore: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	// Same wiring the server performs, in the same order: the service reads
	// workspaces through the session store, projects designations onto
	// workspace.json through the handler, and resolves that canonical
	// projection through the folder store.
	service := personalhq.NewService(userprofile.NewSQLiteStore(handler.store.DB()), handler.store)
	service.SetDesignationSyncer(handler)
	service.SetDesignationReader(fileStore)
	handler.SetPersonalHQDesignator(service)

	return &personalHQImportHarness{handler: handler, fileStore: fileStore, service: service},
		func() {
			_ = fileStore.Close()
			cleanup()
		}
}

// status resolves the authoritative Personal HQ record for the local user.
func (h *personalHQImportHarness) status(t *testing.T) *personalhq.Status {
	t.Helper()
	status, err := h.service.Status(context.Background(), userprofile.LocalUserID)
	if err != nil {
		t.Fatalf("personal hq status: %v", err)
	}
	return status
}

// importFolder drives the ordinary Import Folder endpoint — never the explicit
// Import HQ action — and returns the decoded response.
func (h *personalHQImportHarness) importFolder(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"path": path})
	if err != nil {
		t.Fatalf("encode import payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handler.HandleWorkspaces(w, req)

	resp := map[string]any{}
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode import response (status %d): %v", w.Code, err)
		}
	}
	return w.Code, resp
}

// writeExportedWorkspaceFixture writes a disposable exported workspace root,
// with the secure permissions this repo's gosec gate expects.
func writeExportedWorkspaceFixture(t *testing.T, dir string, ws *agentworkspace.Workspace) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create exported workspace fixture %s: %v", dir, err)
	}
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("encode exported workspace %s: %v", ws.ID, err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentworkspace.WorkspaceConfigFile), data, 0o600); err != nil {
		t.Fatalf("write exported workspace.json for %s: %v", ws.ID, err)
	}
	return dir
}

// exportedWorkspaceFixture builds an ordinary exported workspace carrying the
// given designation verbatim, so tests can supply marked, unmarked, and
// unsupported values without inferring identity from the name or kind.
func exportedWorkspaceFixture(id, name, designation string) *agentworkspace.Workspace {
	now := time.Now()
	return &agentworkspace.Workspace{
		ID:          id,
		Name:        name,
		FolderSlug:  agentworkspace.Slugify(name),
		Designation: designation,
		Status:      agentworkspace.StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestHandleWorkspaceImportPreservesRuntimeContractSnapshotAndState(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	fixture := exportedWorkspaceFixture("ws-runtime-import", "Runtime Import", "")
	fixture.SetTemplateProvenance(&agentworkspace.TemplateProvenance{
		TemplateID: "runtime-template",
		RuntimeRequirements: &agentworkspace.RuntimeRequirementsContract{
			SchemaVersion: agentworkspace.RuntimeRequirementsSchemaVersion,
			OperatingModes: []agentworkspace.RuntimeOperatingMode{
				{ID: "limited", Label: "Limited", Description: "Use files."},
				{ID: "assisted", Label: "Assisted", Description: "Use live control.", Requires: []string{"runtime"}},
			},
			Requirements: []agentworkspace.RuntimeRequirement{{Key: "runtime", Label: "Runtime", Description: "Configure it.", Adapter: "reaper_live_control"}},
		},
	})
	fixture.SetRuntimeState(&agentworkspace.WorkspaceRuntimeState{SelectedModeID: "limited"})
	exportRoot := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "export"), fixture)

	payload, _ := json.Marshal(map[string]any{"path": exportRoot})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	imported, err := fileStore.Get(fixture.ID)
	if err != nil {
		t.Fatalf("load imported workspace: %v", err)
	}
	contract := imported.RuntimeRequirementsSnapshot()
	if contract == nil || !contract.StructurallyValid() || contract.OperatingModes[1].Requires[0] != "runtime" {
		t.Fatalf("portable runtime contract was lost on import: %+v", contract)
	}
	state := imported.GetRuntimeState()
	if state == nil || state.SelectedModeID != "limited" {
		t.Fatalf("portable selected mode was lost on import: %+v", state)
	}
}

// diskDesignation reads the designation straight out of the local copied
// workspace.json, the canonical store for the projection.
func diskDesignation(t *testing.T, fileStore *agentworkspace.FileStore, workspaceID string) string {
	t.Helper()
	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath(%s): %v", workspaceID, err)
	}
	raw, err := os.ReadFile(filepath.Join(folderPath, agentworkspace.WorkspaceConfigFile)) // #nosec G304 -- test-owned temporary workspace folder
	if err != nil {
		t.Fatalf("read local workspace.json for %s: %v", workspaceID, err)
	}
	ws, err := agentworkspace.FromJSON(raw)
	if err != nil {
		t.Fatalf("decode local workspace.json for %s: %v", workspaceID, err)
	}
	return strings.TrimSpace(ws.Designation)
}

// listedPersonalHQIDs returns every workspace the ordinary list read reports as
// the Personal HQ, so tests can prove a second one never appears.
func listedPersonalHQIDs(t *testing.T, handler *Handler) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 listing workspaces, got %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		Workspaces []session.Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode workspace list: %v", err)
	}

	var designated []string
	var walk func(items []session.Workspace)
	walk = func(items []session.Workspace) {
		for i := range items {
			if items[i].IsPersonalHQ() {
				designated = append(designated, items[i].ID)
			}
			walk(items[i].Children)
		}
	}
	walk(payload.Workspaces)
	return designated
}

// hydratedDesignation reads the workspace back through the ordinary workspace
// detail endpoint, proving a stale SQLite-only object cannot hide the result.
func hydratedDesignation(t *testing.T, handler *Handler, workspaceID string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID, nil)
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 reading workspace %s, got %d: %s", workspaceID, w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode workspace %s: %v", workspaceID, err)
	}
	designation, _ := payload["designation"].(string)
	return designation
}

// recordingDesignator captures every automatic Personal HQ designation attempt
// the import path makes, so tests can assert both that a marked export
// designates and — just as importantly — that unmarked imports never call in.
type recordingDesignator struct {
	calls []designateCall
	err   error
}

type designateCall struct {
	userID      string
	workspaceID string
}

func (d *recordingDesignator) Designate(_ context.Context, userID, workspaceID string) (*personalhq.Status, error) {
	d.calls = append(d.calls, designateCall{userID: userID, workspaceID: workspaceID})
	if d.err != nil {
		return nil, d.err
	}
	return &personalhq.Status{UserID: userID, WorkspaceID: workspaceID, Valid: true}, nil
}

func TestPersonalHQDesignatorWiringIsNilSafeAndObservable(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	if handler.PersonalHQDesignatorWired() {
		t.Fatalf("expected a freshly constructed handler to report no Personal HQ designator")
	}

	handler.SetPersonalHQDesignator(&recordingDesignator{})
	if !handler.PersonalHQDesignatorWired() {
		t.Fatalf("expected PersonalHQDesignatorWired to report the injected designator")
	}
}

func TestHandleWorkspaceImportRestoresExportedPersonalHQDesignation(t *testing.T) {
	harness, cleanup := newPersonalHQImportHarness(t)
	defer cleanup()

	if status := harness.status(t); status.HasDesignation() || status.Valid {
		t.Fatalf("expected no personal hq before import, got %#v", status)
	}

	fixture := exportedWorkspaceFixture("ws-imported-hq", "Command Center", string(session.WorkspaceDesignationPersonalHQ))
	exportRoot := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "command-center-export"), fixture)

	code, resp := harness.importFolder(t, exportRoot)
	if code != http.StatusCreated {
		t.Fatalf("expected 201 for marked exported workspace import, got %d: %#v", code, resp)
	}
	if restored, _ := resp["restored_from_config"].(bool); !restored {
		t.Fatalf("expected restored_from_config=true, got %#v", resp["restored_from_config"])
	}
	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder object in response, got %#v", resp)
	}
	if got := folder["id"]; got != fixture.ID {
		t.Fatalf("expected restored workspace id %q, got %#v", fixture.ID, got)
	}
	if got, _ := folder["designation"].(string); got != string(session.WorkspaceDesignationPersonalHQ) {
		t.Fatalf("expected import response designation %q, got %#v", session.WorkspaceDesignationPersonalHQ, folder["designation"])
	}

	// The authoritative record — a workspace.json marker alone is not one.
	status := harness.status(t)
	if !status.Valid || status.WorkspaceID != fixture.ID {
		t.Fatalf("expected imported workspace %q to be the valid personal hq, got %#v", fixture.ID, status)
	}

	// The canonical folder projection, written through the existing syncer.
	if got := diskDesignation(t, harness.fileStore, fixture.ID); got != string(session.WorkspaceDesignationPersonalHQ) {
		t.Fatalf("expected local workspace.json designation %q, got %q", session.WorkspaceDesignationPersonalHQ, got)
	}
	if got := hydratedDesignation(t, harness.handler, fixture.ID); got != string(session.WorkspaceDesignationPersonalHQ) {
		t.Fatalf("expected hydrated workspace read to report %q, got %q", session.WorkspaceDesignationPersonalHQ, got)
	}
}

func TestHandleWorkspaceImportPreservesExistingPersonalHQOnConflict(t *testing.T) {
	harness, cleanup := newPersonalHQImportHarness(t)
	defer cleanup()

	// The current HQ, established the ordinary way: import a marked export
	// while nothing is designated.
	current := exportedWorkspaceFixture("ws-current-hq", "Current HQ", string(session.WorkspaceDesignationPersonalHQ))
	currentRoot := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "current-hq-export"), current)
	if code, resp := harness.importFolder(t, currentRoot); code != http.StatusCreated {
		t.Fatalf("expected 201 establishing the current hq, got %d: %#v", code, resp)
	}
	if status := harness.status(t); !status.Valid || status.WorkspaceID != current.ID {
		t.Fatalf("expected %q to be the current hq before the conflict, got %#v", current.ID, harness.status(t))
	}

	// A second export that also claims to be an HQ.
	incoming := exportedWorkspaceFixture("ws-incoming-hq", "Incoming HQ", string(session.WorkspaceDesignationPersonalHQ))
	incomingRoot := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "incoming-hq-export"), incoming)

	code, resp := harness.importFolder(t, incomingRoot)
	if code != http.StatusCreated {
		t.Fatalf("expected the conflicting import to still succeed with 201, got %d: %#v", code, resp)
	}
	if restored, _ := resp["restored_from_config"].(bool); !restored {
		t.Fatalf("expected restored_from_config=true on conflict, got %#v", resp["restored_from_config"])
	}
	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder object in response, got %#v", resp)
	}
	if got := folder["id"]; got != incoming.ID {
		t.Fatalf("expected imported workspace id %q, got %#v", incoming.ID, got)
	}
	if got, _ := folder["designation"].(string); got != "" {
		t.Fatalf("expected the conflicting import to respond undesignated, got %q", got)
	}

	// The authoritative designation never moved.
	status := harness.status(t)
	if !status.Valid || status.WorkspaceID != current.ID {
		t.Fatalf("expected the original hq %q to survive the conflict, got %#v", current.ID, status)
	}

	// Exactly one workspace carries the projection, on disk and through reads.
	if got := diskDesignation(t, harness.fileStore, current.ID); got != string(session.WorkspaceDesignationPersonalHQ) {
		t.Fatalf("expected the original hq folder to stay marked, got %q", got)
	}
	if got := diskDesignation(t, harness.fileStore, incoming.ID); got != "" {
		t.Fatalf("expected the imported folder to stay unmarked locally, got %q", got)
	}
	if got := hydratedDesignation(t, harness.handler, incoming.ID); got != "" {
		t.Fatalf("expected hydrated read of the imported workspace to be undesignated, got %q", got)
	}
	if designated := listedPersonalHQIDs(t, harness.handler); len(designated) != 1 || designated[0] != current.ID {
		t.Fatalf("expected exactly one designated workspace (%q) in list reads, got %#v", current.ID, designated)
	}

	// The rest of the restore is untouched by the refused designation.
	restored, err := harness.handler.store.GetWorkspace(context.Background(), incoming.ID)
	if err != nil {
		t.Fatalf("expected the imported workspace to be stored: %v", err)
	}
	if restored.Name != incoming.Name {
		t.Fatalf("expected imported workspace name %q, got %q", incoming.Name, restored.Name)
	}
	localFolder, err := harness.fileStore.GetFolderPath(incoming.ID)
	if err != nil {
		t.Fatalf("GetFolderPath(%s): %v", incoming.ID, err)
	}
	refs, err := decodeDirectoryReferences(restored.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("decode directory references: %v", err)
	}
	if len(refs) != 1 || cleanWorkspaceSyncPath(refs[0].Path) != cleanWorkspaceSyncPath(localFolder) {
		t.Fatalf("expected the imported workspace's directory reference rebased onto %q, got %#v", localFolder, refs)
	}

	// AR7: the imported workspace stays eligible, so the existing explicit
	// "use as my HQ" action — the only caller of Replace — still works.
	if _, err := harness.service.Replace(context.Background(), userprofile.LocalUserID, incoming.ID); err != nil {
		t.Fatalf("expected the explicit replace action to accept the imported workspace: %v", err)
	}
	if status := harness.status(t); !status.Valid || status.WorkspaceID != incoming.ID {
		t.Fatalf("expected the explicit replacement to move the hq to %q, got %#v", incoming.ID, status)
	}
	if got := diskDesignation(t, harness.fileStore, incoming.ID); got != string(session.WorkspaceDesignationPersonalHQ) {
		t.Fatalf("expected the explicitly designated folder to be marked, got %q", got)
	}
	if got := diskDesignation(t, harness.fileStore, current.ID); got != "" {
		t.Fatalf("expected the previous hq folder to be cleared by the explicit replacement, got %q", got)
	}
}

func TestHandleWorkspaceImportDesignatesOnlyOnTheNormalizedMarker(t *testing.T) {
	hqTemplate := &agentworkspace.TemplateProvenance{
		TemplateID:   "personal-hq",
		TemplateName: "Personal HQ",
		Builtin:      true,
	}

	cases := []struct {
		name          string
		designation   string
		workspaceName string
		provenance    *agentworkspace.TemplateProvenance
		wantDesignate bool
	}{
		{name: "unmarked export", designation: "", workspaceName: "Research"},
		{name: "whitespace only", designation: "   ", workspaceName: "Research"},
		{name: "unsupported value", designation: "hq", workspaceName: "Research"},
		{name: "padded unsupported value", designation: "  headquarters  ", workspaceName: "Research"},
		{name: "hq-sounding name", designation: "", workspaceName: "Personal HQ"},
		{name: "personal hq template provenance", designation: "", workspaceName: "Research", provenance: hqTemplate},
		{name: "padded marker", designation: "  personal_hq  ", workspaceName: "Research", wantDesignate: true},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, cleanup := createTestHandler(t)
			defer cleanup()

			fileStore, err := agentworkspace.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileStore: %v", err)
			}
			defer func() { _ = fileStore.Close() }()
			handler.SetWorkspaceStore(fileStore)

			designator := &recordingDesignator{}
			handler.SetPersonalHQDesignator(designator)

			workspaceID := fmt.Sprintf("ws-designation-case-%d", i)
			fixture := exportedWorkspaceFixture(workspaceID, tc.workspaceName, tc.designation)
			fixture.TemplateProvenance = tc.provenance
			exportRoot := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "export"), fixture)

			payload, _ := json.Marshal(map[string]any{"path": exportRoot})
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.HandleWorkspaces(w, req)

			if w.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
			}

			switch {
			case tc.wantDesignate && len(designator.calls) != 1:
				t.Fatalf("expected exactly one designation attempt, got %#v", designator.calls)
			case !tc.wantDesignate && len(designator.calls) != 0:
				t.Fatalf("expected no designation attempt for designation %q, got %#v", tc.designation, designator.calls)
			}
			// The recording designator projects nothing, so disk shows exactly
			// what import persisted: the marker stripped pending designation on
			// the marked path, and every other value carried through untouched.
			wantDisk := strings.TrimSpace(tc.designation)
			if tc.wantDesignate {
				if got := designator.calls[0]; got.userID != userprofile.LocalUserID || got.workspaceID != workspaceID {
					t.Fatalf("expected Designate(%q, %q), got %#v", userprofile.LocalUserID, workspaceID, got)
				}
				wantDisk = ""
			}
			if got := diskDesignation(t, fileStore, workspaceID); got != wantDisk {
				t.Fatalf("expected local workspace.json designation %q, got %q", wantDisk, got)
			}
		})
	}
}

func TestHandleWorkspaceImportPlainFolderNeverDesignates(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	designator := &recordingDesignator{}
	handler.SetPersonalHQDesignator(designator)

	importDir := filepath.Join(t.TempDir(), "plain-folder")
	if err := os.MkdirAll(importDir, 0o750); err != nil {
		t.Fatalf("create plain folder: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{"path": importDir, "entry_point": "workspace_hub_import"})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for plain folder import, got %d: %s", w.Code, w.Body.String())
	}
	if len(designator.calls) != 0 {
		t.Fatalf("expected a plain folder import to never designate, got %#v", designator.calls)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["restored_from_config"]; ok {
		t.Fatalf("expected the plain-folder response shape to be unchanged, got %#v", resp)
	}
	if _, ok := resp["directory"].(map[string]any); !ok {
		t.Fatalf("expected the plain-folder response to still carry the directory object, got %#v", resp)
	}
	folder, _ := resp["folder"].(map[string]any)
	workspaceID, _ := folder["id"].(string)
	if workspaceID == "" {
		t.Fatalf("expected workspace id in response, got %#v", resp)
	}
	if got, _ := folder["designation"].(string); got != "" {
		t.Fatalf("expected a plain-folder import to stay undesignated, got %q", got)
	}

	stored, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("load imported workspace: %v", err)
	}
	normalizedPath, err := normalizeImportPath(importDir)
	if err != nil {
		t.Fatalf("normalize import path: %v", err)
	}
	refs, err := decodeDirectoryReferences(stored.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("decode directory references: %v", err)
	}
	if len(refs) != 1 || filepath.Clean(refs[0].Path) != filepath.Clean(normalizedPath) {
		t.Fatalf("expected the scaffolded directory reference for %q, got %#v", normalizedPath, refs)
	}
	bindings, err := decodeWorkspaceMCPBindings(stored.MCPBindingsJSON)
	if err != nil {
		t.Fatalf("decode mcp bindings: %v", err)
	}
	if len(bindings) != 1 || !workspaceBindingHasRoot(bindings[0].Config, normalizedPath) {
		t.Fatalf("expected the workspace-files binding for %q, got %#v", normalizedPath, bindings)
	}
	if _, ok := stored.SharedData["folder_import"]; !ok {
		t.Fatalf("expected folder_import metadata to survive, got %#v", stored.SharedData)
	}
}

func TestHandleWorkspaceImportMarkedExportWithoutDesignatorDoesNotLie(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	if handler.PersonalHQDesignatorWired() {
		t.Fatalf("this test requires the degraded, unwired handler")
	}

	fixture := exportedWorkspaceFixture("ws-degraded-hq", "Degraded HQ", string(session.WorkspaceDesignationPersonalHQ))
	exportRoot := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "degraded-export"), fixture)

	payload, _ := json.Marshal(map[string]any{"path": exportRoot})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Nil dependency: importing must still succeed rather than panic, and the
	// copied marker must not survive as a projection no record backs.
	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 without a designator, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	folder, _ := resp["folder"].(map[string]any)
	if got, _ := folder["designation"].(string); got != "" {
		t.Fatalf("expected no designation without a designator, got %q", got)
	}
	if got := diskDesignation(t, fileStore, fixture.ID); got != "" {
		t.Fatalf("expected the imported folder projection to be empty without a designator, got %q", got)
	}
}

func TestHandleWorkspaceImportSkipsDesignationWhenRestoreFails(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	designator := &recordingDesignator{}
	handler.SetPersonalHQDesignator(designator)

	// A config the restore cannot decode: the import fails before anything is
	// durable, so designation must never be attempted.
	exportRoot := filepath.Join(t.TempDir(), "broken-export")
	if err := os.MkdirAll(exportRoot, 0o750); err != nil {
		t.Fatalf("create broken export fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exportRoot, agentworkspace.WorkspaceConfigFile), []byte(`{"id": "ws-broken", "designation":`), 0o600); err != nil {
		t.Fatalf("write broken workspace.json: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{"path": exportRoot})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for an undecodable exported workspace, got %d: %s", w.Code, w.Body.String())
	}
	if len(designator.calls) != 0 {
		t.Fatalf("expected no designation attempt after a failed restore, got %#v", designator.calls)
	}
}

func TestHandleWorkspaceImportCreatesWorkspaceWithDirectoryReference(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	importDir := filepath.Join(t.TempDir(), "import-target")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("failed to create temp import directory: %v", err)
	}

	body := map[string]any{
		"path":        importDir,
		"entry_point": "create_modal",
		"workspace_bootstrap": map[string]any{
			"goal":         "Build the Q2 presentation",
			"systems":      "Keynote, Finder",
			"capabilities": "Create slides and organize imported assets",
			"context":      "Source files live in the imported folder",
		},
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for import, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder object in response")
	}

	workspaceID, ok := folder["id"].(string)
	if !ok || workspaceID == "" {
		t.Fatalf("expected workspace id in response")
	}

	ws, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("failed to fetch created workspace: %v", err)
	}

	refs, err := decodeDirectoryReferences(ws.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("failed to decode directory references: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 directory reference, got %d", len(refs))
	}
	expectedPath, err := normalizeImportPath(importDir)
	if err != nil {
		t.Fatalf("failed to normalize expected path: %v", err)
	}
	if filepath.Clean(refs[0].Path) != filepath.Clean(expectedPath) {
		t.Fatalf("expected directory path %q, got %q", expectedPath, refs[0].Path)
	}

	if ws.SharedData == nil {
		t.Fatalf("expected shared_data for imported workspace")
	}
	if _, ok := ws.SharedData["folder_import"]; !ok {
		t.Fatalf("expected folder_import metadata in shared_data")
	}
	bootstrapRaw, ok := ws.SharedData["workspace_bootstrap"]
	if !ok {
		t.Fatalf("expected workspace_bootstrap metadata in shared_data")
	}
	bootstrapMap, ok := bootstrapRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected workspace_bootstrap to be an object, got %T", bootstrapRaw)
	}
	if bootstrapMap["goal"] != "Build the Q2 presentation" {
		t.Fatalf("expected workspace_bootstrap.goal to persist, got %#v", bootstrapMap["goal"])
	}
	if bootstrapMap["systems"] != "Keynote, Finder" {
		t.Fatalf("expected workspace_bootstrap.systems to persist, got %#v", bootstrapMap["systems"])
	}
}

func TestHandleWorkspaceImportScaffoldsPlainFolderAsWorkspace(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	storeDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	importDir := filepath.Join(t.TempDir(), "plain-folder")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("failed to create import directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(importDir, "source.txt"), []byte("keep existing content"), 0644); err != nil {
		t.Fatalf("failed to seed import directory: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"path":        importDir,
		"entry_point": "workspace_hub_import",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for import, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder object in response")
	}
	workspaceID, _ := folder["id"].(string)
	if workspaceID == "" {
		t.Fatalf("expected workspace id in response")
	}

	normalizedPath, err := normalizeImportPath(importDir)
	if err != nil {
		t.Fatalf("normalize import path: %v", err)
	}
	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	if filepath.Clean(folderPath) != filepath.Clean(normalizedPath) {
		t.Fatalf("expected file store path %q, got %q", normalizedPath, folderPath)
	}
	for _, path := range []string{
		filepath.Join(importDir, agentworkspace.WorkspaceConfigFile),
		filepath.Join(importDir, agentworkspace.FilesDir),
		filepath.Join(importDir, agentworkspace.NotesDir),
		filepath.Join(importDir, "source.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected scaffolded import path %s: %v", path, err)
		}
	}

	sessionWS, err := handler.store.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("failed to fetch imported workspace: %v", err)
	}
	if workspacePrimaryDirectoryID(sessionWS) == "" {
		t.Fatalf("expected primary directory id to be set")
	}
	refs, err := decodeDirectoryReferences(sessionWS.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("decode directory references: %v", err)
	}
	if len(refs) != 1 || filepath.Clean(refs[0].Path) != filepath.Clean(normalizedPath) {
		t.Fatalf("expected session directory reference for %q, got %#v", normalizedPath, refs)
	}
	bindings, err := decodeWorkspaceMCPBindings(sessionWS.MCPBindingsJSON)
	if err != nil {
		t.Fatalf("decode mcp bindings: %v", err)
	}
	if len(bindings) != 1 || !workspaceBindingHasRoot(bindings[0].Config, normalizedPath) {
		t.Fatalf("expected session workspace-files binding for %q, got %#v", normalizedPath, bindings)
	}

	diskWS, err := fileStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("fileStore.Get: %v", err)
	}
	if len(diskWS.DirectoryReferences) != 1 || filepath.Clean(diskWS.DirectoryReferences[0].Path) != filepath.Clean(normalizedPath) {
		t.Fatalf("expected disk directory reference for %q, got %#v", normalizedPath, diskWS.DirectoryReferences)
	}
	if len(diskWS.MCPBindings) != 1 || !workspaceBindingHasRoot(diskWS.MCPBindings[0].Config, normalizedPath) {
		t.Fatalf("expected disk workspace-files binding for %q, got %#v", normalizedPath, diskWS.MCPBindings)
	}
}

func TestHandleWorkspaceImportRestoresExportedWorkspaceAgents(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	storeDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	exportRoot := filepath.Join(t.TempDir(), "spain-export")
	childDir := filepath.Join(exportRoot, agentworkspace.SubWorkspacesDir, "madrid")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("failed to create exported workspace folders: %v", err)
	}

	now := time.Now()
	staleRootPath := filepath.Join("/Users", "johnj", "Ori Workspaces", "spain-export")
	staleChildPath := filepath.Join(staleRootPath, agentworkspace.SubWorkspacesDir, "madrid")
	rootWorkspace := &agentworkspace.Workspace{
		ID:         "ws-imported-spain",
		Name:       "Spain",
		FolderSlug: "spain-export",
		SharedData: map[string]any{"entry_agent_name": "Trip Manager"},
		Status:     agentworkspace.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		AgentInstances: []agentworkspace.AgentInstance{
			{
				ID:             "trip-manager-1",
				Name:           "Trip Manager",
				InstanceNumber: 1,
				NodeID:         "trip-manager-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
		DirectoryReferences: []agentworkspace.DirectoryReference{
			{
				ID:          "root-dir-ref",
				WorkspaceID: "ws-imported-spain",
				Name:        "spain-export",
				Path:        staleRootPath,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
		MCPBindings: []agentworkspace.MCPBinding{
			{
				ID:         "root-files-binding",
				ServerName: "filesystem",
				Alias:      "workspace-files",
				Enabled:    true,
				Config: map[string]any{
					"roots": []string{staleRootPath},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
	rootData, err := rootWorkspace.ToJSON()
	if err != nil {
		t.Fatalf("failed to encode root workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exportRoot, agentworkspace.WorkspaceConfigFile), rootData, 0644); err != nil {
		t.Fatalf("failed to write root workspace.json: %v", err)
	}
	rootAgent := &agent.Agent{Type: agent.TypeToolCalling}
	rootAgent.Settings.Model = "imported-trip-model"
	if err := agentworkspace.WriteWorkspaceAgentToFolder(exportRoot, "Trip Manager", rootAgent); err != nil {
		t.Fatalf("failed to write root workspace agent snapshot: %v", err)
	}

	childWorkspace := &agentworkspace.Workspace{
		ID:         "ws-imported-madrid",
		Name:       "Madrid",
		FolderSlug: "madrid",
		SharedData: map[string]any{"entry_agent_name": "Madrid Planner"},
		Status:     agentworkspace.StatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
		AgentInstances: []agentworkspace.AgentInstance{
			{
				ID:             "madrid-planner-1",
				Name:           "Madrid Planner",
				InstanceNumber: 1,
				NodeID:         "madrid-planner-node-1",
				EntryPoint:     true,
				CreatedAt:      now,
			},
		},
		DirectoryReferences: []agentworkspace.DirectoryReference{
			{
				ID:          "child-dir-ref",
				WorkspaceID: "ws-imported-madrid",
				Name:        "madrid",
				Path:        staleChildPath,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
		MCPBindings: []agentworkspace.MCPBinding{
			{
				ID:         "child-files-binding",
				ServerName: "filesystem",
				Alias:      "workspace-files",
				Enabled:    true,
				Config: map[string]any{
					"roots": []string{staleChildPath},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
	childData, err := childWorkspace.ToJSON()
	if err != nil {
		t.Fatalf("failed to encode child workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(childDir, agentworkspace.WorkspaceConfigFile), childData, 0644); err != nil {
		t.Fatalf("failed to write child workspace.json: %v", err)
	}
	childAgent := &agent.Agent{Type: agent.TypeToolCalling}
	childAgent.Settings.Model = "imported-madrid-model"
	if err := agentworkspace.WriteWorkspaceAgentToFolder(childDir, "Madrid Planner", childAgent); err != nil {
		t.Fatalf("failed to write child workspace agent snapshot: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"path": exportRoot,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for exported workspace restore, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	folder, ok := resp["folder"].(map[string]any)
	if !ok {
		t.Fatalf("expected folder object in response")
	}
	if got := folder["id"]; got != rootWorkspace.ID {
		t.Fatalf("expected restored workspace id %q, got %#v", rootWorkspace.ID, got)
	}

	restoredRoot, err := handler.store.GetWorkspace(context.Background(), rootWorkspace.ID)
	if err != nil {
		t.Fatalf("failed to fetch restored root workspace: %v", err)
	}
	if len(restoredRoot.AgentInstances) != 1 {
		t.Fatalf("expected 1 root agent instance, got %d", len(restoredRoot.AgentInstances))
	}
	if restoredRoot.AgentInstances[0].Name != "Trip Manager" {
		t.Fatalf("expected root agent Trip Manager, got %#v", restoredRoot.AgentInstances[0].Name)
	}
	if got := currentWorkspaceEntryAgentName(restoredRoot); got != "Trip Manager" {
		t.Fatalf("expected restored root entry agent Trip Manager, got %q", got)
	}
	if _, ok := restoredRoot.SharedData["folder_import"]; ok {
		t.Fatalf("expected restored workspace to avoid folder_import metadata, got %#v", restoredRoot.SharedData["folder_import"])
	}
	if got, ok := handler.agentStore.GetAgent("Trip Manager"); !ok || got == nil || got.Settings.Model != "imported-trip-model" {
		t.Fatalf("expected Trip Manager snapshot restored into agent store, ok=%v agent=%#v", ok, got)
	}
	rootFolderPath, err := fileStore.GetFolderPath(rootWorkspace.ID)
	if err != nil {
		t.Fatalf("failed to get restored root folder path: %v", err)
	}
	rootRefs, err := decodeDirectoryReferences(restoredRoot.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("failed to decode restored root directory references: %v", err)
	}
	if len(rootRefs) != 1 || cleanWorkspaceSyncPath(rootRefs[0].Path) != cleanWorkspaceSyncPath(rootFolderPath) {
		t.Fatalf("expected restored root directory reference %q, got %#v", rootFolderPath, rootRefs)
	}
	rootBindings, err := decodeWorkspaceMCPBindings(restoredRoot.MCPBindingsJSON)
	if err != nil {
		t.Fatalf("failed to decode restored root mcp bindings: %v", err)
	}
	if len(rootBindings) != 1 || !workspaceBindingHasRoot(rootBindings[0].Config, rootFolderPath) {
		t.Fatalf("expected restored root workspace-files binding for %q, got %#v", rootFolderPath, rootBindings)
	}
	if workspaceBindingHasRoot(rootBindings[0].Config, staleRootPath) {
		t.Fatalf("expected stale root path %q to be removed from bindings, got %#v", staleRootPath, rootBindings)
	}
	diskRoot, err := fileStore.Get(rootWorkspace.ID)
	if err != nil {
		t.Fatalf("failed to load restored root disk workspace: %v", err)
	}
	if len(diskRoot.DirectoryReferences) != 1 || cleanWorkspaceSyncPath(diskRoot.DirectoryReferences[0].Path) != cleanWorkspaceSyncPath(rootFolderPath) {
		t.Fatalf("expected disk root directory reference %q, got %#v", rootFolderPath, diskRoot.DirectoryReferences)
	}

	restoredChild, err := handler.store.GetWorkspace(context.Background(), childWorkspace.ID)
	if err != nil {
		t.Fatalf("failed to fetch restored child workspace: %v", err)
	}
	if restoredChild.ParentID != rootWorkspace.ID {
		t.Fatalf("expected restored child parent %q, got %q", rootWorkspace.ID, restoredChild.ParentID)
	}
	if len(restoredChild.AgentInstances) != 1 {
		t.Fatalf("expected 1 child agent instance, got %d", len(restoredChild.AgentInstances))
	}
	if restoredChild.AgentInstances[0].Name != "Madrid Planner" {
		t.Fatalf("expected child agent Madrid Planner, got %#v", restoredChild.AgentInstances[0].Name)
	}
	if got := currentWorkspaceEntryAgentName(restoredChild); got != "Madrid Planner" {
		t.Fatalf("expected restored child entry agent Madrid Planner, got %q", got)
	}
	if got, ok := handler.agentStore.GetAgent("Madrid Planner"); !ok || got == nil || got.Settings.Model != "imported-madrid-model" {
		t.Fatalf("expected Madrid Planner snapshot restored into agent store, ok=%v agent=%#v", ok, got)
	}
	childFolderPath, err := fileStore.GetFolderPath(childWorkspace.ID)
	if err != nil {
		t.Fatalf("failed to get restored child folder path: %v", err)
	}
	childRefs, err := decodeDirectoryReferences(restoredChild.DirectoryReferencesJSON)
	if err != nil {
		t.Fatalf("failed to decode restored child directory references: %v", err)
	}
	if len(childRefs) != 1 || cleanWorkspaceSyncPath(childRefs[0].Path) != cleanWorkspaceSyncPath(childFolderPath) {
		t.Fatalf("expected restored child directory reference %q, got %#v", childFolderPath, childRefs)
	}
	childBindings, err := decodeWorkspaceMCPBindings(restoredChild.MCPBindingsJSON)
	if err != nil {
		t.Fatalf("failed to decode restored child mcp bindings: %v", err)
	}
	if len(childBindings) != 1 || !workspaceBindingHasRoot(childBindings[0].Config, childFolderPath) {
		t.Fatalf("expected restored child workspace-files binding for %q, got %#v", childFolderPath, childBindings)
	}
	if workspaceBindingHasRoot(childBindings[0].Config, staleChildPath) {
		t.Fatalf("expected stale child path %q to be removed from bindings, got %#v", staleChildPath, childBindings)
	}
}

func TestHandleWorkspaceImportRestoresExportedWorkspaceNotes(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	storeDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	exportRoot := filepath.Join(t.TempDir(), "branding-export")
	notesDir := filepath.Join(exportRoot, agentworkspace.NotesDir)
	if err := os.MkdirAll(notesDir, 0755); err != nil {
		t.Fatalf("failed to create exported notes folder: %v", err)
	}

	createdAt := time.Date(2026, 4, 29, 9, 7, 2, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Second)
	rootWorkspace := &agentworkspace.Workspace{
		ID:         "ws-imported-branding-notes",
		Name:       "Branding",
		FolderSlug: "branding-export",
		Status:     agentworkspace.StatusActive,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
	rootData, err := rootWorkspace.ToJSON()
	if err != nil {
		t.Fatalf("failed to encode root workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exportRoot, agentworkspace.WorkspaceConfigFile), rootData, 0644); err != nil {
		t.Fatalf("failed to write root workspace.json: %v", err)
	}

	noteID := "3a71c2e1-29f7-45ff-aa5c-a4748fc80bc3"
	noteBody := "# Brand Kit\n\nImported brand context.\n"
	noteMarkdown := "---\n" +
		"id: \"" + noteID + "\"\n" +
		"created_at: \"" + createdAt.Format(time.RFC3339) + "\"\n" +
		"updated_at: \"" + updatedAt.Format(time.RFC3339) + "\"\n" +
		"---\n\n" +
		noteBody
	notePath := filepath.Join(notesDir, agentworkspace.NoteFilename("Brand Kit", noteID))
	if err := os.WriteFile(notePath, []byte(noteMarkdown), 0644); err != nil {
		t.Fatalf("failed to write exported note: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"path": exportRoot,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleWorkspaces(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for exported workspace restore, got %d: %s", w.Code, w.Body.String())
	}

	notes, err := handler.store.ListNotesByWorkspace(context.Background(), rootWorkspace.ID)
	if err != nil {
		t.Fatalf("ListNotesByWorkspace: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 imported note, got %#v", notes)
	}
	if notes[0].ID != noteID {
		t.Fatalf("expected imported note id %q, got %q", noteID, notes[0].ID)
	}
	if notes[0].Name != "Brand Kit" {
		t.Fatalf("expected imported note name Brand Kit, got %q", notes[0].Name)
	}

	note, err := handler.store.GetNote(context.Background(), noteID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if note.Content != noteBody {
		t.Fatalf("expected note content %q, got %q", noteBody, note.Content)
	}
	if !note.CreatedAt.Equal(createdAt) {
		t.Fatalf("expected note created_at %s, got %s", createdAt, note.CreatedAt)
	}
	if !note.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("expected note updated_at %s, got %s", updatedAt, note.UpdatedAt)
	}
}

func TestListWorkspaceNotesHydratesExistingNoteFiles(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	storeDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Hydrate Notes")
	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	createdAt := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)
	noteID := "88b94c66-a6ef-4555-8990-8da06b0e6803"
	noteBody := "# Launch Task List\n\nImported from disk.\n"
	noteMarkdown := "---\n" +
		"id: \"" + noteID + "\"\n" +
		"created_at: \"" + createdAt.Format(time.RFC3339) + "\"\n" +
		"updated_at: \"" + createdAt.Format(time.RFC3339) + "\"\n" +
		"---\n\n" +
		noteBody
	notePath := filepath.Join(folderPath, agentworkspace.NotesDir, agentworkspace.NoteFilename("Launch Task List", noteID))
	if err := os.WriteFile(notePath, []byte(noteMarkdown), 0644); err != nil {
		t.Fatalf("failed to write note file: %v", err)
	}
	rawNotePath := filepath.Join(folderPath, agentworkspace.NotesDir, "loose-note.md")
	if err := os.WriteFile(rawNotePath, []byte("# Loose Note\n\nImported without frontmatter.\n"), 0644); err != nil {
		t.Fatalf("failed to write raw note file: %v", err)
	}

	rawNoteID := importedNoteStableID(workspaceID, "loose-note.md")
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/notes", nil)
		w := httptest.NewRecorder()
		handler.HandleWorkspaceNotes(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for workspace notes, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Notes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"notes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Notes) != 2 {
			t.Fatalf("expected 2 hydrated notes on attempt %d, got %#v", attempt+1, resp.Notes)
		}

		namesByID := make(map[string]string, len(resp.Notes))
		for _, note := range resp.Notes {
			namesByID[note.ID] = note.Name
		}
		if namesByID[noteID] != "Launch Task List" {
			t.Fatalf("expected hydrated note %q to be Launch Task List, got %#v", noteID, namesByID)
		}
		if namesByID[rawNoteID] != "Loose Note" {
			t.Fatalf("expected raw note %q to be Loose Note, got %#v", rawNoteID, namesByID)
		}
	}
}

func TestListWorkspaceNotesSyncsExternallyEditedFiles(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	storeDir := t.TempDir()
	fileStore, err := agentworkspace.NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = fileStore.Close() }()
	handler.SetWorkspaceStore(fileStore)

	workspaceID := createTestWorkspace(t, handler, "Edit Sync")
	folderPath, err := fileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}

	createdAt := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)
	noteID := "1f2e3d4c-5b6a-7980-a1b2-c3d4e5f60718"
	notePath := filepath.Join(folderPath, agentworkspace.NotesDir, agentworkspace.NoteFilename("Roadmap", noteID))
	writeNoteFile := func(body string) {
		markdown := "---\n" +
			"id: \"" + noteID + "\"\n" +
			"name: \"Roadmap\"\n" +
			"created_at: \"" + createdAt.Format(time.RFC3339) + "\"\n" +
			"updated_at: \"" + createdAt.Format(time.RFC3339) + "\"\n" +
			"---\n\n" + body
		if err := os.WriteFile(notePath, []byte(markdown), 0644); err != nil {
			t.Fatalf("write note file: %v", err)
		}
	}

	// First hydration imports the note into the DB.
	writeNoteFile("# Roadmap\n\nFirst draft.\n")
	if _, err := handler.importWorkspaceNoteFilesForWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("import (create): %v", err)
	}
	note, err := handler.store.GetNote(context.Background(), noteID)
	if err != nil {
		t.Fatalf("GetNote after import: %v", err)
	}
	if note.Content != "# Roadmap\n\nFirst draft.\n" {
		t.Fatalf("unexpected imported content %q", note.Content)
	}

	// Re-hydrating without changes must not rewrite the note (no spurious churn).
	importedUpdatedAt := note.UpdatedAt
	if _, err := handler.importWorkspaceNoteFilesForWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("import (unchanged): %v", err)
	}
	note, err = handler.store.GetNote(context.Background(), noteID)
	if err != nil {
		t.Fatalf("GetNote after no-op import: %v", err)
	}
	if !note.UpdatedAt.Equal(importedUpdatedAt) {
		t.Fatalf("expected unchanged note to keep updated_at %s, got %s", importedUpdatedAt, note.UpdatedAt)
	}

	// Edit the note file outside the app, then re-hydrate.
	editedAt := createdAt.Add(time.Hour)
	writeNoteFile("# Roadmap\n\nSecond draft with new content.\n")
	if err := os.Chtimes(notePath, editedAt, editedAt); err != nil {
		t.Fatalf("set note mod time: %v", err)
	}
	if _, err := handler.importWorkspaceNoteFilesForWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("import (edited): %v", err)
	}

	note, err = handler.store.GetNote(context.Background(), noteID)
	if err != nil {
		t.Fatalf("GetNote after edit: %v", err)
	}
	if note.Content != "# Roadmap\n\nSecond draft with new content.\n" {
		t.Fatalf("expected externally edited content to sync, got %q", note.Content)
	}
	if !note.UpdatedAt.Equal(editedAt) {
		t.Fatalf("expected updated_at to track file mod time %s, got %s", editedAt, note.UpdatedAt)
	}
}

func TestHandleWorkspaceImportDuplicateCheckAndConflict(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	importDir := filepath.Join(t.TempDir(), "duplicate-target")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("failed to create temp import directory: %v", err)
	}

	createPayload, _ := json.Marshal(map[string]any{
		"name": "First Import",
		"path": importDir,
	})
	firstReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(createPayload))
	firstReq.Header.Set("Content-Type", "application/json")
	firstW := httptest.NewRecorder()
	handler.HandleWorkspaces(firstW, firstReq)
	if firstW.Code != http.StatusCreated {
		t.Fatalf("expected first import to succeed, got %d: %s", firstW.Code, firstW.Body.String())
	}

	checkReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/import/check?path="+importDir, nil)
	checkW := httptest.NewRecorder()
	handler.HandleWorkspaces(checkW, checkReq)
	if checkW.Code != http.StatusOK {
		t.Fatalf("expected duplicate check 200, got %d: %s", checkW.Code, checkW.Body.String())
	}

	var checkResp map[string]any
	if err := json.Unmarshal(checkW.Body.Bytes(), &checkResp); err != nil {
		t.Fatalf("failed to decode duplicate check response: %v", err)
	}
	dupMap, ok := checkResp["duplicate"].(map[string]any)
	if !ok {
		t.Fatalf("expected duplicate payload")
	}
	if found, _ := dupMap["found"].(bool); !found {
		t.Fatalf("expected duplicate found=true")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(createPayload))
	secondReq.Header.Set("Content-Type", "application/json")
	secondW := httptest.NewRecorder()
	handler.HandleWorkspaces(secondW, secondReq)
	if secondW.Code != http.StatusConflict {
		t.Fatalf("expected duplicate import conflict 409, got %d: %s", secondW.Code, secondW.Body.String())
	}

	overridePayload, _ := json.Marshal(map[string]any{
		"name":            "Duplicate Override",
		"path":            importDir,
		"allow_duplicate": true,
	})
	overrideReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(overridePayload))
	overrideReq.Header.Set("Content-Type", "application/json")
	overrideW := httptest.NewRecorder()
	handler.HandleWorkspaces(overrideW, overrideReq)
	if overrideW.Code != http.StatusConflict {
		t.Fatalf("expected global slug conflict despite duplicate-folder override, got %d: %s", overrideW.Code, overrideW.Body.String())
	}
	var overrideResp map[string]any
	if err := json.Unmarshal(overrideW.Body.Bytes(), &overrideResp); err != nil {
		t.Fatalf("decode override conflict: %v", err)
	}
	conflict, ok := overrideResp["conflict"].(map[string]any)
	if !ok || conflict["suggested_slug"] != "duplicate-target-2" {
		t.Fatalf("expected globally available duplicate-target-2 suggestion, got %#v", overrideResp)
	}

	retryPayload, _ := json.Marshal(map[string]any{
		"name":            "Duplicate Override",
		"path":            importDir,
		"allow_duplicate": true,
		"folder_slug":     "duplicate-target-2",
	})
	retryReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewBuffer(retryPayload))
	retryReq.Header.Set("Content-Type", "application/json")
	retryW := httptest.NewRecorder()
	handler.HandleWorkspaces(retryW, retryReq)
	if retryW.Code != http.StatusCreated {
		t.Fatalf("expected suggested slug retry to succeed, got %d: %s", retryW.Code, retryW.Body.String())
	}
}

func TestHandleWorkspaceImportDuplicateActionTelemetry(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	importDir := filepath.Join(t.TempDir(), "telemetry-target")
	if err := os.MkdirAll(importDir, 0755); err != nil {
		t.Fatalf("failed to create temp import directory: %v", err)
	}

	validPayload, _ := json.Marshal(map[string]any{
		"action":       "suggestion_accepted",
		"workspace_id": "workspace-123",
		"entry_point":  "dashboard_button",
		"path":         importDir,
	})
	validReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import/duplicate-action", bytes.NewBuffer(validPayload))
	validReq.Header.Set("Content-Type", "application/json")
	validW := httptest.NewRecorder()
	handler.HandleWorkspaces(validW, validReq)
	if validW.Code != http.StatusOK {
		t.Fatalf("expected duplicate action request to succeed, got %d: %s", validW.Code, validW.Body.String())
	}

	invalidPayload, _ := json.Marshal(map[string]any{
		"action": "not_allowed",
	})
	invalidReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/import/duplicate-action", bytes.NewBuffer(invalidPayload))
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidW := httptest.NewRecorder()
	handler.HandleWorkspaces(invalidW, invalidReq)
	if invalidW.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid duplicate action to fail with 400, got %d: %s", invalidW.Code, invalidW.Body.String())
	}
}
