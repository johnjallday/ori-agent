package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func seedEditableInbox(t *testing.T, f *draftServerFixture) (map[string]any, string) {
	t.Helper()
	input, root := stageInboxPlacement(t, f)
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusCreated {
		t.Fatalf("seed: %d %v", status, response)
	}
	return response["session"].(map[string]any), root
}

func saveEditedInbox(t *testing.T, f *draftServerFixture, build map[string]any, monitoring bool) map[string]any {
	t.Helper()
	status, response := f.call(t, http.MethodPatch, "/api/workspaces/build-sessions/"+build["id"].(string)+"/draft", map[string]any{"version": build["version"], "draft": build["draft"], "monitoring": monitoring})
	if status != http.StatusOK {
		t.Fatalf("save: %d %v", status, response)
	}
	return response["session"].(map[string]any)
}

func reviewEditedInbox(t *testing.T, f *draftServerFixture, build map[string]any, monitoring bool) map[string]any {
	t.Helper()
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+build["id"].(string)+"/janitor-review", map[string]any{"version": build["version"], "monitoring": monitoring})
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, response)
	}
	return response["review"].(map[string]any)
}

func TestInboxPlacementEditing_ActualAlternateTeamAndGoalAreCreatedOnlyAfterReview(t *testing.T) {
	for _, mode := range []string{"assign", "create"} {
		t.Run(mode, func(t *testing.T) {
			f := newDraftServerFixture(t)
			const selected = "Archive Expert"
			var beforeProfile []byte
			if mode == "assign" {
				if err := f.builder.st.CreateAgent(selected, &store.CreateAgentConfig{Model: "saved-model", LLMProvider: "openai", SystemPrompt: "My global profile stays unchanged."}); err != nil {
					t.Fatal(err)
				}
				profile, _ := f.builder.st.GetAgent(selected)
				beforeProfile, _ = json.Marshal(profile.Settings)
			}
			profiles, workspaces := len(f.builder.st.ListAgents()), len(f.builder.workspaceFileStore.CachedWorkspaces())
			build, root := seedEditableInbox(t, f)
			draft := build["draft"].(map[string]any)
			draft["name"], draft["mission"] = "Edited inbox", "  음악 — exact\ninterior whitespace  "
			role := map[string]any{"role_id": "file-curator", "mode": mode, "name": selected}
			if mode == "create" {
				role["system_prompt"] = "My draft instructions."
			}
			draft["role_staffing"] = []any{role}
			build = saveEditedInbox(t, f, build, false)
			review := reviewEditedInbox(t, f, build, false)
			if review["workspace"].(map[string]any)["mission"] != "음악 — exact\ninterior whitespace" {
				t.Fatalf("goal changed: %v", review)
			}
			if review["team"].([]any)[0].(map[string]any)["name"] != selected {
				t.Fatal("default team replaced the user's choice")
			}
			if len(f.builder.st.ListAgents()) != profiles || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces {
				t.Fatal("editing/review created resources")
			}
			if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
				t.Fatal("editing granted the folder")
			}
			status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+build["id"].(string)+"/janitor-prepare", map[string]any{"version": build["version"], "monitoring": false, "review_revision": review["review_revision"]})
			if status != http.StatusOK {
				t.Fatalf("prepare: %d %v", status, response)
			}
			ws, err := f.builder.workspaceFileStore.Get(response["workspace_id"].(string))
			if err != nil || len(ws.AgentInstances) != 1 || ws.AgentInstances[0].Name != selected || ws.Mission != "음악 — exact\ninterior whitespace" {
				t.Fatalf("wrong actual workspace: %+v %v", ws, err)
			}
			source, err := f.builder.personalAssistantFolderDigest.StoredOffer(context.Background(), "local", build["placement_source"].(map[string]any)["offer_id"].(string))
			if err != nil || source.Outcome == nil || source.Outcome.WorkspaceID != ws.ID {
				t.Fatalf("missing edited completion: %+v %v", source, err)
			}
			assigned := false
			for _, row := range source.Outcome.Receipt {
				if row.Kind == "agent" {
					if row.Name != selected || !row.Assigned {
						t.Fatalf("completion substituted the default: %+v", row)
					}
					assigned = true
				}
			}
			if !assigned {
				t.Fatal("missing observed membership")
			}
			profile, ok := f.builder.st.GetAgent(selected)
			if !ok {
				t.Fatal("selected agent missing")
			}
			if mode == "assign" {
				after, _ := json.Marshal(profile.Settings)
				if string(beforeProfile) != string(after) || len(f.builder.st.ListAgents()) != profiles {
					t.Fatal("assignment changed or duplicated saved settings")
				}
			} else if len(f.builder.st.ListAgents()) != profiles+1 || profile.Settings.SystemPrompt != "My draft instructions." {
				t.Fatal("new draft did not create exactly one profile with its edited configuration")
			}
			if _, exists := f.builder.st.GetAgent("File Curator"); exists {
				t.Fatal("unused default was created")
			}
		})
	}
}

func TestInboxPlacementEditing_RemovedSavedAgentInvalidatesFinalConsentWithoutWrites(t *testing.T) {
	f := newDraftServerFixture(t)
	if err := f.builder.st.CreateAgent("Archive Expert", nil); err != nil {
		t.Fatal(err)
	}
	build, root := seedEditableInbox(t, f)
	build["draft"].(map[string]any)["role_staffing"] = []any{map[string]any{"role_id": "file-curator", "mode": "assign", "name": "Archive Expert"}}
	build = saveEditedInbox(t, f, build, false)
	review := reviewEditedInbox(t, f, build, false)
	if err := f.builder.st.DeleteAgent("Archive Expert"); err != nil {
		t.Fatal(err)
	}
	before, profiles := len(f.builder.workspaceFileStore.CachedWorkspaces()), len(f.builder.st.ListAgents())
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+build["id"].(string)+"/janitor-prepare", map[string]any{"version": build["version"], "monitoring": false, "review_revision": review["review_revision"]})
	if status < 400 {
		t.Fatalf("removed profile confirmed: %d %v", status, response)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("stale team created resources")
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("stale team granted a folder")
	}
}

// canonicalFolderPath is the path a selected folder is stored under: the
// picker resolves symlinks, and on macOS the fixture HOME sits under /var,
// which is a link to /private/var.
func canonicalFolderPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestInboxPlacementEditing_TrustedReplacementRetainsMonitoringAndRejectsIncompatibleOrReplacedRoots(t *testing.T) {
	f := newDraftServerFixture(t)
	build, _ := seedEditableInbox(t, f)
	oldReview := reviewEditedInbox(t, f, build, false)
	build = saveEditedInbox(t, f, build, true)
	id := build["id"].(string)
	initial := build["folder"].(map[string]any)["observation"].(map[string]any)["id"]
	before, profiles := len(f.builder.workspaceFileStore.CachedWorkspaces()), len(f.builder.st.ListAgents())
	pick := func(root string) (int, map[string]any) {
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
		status, picked := f.call(t, http.MethodPost, "http://localhost/api/folder-picker/select-path", map[string]any{"path": root})
		if status != http.StatusOK || picked["selection_token"] == nil {
			t.Fatalf("trusted path selection: %d %v", status, picked)
		}
		return f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder", map[string]any{"version": build["version"], "mode": "selection", "selection_token": picked["selection_token"]})
	}
	var currentPath string
	for _, parent := range []string{"alpha", "beta"} {
		currentPath = filepath.Join(os.Getenv("HOME"), parent, "Inbox")
		status, response := pick(currentPath)
		if status != http.StatusOK {
			t.Fatalf("replace: %d %v", status, response)
		}
		build = response["session"].(map[string]any)
		if build["placement_monitoring"] != true || build["folder_path"] != canonicalFolderPath(t, currentPath) || build["proposal"] != nil || build["draft"].(map[string]any)["template_id"] != "file-janitor" {
			t.Fatalf("replacement changed intent: %v", build)
		}
		observation := build["folder"].(map[string]any)["observation"].(map[string]any)
		if observation["folder"] != "Inbox" || observation["id"] == initial {
			t.Fatalf("same-named folders lost identity: %v", observation)
		}
		initial = observation["id"]
		reviewEditedInbox(t, f, build, true)
	}
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-prepare", map[string]any{"version": oldReview["version"], "monitoring": false, "review_revision": oldReview["review_revision"]})
	if status != http.StatusConflict {
		t.Fatalf("old folder/mode review accepted: %d %v", status, response)
	}
	code := filepath.Join(os.Getenv("HOME"), "code-project")
	if err := os.MkdirAll(code, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(code, "go.mod"), []byte("module example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, response := pick(code); status < 400 {
		t.Fatalf("incompatible root replaced inbox: %d %v", status, response)
	}
	status, latest := f.call(t, http.MethodGet, "/api/workspaces/build-sessions/"+id, nil)
	if status != http.StatusOK || latest["session"].(map[string]any)["folder_path"] != canonicalFolderPath(t, currentPath) || latest["session"].(map[string]any)["folder"].(map[string]any)["observation"].(map[string]any)["id"] != initial {
		t.Fatalf("failed selection altered last valid folder: %d %v", status, latest)
	}
	beforeDraft, _ := json.Marshal(build["draft"])
	afterDraft, _ := json.Marshal(latest["session"].(map[string]any)["draft"])
	if string(beforeDraft) != string(afterDraft) {
		t.Fatal("failed selection changed draft choices")
	}
	// Attachment claim/release may advance its version even on refusal. The
	// retained material choices are unchanged; the next review uses that version.
	build = latest["session"].(map[string]any)
	if err := os.Rename(currentPath, currentPath+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(currentPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-review", map[string]any{"version": build["version"], "monitoring": true}); status < 400 {
		t.Fatalf("replacement root revived authority: %d %v", status, response)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("folder edits created domain resources")
	}
	if _, err := os.Stat(filepath.Join(currentPath, "Filed")); !os.IsNotExist(err) {
		t.Fatal("folder editing granted access")
	}
}

func TestInboxPlacementEditing_HeldFolderCardsReuseExactBuildScopeWithoutRefreshingObservation(t *testing.T) {
	f := newDraftServerFixture(t)
	build, _ := seedEditableInbox(t, f)
	id := build["id"].(string)
	original := build["folder"].(map[string]any)["observation"].(map[string]any)
	expires := build["folder_expires_at"]
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), "Downloads"), 0o750); err != nil {
		t.Fatal(err)
	}
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder", map[string]any{"version": build["version"], "mode": "chip", "chip": "downloads"})
	if status != http.StatusOK {
		t.Fatalf("pick: %d %v", status, response)
	}
	build = response["session"].(map[string]any)
	status, response = f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder", map[string]any{"version": build["version"], "mode": "held", "observation_id": original["id"]})
	if status != http.StatusOK {
		t.Fatalf("reselect held folder: %d %v", status, response)
	}
	build = response["session"].(map[string]any)
	before, _ := json.Marshal(original)
	after, _ := json.Marshal(build["folder"].(map[string]any)["observation"])
	if string(before) != string(after) || build["folder_expires_at"] != expires {
		t.Fatal("reselect refreshed the held observation")
	}
	reviewEditedInbox(t, f, build, false)
	// A valid conversation-owned observation of the very same directory is not
	// a build-owned hold, even though this authenticated user selected both.
	foreign := build["placement_source"].(map[string]any)["observation_id"]
	status, response = f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder", map[string]any{"version": build["version"], "mode": "held", "observation_id": foreign})
	if status < 400 {
		t.Fatalf("foreign scope accepted: %d %v", status, response)
	}
	_, latest := f.call(t, http.MethodGet, "/api/workspaces/build-sessions/"+id, nil)
	if latest["session"].(map[string]any)["folder"].(map[string]any)["observation"].(map[string]any)["id"] != original["id"] {
		t.Fatal("foreign card displaced the valid folder")
	}
}

func TestInboxPlacementEditing_AlreadyOwnedAndOverlappingFoldersDoNotReplaceTheDraft(t *testing.T) {
	f := newDraftServerFixture(t)
	build, source := seedEditableInbox(t, f)
	id := build["id"].(string)
	original := build["folder"].(map[string]any)["observation"].(map[string]any)["id"]
	root := filepath.Join(os.Getenv("HOME"), "owned", "Inbox")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	existing := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Existing Janitor"})
	existing.FolderSlug, existing.OwnerUserID = "existing-janitor", "local"
	existing.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: filejanitor.LegacyTemplateID, Builtin: true})
	if err := f.builder.workspaceStore.Save(existing); err != nil {
		t.Fatal(err)
	}
	if _, err := f.builder.fileJanitorService.ConfirmSetup(filejanitor.SetupRequest{WorkspaceID: existing.ID, Path: root}); err != nil {
		t.Fatal(err)
	}
	before, profiles := len(f.builder.workspaceFileStore.CachedWorkspaces()), len(f.builder.st.ListAgents())
	for _, candidate := range []string{root, filepath.Join(root, "nested"), filepath.Dir(root)} {
		status, selection := f.call(t, http.MethodPost, "http://localhost/api/folder-picker/select-path", map[string]any{"path": candidate})
		if status != http.StatusOK {
			t.Fatalf("select: %d %v", status, selection)
		}
		status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder", map[string]any{"version": build["version"], "mode": "selection", "selection_token": selection["selection_token"]})
		if status < 400 {
			t.Fatalf("owned/overlapping selection accepted: %s: %d %v", candidate, status, response)
		}
		_, latest := f.call(t, http.MethodGet, "/api/workspaces/build-sessions/"+id, nil)
		build = latest["session"].(map[string]any)
		if build["folder"].(map[string]any)["observation"].(map[string]any)["id"] != original {
			t.Fatal("refusal displaced the original folder")
		}
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("refused folders created resources")
	}
	if _, err := os.Stat(filepath.Join(source, "Filed")); !os.IsNotExist(err) {
		t.Fatal("editing granted the original folder")
	}
	batches, err := f.builder.fileJanitorService.ListBatches(existing.ID)
	if err != nil || len(batches) != 0 {
		t.Fatalf("editing ran a scan: %v %v", batches, err)
	}
}

func TestInboxPlacementEditing_UnsupportedRoleConfigurationCannotBePrepared(t *testing.T) {
	for _, mode := range []string{"unknown-role", "vacant", "extra-agent", "assigned-config"} {
		t.Run(mode, func(t *testing.T) {
			f := newDraftServerFixture(t)
			build, root := seedEditableInbox(t, f)
			draft := build["draft"].(map[string]any)
			switch mode {
			case "unknown-role":
				draft["role_staffing"] = []any{map[string]any{"role_id": "administrator", "mode": "create", "name": "Other"}}
			case "vacant":
				draft["role_staffing"] = []any{}
			case "extra-agent":
				draft["existing_agent_names"] = []string{"Atlas"}
			case "assigned-config":
				draft["role_staffing"] = []any{map[string]any{"role_id": "file-curator", "mode": "assign", "name": "Atlas", "system_prompt": "Do not change saved settings"}}
			}
			build = saveEditedInbox(t, f, build, false)
			status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+build["id"].(string)+"/janitor-review", map[string]any{"version": build["version"], "monitoring": false})
			if status < 400 {
				t.Fatalf("unsupported team reviewed: %d %v", status, response)
			}
			if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
				t.Fatal("unsupported team granted a folder")
			}
		})
	}
}
