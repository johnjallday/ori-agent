package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
)

func updateHTTPFixture(t *testing.T) (*Handler, session.LeadConversationInput) {
	t.Helper()
	h, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	adapter := session.NewWorkspaceStoreAdapter(h.store)
	ws := &workspace.Workspace{ID: "ws", Name: "Desk", OwnerUserID: "local", Status: workspace.StatusActive, AgentInstances: []workspace.AgentInstance{{ID: "lead", Name: "Mira", EntryPoint: true}}}
	if err := adapter.Save(ws); err != nil {
		t.Fatal(err)
	}
	stored, err := adapter.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.SetWorkspaceUpdates(workspaceupdates.New(h.store.(workspaceupdates.Store), adapter, nil, nil))
	return h, session.LeadConversationInput{WorkspaceID: ws.ID, WorkspaceVersion: stored.Version, OwnerUserID: "local", AgentName: "Mira", AgentInstanceID: "lead", Introduction: "Private transcript text stays in chat."}
}

func updateRequest(h *Handler, method, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/workspaces/ws/updates"+query, strings.NewReader(body))
	r.SetPathValue("workspaceID", "ws")
	w := httptest.NewRecorder()
	if method == http.MethodGet {
		h.GetWorkspaceUpdates(w, r)
	} else {
		h.AcknowledgeWorkspaceUpdate(w, r)
	}
	return w
}

func TestWorkspaceUpdatesHTTPReadIsInertAndSeenNamesExactMessage(t *testing.T) {
	h, input := updateHTTPFixture(t)
	w := updateRequest(h, http.MethodGet, "", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"unread_count":0`) {
		t.Fatalf("known absence: %d %s", w.Code, w.Body)
	}
	var count int
	if err := h.store.DB().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM sessions)+(SELECT COUNT(*) FROM workspace_welcome_enrollments)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("GET delivered/enrolled", count, err)
	}
	if _, err := h.store.(workspaceupdates.Store).EnsureLeadConversation(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	w = updateRequest(h, http.MethodGet, "", "")
	var first workspaceupdates.Summary
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || first.Welcome.Unread != 1 || len(first.Welcome.Messages) != 1 || strings.Contains(w.Body.String(), "Private transcript") {
		t.Fatalf("welcome summary: %d %s", w.Code, w.Body)
	}
	ack := session.WelcomeAcknowledgement{SessionID: first.Welcome.SessionID, MessageID: first.Welcome.Messages[0].MessageID, Revision: 1}
	encoded, err := json.Marshal(ack)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // lost reply and retry acknowledge the exact same exposure
		w = updateRequest(h, http.MethodPost, "", string(encoded))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"unread_count":0`) {
			t.Fatalf("seen: %d %s", w.Code, w.Body)
		}
	}
	input.Assessment = "Later saved result"
	if _, err := h.store.(workspaceupdates.Store).EnsureLeadConversation(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	w = updateRequest(h, http.MethodPost, "", string(encoded))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"unread_count":1`) {
		t.Fatalf("old ack cleared newer unseen message: %d %s", w.Code, w.Body)
	}
}

func TestWorkspaceUpdatesHTTPRefusesSpoofedAndStaleAcknowledgements(t *testing.T) {
	for _, mode := range []string{"owner-body", "owner-query", "foreign-owner", "owner-unavailable", "malformed", "trailing", "empty", "oversize", "future", "wrong-message", "wrong-session", "deleted", "moved", "changed-lead"} {
		t.Run(mode, func(t *testing.T) {
			h, input := updateHTTPFixture(t)
			out, err := h.store.(workspaceupdates.Store).EnsureLeadConversation(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			messages, err := h.store.GetSession(t.Context(), out.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			material := map[string]any{"session_id": out.SessionID, "message_id": messages.Messages[0].ID, "revision": 1}
			query, want := "", http.StatusBadRequest
			switch mode {
			case "owner-body":
				material["owner_user_id"] = "local"
			case "owner-query":
				query = "?owner_user_id=local"
			case "foreign-owner":
				h.currentUserID = func(context.Context) (string, error) { return "foreign", nil }
				want = http.StatusNotFound
			case "owner-unavailable":
				h.currentUserID = func(context.Context) (string, error) { return "", errors.New("PRIVATE owner error") }
				want = http.StatusServiceUnavailable
			case "future":
				material["revision"] = 999
			case "wrong-message":
				material["message_id"] = "other"
				want = http.StatusConflict
			case "wrong-session":
				material["session_id"] = "other"
				want = http.StatusConflict
			case "deleted":
				if err := h.store.DeleteSession(t.Context(), out.SessionID); err != nil {
					t.Fatal(err)
				}
				want = http.StatusConflict
			case "moved":
				if _, err := h.store.DB().ExecContext(t.Context(), `UPDATE sessions SET workspace_id=NULL WHERE id=?`, out.SessionID); err != nil {
					t.Fatal(err)
				}
				want = http.StatusConflict
			case "changed-lead":
				adapter := session.NewWorkspaceStoreAdapter(h.store)
				if err := adapter.Update("ws", func(ws *workspace.Workspace) error { ws.AgentInstances[0].ID = "namesake"; return nil }); err != nil {
					t.Fatal(err)
				}
				want = http.StatusConflict
			}
			body, _ := json.Marshal(material)
			switch mode {
			case "malformed":
				body = []byte(`{"revision":`)
			case "trailing":
				body = append(body, []byte(` {}`)...)
			case "empty":
				body = nil
			case "oversize":
				body = []byte(`{"message_id":"` + strings.Repeat("x", 2048) + `"}`)
			}
			w := updateRequest(h, http.MethodPost, query, string(body))
			if w.Code != want || strings.Contains(w.Body.String(), "PRIVATE") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("ack status %d want %d: %s", w.Code, want, w.Body)
			}
			var seen int
			if err := h.store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_welcome_messages WHERE seen_at IS NOT NULL`).Scan(&seen); err != nil || seen != 0 {
				t.Fatal("invalid ack changed saved state", seen, err)
			}
			if mode == "deleted" || mode == "moved" || mode == "changed-lead" {
				w = updateRequest(h, http.MethodGet, "", "")
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unavailable"`) || strings.Contains(w.Body.String(), "unread_count") {
					t.Fatalf("unavailable became authoritative zero: %d %s", w.Code, w.Body)
				}
			}
		})
	}
}

func TestWelcomeCreationCallbackOnlyAfterSuccessfulCreation(t *testing.T) {
	for _, mode := range []string{"success", "refused", "bookkeeping-failed"} {
		t.Run(mode, func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			calls := 0
			h.SetWorkspaceWelcomeCreated(func(ctx context.Context, id string) error {
				calls++
				ws, err := h.store.GetWorkspace(ctx, id)
				if err != nil || ws.Name != "New workspace" {
					t.Fatal("enrollment preceded durable creation", err)
				}
				if mode == "bookkeeping-failed" {
					return errors.New("PRIVATE storage cause")
				}
				return nil
			})
			body := `{"name":"New workspace","blank":true,"create_template_agents":false,"existing_agent_names":[]}`
			if mode == "refused" {
				body = `{"name":""}`
			}
			r := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.HandleWorkspaces(w, r)
			if mode == "refused" {
				if w.Code < 400 || calls != 0 {
					t.Fatal("refused create enrolled", w.Code, calls)
				}
				return
			}
			if calls != 1 {
				t.Fatalf("missing enrollment: %d %s calls=%d", w.Code, w.Body, calls)
			}
			if mode == "success" && w.Code != http.StatusCreated {
				t.Fatalf("create: %d %s", w.Code, w.Body)
			}
			if mode == "bookkeeping-failed" && (w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "workspace_id") || strings.Contains(w.Body.String(), "PRIVATE")) {
				t.Fatalf("uncertain creation hidden or private cause leaked: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestWelcomeReviewedPreparationCallbacksObserveCanonicalBoundaries(t *testing.T) {
	f := newBuildPreparationFixture(t)
	created, prepared := 0, 0
	f.h.SetWorkspaceWelcomeCreated(func(_ context.Context, id string) error {
		created++
		ws, err := f.h.readReviewedWorkspace(id)
		if err != nil || ws.GetTemplateProvenance() == nil || !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) || ws.EntryAgentName() != "Reviewed Curator" {
			t.Fatalf("enrollment preceded canonical template/lead: %+v %v", ws, err)
		}
		if ws.SetupWizardState() == workspace.SetupWizardStateReady {
			t.Fatal("creation invented completed setup")
		}
		return nil
	})
	f.h.SetWorkspacePreparationCompleted(func(_ context.Context, id string) error {
		prepared++
		batch, candidates, found, err := f.janitor.LatestPendingBatch(id)
		if err != nil || !found || len(candidates) != 1 || batch.CompletedAt.IsZero() {
			t.Fatal("readiness callback preceded the saved explicit scan result", err)
		}
		return nil
	})
	request := f.review(t)
	if created != 0 || prepared != 0 {
		t.Fatal("review triggered completion callbacks")
	}
	response := f.call("janitor-prepare", request)
	if response.Code != http.StatusOK || created != 1 || prepared != 1 {
		t.Fatalf("prepare: %d %s created=%d prepared=%d", response.Code, response.Body, created, prepared)
	}
	var result struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	before, err := f.janitor.ListBatches(result.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	response = f.call("janitor-prepare", request)
	if response.Code != http.StatusOK || created != 1 {
		t.Fatalf("replay re-entered creation: %d %s created=%d", response.Code, response.Body, created)
	}
	after, err := f.janitor.ListBatches(result.WorkspaceID)
	if err != nil || len(before) != len(after) || before[0].ID != after[0].ID {
		t.Fatal("completion/replay scanned again", err)
	}
}
