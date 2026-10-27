package server

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/store"
)

func stageInboxPlacement(t *testing.T, f *draftServerFixture) (map[string]any, string) {
	t.Helper()
	root := filepath.Join(os.Getenv("HOME"), "Desktop") // fixture owns HOME
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "invoice.pdf"), []byte("unchanged synthetic source"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "invoice.pdf"), old, old); err != nil {
		t.Fatal(err)
	}
	draftID := uuid.NewString()
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draftID, "revision": "", "mode": "chip", "chip": "desktop"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, selected)
	}
	observation := selected["observation"].(map[string]any)
	conversation, revision, offerID := reviewFolder(t, f, map[string]any{"draft_id": draftID, "revision": "", "selection_id": observation["id"], "candidate_id": "candidate-0"})
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation, nil)
	if status != http.StatusOK {
		t.Fatalf("history: %d %v", status, history)
	}
	offer := history["folder_reviews"].(map[string]any)[offerID].(map[string]any)
	plan, ok := offer["plan"].(map[string]any)
	if !ok {
		t.Fatalf("no inbox plan: %v", offer)
	}
	return map[string]any{"offer_id": offerID, "plan_digest": plan["digest"], "conversation_id": conversation, "source_revision": revision, "observation_id": observation["id"]}, root
}

func TestInboxPlacementHandoff_NoModelNoEffectsReplayAndExactPreparation(t *testing.T) {
	f := newDraftServerFixture(t)
	status, availability := f.call(t, http.MethodGet, "/api/workspaces/build-sessions/availability", nil)
	if status != http.StatusOK || availability["available"] != false || availability["reason"] != "no_model" {
		t.Fatalf("fixture must have no model configured: %d %v", status, availability)
	}
	input, root := stageInboxPlacement(t, f)
	before, profiles := len(f.builder.workspaceFileStore.CachedWorkspaces()), len(f.builder.st.ListAgents())
	input["name"], input["brief"] = "My inbox", "Keep my exact goal — 음악."
	status, result := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusCreated {
		t.Fatalf("handoff: %d %v", status, result)
	}
	build := result["session"].(map[string]any)
	id := build["id"].(string)
	if status, body := f.call(t, http.MethodGet, "/api/workspaces/build-sessions/folder-choices?build_id="+id, nil); status != http.StatusOK || body["available"] != true {
		t.Fatalf("bound no-model folder controls unavailable: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/turns", map[string]any{"version": build["version"], "text": "create now"}); status != http.StatusConflict || body["code"] != "placement_only" {
		t.Fatalf("placement allowed inference: %d %v", status, body)
	}
	draft := build["draft"].(map[string]any)
	if draft["name"] != input["name"] || draft["mission"] != input["brief"] || draft["parent_id"] != nil || build["proposal"] != nil || build["placement_monitoring"] == true {
		t.Fatalf("incorrect seed: %v", build)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("handoff created domain resources")
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("handoff created Filed")
	}
	if status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+input["offer_id"].(string)+"/setup", map[string]any{"request_id": "old-card", "plan_digest": input["plan_digest"]}); status < 400 {
		t.Fatalf("original card remained confirmable: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPost, "/api/workspaces", map[string]any{"name": "Bypass", "template_id": "file-janitor", "build_session_id": id}); status < 400 {
		t.Fatalf("ordinary create bypassed review: %d %v", status, body)
	}
	status, reopened := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", map[string]any{"conversation_id": input["conversation_id"], "revision": input["source_revision"], "selection_id": input["observation_id"], "candidate_id": "candidate-0"})
	if status != http.StatusOK || reopened["folder_context"].(map[string]any)["revision"] != input["source_revision"] {
		t.Fatalf("reopening rotated the transferred source: %d %v", status, reopened)
	}
	draft["name"] = "Edited inbox"
	status, patched := f.call(t, http.MethodPatch, "/api/workspaces/build-sessions/"+id+"/draft", map[string]any{"version": build["version"], "draft": draft})
	if status != http.StatusOK {
		t.Fatalf("edit: %d %v", status, patched)
	}
	status, replay := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusOK || replay["resumed"] != true || replay["session"].(map[string]any)["draft"].(map[string]any)["name"] != "Edited inbox" {
		t.Fatalf("replay overwrote current edits: %d %v", status, replay)
	}
	build = replay["session"].(map[string]any)
	status, reviewed := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-review", map[string]any{"version": build["version"], "monitoring": false})
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, reviewed)
	}
	review := reviewed["review"].(map[string]any)
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("permission review created resources")
	}
	confirmation := map[string]any{"version": build["version"], "monitoring": false, "review_revision": review["review_revision"]}
	responses := make([]map[string]any, 4)
	statuses := make([]int, 4)
	var confirmations sync.WaitGroup
	for i := range responses {
		confirmations.Add(1)
		go func() {
			defer confirmations.Done()
			statuses[i], responses[i] = f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-prepare", confirmation)
		}()
	}
	var oldStatus int
	confirmations.Add(1)
	go func() {
		defer confirmations.Done()
		oldStatus, _ = f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+input["offer_id"].(string)+"/setup", map[string]any{"request_id": "racing-old-card", "plan_digest": input["plan_digest"]})
	}()
	confirmations.Wait()
	if oldStatus < 400 {
		t.Fatal("source card raced the edited preparation")
	}
	for i, response := range responses {
		if statuses[i] != http.StatusOK || response["workspace_id"] != responses[0]["workspace_id"] {
			t.Fatalf("concurrent preparation: %d %v", statuses[i], response)
		}
	}
	workspaceID := responses[0]["workspace_id"].(string)
	status, repeated := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-prepare", confirmation)
	if status != http.StatusOK || repeated["workspace_id"] != workspaceID {
		t.Fatalf("replay created another identity: %d %v", status, repeated)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before+1 {
		t.Fatal("wrong workspace count")
	}
	batches, err := f.builder.fileJanitorService.ListBatches(workspaceID)
	if err != nil || len(batches) != 1 {
		t.Fatalf("initial result duplicated/missing: %v %v", batches, err)
	}
	if bytes, err := os.ReadFile(filepath.Join(root, "invoice.pdf")); err != nil || string(bytes) != "unchanged synthetic source" {
		t.Fatal("source bytes changed")
	}
}

func TestInboxPlacementHandoff_ReusesDefaultCuratorWithoutEditingItsProfile(t *testing.T) {
	f := newDraftServerFixture(t)
	if err := f.builder.st.CreateAgent("File Curator", &store.CreateAgentConfig{Model: "saved-model", LLMProvider: "openai", Temperature: 0.3, SystemPrompt: "Keep this saved profile exactly."}); err != nil {
		t.Fatal(err)
	}
	profile, ok := f.builder.st.GetAgent("File Curator")
	if !ok {
		t.Fatal("fixture profile missing")
	}
	before, err := json.Marshal(profile.Settings)
	if err != nil {
		t.Fatal(err)
	}
	profiles := len(f.builder.st.ListAgents())
	input, _ := stageInboxPlacement(t, f)
	status, seeded := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusCreated {
		t.Fatalf("handoff: %d %v", status, seeded)
	}
	build := seeded["session"].(map[string]any)
	id := build["id"].(string)
	if roles := build["draft"].(map[string]any)["role_staffing"].([]any); len(roles) != 1 || roles[0].(map[string]any)["mode"] != "assign" {
		t.Fatalf("default was not assigned: %v", roles)
	}
	status, reviewed := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-review", map[string]any{"version": build["version"], "monitoring": false})
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, reviewed)
	}
	review := reviewed["review"].(map[string]any)
	if member := review["team"].([]any)[0].(map[string]any); member["action"] != "attach" || member["name"] != "File Curator" {
		t.Fatalf("wrong effective team: %v", member)
	}
	status, prepared := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-prepare", map[string]any{"version": build["version"], "monitoring": false, "review_revision": review["review_revision"]})
	if status != http.StatusOK {
		t.Fatalf("preparation: %d %v", status, prepared)
	}
	profile, ok = f.builder.st.GetAgent("File Curator")
	if !ok {
		t.Fatal("assigned profile disappeared")
	}
	after, err := json.Marshal(profile.Settings)
	if err != nil || string(before) != string(after) || len(f.builder.st.ListAgents()) != profiles {
		t.Fatal("assignment changed or duplicated the saved profile", err)
	}
	ws, err := f.builder.workspaceFileStore.Get(prepared["workspace_id"].(string))
	if err != nil || len(ws.AgentInstances) != 1 || ws.AgentInstances[0].Name != "File Curator" {
		t.Fatal("observed membership differs from review", err)
	}
}

func TestInboxPlacementHandoff_RejectsStaleUntrustedAndDetachedSourceInputs(t *testing.T) {
	f := newDraftServerFixture(t)
	input, root := stageInboxPlacement(t, f)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	for _, change := range []struct {
		field  string
		value  any
		status int
	}{
		{"source_revision", "historical", http.StatusConflict},
		{"conversation_id", "another-conversation", http.StatusConflict},
		{"observation_id", "replaced-observation", http.StatusConflict},
		{"plan_digest", strings.Repeat("b", 64), http.StatusConflict},
		{"path", "/untrusted/path", http.StatusBadRequest},
		{"parent_id", "district-is-not-membership", http.StatusBadRequest},
		{"name", "../escape", http.StatusBadRequest},
	} {
		t.Run(change.field, func(t *testing.T) {
			request := maps.Clone(input)
			request[change.field] = change.value
			if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", request); status != change.status {
				t.Fatalf("refusal: %d %v", status, body)
			}
		})
	}
	// Even a still-live hold is not authority after its canonical event detached.
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{"conversation_id": input["conversation_id"], "revision": input["source_revision"]}); status != http.StatusOK {
		t.Fatalf("detach: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input); status != http.StatusConflict {
		t.Fatalf("detached: %d %v", status, body)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("refused handoff created a workspace")
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("refused handoff created Filed")
	}
}

func TestInboxPlacementHandoff_DetachBlocksMaterialWritesButOwnerCanCancel(t *testing.T) {
	f := newDraftServerFixture(t)
	input, root := stageInboxPlacement(t, f)
	status, response := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusCreated {
		t.Fatalf("handoff: %d %v", status, response)
	}
	build := response["session"].(map[string]any)
	id := build["id"].(string)
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{"conversation_id": input["conversation_id"], "revision": input["source_revision"]}); status != http.StatusOK {
		t.Fatalf("detach: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPatch, "/api/workspaces/build-sessions/"+id+"/draft", map[string]any{"version": build["version"], "draft": build["draft"]}); status != http.StatusConflict {
		t.Fatalf("detached source edited draft: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/janitor-review", map[string]any{"version": build["version"], "monitoring": false}); status != http.StatusConflict {
		t.Fatalf("detached source obtained review: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/"+id+"/abandon", map[string]any{"version": build["version"]}); status != http.StatusOK {
		t.Fatalf("owner cannot cancel stale draft: %d %v", status, body)
	}
	offer, err := f.builder.personalAssistantFolderDigest.StoredOffer(context.Background(), "local", input["offer_id"].(string))
	if err != nil || offer.Status != personalassistant.FolderOfferClosed || offer.PlacementHandoff == nil || !offer.PlacementHandoff.DraftSaved {
		t.Fatal("cancellation restored original confirmation", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("cancellation created Filed")
	}
}

func TestInboxPlacementHandoff_ConflictingBuildAndStaleSourceHaveNoEffects(t *testing.T) {
	f := newDraftServerFixture(t)
	input, root := stageInboxPlacement(t, f)
	now := time.Now().UTC()
	_, err := f.builder.workspaceBuildStore.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		doc.Add(personalassistant.WorkspaceBuildSession{ID: "unrelated", Status: personalassistant.WorkspaceBuildOpen, Version: 1, CreatedAt: now, UpdatedAt: now, EntryPoint: "home_cockpit_create"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, conflict := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input)
	if status != http.StatusConflict || conflict["code"] != "build_conflict" {
		t.Fatalf("conflict: %d %v", status, conflict)
	}
	offer, err := f.builder.personalAssistantFolderDigest.StoredOffer(context.Background(), "local", input["offer_id"].(string))
	if err != nil || offer.PlacementHandoff != nil || offer.Status != personalassistant.FolderOfferPending {
		t.Fatal("unrelated build retired source", err)
	}
	_, err = f.builder.workspaceBuildStore.Mutate(context.Background(), "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		build := doc.Session("unrelated")
		build.Status = personalassistant.WorkspaceBuildPreparing
		build.Setup = &personalassistant.BuildSetupClaim{OfferID: "other-offer", PlanDigest: strings.Repeat("a", 64), RequestID: "other-request", StartedAt: now}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input); status != http.StatusConflict || body["code"] != "build_conflict" {
		t.Fatalf("preparing build overwritten: %d %v", status, body)
	}
	input["source_revision"] = "stale"
	if status, body := f.call(t, http.MethodPost, "/api/workspaces/build-sessions/from-inbox", input); status != http.StatusConflict {
		t.Fatalf("stale source: %d %v", status, body)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("refusal created Filed")
	}
}
