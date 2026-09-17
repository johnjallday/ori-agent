package sessionhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// attachTemplate is policyTemplate plus a declaration that accepts existing
// projects whose project file ends in ".demo".
func attachTemplate(policy projecttemplates.GroupPolicy) projecttemplates.Template {
	template := policyTemplate(policy)
	template.ProjectConnection = &projecttemplates.ProjectConnectionDeclaration{
		SchemaVersion: projecttemplates.ProjectConnectionSchemaVersion,
		SupportedModes: []projecttemplates.ProjectConnectionMode{
			projecttemplates.ProjectConnectionExistingProject, projecttemplates.ProjectConnectionNewProject,
		},
		AttachExisting: &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".demo"}},
	}
	return template
}

type attachFixture struct {
	handler    *Handler
	store      agentworkspace.Store
	selections *pathselection.Store
	root       string
	template   projecttemplates.Template
}

func newAttachFixture(t *testing.T, template projecttemplates.Template) attachFixture {
	t.Helper()
	root := t.TempDir()
	handler, store, cleanup := newPolicyHandlerAt(t, &template, root)
	t.Cleanup(cleanup)
	selections := pathselection.NewStore()
	handler.SetTrustedPathSelectionResolver(selections)
	return attachFixture{handler: handler, store: store, selections: selections, root: root, template: template}
}

// existingProjectFolder writes files (name -> content) into a fresh folder
// outside the workspace root, the way a user's project already lives on disk.
func existingProjectFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	folder := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

// folderChecksum fingerprints every entry's name, mode, size, and content, so
// a created, removed, renamed, or modified file changes it.
func folderChecksum(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(root, path)
		fact := info.Mode().String()
		if info.Mode().IsRegular() {
			content, readErr := os.ReadFile(path) // #nosec G304 -- test fixture folder
			if readErr != nil {
				return readErr
			}
			fact = fmt.Sprintf("%s %d %x", fact, len(content), sha256.Sum256(content))
		}
		result[rel] = fact
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// reviewAndCreate replays the modal's two requests: an inert placement review,
// then the commit carrying the returned token.
func reviewAndCreate(t *testing.T, handler *Handler, payload map[string]any, key string) (int, map[string]any) {
	t.Helper()
	review := map[string]any{"group_requirement_review": true}
	for field, value := range payload {
		review[field] = value
	}
	code, body := postPolicyWorkspace(t, handler, review)
	if code != http.StatusOK {
		return code, body
	}
	receipt, _ := body["group_requirement_review"].(map[string]any)
	commit := map[string]any{"group_review_token": receipt["review_token"], "idempotency_key": key}
	for field, value := range payload {
		commit[field] = value
	}
	return postPolicyWorkspace(t, handler, commit)
}

// attachPayload is a standalone "Night Drive" attach; callers override fields.
func attachPayload(templateID, token string) map[string]any {
	return map[string]any{
		"name": "Night Drive", "template_id": templateID, "group_composition": "standalone", "parent_id": "",
		"project_connection": map[string]any{"mode_id": "existing_project", "selection_token": token},
	}
}

func canonicalWorkspace(t *testing.T, store agentworkspace.Store, id string) *agentworkspace.Workspace {
	t.Helper()
	canonical, err := store.(interface {
		GetFolderWorkspace(string) (*agentworkspace.Workspace, error)
	}).GetFolderWorkspace(id)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// attachedReference returns the directory reference the typed locator names.
func attachedReference(t *testing.T, ws *agentworkspace.Workspace) (*agentworkspace.ProjectEntryLocator, *agentworkspace.DirectoryReference) {
	t.Helper()
	locator, err := agentworkspace.GetProjectEntryLocator(ws.SharedData)
	if err != nil || locator == nil || locator.Kind != agentworkspace.ProjectEntryDirectoryReference {
		t.Fatalf("locator = %#v err=%v", locator, err)
	}
	reference, err := ws.GetDirectoryReference(locator.DirectoryReferenceID)
	if err != nil {
		t.Fatalf("locator names a missing reference %q: %v (refs %#v)", locator.DirectoryReferenceID, err, ws.DirectoryReferences)
	}
	return locator, reference
}

func TestCreateWorkspaceAttachesExistingProjectInPlace(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	external := existingProjectFolder(t, map[string]string{"Night Drive.demo": "original project", "notes.txt": "mix notes"})
	before := folderChecksum(t, external)
	token, err := fixture.selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}

	code, body := reviewAndCreate(t, fixture.handler, attachPayload(fixture.template.ID, token), "attach-night-drive")
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, body)
	}
	id := body["folder"].(map[string]any)["id"].(string)

	if after := folderChecksum(t, external); !reflect.DeepEqual(after, before) {
		t.Fatalf("attach changed the user's folder:\nbefore=%v\nafter=%v", before, after)
	}
	// The session row (read-modify-write source) and workspace.json must both
	// carry the attach, or a later update would erase it.
	for label, ws := range map[string]*agentworkspace.Workspace{
		"session": mustGetWorkspace(t, fixture.store, id), "canonical": canonicalWorkspace(t, fixture.store, id),
	} {
		locator, reference := attachedReference(t, ws)
		if reference.Path != external || reference.WorkspaceID != id || locator.RelativePath != "Night Drive.demo" {
			t.Fatalf("%s attach = locator %#v reference %#v", label, locator, reference)
		}
		if ws.ProjectPath != "" {
			t.Fatalf("%s attach scaffolded a managed project %q", label, ws.ProjectPath)
		}
	}
	project := mustGetWorkspace(t, fixture.store, id)
	folderRoot, err := fixture.handler.workspaceStore.GetFolderPath(id)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := agentworkspace.ResolveProjectEntry(project, folderRoot)
	if err != nil || resolved.AbsolutePath != filepath.Join(external, "Night Drive.demo") {
		t.Fatalf("resolved entry = %#v err=%v", resolved, err)
	}
	// Nothing from the scaffold was copied into the workspace folder either.
	if _, err := os.Stat(filepath.Join(folderRoot, "Night Drive")); !os.IsNotExist(err) {
		t.Fatalf("attach scaffolded a project folder: %v", err)
	}
}

func TestCreateWorkspaceAttachesExistingProjectInsideTheRequiredGroup(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyRequired))
	external := existingProjectFolder(t, map[string]string{"Night Drive.demo": "original project"})
	before := folderChecksum(t, external)
	homeID := preparePolicyHome(t, fixture.handler)
	token, err := fixture.selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	payload := attachPayload(fixture.template.ID, token)
	payload["group_composition"] = "grouped"

	code, body := reviewAndCreate(t, fixture.handler, payload, "attach-grouped")
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, body)
	}
	id := body["folder"].(map[string]any)["id"].(string)
	project := mustGetWorkspace(t, fixture.store, id)
	if link := project.GetAssistantProjectLink(); project.ParentID != homeID || link == nil || link.StationWorkspaceID != homeID {
		t.Fatalf("attached project placement = parent %q link %#v, Home %q", project.ParentID, link, homeID)
	}
	// Group finalization rewrites the record; the attach must survive it.
	for _, ws := range []*agentworkspace.Workspace{project, canonicalWorkspace(t, fixture.store, id)} {
		if _, reference := attachedReference(t, ws); reference.Path != external {
			t.Fatalf("attached reference = %#v", reference)
		}
	}
	if after := folderChecksum(t, external); !reflect.DeepEqual(after, before) {
		t.Fatalf("attach changed the user's folder")
	}
}

func TestCreateWorkspaceAttachRecordsWhatTheGuidedJourneyRecords(t *testing.T) {
	external := existingProjectFolder(t, map[string]string{"Night Drive.demo": "original project"})

	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	token, err := fixture.selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	code, body := reviewAndCreate(t, fixture.handler, attachPayload(fixture.template.ID, token), "attach-parity")
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, body)
	}
	modal := canonicalWorkspace(t, fixture.store, body["folder"].(map[string]any)["id"].(string))

	// The guided journey attaches the same folder in a separate profile.
	folders, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	journeyStore := agentworkspace.NewSyncStore(agentworkspace.NewInMemoryStore(), folders)
	journeySelections := pathselection.NewStore()
	service := projectconnection.NewService(journeyStore, journeySelections)
	journeyToken, err := journeySelections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	journeyTemplate := attachTemplate(projecttemplates.GroupPolicyNone)
	journeyTemplate.GroupRequirement = nil
	journeyTemplate.AssistantProgram = journeyAssistantProgram()
	scope := projectconnection.Scope{OwnerUserID: "local", RunID: "parity-run", Template: journeyTemplate}
	request := projectconnection.Request{
		ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: journeyToken, WorkspaceName: "Night Drive",
	}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	journey, err := journeyStore.GetFolderWorkspace(result.ProjectWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}

	modalLocator, modalReference := attachedReference(t, modal)
	journeyLocator, journeyReference := attachedReference(t, journey)
	// IDs differ by construction (the journey derives them from its run); every
	// other stored fact must match.
	modalLocator.DirectoryReferenceID, journeyLocator.DirectoryReferenceID = "", ""
	if *modalLocator != *journeyLocator {
		t.Fatalf("locators differ: modal %#v journey %#v", modalLocator, journeyLocator)
	}
	if modalReference.Path != journeyReference.Path || modalReference.Name != journeyReference.Name ||
		modalReference.Purpose != journeyReference.Purpose || modalReference.WorkspaceID != modal.ID || journeyReference.WorkspaceID != journey.ID {
		t.Fatalf("references differ: modal %#v journey %#v", modalReference, journeyReference)
	}
	if _, legacy := modal.SharedData[agentworkspace.ProjectEntryPathKey]; legacy {
		t.Fatalf("modal attach kept a managed entry path: %#v", modal.SharedData)
	}
}

func TestCreateWorkspaceRefusesInvalidProjectConnectionBeforeCreatingAnything(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	unsupported := newAttachFixture(t, policyTemplate(projecttemplates.GroupPolicyNone))
	single := existingProjectFolder(t, map[string]string{"Night Drive.demo": "project"})
	several := existingProjectFolder(t, map[string]string{"Take One.demo": "one", "Take Two.demo": "two"})
	empty := existingProjectFolder(t, map[string]string{"notes.txt": "no project here"})
	issue := func(folder string) string {
		token, err := fixture.selections.Issue(folder)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	connection := func(token string, extra map[string]any) map[string]any {
		value := map[string]any{"mode_id": "existing_project", "selection_token": token}
		for key, item := range extra {
			value[key] = item
		}
		return value
	}
	checksums := map[string]map[string]string{
		single: folderChecksum(t, single), several: folderChecksum(t, several), empty: folderChecksum(t, empty),
	}

	cases := []struct {
		name    string
		fixture attachFixture
		payload map[string]any
		want    string
	}{
		{"blank blueprint", fixture, map[string]any{
			"name": "Blank Attach", "blank": true, "project_connection": connection(issue(single), nil),
		}, "Choose a blueprint"},
		{"group", fixture, map[string]any{
			"name": "Group Attach", "kind": "group", "project_connection": connection(issue(single), nil),
		}, "not a group"},
		{"template path", fixture, map[string]any{
			"name": "Path Attach", "template_path": t.TempDir(), "project_connection": connection(issue(single), nil),
		}, "library"},
		{"blueprint without existing projects", unsupported, map[string]any{
			"name": "Unsupported Attach", "template_id": unsupported.template.ID, "group_composition": "standalone",
			"project_connection": connection(issue(single), nil),
		}, "does not support"},
		{"wrong mode", fixture, map[string]any{
			"name": "Mode Attach", "template_id": fixture.template.ID, "group_composition": "standalone",
			"project_connection": map[string]any{"mode_id": "new_project", "selection_token": issue(single)},
		}, "mode_id"},
		{"project name", fixture, map[string]any{
			"name": "Named Attach", "template_id": fixture.template.ID, "group_composition": "standalone",
			"project_name": "Night Drive", "project_connection": connection(issue(single), nil),
		}, "project_name"},
		{"raw path instead of a token", fixture, map[string]any{
			"name": "Raw Attach", "template_id": fixture.template.ID, "group_composition": "standalone",
			"project_connection": connection(single, nil),
		}, "Choose the project folder again"},
		{"no project file", fixture, map[string]any{
			"name": "Empty Attach", "template_id": fixture.template.ID, "group_composition": "standalone",
			"project_connection": connection(issue(empty), nil),
		}, "ending in .demo"},
		{"several project files", fixture, map[string]any{
			"name": "Several Attach", "template_id": fixture.template.ID, "group_composition": "standalone",
			"project_connection": connection(issue(several), nil),
		}, "several project files"},
		{"requested file not in folder", fixture, map[string]any{
			"name": "Missing Attach", "template_id": fixture.template.ID, "group_composition": "standalone",
			"project_connection": connection(issue(several), map[string]any{"entry_name": "Take Three.demo"}),
		}, "not in the chosen folder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixture.handler == unsupported.handler {
				// Tokens are issued by the fixture's own picker store.
				tc.fixture.handler.SetTrustedPathSelectionResolver(fixture.selections)
			}
			code, body := postPolicyWorkspace(t, tc.fixture.handler, tc.payload)
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v", code, body)
			}
			if message, _ := body["error"].(string); !strings.Contains(message, tc.want) {
				t.Fatalf("error %q does not mention %q", message, tc.want)
			}
			if ids, _ := tc.fixture.store.List(); len(ids) != 0 {
				t.Fatalf("refused request created workspaces: %v", ids)
			}
			if folders := workspaceFolders(t, tc.fixture.root); len(folders) != 0 {
				t.Fatalf("refused request created workspace folders: %v", folders)
			}
		})
	}
	for folder, before := range checksums {
		if after := folderChecksum(t, folder); !reflect.DeepEqual(after, before) {
			t.Fatalf("refusals changed %s", folder)
		}
	}
}

func TestImportFolderRefusesProjectConnection(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	folder := existingProjectFolder(t, map[string]string{"Night Drive.demo": "project"})
	encoded, err := json.Marshal(map[string]any{
		"path": folder, "project_connection": map[string]any{"mode_id": "existing_project", "selection_token": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	fixture.handler.HandleWorkspaces(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Import Folder") {
		t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
	}
	if ids, _ := fixture.store.List(); len(ids) != 0 {
		t.Fatalf("refused import created workspaces: %v", ids)
	}
}

func TestCreateWorkspaceAttachRollsBackWhenTheFolderChangesBeforeRecording(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	external := existingProjectFolder(t, map[string]string{"Night Drive.demo": "original project"})
	token, err := fixture.selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	payload := attachPayload(fixture.template.ID, token)
	review := map[string]any{"group_requirement_review": true}
	for field, value := range payload {
		review[field] = value
	}
	code, body := postPolicyWorkspace(t, fixture.handler, review)
	if code != http.StatusOK {
		t.Fatalf("review status=%d body=%v", code, body)
	}
	receipt := body["group_requirement_review"].(map[string]any)

	// The workspace root is resolved after validation and after the workspace
	// row exists, just before its folder is saved: the last moment another
	// program could add a second project file.
	fixture.handler.SetWorkspaceRootResolver(func() string {
		if err := os.WriteFile(filepath.Join(external, "Night Drive (autosave).demo"), []byte("new"), 0o600); err != nil {
			t.Error(err)
		}
		return ""
	})
	payload["group_review_token"] = receipt["review_token"]
	payload["idempotency_key"] = "attach-changed"
	code, body = postPolicyWorkspace(t, fixture.handler, payload)
	if code != http.StatusConflict {
		t.Fatalf("changed-folder status=%d body=%v", code, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "changed") {
		t.Fatalf("changed-folder error = %q", message)
	}
	if ids, _ := fixture.store.List(); len(ids) != 0 {
		t.Fatalf("failed attach left workspaces: %v", ids)
	}
	if folders := workspaceFolders(t, fixture.root); len(folders) != 0 {
		t.Fatalf("failed attach left workspace folders: %v", folders)
	}
	if content, err := os.ReadFile(filepath.Join(external, "Night Drive.demo")); err != nil || string(content) != "original project" {
		t.Fatalf("user's project file changed: %q err=%v", content, err)
	}
}

// workspaceFolders lists the folders under a workspace root, ignoring the
// folder store's own index files.
func workspaceFolders(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var folders []string
	for _, entry := range entries {
		if entry.IsDir() {
			folders = append(folders, entry.Name())
		}
	}
	return folders
}

func postProjectConnectionReview(t *testing.T, handler *Handler, payload map[string]any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/project-connection/review", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ReviewProjectConnection(response, request)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode review response %d: %v: %s", response.Code, err, response.Body.String())
	}
	return response.Code, body
}

func stringList(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.(string))
	}
	return out
}

func TestReviewProjectConnectionReadsTheFolderAndCreatesNothing(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	unsupported := newAttachFixture(t, policyTemplate(projecttemplates.GroupPolicyNone))
	single := existingProjectFolder(t, map[string]string{"Night Drive.demo": "project", "notes.txt": "notes"})
	several := existingProjectFolder(t, map[string]string{"Take Two.demo": "two", "Take One.demo": "one"})
	empty := existingProjectFolder(t, map[string]string{"notes.txt": "no project here"})
	before := map[string]map[string]string{
		single: folderChecksum(t, single), several: folderChecksum(t, several), empty: folderChecksum(t, empty),
	}
	issue := func(folder string) string {
		token, err := fixture.selections.Issue(folder)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	review := func(extra map[string]any) (int, map[string]any) {
		payload := map[string]any{"template_id": fixture.template.ID}
		for key, value := range extra {
			payload[key] = value
		}
		return postProjectConnectionReview(t, fixture.handler, payload)
	}

	code, body := review(map[string]any{"selection_token": issue(single)})
	if code != http.StatusOK || body["entry_name"] != "Night Drive.demo" || body["duplicate"] != nil {
		t.Fatalf("one project file: status=%d body=%v", code, body)
	}
	if !reflect.DeepEqual(stringList(body["entry_candidates"]), []string{"Night Drive.demo"}) ||
		!reflect.DeepEqual(stringList(body["entry_extensions"]), []string{".demo"}) {
		t.Fatalf("one project file lists = %v", body)
	}
	if folder, _ := body["selected_folder"].(string); folder == "" {
		t.Fatalf("selected_folder missing: %v", body)
	}

	severalToken := issue(several)
	code, body = review(map[string]any{"selection_token": severalToken})
	if code != http.StatusOK || body["entry_name"] != "" ||
		!reflect.DeepEqual(stringList(body["entry_candidates"]), []string{"Take One.demo", "Take Two.demo"}) {
		t.Fatalf("several project files: status=%d body=%v", code, body)
	}
	code, body = review(map[string]any{"selection_token": severalToken, "entry_name": "Take Two.demo"})
	if code != http.StatusOK || body["entry_name"] != "Take Two.demo" {
		t.Fatalf("chosen project file: status=%d body=%v", code, body)
	}

	for name, tc := range map[string]struct {
		handler *Handler
		payload map[string]any
		want    string
	}{
		"requested file not in folder": {fixture.handler, map[string]any{
			"template_id": fixture.template.ID, "selection_token": severalToken, "entry_name": "Take Three.demo",
		}, "not in the chosen folder"},
		"no project file": {fixture.handler, map[string]any{
			"template_id": fixture.template.ID, "selection_token": issue(empty),
		}, "ending in .demo"},
		"expired token": {fixture.handler, map[string]any{
			"template_id": fixture.template.ID, "selection_token": "not-a-picker-token",
		}, "Choose the project folder again"},
		"missing blueprint": {fixture.handler, map[string]any{"selection_token": issue(single)}, "Choose a blueprint"},
		"blueprint without existing projects": {unsupported.handler, map[string]any{
			"template_id": unsupported.template.ID, "selection_token": issue(single),
		}, "does not support"},
	} {
		t.Run(name, func(t *testing.T) {
			tc.handler.SetTrustedPathSelectionResolver(fixture.selections)
			code, body := postProjectConnectionReview(t, tc.handler, tc.payload)
			message, _ := body["error"].(string)
			if code != http.StatusBadRequest || !strings.Contains(message, tc.want) {
				t.Fatalf("status=%d body=%v, want 400 mentioning %q", code, body, tc.want)
			}
		})
	}

	for _, handlerFixture := range []attachFixture{fixture, unsupported} {
		if ids, _ := handlerFixture.store.List(); len(ids) != 0 {
			t.Fatalf("review created workspaces: %v", ids)
		}
		if folders := workspaceFolders(t, handlerFixture.root); len(folders) != 0 {
			t.Fatalf("review created workspace folders: %v", folders)
		}
	}
	for folder, checksum := range before {
		if after := folderChecksum(t, folder); !reflect.DeepEqual(after, checksum) {
			t.Fatalf("review changed %s", folder)
		}
	}
}

func TestReviewProjectConnectionReportsEveryKindOfOwner(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	reviewDuplicate := func(folder string) map[string]any {
		t.Helper()
		token, err := fixture.selections.Issue(folder)
		if err != nil {
			t.Fatal(err)
		}
		code, body := postProjectConnectionReview(t, fixture.handler, map[string]any{
			"template_id": fixture.template.ID, "selection_token": token,
		})
		if code != http.StatusOK {
			t.Fatalf("review status=%d body=%v", code, body)
		}
		duplicate, _ := body["duplicate"].(map[string]any)
		if duplicate == nil || body["duplicate_message"] == nil {
			t.Fatalf("no duplicate reported for %s: %v", folder, body)
		}
		return duplicate
	}

	// Attached from this modal.
	modalFolder := existingProjectFolder(t, map[string]string{"Night Drive.demo": "project"})
	token, err := fixture.selections.Issue(modalFolder)
	if err != nil {
		t.Fatal(err)
	}
	code, body := reviewAndCreate(t, fixture.handler, attachPayload(fixture.template.ID, token), "owner-modal")
	if code != http.StatusCreated {
		t.Fatalf("modal attach status=%d body=%v", code, body)
	}
	modalID := body["folder"].(map[string]any)["id"].(string)
	if duplicate := reviewDuplicate(modalFolder); duplicate["workspace_id"] != modalID || duplicate["name"] != "Night Drive" {
		t.Fatalf("modal owner = %v", duplicate)
	}

	// Attached by the guided journey, into the same store.
	journeyFolder := existingProjectFolder(t, map[string]string{"Journey Song.demo": "project"})
	journeyTemplate := attachTemplate(projecttemplates.GroupPolicyNone)
	journeyTemplate.GroupRequirement = nil
	journeyTemplate.AssistantProgram = journeyAssistantProgram()
	journeySelections := pathselection.NewStore()
	service := projectconnection.NewService(fixture.store.(interface {
		agentworkspace.Store
		GetFolderPath(string) (string, error)
	}), journeySelections)
	journeyToken, err := journeySelections.Issue(journeyFolder)
	if err != nil {
		t.Fatal(err)
	}
	scope := projectconnection.Scope{OwnerUserID: "local", RunID: "owner-journey", Template: journeyTemplate}
	request := projectconnection.Request{
		ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: journeyToken, WorkspaceName: "Journey Song",
	}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(context.Background(), scope, request, preview.InputDigest, preview.OwnerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate := reviewDuplicate(journeyFolder); duplicate["workspace_id"] != result.ProjectWorkspaceID {
		t.Fatalf("journey owner = %v, want %s", duplicate, result.ProjectWorkspaceID)
	}

	// Adopted with Import Folder.
	importFolder := existingProjectFolder(t, map[string]string{"Imported Song.demo": "project"})
	encoded, err := json.Marshal(map[string]any{"path": importFolder, "name": "Imported Song"})
	if err != nil {
		t.Fatal(err)
	}
	importRequest := httptest.NewRequest(http.MethodPost, "/api/workspaces/import", bytes.NewReader(encoded))
	importRequest.Header.Set("Content-Type", "application/json")
	importResponse := httptest.NewRecorder()
	fixture.handler.HandleWorkspaces(importResponse, importRequest)
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", importResponse.Code, importResponse.Body.String())
	}
	if duplicate := reviewDuplicate(importFolder); duplicate["name"] != "Imported Song" {
		t.Fatalf("import owner = %v", duplicate)
	}
}

func TestCreateWorkspaceRefusesAFolderClaimedAfterItsReview(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	external := existingProjectFolder(t, map[string]string{"Night Drive.demo": "project"})
	before := folderChecksum(t, external)
	firstToken, err := fixture.selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	secondToken, err := fixture.selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}

	// Both tabs review the free folder.
	reviewed := func(token, key string) map[string]any {
		payload := attachPayload(fixture.template.ID, token)
		review := map[string]any{"group_requirement_review": true}
		for field, value := range payload {
			review[field] = value
		}
		code, body := postPolicyWorkspace(t, fixture.handler, review)
		if code != http.StatusOK {
			t.Fatalf("review status=%d body=%v", code, body)
		}
		payload["group_review_token"] = body["group_requirement_review"].(map[string]any)["review_token"]
		payload["idempotency_key"] = key
		return payload
	}
	first := reviewed(firstToken, "claim-first")
	second := reviewed(secondToken, "claim-second")

	code, body := postPolicyWorkspace(t, fixture.handler, first)
	if code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%v", code, body)
	}
	firstID := body["folder"].(map[string]any)["id"].(string)

	code, body = postPolicyWorkspace(t, fixture.handler, second)
	duplicate, _ := body["duplicate"].(map[string]any)
	if code != http.StatusConflict || duplicate == nil || duplicate["workspace_id"] != firstID {
		t.Fatalf("second create status=%d body=%v", code, body)
	}
	if ids, _ := fixture.store.List(); len(ids) != 1 || ids[0] != firstID {
		t.Fatalf("refused create left workspaces: %v", ids)
	}
	if folders := workspaceFolders(t, fixture.root); len(folders) != 1 {
		t.Fatalf("refused create left workspace folders: %v", folders)
	}
	if after := folderChecksum(t, external); !reflect.DeepEqual(after, before) {
		t.Fatalf("user's folder changed")
	}
}

func TestCreateWorkspaceRechecksOwnershipImmediatelyBeforeRecording(t *testing.T) {
	fixture := newAttachFixture(t, attachTemplate(projecttemplates.GroupPolicyNone))
	external := existingProjectFolder(t, map[string]string{"Night Drive.demo": "project"})
	token, err := fixture.selections.Issue(external)
	if err != nil {
		t.Fatal(err)
	}
	payload := attachPayload(fixture.template.ID, token)
	review := map[string]any{"group_requirement_review": true}
	for field, value := range payload {
		review[field] = value
	}
	code, body := postPolicyWorkspace(t, fixture.handler, review)
	if code != http.StatusOK {
		t.Fatalf("review status=%d body=%v", code, body)
	}
	payload["group_review_token"] = body["group_requirement_review"].(map[string]any)["review_token"]
	payload["idempotency_key"] = "recheck"

	// Another owner appears after validation, while this create is running.
	var claimant *agentworkspace.Workspace
	fixture.handler.SetWorkspaceRootResolver(func() string {
		if claimant == nil {
			claimant = agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Imported Elsewhere"})
			claimant.SharedData = map[string]any{"folder_import": map[string]any{"enabled": true, "path": external}}
			if err := fixture.store.Save(claimant); err != nil {
				t.Error(err)
			}
		}
		return ""
	})
	code, body = postPolicyWorkspace(t, fixture.handler, payload)
	duplicate, _ := body["duplicate"].(map[string]any)
	if code != http.StatusConflict || duplicate == nil || duplicate["workspace_id"] != claimant.ID {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if ids, _ := fixture.store.List(); len(ids) != 1 || ids[0] != claimant.ID {
		t.Fatalf("refused create left workspaces: %v", ids)
	}
}

func mustGetWorkspace(t *testing.T, store agentworkspace.Store, id string) *agentworkspace.Workspace {
	t.Helper()
	ws, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// journeyAssistantProgram is the legacy (no group requirement) program the
// guided journey needs to create a Home for an attach.
func journeyAssistantProgram() *agentworkspace.AssistantProgramDeclaration {
	return &agentworkspace.AssistantProgramDeclaration{
		SchemaVersion: agentworkspace.AssistantProgramSchemaVersion, ID: "neutral-program",
		StationName: "Neutral Program Home", DefaultPrimaryName: "Guide", HireTitle: "Hire guide",
		Roles: []agentworkspace.AssistantProgramRoleSpec{
			{ID: "guide", Label: "Guide", Scope: agentworkspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate."},
			{ID: "reviewer", Label: "Reviewer", Scope: agentworkspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Review."},
		},
		Stages: []agentworkspace.AssistantProgramStageSpec{{ID: "initial", Label: "Initial", AcceptedCompletionThreshold: 0}},
		Reflection: agentworkspace.AssistantReflectionConfig{
			MinimumProjects: 2, CadenceHours: 24, MaxProjects: 8, MaxEventsPerProject: 16, MaxCandidates: 4, MaxEvidence: 4, Rubric: "Review patterns.",
		},
	}
}
