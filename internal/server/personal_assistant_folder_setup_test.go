package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func stageReviewFolder(t *testing.T, f *draftServerFixture) map[string]any {
	t.Helper()
	home := os.Getenv("HOME") // newDraftServerFixture sandboxes HOME, never the user's Documents.
	folder := filepath.Join(home, "Documents", "Chosen")
	if err := os.MkdirAll(folder, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("source must stay unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	draft := uuid.NewString()
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draft, "revision": "", "mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, body)
	}
	observation := body["observation"].(map[string]any)
	candidate := ""
	for _, item := range observation["projects"].([]any) {
		project := item.(map[string]any)
		if project["name"] == "Chosen" {
			candidate = project["id"].(string)
		}
	}
	if candidate == "" {
		t.Fatal("bounded preview did not contain fixture")
	}
	return map[string]any{"draft_id": draft, "revision": "", "selection_id": observation["id"], "candidate_id": candidate}
}

func reviewFolder(t *testing.T, f *draftServerFixture, request map[string]any) (string, string, string) {
	t.Helper()
	status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request)
	if status != http.StatusOK {
		t.Fatalf("review: %d %v", status, body)
	}
	conversation := body["conversation"].(map[string]any)
	folder := body["folder_context"].(map[string]any)
	return conversation["id"].(string), folder["revision"].(string), folder["offer_id"].(string)
}

func TestAssistantFolderSetup_RealGenericCreatorWithoutModelAndCanonicalReceipt(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	request := stageReviewFolder(t, f)
	id, revision, offer := reviewFolder(t, f, request)
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before || len(provider.requests) != 0 {
		t.Fatal("review created a workspace or called a model")
	}
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if status != http.StatusOK || len(history["messages"].([]any)) != 1 {
		t.Fatal("review is not a canonical typed event", history)
	}
	view := history["folder_reviews"].(map[string]any)[offer].(map[string]any)
	if view["needs_pick"] == true || view["subject"].(map[string]any)["name"] != "Chosen" {
		t.Fatalf("wrong live candidate: %v", view)
	}
	confirmation := map[string]any{"decision": "yes", "choice": "project", "create": true, "workspace_name": "My next step", "request_id": "confirmed-once", "review_digest": view["review_digest"]}
	status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", confirmation)
	if status != http.StatusOK {
		t.Fatalf("confirm: %d %v", status, result)
	}
	outcome := result["offer"].(map[string]any)["outcome"].(map[string]any)
	workspaceID := outcome["workspace_id"].(string)
	workspace, err := f.builder.workspaceFileStore.Get(workspaceID)
	if err != nil || workspace.Name != "My next step" {
		t.Fatalf("workspace: %+v %v", workspace, err)
	}
	if len(f.builder.workspaceFileStore.CachedWorkspaces()) != before+1 || len(provider.requests) != 0 {
		t.Fatal("wrong workspace count or model used")
	}
	if status, replay := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", confirmation); status != http.StatusOK || replay["offer"].(map[string]any)["outcome"].(map[string]any)["workspace_id"] != workspaceID {
		t.Fatalf("replay: %d %v", status, replay)
	}
	// Removing future discussion does not undo or hide the completed receipt.
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{"conversation_id": id, "revision": revision}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	_, history = f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
	if history["folder_reviews"].(map[string]any)[offer].(map[string]any)["outcome"].(map[string]any)["workspace_id"] != workspaceID {
		t.Fatal("receipt lost on detach")
	}
	contents, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "Documents", "Chosen", "notes.txt"))
	if err != nil || string(contents) != "source must stay unchanged" {
		t.Fatal("source changed", err)
	}
}

func TestAssistantFolderSetup_UnrelatedPendingCancelAndDetachedReviewsFailClosed(t *testing.T) {
	f := newDraftServerFixture(t)
	before := len(f.builder.workspaceFileStore.CachedWorkspaces())
	first := stageReviewFolder(t, f)
	id, revision, offer := reviewFolder(t, f, first)
	second := stageReviewFolder(t, f)
	status, conflict := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", second)
	if status != http.StatusConflict || conflict["error"] != "folder_review_pending" {
		t.Fatalf("pending overwritten: %d %v", status, conflict)
	}
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review/close", map[string]any{"conversation_id": id, "revision": "", "offer_id": offer}); status != http.StatusConflict {
		t.Fatalf("stale empty revision closed a live review: %d %v", status, body)
	}
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review/close", map[string]any{"conversation_id": id, "revision": revision, "offer_id": offer}); status != http.StatusOK {
		t.Fatalf("close: %d %v", status, body)
	}
	nextID, nextRevision, nextOffer := reviewFolder(t, f, second)
	if nextOffer == offer {
		t.Fatal("different conversation adopted the old offer")
	}
	status, _ = f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "create": true, "request_id": "old-button"})
	if status == http.StatusOK {
		t.Fatal("closed history confirmed")
	}
	// A distinct active review is invalidated by detach, even through the old API.
	_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	if len(history["conversations"].([]any)) != 2 {
		t.Fatal("failed review left an orphan conversation")
	}
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{"conversation_id": nextID, "revision": nextRevision}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	status, _ = f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+nextOffer+"/decide", map[string]any{"decision": "yes", "create": true, "request_id": "detached-button"})
	if status != http.StatusConflict || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
		t.Fatal("detached proposal created a workspace")
	}
	_, detached := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+nextID, nil)
	if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review/close", map[string]any{"conversation_id": nextID, "revision": detached["folder_context"].(map[string]any)["revision"], "offer_id": nextOffer}); status != http.StatusOK {
		t.Fatal("outdated review cannot be closed", status, body)
	}
}

func TestAssistantFolderSetup_ConfirmedContinuationStillRequiresCanonicalOwnerAndSource(t *testing.T) {
	for _, mode := range []string{"deleted", "moved", "imported", "changed-child"} {
		t.Run(mode, func(t *testing.T) {
			f := newDraftServerFixture(t)
			id, revision, offer := reviewFolder(t, f, stageReviewFolder(t, f))
			_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
			digest := history["folder_reviews"].(map[string]any)[offer].(map[string]any)["review_digest"]
			if status, body := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "create": true, "request_id": "confirmed", "review_digest": digest}); status != http.StatusOK {
				t.Fatal(status, body)
			}
			if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{"conversation_id": id, "revision": revision}); status != http.StatusOK {
				t.Fatal(status, body)
			}
			ctx := context.Background()
			if path, err := f.builder.personalAssistantFolderDigest.SubjectPath(ctx, "local", offer); err != nil || path == "" {
				t.Fatal("detach revoked already confirmed continuation", err)
			}
			switch mode {
			case "deleted":
				if err := f.builder.sessionStore.DeleteSession(ctx, id); err != nil {
					t.Fatal(err)
				}
			case "moved":
				if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `UPDATE sessions SET agent_name='another-owner' WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "imported":
				if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `UPDATE messages SET continuity_source_sequence=1 WHERE id=?`, revision); err != nil {
					t.Fatal(err)
				}
			case "changed-child":
				child := filepath.Join(os.Getenv("HOME"), "Documents", "Chosen")
				if err := os.Rename(child, child+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(child, 0750); err != nil {
					t.Fatal(err)
				}
			}
			if path, err := f.builder.personalAssistantFolderDigest.SubjectPath(ctx, "local", offer); err == nil || path != "" {
				t.Fatal("stale confirmed review retained source authority", mode)
			}
		})
	}
}

func TestAssistantFolderSetup_ConsumedReferencesCannotResurrectAsFreshStaging(t *testing.T) {
	for _, mode := range []string{"detach", "import", "move-back"} {
		t.Run(mode, func(t *testing.T) {
			f := newDraftServerFixture(t)
			request := stageReviewFolder(t, f)
			id, revision, _ := reviewFolder(t, f, request)
			ctx := context.Background()
			switch mode {
			case "detach":
				if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/detach", map[string]any{"conversation_id": id, "revision": revision}); status != http.StatusOK {
					t.Fatal(status, body)
				}
			case "import":
				if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `UPDATE messages SET continuity_source_sequence=1 WHERE id=?`, revision); err != nil {
					t.Fatal(err)
				}
			case "move-back":
				sess, err := f.builder.sessionStore.GetSession(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				for _, owner := range []string{"another-owner", sess.AgentName} {
					if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `UPDATE sessions SET agent_name=? WHERE id=?`, owner, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
			current := history["folder_context"].(map[string]any)["revision"]
			delete(request, "draft_id")
			request["conversation_id"], request["revision"] = id, current
			if status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request); status != http.StatusConflict {
				t.Fatal("old reference reauthorized review", mode, status, body)
			}
			if status, body := f.call(t, http.MethodPost, "/api/home-assistant/route", map[string]any{"prompt": "Use this old reference", "context": map[string]any{"origin": "personal_assistant_panel"}, "conversation": map[string]any{"id": id}, "folder_context": map[string]any{"selection_id": request["selection_id"], "revision": current}}); status != http.StatusConflict {
				t.Fatal("old reference reauthorized chat", mode, status, body)
			}
		})
	}
}

func TestAssistantFolderSetup_FailedCanonicalWriteDiscardsSessionAndCanRetry(t *testing.T) {
	f := newDraftServerFixture(t)
	request := stageReviewFolder(t, f)
	ctx := context.Background()
	if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `CREATE TRIGGER fail_review BEFORE INSERT ON messages WHEN NEW.folder_context_json IS NOT NULL BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review", request); status == http.StatusOK {
		t.Fatal("failed review claimed to be saved")
	}
	_, listed := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	if len(listed["conversations"].([]any)) != 0 {
		t.Fatal("failed review left an orphan session")
	}
	if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `DROP TRIGGER fail_review`); err != nil {
		t.Fatal(err)
	}
	reviewFolder(t, f, request) // same staging authority, explicit retry
}

func TestAssistantFolderSetup_DeletedMovedAndImportedConversationsCannotConfirm(t *testing.T) {
	for _, mode := range []string{"deleted", "moved", "imported"} {
		t.Run(mode, func(t *testing.T) {
			f := newDraftServerFixture(t)
			before := len(f.builder.workspaceFileStore.CachedWorkspaces())
			id, revision, offer := reviewFolder(t, f, stageReviewFolder(t, f))
			_, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+id, nil)
			digest := history["folder_reviews"].(map[string]any)[offer].(map[string]any)["review_digest"]
			ctx := context.Background()
			switch mode {
			case "deleted":
				if err := f.builder.sessionStore.DeleteSession(ctx, id); err != nil {
					t.Fatal(err)
				}
			case "moved":
				if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `UPDATE sessions SET agent_name='another-owner' WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "imported":
				if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `UPDATE messages SET continuity_source_sequence=1 WHERE id=?`, revision); err != nil {
					t.Fatal(err)
				}
			}
			status, result := f.call(t, http.MethodPost, "/api/personal-assistant/folder-digest/offers/"+offer+"/decide", map[string]any{"decision": "yes", "create": true, "request_id": "stale", "review_digest": digest})
			if status != http.StatusConflict || len(f.builder.workspaceFileStore.CachedWorkspaces()) != before {
				t.Fatal("stale conversation created", mode, status, result)
			}
			if mode != "imported" {
				status, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/review/close", map[string]any{"conversation_id": id, "revision": "", "offer_id": offer})
				if status != http.StatusOK || body["review_closed"] != true {
					t.Fatal("orphaned review blocked future work", status, body)
				}
				reviewFolder(t, f, stageReviewFolder(t, f))
			}
		})
	}
}
