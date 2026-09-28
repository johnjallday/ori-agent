package sessionhttp

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

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/testdb"
	"github.com/johnjallday/ori-agent/internal/types"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// renameFixtureCase is what each kind-builder hands back to the shared
// PATCH -> folder-save -> restart assertions below.
type renameFixtureCase struct {
	handler    *Handler
	store      agentworkspace.Store // the primary/SyncStore view used to trigger a folder save
	targetID   string               // the workspace the test renames
	targetName string               // its display name before the rename
	oldSlug    string
	childID    string // a nested (group member) or linked (assistant project) child, "" if none
	programKey *agentworkspace.AssistantProgramKey
}

// newUpdateRenameHandler builds a Handler backed by an isolated SQLite store
// (the "primary") that write-throughs to a FileStore rooted at root via a
// SyncStore, mirroring the production wiring that turns a SQLite-only rename
// into a second folder on disk.
func newUpdateRenameHandler(t *testing.T, root string) (*Handler, *agentworkspace.SyncStore) {
	t.Helper()
	db := testdb.Open(t)
	hybrid := session.NewHybridStoreWithDB(db, 20)
	cleanupTestHybridStore(t, hybrid)

	fileStore, err := agentworkspace.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	primary := session.NewWorkspaceStoreAdapter(hybrid)
	syncStore := agentworkspace.NewSyncStore(primary, fileStore)

	handler := New(hybrid)
	agentStorePath := filepath.Join(t.TempDir(), "agents.json")
	agentStore, err := agentstore.NewFileStore(agentStorePath, types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	handler.SetAgentStore(agentStore)
	handler.SetWorkspaceStore(fileStore)
	handler.SetWorkspaceTaskStore(syncStore)

	return handler, syncStore
}

func buildPlainRenameFixture(t *testing.T, root string) renameFixtureCase {
	t.Helper()
	handler, syncStore := newUpdateRenameHandler(t, root)
	const name = "Plain Fixture"
	id := createTestWorkspace(t, handler, name)
	return renameFixtureCase{
		handler: handler, store: syncStore,
		targetID: id, targetName: name, oldSlug: agentworkspace.Slugify(name),
	}
}

func buildGroupRenameFixture(t *testing.T, root string) renameFixtureCase {
	t.Helper()
	handler, syncStore := newUpdateRenameHandler(t, root)
	const name = "Fixture Group"
	groupID := createTestGroup(t, handler, name)
	childID := createTestWorkspace(t, handler, "Nested Member")
	patchWorkspaceParent(t, handler, childID, groupID, http.StatusOK)
	return renameFixtureCase{
		handler: handler, store: syncStore,
		targetID: groupID, targetName: name, oldSlug: agentworkspace.Slugify(name), childID: childID,
	}
}

func buildHomeRenameFixture(t *testing.T, root string) renameFixtureCase {
	t.Helper()
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, _ := newPolicyHandlerAt(t, &template, root)

	homeID := preparePolicyHome(t, handler)
	homeBefore, err := store.Get(homeID)
	if err != nil {
		t.Fatalf("load prepared Home: %v", err)
	}
	homeState := homeBefore.GetAssistantProgramState()
	if homeState == nil {
		t.Fatalf("prepared Home has no assistant program state: %#v", homeBefore)
	}
	programKey := homeState.Key

	// Link a project to the Home so the fixture has a physically nested child,
	// the same way ensureProjectNesting places a project's folder inside its
	// station's folder.
	payload := reviewPayload("Linked Child", false)
	code, body := postPolicyWorkspace(t, handler, payload)
	if code != http.StatusOK {
		t.Fatalf("linked-child review status=%d body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	delete(payload, "group_requirement_review")
	payload["group_review_token"] = review["review_token"]
	payload["idempotency_key"] = "linked-child"
	code, body = postPolicyWorkspace(t, handler, payload)
	if code != http.StatusCreated {
		t.Fatalf("linked-child commit status=%d body=%v", code, body)
	}
	childID := body["folder"].(map[string]any)["id"].(string)

	return renameFixtureCase{
		handler: handler, store: store,
		targetID: homeID, targetName: homeBefore.Name, oldSlug: homeBefore.FolderSlug,
		childID: childID, programKey: &programKey,
	}
}

func patchWorkspaceName(t *testing.T, handler *Handler, id, name string, wantCode int) {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+id, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	if w.Code != wantCode {
		t.Fatalf("PATCH name on %s: got %d, want %d: %s", id, w.Code, wantCode, w.Body.String())
	}
}

type workspaceJSONRecord struct {
	path string
	ws   *agentworkspace.Workspace
}

// collectWorkspaceJSONFiles walks root and parses every workspace.json found,
// regardless of which folder it lives in - the duplicate-folder bug is
// exactly that a second workspace.json for the same ID appears somewhere
// under root.
func collectWorkspaceJSONFiles(t *testing.T, root string) []workspaceJSONRecord {
	t.Helper()
	var records []workspaceJSONRecord
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || info.Name() != agentworkspace.WorkspaceConfigFile {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		ws, parseErr := agentworkspace.FromJSON(data)
		if parseErr != nil {
			return parseErr
		}
		records = append(records, workspaceJSONRecord{path: path, ws: ws})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return records
}

func assertOneWorkspaceJSONPerID(t *testing.T, root string) {
	t.Helper()
	records := collectWorkspaceJSONFiles(t, root)
	byID := make(map[string][]string)
	for _, rec := range records {
		byID[rec.ws.ID] = append(byID[rec.ws.ID], rec.path)
	}
	for id, paths := range byID {
		if len(paths) != 1 {
			t.Errorf("workspace %s has %d workspace.json files, want 1: %v", id, len(paths), paths)
		}
	}
}

func assertOneWorkspaceJSONPerProgramKey(t *testing.T, root string, key agentworkspace.AssistantProgramKey) {
	t.Helper()
	records := collectWorkspaceJSONFiles(t, root)
	var matches []string
	for _, rec := range records {
		if rec.ws.AssistantProgramState != nil && rec.ws.AssistantProgramState.Key == key {
			matches = append(matches, rec.path)
		}
	}
	if len(matches) != 1 {
		t.Errorf("assistant program key %+v has %d workspace.json files, want 1: %v", key, len(matches), matches)
	}
}

// TestUpdateWorkspaceNameRenamesFolder is the regression test for #485: the
// generic item update, PUT/PATCH /api/workspaces/{id} with a "name" field,
// must move the workspace's folder the same way POST .../rename does. Before
// the fix it only rewrote FolderSlug in SQLite, so the next folder save
// (SyncStore.Save, e.g. from a role fill) wrote a second folder under the new
// slug while the old one - carrying the same workspace ID and, for an
// Assistant Program Home, the same program key - was left behind on disk.
func TestUpdateWorkspaceNameRenamesFolder(t *testing.T) {
	roots := []struct {
		name  string
		build func(tmp string) string
	}{
		{"confirmed", func(tmp string) string { return filepath.Join(tmp, "Ori Workspaces") }},
		{"staging", func(tmp string) string { return filepath.Join(tmp, "data", "workspace-staging") }},
	}
	kinds := []struct {
		name  string
		build func(t *testing.T, root string) renameFixtureCase
	}{
		{"plain-workspace", buildPlainRenameFixture},
		{"general-group", buildGroupRenameFixture},
		{"assistant-program-home", buildHomeRenameFixture},
	}

	for _, rootCase := range roots {
		for _, kindCase := range kinds {
			t.Run(rootCase.name+"/"+kindCase.name, func(t *testing.T) {
				tmp := t.TempDir()
				root := rootCase.build(tmp)
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatal(err)
				}

				fx := kindCase.build(t, root)
				newName := fx.targetName + " Renamed"
				newSlug := agentworkspace.Slugify(newName)

				patchWorkspaceName(t, fx.handler, fx.targetID, newName, http.StatusOK)

				// Trigger the write that produces the second folder: load the
				// record from the primary store (its FolderSlug is already the
				// new slug after the buggy update) and run it through the same
				// SyncStore.Save a role fill or other folder-tracked mutation
				// would use.
				ws, err := fx.store.Get(fx.targetID)
				if err != nil {
					t.Fatalf("load renamed workspace from primary store: %v", err)
				}
				if err := fx.store.Save(ws); err != nil {
					t.Fatalf("trigger folder save: %v", err)
				}

				// Simulate a restart: reopen the FileStore fresh against the
				// same root so nothing is served from an in-memory cache.
				restarted, err := agentworkspace.NewFileStore(root)
				if err != nil {
					t.Fatalf("reopen FileStore: %v", err)
				}

				assertOneWorkspaceJSONPerID(t, root)
				if fx.programKey != nil {
					assertOneWorkspaceJSONPerProgramKey(t, root, *fx.programKey)
				}

				newPath, err := restarted.GetFolderPath(fx.targetID)
				if err != nil {
					t.Fatalf("renamed workspace missing after restart: %v", err)
				}
				if filepath.Base(newPath) != newSlug {
					t.Errorf("folder = %q, want slug %q", newPath, newSlug)
				}
				oldPath := filepath.Join(filepath.Dir(newPath), fx.oldSlug)
				if fx.oldSlug != newSlug {
					if _, statErr := os.Stat(oldPath); !os.IsNotExist(statErr) {
						t.Errorf("old folder %s still present after restart, err=%v", oldPath, statErr)
					}
				}

				restartedWS, err := restarted.Get(fx.targetID)
				if err != nil {
					t.Fatalf("read renamed workspace.json after restart: %v", err)
				}
				if restartedWS.Name != newName {
					t.Errorf("workspace.json name = %q, want %q", restartedWS.Name, newName)
				}

				if fx.childID != "" {
					childPath, err := restarted.GetFolderPath(fx.childID)
					if err != nil {
						t.Fatalf("nested/linked child missing after restart: %v", err)
					}
					if !strings.HasPrefix(childPath, newPath+string(os.PathSeparator)) {
						t.Errorf("child %s is not nested under renamed parent %s", childPath, newPath)
					}
				}

				// Protected Assistant Program topology must still hold after the
				// Home was renamed: disconnecting the linked child through the
				// generic update's parent_id field is still rejected, and the
				// child stays nested under the renamed Home.
				if fx.programKey != nil {
					code, respBody := patchWorkspaceParentBody(t, fx.handler, fx.childID, "")
					if code != http.StatusConflict {
						t.Fatalf("unlink linked child after Home rename: got %d, want 409: %s", code, respBody)
					}
					if !strings.Contains(respBody, "Assistant Program membership must be reviewed") {
						t.Errorf("unlink conflict body = %q, want the protected-topology message", respBody)
					}
					childPath, err := restarted.GetFolderPath(fx.childID)
					if err != nil {
						t.Fatalf("linked child missing after protected-topology check: %v", err)
					}
					if !strings.HasPrefix(childPath, newPath+string(os.PathSeparator)) {
						t.Errorf("linked child %s no longer nested under renamed Home %s", childPath, newPath)
					}
				}
			})
		}
	}

	t.Run("edge-case/slug-unchanged-updates-display-name-only", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "Ori Workspaces")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		handler, syncStore := newUpdateRenameHandler(t, root)
		const name = "Same Slug Workspace"
		id := createTestWorkspace(t, handler, name)
		oldPath, err := syncStore.FileStore().GetFolderPath(id)
		if err != nil {
			t.Fatalf("GetFolderPath before rename: %v", err)
		}

		// A different casing slugifies to the exact same slug, so this must
		// change only the display name - no folder move.
		newName := strings.ToUpper(name)
		patchWorkspaceName(t, handler, id, newName, http.StatusOK)

		newPath, err := syncStore.FileStore().GetFolderPath(id)
		if err != nil {
			t.Fatalf("GetFolderPath after rename: %v", err)
		}
		if filepath.Clean(newPath) != filepath.Clean(oldPath) {
			t.Fatalf("folder path changed on a slug-preserving rename: %q -> %q", oldPath, newPath)
		}
		ws, err := syncStore.FileStore().Get(id)
		if err != nil {
			t.Fatalf("read workspace.json: %v", err)
		}
		if ws.Name != newName {
			t.Errorf("workspace.json name = %q, want %q", ws.Name, newName)
		}
	})

	t.Run("edge-case/sibling-slug-conflict-leaves-name-and-folder-unchanged", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "Ori Workspaces")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		handler, syncStore := newUpdateRenameHandler(t, root)
		const originalName = "Sibling Beta"
		id := createTestWorkspace(t, handler, originalName)
		_ = createTestWorkspace(t, handler, "Sibling Alpha")

		patchWorkspaceName(t, handler, id, "Sibling Alpha", http.StatusConflict)

		ws, err := handler.store.GetWorkspace(context.Background(), id)
		if err != nil {
			t.Fatalf("GetWorkspace after conflict: %v", err)
		}
		if ws.Name != originalName || ws.FolderSlug != agentworkspace.Slugify(originalName) {
			t.Errorf("workspace mutated by a rejected rename: name=%q slug=%q", ws.Name, ws.FolderSlug)
		}
		if _, err := syncStore.FileStore().GetFolderPath(id); err != nil {
			t.Fatalf("original folder missing after rejected rename: %v", err)
		}
		if owner, err := syncStore.FileStore().ResolveSlug(agentworkspace.Slugify(originalName)); err != nil || owner.ID != id {
			t.Errorf("folder for slug %q = %v/%v, want unchanged owner %s", agentworkspace.Slugify(originalName), owner, err, id)
		}
	})

	t.Run("edge-case/name-and-parent-id-in-one-request", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "Ori Workspaces")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		handler, syncStore := newUpdateRenameHandler(t, root)
		groupID := createTestGroup(t, handler, "Combo Group")
		itemID := createTestWorkspace(t, handler, "Combo Item")

		encoded, err := json.Marshal(map[string]any{"name": "Combo Item Renamed", "parent_id": groupID})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+itemID, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.HandleWorkspaces(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("combined name+parent_id PATCH: got %d, want 200: %s", w.Code, w.Body.String())
		}

		groupPath, err := syncStore.FileStore().GetFolderPath(groupID)
		if err != nil {
			t.Fatalf("GetFolderPath group: %v", err)
		}
		wantPath := filepath.Join(groupPath, agentworkspace.SubWorkspacesDir, "combo-item-renamed")
		gotPath, err := syncStore.FileStore().GetFolderPath(itemID)
		if err != nil {
			t.Fatalf("GetFolderPath item: %v", err)
		}
		if filepath.Clean(gotPath) != filepath.Clean(wantPath) {
			t.Fatalf("item path = %q, want %q", gotPath, wantPath)
		}
		assertOneWorkspaceJSONPerID(t, root)
	})

	t.Run("edge-case/db-only-workspace-updates-without-a-folder-store", func(t *testing.T) {
		handler, cleanup := createTestHandler(t)
		defer cleanup()
		// No SetWorkspaceStore call: h.workspaceStore stays nil, matching a
		// SQLite-only workspace record with no backing folder to rename.
		const name = "DB Only Workspace"
		id := createTestWorkspace(t, handler, name)

		patchWorkspaceName(t, handler, id, "DB Only Workspace Renamed", http.StatusOK)

		ws, err := handler.store.GetWorkspace(context.Background(), id)
		if err != nil {
			t.Fatalf("GetWorkspace: %v", err)
		}
		if ws.Name != "DB Only Workspace Renamed" {
			t.Errorf("name = %q, want %q", ws.Name, "DB Only Workspace Renamed")
		}
	})
}

// patchWorkspaceParentBody is patchWorkspaceParent without a fixed expected
// status code, returning the response body so a caller can assert on the
// conflict message text.
func patchWorkspaceParentBody(t *testing.T, handler *Handler, id, parentID string) (int, string) {
	t.Helper()
	body := `{"parent_id":"` + parentID + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/workspaces/"+id, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleWorkspaces(w, req)
	return w.Code, w.Body.String()
}
