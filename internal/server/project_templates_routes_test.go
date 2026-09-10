package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func TestHandleProjectTemplates(t *testing.T) {
	libDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(libDir, "alpha"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "alpha", "template.json"), []byte(`{"name":"Alpha","description":"first","tags":[" Music ","music","Client"]}`), 0o640); err != nil {
		t.Fatal(err)
	}

	configMgr := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := configMgr.Load(); err != nil {
		t.Fatal(err)
	}
	if err := configMgr.SetTemplatesRoot(libDir); err != nil {
		t.Fatal(err)
	}

	s := &Server{}
	s.Core = NewCoreSystemFacade(nil, nil, configMgr, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/project-templates", nil)
	w := httptest.NewRecorder()
	s.handleProjectTemplates(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Templates []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Tags        []string `json:"tags"`
		} `json:"templates"`
		TemplatesRoot string `json:"templates_root"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Templates) != 1 || resp.Templates[0].ID != "alpha" || resp.Templates[0].Name != "Alpha" {
		t.Fatalf("unexpected templates: %+v", resp.Templates)
	}
	if len(resp.Templates[0].Tags) != 2 || resp.Templates[0].Tags[0] != "music" || resp.Templates[0].Tags[1] != "client" {
		t.Fatalf("unexpected template tags: %#v", resp.Templates[0].Tags)
	}
	if resp.TemplatesRoot != libDir {
		t.Fatalf("templates_root = %q, want %q", resp.TemplatesRoot, libDir)
	}

	// Method guard.
	w = httptest.NewRecorder()
	s.handleProjectTemplates(w, httptest.NewRequest(http.MethodPost, "/api/project-templates", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", w.Code)
	}
}

func newTemplateRoutesServer(t *testing.T, libDir string) *Server {
	t.Helper()
	configMgr := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := configMgr.Load(); err != nil {
		t.Fatal(err)
	}
	if err := configMgr.SetTemplatesRoot(libDir); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	s.Core = NewCoreSystemFacade(nil, nil, configMgr, nil, nil)
	s.Storage = &StorageSystemFacade{UserProvider: userprofile.LocalUserProvider{}}
	return s
}

func TestProjectTemplateManagementRoutes(t *testing.T) {
	libDir := t.TempDir()
	s := newTemplateRoutesServer(t, libDir)

	// Import an arbitrary folder.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "seed.txt"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	body := `{"path":"` + src + `","name":"Imported Pack"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/project-templates/import", bytes.NewBufferString(body))
	s.handleProjectTemplateImport(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("import: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(libDir, "imported-pack", "seed.txt")); err != nil {
		t.Fatalf("imported file missing: %v", err)
	}

	// Re-import conflicts.
	w = httptest.NewRecorder()
	s.handleProjectTemplateImport(w, httptest.NewRequest(http.MethodPost, "/api/project-templates/import", bytes.NewBufferString(body)))
	if w.Code != http.StatusConflict {
		t.Fatalf("re-import: expected 409, got %d", w.Code)
	}

	// Update metadata via the wildcard route.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/project-templates/imported-pack", bytes.NewBufferString(`{"name":"Renamed","description":"d"}`))
	req.SetPathValue("templateID", "imported-pack")
	s.handleProjectTemplateUpdate(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updateResp struct {
		Template struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"template"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &updateResp); err != nil {
		t.Fatal(err)
	}
	if updateResp.Template.Name != "Renamed" || updateResp.Template.Description != "d" {
		t.Fatalf("unexpected update response: %+v", updateResp)
	}

	// Unknown template → 404.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/project-templates/nope", bytes.NewBufferString(`{"name":"x"}`))
	req.SetPathValue("templateID", "nope")
	s.handleProjectTemplateUpdate(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("update missing: expected 404, got %d", w.Code)
	}

	// Delete removes the folder (trash or permanent depending on platform).
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/project-templates/imported-pack", nil)
	req.SetPathValue("templateID", "imported-pack")
	s.handleProjectTemplateDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(libDir, "imported-pack")); !os.IsNotExist(err) {
		t.Fatalf("template still present after delete (err=%v)", err)
	}
}

func TestUserSetupQuestAuthoringRoutesUsePreviewAndOptimisticSave(t *testing.T) {
	libDir := t.TempDir()
	fixture := filepath.Join("..", "projecttemplates", "testdata", "user-setup-quest-eligible")
	template, err := projecttemplates.ImportFolder(libDir, fixture, "Eligible project")
	if err != nil {
		t.Fatal(err)
	}
	s := newTemplateRoutesServer(t, libDir)
	s.Storage = &StorageSystemFacade{UserProvider: userprofile.LocalUserProvider{}}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/project-templates/"+template.ID+"/setup-quest", nil)
	getRequest.SetPathValue("templateID", template.ID)
	getRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestGet(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", getRecorder.Code, getRecorder.Body.String())
	}
	var metadata userSetupQuestAuthoringResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Source != "user_template" || !metadata.Editable || !metadata.Eligibility.Eligible || len(metadata.Integrations) == 0 {
		t.Fatalf("metadata = %+v", metadata)
	}

	manifestPath := filepath.Join(template.Path, projecttemplates.ManifestFileName)
	beforePreview, _ := os.ReadFile(manifestPath)
	previewBody, _ := json.Marshal(map[string]any{"user_setup_quest": metadata.Draft})
	previewRequest := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewReader(previewBody))
	previewRequest.SetPathValue("templateID", template.ID)
	previewRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestPreview(previewRecorder, previewRequest)
	if previewRecorder.Code != http.StatusOK || !strings.Contains(previewRecorder.Body.String(), "Preview — no setup started") {
		t.Fatalf("preview = %d: %s", previewRecorder.Code, previewRecorder.Body.String())
	}
	afterPreview, _ := os.ReadFile(manifestPath)
	if string(afterPreview) != string(beforePreview) {
		t.Fatal("preview changed template.json")
	}

	// Omission preserves the current absent attachment.
	omitted := httptest.NewRequest(http.MethodPut, "/setup-quest", bytes.NewBufferString(`{"if_revision":"`+metadata.Revision+`"}`))
	omitted.SetPathValue("templateID", template.ID)
	omittedRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestPut(omittedRecorder, omitted)
	if omittedRecorder.Code != http.StatusOK {
		t.Fatalf("omitted = %d: %s", omittedRecorder.Code, omittedRecorder.Body.String())
	}

	saveBody, _ := json.Marshal(map[string]any{"if_revision": metadata.Revision, "user_setup_quest": metadata.Draft})
	saveRequest := httptest.NewRequest(http.MethodPut, "/setup-quest", bytes.NewReader(saveBody))
	saveRequest.SetPathValue("templateID", template.ID)
	saveRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestPut(saveRecorder, saveRequest)
	if saveRecorder.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", saveRecorder.Code, saveRecorder.Body.String())
	}
	var saved userSetupQuestAuthoringResponse
	if err := json.Unmarshal(saveRecorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Quest == nil || saved.Revision == metadata.Revision {
		t.Fatalf("saved metadata = %+v", saved)
	}

	staleRequest := httptest.NewRequest(http.MethodPut, "/setup-quest", bytes.NewReader(saveBody))
	staleRequest.SetPathValue("templateID", template.ID)
	staleRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestPut(staleRecorder, staleRequest)
	if staleRecorder.Code != http.StatusConflict {
		t.Fatalf("stale = %d: %s", staleRecorder.Code, staleRecorder.Body.String())
	}

	malicious := map[string]any{}
	encodedDraft, _ := json.Marshal(metadata.Draft)
	if err := json.Unmarshal(encodedDraft, &malicious); err != nil {
		t.Fatal(err)
	}
	malicious["route"] = "/api/run"
	maliciousBody, _ := json.Marshal(map[string]any{"if_revision": saved.Revision, "user_setup_quest": malicious})
	maliciousRequest := httptest.NewRequest(http.MethodPut, "/setup-quest", bytes.NewReader(maliciousBody))
	maliciousRequest.SetPathValue("templateID", template.ID)
	maliciousRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestPut(maliciousRecorder, maliciousRequest)
	if maliciousRecorder.Code != http.StatusBadRequest {
		t.Fatalf("malicious = %d: %s", maliciousRecorder.Code, maliciousRecorder.Body.String())
	}

	removeRequest := httptest.NewRequest(http.MethodPut, "/setup-quest", bytes.NewBufferString(`{"if_revision":"`+saved.Revision+`","user_setup_quest":null}`))
	removeRequest.SetPathValue("templateID", template.ID)
	removeRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestPut(removeRecorder, removeRequest)
	if removeRecorder.Code != http.StatusOK {
		t.Fatalf("remove = %d: %s", removeRecorder.Code, removeRecorder.Body.String())
	}
}

func TestUserSetupQuestBindingLocksProtectedTemplateMutations(t *testing.T) {
	libDir := t.TempDir()
	fixture := filepath.Join("..", "projecttemplates", "testdata", "user-setup-quest-eligible")
	template, err := projecttemplates.ImportFolder(libDir, fixture, "Locked project")
	if err != nil {
		t.Fatal(err)
	}
	draft := projecttemplates.DefaultUserSetupQuestDraft()
	draft.IntegrationKey = "ori_reaper"
	template, err = projecttemplates.UpdateUserSetupQuest(libDir, template.ID, projecttemplates.UserSetupQuestEdit{
		ExpectedRevision: template.UserSetupQuestRevision, Draft: &draft,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(context.Background(), &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := setupjourney.NewSQLiteStore(db)
	if _, _, err := store.ClaimUserTemplateBinding(context.Background(), setupjourney.UserTemplateBinding{
		UserID: "other-user", TemplateID: template.ID,
		AttachmentID: template.UserSetupQuest.AttachmentID, QuestID: template.UserSetupQuest.Declaration.ID,
		DefinitionDigest: projecttemplates.UserSetupQuestDefinitionDigest(template.UserSetupQuest),
		ExecutionDigest:  projecttemplates.UserSetupQuestExecutionDigest(template),
	}); err != nil {
		t.Fatal(err)
	}
	s := newTemplateRoutesServer(t, libDir)
	s.setupJourneyStore = store

	get := httptest.NewRequest(http.MethodGet, "/setup-quest", nil)
	get.SetPathValue("templateID", template.ID)
	getRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestGet(getRecorder, get)
	var metadata userSetupQuestAuthoringResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if !metadata.Locked || metadata.RecoveryCode != "" {
		t.Fatalf("locked metadata=%+v", metadata)
	}

	changedDraft := projecttemplates.UserSetupQuestDraftFor(template.UserSetupQuest)
	changedDraft.Title = "Changed after start"
	changedBody, _ := json.Marshal(map[string]any{"if_revision": template.UserSetupQuestRevision, "user_setup_quest": changedDraft})
	changed := httptest.NewRequest(http.MethodPut, "/setup-quest", bytes.NewReader(changedBody))
	changed.SetPathValue("templateID", template.ID)
	changedRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestPut(changedRecorder, changed)
	if changedRecorder.Code != http.StatusConflict {
		t.Fatalf("quest edit=%d: %s", changedRecorder.Code, changedRecorder.Body.String())
	}

	// Display-only metadata remains mutable because it is outside the execution digest.
	display := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(`{"name":"Renamed after start","description":"Display copy"}`))
	display.SetPathValue("templateID", template.ID)
	displayRecorder := httptest.NewRecorder()
	s.handleProjectTemplateUpdate(displayRecorder, display)
	if displayRecorder.Code != http.StatusOK {
		t.Fatalf("display edit=%d: %s", displayRecorder.Code, displayRecorder.Body.String())
	}

	// A project-entry change and any scaffold write are protected.
	entryBody := `{"name":"Renamed after start","description":"Display copy","project_entry":{"relative_path":"{{name}}.rpp","open_after_create_default":true}}`
	entry := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(entryBody))
	entry.SetPathValue("templateID", template.ID)
	entryRecorder := httptest.NewRecorder()
	s.handleProjectTemplateUpdate(entryRecorder, entry)
	if entryRecorder.Code != http.StatusConflict {
		t.Fatalf("project entry edit=%d: %s", entryRecorder.Code, entryRecorder.Body.String())
	}
	file := httptest.NewRequest(http.MethodPut, "/files/content", bytes.NewBufferString(`{"path":"{{name}}.rpp","content":"changed"}`))
	file.SetPathValue("templateID", template.ID)
	fileRecorder := httptest.NewRecorder()
	s.handleProjectTemplateFileWrite(fileRecorder, file)
	if fileRecorder.Code != http.StatusConflict {
		t.Fatalf("scaffold edit=%d: %s", fileRecorder.Code, fileRecorder.Body.String())
	}

	// The ordinary Agents roster is not the Assistant Program and stays editable.
	agents := httptest.NewRequest(http.MethodPut, "/agents", bytes.NewBufferString(`{"agents":[{"name":"Producer"}]}`))
	agents.SetPathValue("templateID", template.ID)
	agentsRecorder := httptest.NewRecorder()
	s.handleProjectTemplateAgentsSet(agentsRecorder, agents)
	if agentsRecorder.Code != http.StatusOK {
		t.Fatalf("agents edit=%d: %s", agentsRecorder.Code, agentsRecorder.Body.String())
	}

	remove := httptest.NewRequest(http.MethodDelete, "/template", nil)
	remove.SetPathValue("templateID", template.ID)
	removeRecorder := httptest.NewRecorder()
	s.handleProjectTemplateDelete(removeRecorder, remove)
	if removeRecorder.Code != http.StatusConflict {
		t.Fatalf("template delete=%d: %s", removeRecorder.Code, removeRecorder.Body.String())
	}

	// Out-of-band protected drift is reported as recovery, never silently rebound.
	manifestPath := filepath.Join(template.Path, projecttemplates.ManifestFileName)
	var raw map[string]any
	manifestBytes, _ := os.ReadFile(manifestPath)
	if err := json.Unmarshal(manifestBytes, &raw); err != nil {
		t.Fatal(err)
	}
	raw["project_entry"].(map[string]any)["open_after_create_default"] = true
	drifted, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(manifestPath, drifted, 0o640); err != nil {
		t.Fatal(err)
	}
	driftGet := httptest.NewRequest(http.MethodGet, "/setup-quest", nil)
	driftGet.SetPathValue("templateID", template.ID)
	driftRecorder := httptest.NewRecorder()
	s.handleUserSetupQuestGet(driftRecorder, driftGet)
	if err := json.Unmarshal(driftRecorder.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.RecoveryCode != "protected_content_changed" || !strings.Contains(metadata.LockMessage, "will not rebind") {
		t.Fatalf("drift metadata=%+v", metadata)
	}
	driftDisplay := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(`{"name":"Metadata during recovery","description":"Still editable"}`))
	driftDisplay.SetPathValue("templateID", template.ID)
	driftDisplayRecorder := httptest.NewRecorder()
	s.handleProjectTemplateUpdate(driftDisplayRecorder, driftDisplay)
	if driftDisplayRecorder.Code != http.StatusOK {
		t.Fatalf("display edit during recovery=%d: %s", driftDisplayRecorder.Code, driftDisplayRecorder.Body.String())
	}
}

func TestHandleProjectTemplateUpdateRuntimeRequirementsRoundTrip(t *testing.T) {
	previousAdapters := append([]string(nil), projecttemplates.ValidRuntimeRequirementAdapters...)
	projecttemplates.ValidRuntimeRequirementAdapters = []string{"test_runtime"}
	t.Cleanup(func() { projecttemplates.ValidRuntimeRequirementAdapters = previousAdapters })
	libDir := t.TempDir()
	templateDir := filepath.Join(libDir, "runtime-demo")
	if err := os.MkdirAll(templateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(templateDir, projecttemplates.ManifestFileName)
	if err := os.WriteFile(manifestPath, []byte(`{"name":"Runtime Demo","agents":[{"name":"Lead"}],"custom_key":"kept"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	s := newTemplateRoutesServer(t, libDir)

	callUpdate := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/project-templates/runtime-demo", bytes.NewBufferString(body))
		req.SetPathValue("templateID", "runtime-demo")
		s.handleProjectTemplateUpdate(w, req)
		return w
	}

	valid := `{
		"name":"Runtime Demo",
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[
				{"id":"limited","label":"Limited","description":"Use files."},
				{"id":"assisted","label":"Assisted","description":"Use live control.","requires":["runtime"]}
			],
			"requirements":[{"key":"runtime","label":"Runtime","description":"Configure it.","adapter":"test_runtime"}]
		}
	}`
	w := callUpdate(valid)
	if w.Code != http.StatusOK {
		t.Fatalf("valid update: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Template struct {
			RuntimeRequirements *projecttemplates.RuntimeRequirementsContract `json:"runtime_requirements"`
		} `json:"template"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Template.RuntimeRequirements == nil || len(response.Template.RuntimeRequirements.OperatingModes) != 2 || response.Template.RuntimeRequirements.Requirements[0].Adapter != "test_runtime" {
		t.Fatalf("update response lost public runtime metadata: %s", w.Body.String())
	}

	// The list API returns the same normalized contract.
	list := httptest.NewRecorder()
	s.handleProjectTemplates(list, httptest.NewRequest(http.MethodGet, "/api/project-templates", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"runtime_requirements"`) || !strings.Contains(list.Body.String(), `"operating_modes"`) {
		t.Fatalf("list response lost runtime metadata: %d %s", list.Code, list.Body.String())
	}

	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	invalid := `{
		"name":"Must not persist",
		"runtime_requirements":{
			"schema_version":1,
			"operating_modes":[{"id":"limited","label":"Limited","description":"Use files."}],
			"requirements":[],
			"command":"run"
		}
	}`
	w = callUpdate(invalid)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid runtime requirements") || !strings.Contains(w.Body.String(), "unknown field") {
		t.Fatalf("invalid update: expected actionable 400, got %d: %s", w.Code, w.Body.String())
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("invalid HTTP update partially saved template.json:\nbefore %s\nafter %s", before, after)
	}

	w = callUpdate(`{"name":"Runtime Demo","runtime_requirements":null}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"runtime_requirements"`) {
		t.Fatalf("explicit null did not clear runtime contract: %d %s", w.Code, w.Body.String())
	}
}

func TestHandleProjectTemplateUpdateProjectEntryTriState(t *testing.T) {
	libDir := t.TempDir()
	templateDir := filepath.Join(libDir, "song")
	if err := os.MkdirAll(templateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "{{name}}.rpp"), []byte("project"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "template.json"), []byte(`{"name":"Song","custom_key":"kept"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	s := newTemplateRoutesServer(t, libDir)

	callUpdate := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/project-templates/song", bytes.NewBufferString(body))
		req.SetPathValue("templateID", "song")
		s.handleProjectTemplateUpdate(w, req)
		return w
	}

	w := callUpdate(`{"name":"Song","project_entry":{"relative_path":"{{name}}.rpp","open_after_create_default":false}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("set project entry: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Template struct {
			ProjectEntry *struct {
				RelativePath           string `json:"relative_path"`
				OpenAfterCreateDefault bool   `json:"open_after_create_default"`
			} `json:"project_entry"`
		} `json:"template"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Template.ProjectEntry == nil || response.Template.ProjectEntry.RelativePath != "{{name}}.rpp" || response.Template.ProjectEntry.OpenAfterCreateDefault {
		t.Fatalf("unexpected project entry response: %+v", response.Template.ProjectEntry)
	}

	// Omitting the field preserves it for older clients.
	w = callUpdate(`{"name":"Song","description":"preserved"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"project_entry"`) {
		t.Fatalf("omitted project entry was not preserved: %d %s", w.Code, w.Body.String())
	}

	// Explicit null clears it.
	w = callUpdate(`{"name":"Song","project_entry":null}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"project_entry"`) {
		t.Fatalf("project entry was not cleared: %d %s", w.Code, w.Body.String())
	}

	for _, body := range []string{
		`{"name":"Song","project_entry":{"relative_path":"../escape.rpp","open_after_create_default":true}}`,
		`{"name":"Song","project_entry":{"relative_path":"missing.rpp","open_after_create_default":true}}`,
		`{"name":"Song","project_entry":"{{name}}.rpp"}`,
	} {
		w = callUpdate(body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid project entry: expected 400, got %d: %s", w.Code, w.Body.String())
		}
	}

	data, err := os.ReadFile(filepath.Join(templateDir, "template.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"custom_key": "kept"`) {
		t.Fatalf("unknown manifest field was lost: %s", data)
	}
}
