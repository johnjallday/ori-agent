package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestWorkspaceBuildContinuity_AttachRestartRepickAndDetachUseOneReview(t *testing.T) {
	for _, marker := range []string{"Song.rpp", "notes.txt"} {
		t.Run(marker, func(t *testing.T) {
			f := newDraftServerFixture(t)
			provider := &capturingChatProvider{}
			f.builder.llmFactory.Register("claude_code", provider)
			if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
				t.Fatal(body)
			}
			status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions", map[string]any{"entry_point": "home_cockpit_create"})
			if status != http.StatusCreated {
				t.Fatalf("start: %d %v", status, body)
			}
			build := body["session"].(map[string]any)
			id := build["id"].(string)
			workspaces, profiles := len(f.builder.workspaceFileStore.CachedWorkspaces()), len(f.builder.st.ListAgents())
			root := filepath.Join(os.Getenv("HOME"), "Continuity Song")
			if err := os.MkdirAll(root, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, marker), []byte("<REAPER_PROJECT\n>\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			pick := func(path string) map[string]any {
				t.Helper()
				code, picked := f.call(t, http.MethodPost, "http://localhost/api/folder-picker/select-path", map[string]any{"path": path})
				if code != http.StatusOK {
					t.Fatalf("pick: %d %v", code, picked)
				}
				code, attached := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder", map[string]any{"version": build["version"], "mode": "selection", "selection_token": picked["selection_token"]})
				if code != http.StatusOK {
					t.Fatalf("attach: %d %v", code, attached)
				}
				return attached
			}
			body = pick(root)
			build = body["session"].(map[string]any)
			original, _ := json.Marshal(build["folder"])
			build["draft"].(map[string]any)["name"] = "Keep my name"
			status, body = f.call(t, http.MethodPatch, "/api/workspaces/build-sessions/"+id+"/draft", map[string]any{"version": build["version"], "draft": build["draft"], "edited_fields": []string{"name"}})
			if status != http.StatusOK {
				t.Fatalf("edit: %d %v", status, body)
			}
			build = body["session"].(map[string]any)
			beforeDraft, _ := json.Marshal(build["draft"])
			observations := personalassistant.NewFolderObservationService(f.builder.personalAssistantFolderDigest)
			f.builder.server.Storage.PersonalAssistantFolderObservations = observations
			observations.ConfigureBuildReviews(f.builder.sessionHandler.WorkspaceBuildReviewAuthority())
			status, body = f.call(t, http.MethodGet, "/api/workspaces/build-sessions/"+id, nil)
			if status != http.StatusOK {
				t.Fatalf("reopen: %d %v", status, body)
			}
			reopened := body["session"].(map[string]any)
			encoded, _ := json.Marshal(body)
			if reopened["folder_expires_at"] != nil || reopened["folder_needs_pick_reason"] != nil || strings.Contains(string(encoded), canonicalFolderPath(t, root)) {
				t.Fatalf("reopened review expired or disclosed its private root: %v", body)
			}
			if err := os.Rename(root, root+" renamed"); err != nil {
				t.Fatal(err)
			}
			body = pick(root + " renamed")
			if body["resumed_review"] == nil {
				t.Fatal("re-pick made a different review", body)
			}
			offerID := body["resumed_review"].(string)
			build = body["session"].(map[string]any)
			afterDraft, _ := json.Marshal(build["draft"])
			afterFolder, _ := json.Marshal(build["folder"])
			if string(afterDraft) != string(beforeDraft) || string(afterFolder) != string(original) {
				t.Fatal("re-pick reset the canonical folder or choices")
			}
			status, body = f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/folder/detach", map[string]any{"version": build["version"]})
			if status != http.StatusOK {
				t.Fatalf("detach: %d %v", status, body)
			}
			stored, err := f.builder.personalAssistantFolderDigest.StoredOffer(t.Context(), "local", offerID)
			if err != nil || stored.Status != personalassistant.FolderOfferClosed {
				t.Fatalf("detached review: %+v %v", stored, err)
			}
			if len(provider.requests) != 0 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces || len(f.builder.st.ListAgents()) != profiles {
				t.Fatalf("folder continuity changed resources: calls=%d, workspaces=%d (before %d), profiles=%d (before %d)", len(provider.requests), len(f.builder.workspaceFileStore.CachedWorkspaces()), workspaces, len(f.builder.st.ListAgents()), profiles)
			}
		})
	}
}
