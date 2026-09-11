package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func routeVariantSource(t *testing.T) (plugin.InstalledPlugin, string) {
	t.Helper()
	skeleton := t.TempDir()
	if err := os.WriteFile(filepath.Join(skeleton, "{{name}}.project"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := &workspace.PluginTemplateOwner{PluginID: "neutral-plugin", PluginVersion: "1.0.0", BlueprintID: "neutral-project", BlueprintVersion: 2}
	program := &workspace.AssistantProgramDeclaration{
		SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "project-guide", StationName: "Project Guide Home",
		DefaultPrimaryName: "Coordinator", HireTitle: "Staff project guidance",
		Roles: []workspace.AssistantProgramRoleSpec{
			{ID: "coordinator", Label: "Coordinator", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate bounded portfolio records."},
			{ID: "lead", Label: "Project Lead", Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Coordinate one exact linked project."},
		},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper", AcceptedCompletionThreshold: 0}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 16, MaxCandidates: 4, MaxEvidence: 4, Rubric: "Review bounded evidence."},
	}
	template := projecttemplates.Template{
		ID: "plugin:neutral-plugin:neutral-project", Name: "Neutral Project", Description: "Trusted source", Path: skeleton, HasSkeleton: true,
		PluginOwner: owner, AssistantProgram: program,
		ProjectConnection:     &projecttemplates.ProjectConnectionDeclaration{SchemaVersion: 1, SupportedModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionNewProject}},
		StandaloneComposition: &projecttemplates.StandaloneComposition{SchemaVersion: 1, ProjectRoles: []projecttemplates.StandaloneRole{{RoleID: "lead", SystemPrompt: "Coordinate only this workspace and its project."}}},
		GroupRequirement:      &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyRequired, AssistantProgramID: "project-guide", MissingHome: projecttemplates.MissingHomeOfferCreate, DefaultHomeName: "Project Guide Home"},
	}
	blueprint := plugin.ResolvedBlueprint{ID: owner.BlueprintID, QualifiedID: template.ID, Version: owner.BlueprintVersion, Template: template, SkeletonRoot: skeleton, SkeletonDigest: strings.Repeat("a", 64)}
	installed := plugin.InstalledPlugin{
		Name: owner.PluginID, Version: owner.PluginVersion, Enabled: true, Generation: 1,
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			SchemaVersion: 1, Name: owner.PluginID, Version: owner.PluginVersion,
			Protocol:             plugin.ProtocolRange{Min: plugin.SurfaceProtocolVersion, Max: plugin.SurfaceProtocolVersion},
			RequiresHostFeatures: []string{plugin.HostFeatureTemplateGroupRequirementsV1},
		},
		ResolvedBlueprints: []plugin.ResolvedBlueprint{blueprint},
	}
	return installed, template.ID
}

func variantRoutesServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	library := t.TempDir()
	plugins := filepath.Join(t.TempDir(), "plugins")
	installed, sourceID := routeVariantSource(t)
	server := catalogEndpointServer(t, library, plugins, []plugin.InstalledPlugin{installed})
	server.Storage = &StorageSystemFacade{UserProvider: userprofile.LocalUserProvider{}}
	return server, library, sourceID
}

func TestProjectTemplateVariantPreviewSaveAndStaleRefusal(t *testing.T) {
	server, library, sourceID := variantRoutesServer(t)
	requirement := map[string]any{
		"schema_version": 1, "policy": "recommended", "assistant_program_id": "project-guide", "missing_home": "existing_only",
	}
	previewBody, _ := json.Marshal(map[string]any{"group_requirement": requirement})
	preview := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewReader(previewBody))
	preview.SetPathValue("templateID", sourceID)
	previewRecorder := httptest.NewRecorder()
	server.handleProjectTemplateGroupRequirementPreview(previewRecorder, preview)
	if previewRecorder.Code != http.StatusOK || !strings.Contains(previewRecorder.Body.String(), "Preview — no workspace or Home created") {
		t.Fatalf("preview = %d: %s", previewRecorder.Code, previewRecorder.Body.String())
	}
	if entries, err := os.ReadDir(library); err != nil || len(entries) != 0 {
		t.Fatalf("preview created template consequences: %#v err=%v", entries, err)
	}

	createBody, _ := json.Marshal(map[string]any{
		"name": "My Neutral Project", "description": "Local policy", "icon": "N", "group_requirement": requirement,
	})
	create := httptest.NewRequest(http.MethodPost, "/variants", bytes.NewReader(createBody))
	create.SetPathValue("templateID", sourceID)
	createRecorder := httptest.NewRecorder()
	server.handleProjectTemplateVariantCreate(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", createRecorder.Code, createRecorder.Body.String())
	}
	var created struct {
		Template projecttemplates.Template `json:"template"`
	}
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Template.TemplateVariant == nil || created.Template.PluginOwner != nil || created.Template.VariantRevision == "" {
		t.Fatalf("created variant = %#v", created.Template)
	}

	manifestPath := filepath.Join(library, created.Template.ID, projecttemplates.ManifestFileName)
	beforePreview, _ := os.ReadFile(manifestPath)
	variantPreviewBody, _ := json.Marshal(map[string]any{"if_revision": created.Template.VariantRevision, "group_requirement": map[string]any{"schema_version": 1, "policy": "none"}})
	variantPreview := httptest.NewRequest(http.MethodPost, "/preview", bytes.NewReader(variantPreviewBody))
	variantPreview.SetPathValue("templateID", created.Template.ID)
	variantPreviewRecorder := httptest.NewRecorder()
	server.handleProjectTemplateGroupRequirementPreview(variantPreviewRecorder, variantPreview)
	if variantPreviewRecorder.Code != http.StatusOK {
		t.Fatalf("variant preview = %d: %s", variantPreviewRecorder.Code, variantPreviewRecorder.Body.String())
	}
	afterPreview, _ := os.ReadFile(manifestPath)
	if !bytes.Equal(beforePreview, afterPreview) {
		t.Fatal("variant preview mutated template.json")
	}

	updateBody, _ := json.Marshal(map[string]any{
		"name": "My Standalone Project", "description": "No Home", "icon": "N",
		"if_revision":       created.Template.VariantRevision,
		"group_requirement": map[string]any{"schema_version": 1, "policy": "none"},
	})
	update := httptest.NewRequest(http.MethodPut, "/template", bytes.NewReader(updateBody))
	update.SetPathValue("templateID", created.Template.ID)
	updateRecorder := httptest.NewRecorder()
	server.handleProjectTemplateUpdate(updateRecorder, update)
	if updateRecorder.Code != http.StatusOK || !strings.Contains(updateRecorder.Body.String(), `"policy":"none"`) {
		t.Fatalf("update = %d: %s", updateRecorder.Code, updateRecorder.Body.String())
	}
	savedBytes, _ := os.ReadFile(manifestPath)

	stale := httptest.NewRequest(http.MethodPut, "/template", bytes.NewReader(updateBody))
	stale.SetPathValue("templateID", created.Template.ID)
	staleRecorder := httptest.NewRecorder()
	server.handleProjectTemplateUpdate(staleRecorder, stale)
	if staleRecorder.Code != http.StatusConflict {
		t.Fatalf("stale update = %d: %s", staleRecorder.Code, staleRecorder.Body.String())
	}
	afterStale, _ := os.ReadFile(manifestPath)
	if !bytes.Equal(savedBytes, afterStale) {
		t.Fatal("stale update changed variant bytes")
	}
}

func TestProjectTemplateVariantSurfacesAreAllowlistedAndManifestOnly(t *testing.T) {
	server, library, sourceID := variantRoutesServer(t)
	createBody := `{"name":"Bounded variant","group_requirement":{"schema_version":1,"policy":"none"}}`
	create := httptest.NewRequest(http.MethodPost, "/variants", bytes.NewBufferString(createBody))
	create.SetPathValue("templateID", sourceID)
	createRecorder := httptest.NewRecorder()
	server.handleProjectTemplateVariantCreate(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", createRecorder.Code, createRecorder.Body.String())
	}
	var created struct {
		Template projecttemplates.Template `json:"template"`
	}
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(library, created.Template.ID, projecttemplates.ManifestFileName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	unsupported := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(`{
		"name":"Escalated","if_revision":"`+created.Template.VariantRevision+`",
		"group_requirement":{"schema_version":1,"policy":"none"},
		"behavior_profile":"software_project"
	}`))
	unsupported.SetPathValue("templateID", created.Template.ID)
	unsupportedRecorder := httptest.NewRecorder()
	server.handleProjectTemplateUpdate(unsupportedRecorder, unsupported)
	if unsupportedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("unsupported edit = %d: %s", unsupportedRecorder.Code, unsupportedRecorder.Body.String())
	}
	after, _ := os.ReadFile(manifestPath)
	if !bytes.Equal(before, after) {
		t.Fatal("unsupported variant edit changed manifest bytes")
	}

	files := httptest.NewRequest(http.MethodGet, "/files", nil)
	files.SetPathValue("templateID", created.Template.ID)
	filesRecorder := httptest.NewRecorder()
	server.handleProjectTemplateFilesList(filesRecorder, files)
	if filesRecorder.Code != http.StatusForbidden {
		t.Fatalf("variant files = %d: %s", filesRecorder.Code, filesRecorder.Body.String())
	}
	quest := httptest.NewRequest(http.MethodGet, "/setup-quest", nil)
	quest.SetPathValue("templateID", created.Template.ID)
	questRecorder := httptest.NewRecorder()
	server.handleUserSetupQuestGet(questRecorder, quest)
	if questRecorder.Code != http.StatusForbidden {
		t.Fatalf("variant setup quest = %d: %s", questRecorder.Code, questRecorder.Body.String())
	}

	duplicate := httptest.NewRequest(http.MethodPost, "/duplicate", bytes.NewBufferString(`{"name":"Second overlay"}`))
	duplicate.SetPathValue("templateID", created.Template.ID)
	duplicateRecorder := httptest.NewRecorder()
	server.handleProjectTemplateDuplicate(duplicateRecorder, duplicate)
	if duplicateRecorder.Code != http.StatusCreated {
		t.Fatalf("variant duplicate = %d: %s", duplicateRecorder.Code, duplicateRecorder.Body.String())
	}
	var duplicated struct {
		Template projecttemplates.Template `json:"template"`
	}
	if err := json.Unmarshal(duplicateRecorder.Body.Bytes(), &duplicated); err != nil {
		t.Fatal(err)
	}
	if duplicated.Template.ID == created.Template.ID || duplicated.Template.TemplateVariant == nil ||
		duplicated.Template.TemplateVariant.VariantID == created.Template.TemplateVariant.VariantID ||
		duplicated.Template.TemplateVariant.Source != created.Template.TemplateVariant.Source {
		t.Fatalf("duplicate identities = created %#v duplicate %#v", created.Template.TemplateVariant, duplicated.Template.TemplateVariant)
	}

	unconfirmedImportBody, _ := json.Marshal(map[string]any{"path": filepath.Join(library, created.Template.ID), "name": "Rehosted overlay"})
	unconfirmedImport := httptest.NewRequest(http.MethodPost, "/import", bytes.NewReader(unconfirmedImportBody))
	unconfirmedRecorder := httptest.NewRecorder()
	server.handleProjectTemplateImport(unconfirmedRecorder, unconfirmedImport)
	if unconfirmedRecorder.Code != http.StatusConflict {
		t.Fatalf("unconfirmed rehost = %d: %s", unconfirmedRecorder.Code, unconfirmedRecorder.Body.String())
	}
	confirmedImportBody, _ := json.Marshal(map[string]any{
		"path": filepath.Join(library, created.Template.ID), "name": "Rehosted overlay", "confirm_rehost": true,
	})
	confirmedImport := httptest.NewRequest(http.MethodPost, "/import", bytes.NewReader(confirmedImportBody))
	confirmedRecorder := httptest.NewRecorder()
	server.handleProjectTemplateImport(confirmedRecorder, confirmedImport)
	if confirmedRecorder.Code != http.StatusCreated {
		t.Fatalf("confirmed rehost = %d: %s", confirmedRecorder.Code, confirmedRecorder.Body.String())
	}
}

func TestProjectTemplateVariantCatalogStatesAndOwnerIsolation(t *testing.T) {
	installed, _ := routeVariantSource(t)
	blueprint := installed.ResolvedBlueprints[0]
	source := projecttemplates.VariantSource{Template: blueprint.Template, SkeletonDigest: blueprint.SkeletonDigest}
	library := t.TempDir()
	local, err := projecttemplates.CreateTemplateVariant(library, source, "local", projecttemplates.VariantEdit{
		Name: "Local variant", GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := projecttemplates.CreateTemplateVariant(library, source, "other-owner", projecttemplates.VariantEdit{
		Name: "Foreign variant", GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	overlays, err := projecttemplates.ListLibrary(library)
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolveCatalogVariants(overlays, []plugin.InstalledPlugin{installed}, "local")
	if len(resolved) != 1 || resolved[0].ID != local.ID || resolved[0].VariantSourceState != projecttemplates.VariantSourceReady {
		t.Fatalf("owner-scoped ready catalog = %#v", resolved)
	}
	missing := resolveCatalogVariants(overlays, nil, "local")
	if len(missing) != 1 || missing[0].VariantSourceState != projecttemplates.VariantSourceMissing || missing[0].HasSkeleton {
		t.Fatalf("missing source catalog = %#v", missing)
	}
	disabledPlugin := installed
	disabledPlugin.Enabled = false
	disabled := resolveCatalogVariants(overlays, []plugin.InstalledPlugin{disabledPlugin}, "local")
	if len(disabled) != 1 || disabled[0].VariantSourceState != projecttemplates.VariantSourceDisabled {
		t.Fatalf("disabled source catalog = %#v", disabled)
	}
	reenabled := resolveCatalogVariants(overlays, []plugin.InstalledPlugin{installed}, "local")
	if len(reenabled) != 1 || reenabled[0].VariantSourceState != projecttemplates.VariantSourceReady ||
		reenabled[0].ID != local.ID || reenabled[0].VariantRevision != local.VariantRevision ||
		reenabled[0].TemplateVariant.Source.DefinitionDigest != local.TemplateVariant.Source.DefinitionDigest {
		t.Fatalf("re-enabled source changed variant identity or provenance = %#v", reenabled)
	}
	changedPlugin := installed
	changedPlugin.Version = "2.0.0"
	changed := resolveCatalogVariants(overlays, []plugin.InstalledPlugin{changedPlugin}, "local")
	if len(changed) != 1 || changed[0].VariantSourceState != projecttemplates.VariantSourceChanged ||
		changed[0].Path != "" || changed[0].AssistantProgram != nil || changed[0].PluginOwner != nil || changed[0].SetupQuestID != "" {
		t.Fatalf("changed source catalog fell back to unrelated source data = %#v", changed)
	}

	server := catalogEndpointServer(t, library, t.TempDir(), []plugin.InstalledPlugin{installed})
	server.Storage = &StorageSystemFacade{UserProvider: userprofile.LocalUserProvider{}}
	foreignUpdate := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(`{"name":"Nope"}`))
	foreignUpdate.SetPathValue("templateID", foreign.ID)
	foreignRecorder := httptest.NewRecorder()
	server.handleProjectTemplateUpdate(foreignRecorder, foreignUpdate)
	if foreignRecorder.Code != http.StatusNotFound || strings.Contains(foreignRecorder.Body.String(), "Foreign variant") {
		t.Fatalf("foreign update = %d: %s", foreignRecorder.Code, foreignRecorder.Body.String())
	}
	foreignFiles := httptest.NewRequest(http.MethodGet, "/files", nil)
	foreignFiles.SetPathValue("templateID", foreign.ID)
	foreignFilesRecorder := httptest.NewRecorder()
	server.handleProjectTemplateFilesList(foreignFilesRecorder, foreignFiles)
	if foreignFilesRecorder.Code != http.StatusNotFound {
		t.Fatalf("foreign files = %d: %s", foreignFilesRecorder.Code, foreignFilesRecorder.Body.String())
	}
}

func TestProjectTemplateVariantRejectsOwnerAndSourceClaims(t *testing.T) {
	server, _, sourceID := variantRoutesServer(t)
	body := `{"name":"Hostile","group_requirement":{"schema_version":1,"policy":"none"},"owner_user_id":"attacker"}`
	request := httptest.NewRequest(http.MethodPost, "/variants", bytes.NewBufferString(body))
	request.SetPathValue("templateID", sourceID)
	recorder := httptest.NewRecorder()
	server.handleProjectTemplateVariantCreate(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("owner claim = %d: %s", recorder.Code, recorder.Body.String())
	}

	pluginEdit := httptest.NewRequest(http.MethodPut, "/template", bytes.NewBufferString(`{"name":"Changed"}`))
	pluginEdit.SetPathValue("templateID", sourceID)
	pluginRecorder := httptest.NewRecorder()
	server.handleProjectTemplateUpdate(pluginRecorder, pluginEdit)
	if pluginRecorder.Code != http.StatusForbidden {
		t.Fatalf("plugin original edit = %d: %s", pluginRecorder.Code, pluginRecorder.Body.String())
	}
}
