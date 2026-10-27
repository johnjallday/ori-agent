package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
)

func creationOperationRequest(t *testing.T, h *Handler, method, path string, input any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "/api/workspaces/create-operations"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleWorkspaces(w, r)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("response %d: %s (%v)", w.Code, w.Body, err)
	}
	return w, result
}

func reviewCreationOperation(t *testing.T, h *Handler, request map[string]any) (string, string) {
	t.Helper()
	id := uuid.NewString()
	w, result := creationOperationRequest(t, h, http.MethodPost, "", map[string]any{"id": id, "request": request})
	if w.Code != http.StatusOK {
		t.Fatalf("review: %d %s", w.Code, w.Body)
	}
	op := result["operation"].(map[string]any)
	if op["state"] != "reviewed" || op["workspace_id"] != nil || result["result"] != nil {
		t.Fatalf("review claimed creation: %s", w.Body)
	}
	return id, op["review_digest"].(string)
}

func assertCreationOperationState(t *testing.T, h *Handler, id, state string) map[string]any {
	t.Helper()
	w, result := creationOperationRequest(t, h, http.MethodGet, "/"+id, nil)
	if w.Code != http.StatusOK || result["operation"].(map[string]any)["state"] != state {
		t.Fatalf("want %s: %d %s", state, w.Code, w.Body)
	}
	return result
}

func TestWorkspaceCreationOperation_ReviewIsInertAndReplayObservesExactResourceWithoutHQ(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	request := map[string]any{"name": "Reviewed blank", "blank": true, "create_template_agents": false, "existing_agent_names": []string{}}
	id, digest := reviewCreationOperation(t, h, request)
	before, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(before) != 0 || len(h.agentStore.ListAgents()) != 0 || h.workspaceBuild != nil {
		t.Fatalf("review had effects or acquired an assistant dependency: %v %v", before, err)
	}
	w, result := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusCreated || result["creation_operation"].(map[string]any)["state"] != "complete" {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	workspaceID := result["folder"].(map[string]any)["id"].(string)
	// Reconstruct the controller, not its durable store. No preparation/model
	// dependencies are wired; status and repeated confirm must stay read-only.
	h = New(h.store)
	for range 3 {
		observed := assertCreationOperationState(t, h, id, "complete")
		if observed["result"].(map[string]any)["folder"].(map[string]any)["id"] != workspaceID {
			t.Fatalf("observation adopted another identity: %v", observed)
		}
		w, replay := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusCreated || replay["folder"].(map[string]any)["id"] != workspaceID {
			t.Fatalf("replay: %d %s", w.Code, w.Body)
		}
	}
	after, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(after) != 1 || len(after[0].AgentInstances) != 0 {
		t.Fatalf("replayed creation: %v %v", after, err)
	}
	if err := h.store.DeleteWorkspace(t.Context(), workspaceID); err != nil {
		t.Fatal(err)
	}
	assertCreationOperationState(t, h, id, "reconciliation_required")
	w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusConflict {
		t.Fatalf("deleted result recreated: %d %s", w.Code, w.Body)
	}
}

type countedCreationStore struct {
	session.HybridStore
	calls atomic.Int32
	fail  bool
}

func (s *countedCreationStore) CreateWorkspace(ctx context.Context, ws *session.Workspace) error {
	s.calls.Add(1)
	if s.fail {
		return errors.New("injected domain write failure")
	}
	return s.HybridStore.CreateWorkspace(ctx, ws)
}

func TestWorkspaceCreationOperation_ConcurrentConfirmationAcceptsOneAttempt(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	store := &countedCreationStore{HybridStore: h.store}
	h.store = store
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "One accepted operation", "create_template_agents": false})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
			if w.Code != http.StatusCreated && w.Code != http.StatusAccepted {
				t.Errorf("concurrent confirmation: %d %s", w.Code, w.Body)
			}
		})
	}
	wg.Wait()
	if store.calls.Load() != 1 {
		t.Fatalf("domain create called %d times", store.calls.Load())
	}
	assertCreationOperationState(t, h, id, "complete")
}

func TestWorkspaceCreationOperation_ForeignStaleAndStrictEnvelopeRefuseBeforeEffects(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	owner := "alice"
	h.currentUserID = func(context.Context) (string, error) { return owner, nil }
	request := map[string]any{"name": "Original review", "create_template_agents": false}
	id, digest := reviewCreationOperation(t, h, request)
	w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": "stale"})
	if w.Code != http.StatusConflict {
		t.Fatalf("stale review: %d", w.Code)
	}
	w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest, "name": "Changed terms"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown confirm material: %d", w.Code)
	}
	request["name"] = "Changed terms"
	w, _ = creationOperationRequest(t, h, http.MethodPost, "", map[string]any{"id": id, "request": request})
	if w.Code != http.StatusConflict {
		t.Fatalf("review identity retargeted: %d", w.Code)
	}
	owner = "bob"
	w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign confirmation: %d", w.Code)
	}
	w, _ = creationOperationRequest(t, h, http.MethodGet, "/"+id, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign observation: %d", w.Code)
	}
	owner = ""
	w, _ = creationOperationRequest(t, h, http.MethodGet, "/"+id, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("owner failure fell back to local: %d", w.Code)
	}
	workspaces, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(workspaces) != 0 {
		t.Fatalf("refusal created resources: %v %v", workspaces, err)
	}
}

func TestWorkspaceCreationOperation_DefiniteRefusalRequiresNewReview(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "Invalid kind", "kind": "invalid"})
	for range 2 {
		w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid kind was accepted: %d %s", w.Code, w.Body)
		}
	}
	assertCreationOperationState(t, h, id, "refused")
	workspaces, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(workspaces) != 0 {
		t.Fatalf("refusal created resources: %v %v", workspaces, err)
	}
}

func TestWorkspaceCreationOperation_SourceChangeAndUnavailableBlueprintCannotConfirm(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, h, "changing", `{"name":"Changing","goal":"Original goal","agents":[]}`)
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "Source-fenced", "template_id": "changing", "create_template_agents": false})
	writeRosterTemplate(t, h, "changing", `{"name":"Changing","goal":"New terms","agents":[]}`)
	w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusConflict {
		t.Fatalf("changed source confirmed: %d %s", w.Code, w.Body)
	}
	assertCreationOperationState(t, h, id, "refused")
	w, _ = creationOperationRequest(t, h, http.MethodPost, "", map[string]any{"id": uuid.NewString(), "request": map[string]any{
		"name": "Missing blueprint", "template_path": "/definitely/missing/blueprint",
	}})
	if w.Code != http.StatusConflict {
		t.Fatalf("unreadable blueprint became Blank: %d %s", w.Code, w.Body)
	}
	workspaces, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(workspaces) != 0 {
		t.Fatalf("source refusal created resources: %v %v", workspaces, err)
	}
}

func TestWorkspaceCreationOperation_AcceptedFailureNeverRetriesDomainWrites(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	store := &countedCreationStore{HybridStore: h.store, fail: true}
	h.store = store
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "Uncertain effects", "create_template_agents": false})
	for range 3 {
		w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusConflict {
			t.Fatalf("accepted failure lost its claim: %d %s", w.Code, w.Body)
		}
		assertCreationOperationState(t, h, id, "reconciliation_required")
	}
	if store.calls.Load() != 1 {
		t.Fatalf("accepted effects repeated: %d", store.calls.Load())
	}
}

func TestWorkspaceCreationOperation_ReceiptWriteFailureRetainsAcceptedIdentity(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	store := &countedCreationStore{HybridStore: h.store}
	h.store = store
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "Lost final receipt", "create_template_agents": false})
	_, err := h.store.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_creation_receipt BEFORE UPDATE ON workspace_creation_operations
		WHEN NEW.state='complete' BEGIN SELECT RAISE(ABORT, 'injected receipt write failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing receipt claimed success: %d %s", w.Code, w.Body)
	}
	for range 2 {
		observed := assertCreationOperationState(t, h, id, "accepted")
		if observed["result"] != nil || observed["observed_workspace"].(map[string]any)["name"] != "Lost final receipt" {
			t.Fatalf("partial existence was hidden or called complete: %v", observed)
		}
		w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
		if w.Code != http.StatusAccepted {
			t.Fatalf("missing receipt confirmation: %d %s", w.Code, w.Body)
		}
	}
	if store.calls.Load() != 1 {
		t.Fatalf("lost receipt repeated effects: %d", store.calls.Load())
	}
}

func TestWorkspaceCreationOperation_AbandonReleasesOnlyUnacceptedReview(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "Cancelled"})
	for range 2 {
		w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/abandon", map[string]any{})
		if w.Code != http.StatusOK {
			t.Fatalf("abandon: %d %s", w.Code, w.Body)
		}
	}
	assertCreationOperationState(t, h, id, "abandoned")
	w, _ := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusConflict {
		t.Fatalf("abandoned review confirmed: %d %s", w.Code, w.Body)
	}
	id, digest = reviewCreationOperation(t, h, map[string]any{"name": "Accepted", "create_template_agents": false})
	w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusCreated {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/abandon", map[string]any{})
	if w.Code != http.StatusConflict {
		t.Fatalf("accepted operation abandoned: %d %s", w.Code, w.Body)
	}
	assertCreationOperationState(t, h, id, "complete")
}

func TestWorkspaceCreationOperation_ReopenedDatabaseRetainsOriginalReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creation.db")
	open := func() session.HybridStore {
		t.Helper()
		db, err := database.Open(t.Context(), &database.Config{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		return session.NewHybridStoreWithDB(db, 5)
	}
	store := open()
	h := New(store)
	id, digest := reviewCreationOperation(t, h, map[string]any{"name": "Durable operation", "create_template_agents": false})
	w, result := creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusCreated {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open()
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	h = New(store)
	observed := assertCreationOperationState(t, h, id, "complete")
	if observed["result"].(map[string]any)["folder"].(map[string]any)["id"] != result["folder"].(map[string]any)["id"] {
		t.Fatal("reopened database lost exact identity")
	}
	w, _ = creationOperationRequest(t, h, http.MethodPost, "/"+id+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != http.StatusCreated {
		t.Fatalf("reopened replay: %d %s", w.Code, w.Body)
	}
	workspaces, err := store.ListWorkspaces(t.Context())
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("reopened replay created resources: %v %v", workspaces, err)
	}
}

func TestWorkspaceCreationOperation_DoesNotAdoptUnprovenResourceOrExecuteOnRead(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	store := &countedCreationStore{HybridStore: h.store}
	h.store = store
	id, _ := reviewCreationOperation(t, h, map[string]any{"name": "Not proof"})
	op, err := h.readWorkspaceCreationOperation(t.Context(), "local", id)
	if err != nil {
		t.Fatal(err)
	}
	// Even the reserved ID plus matching name and owner cannot prove this
	// journal created a workspace without its canonical operation provenance.
	if err := store.HybridStore.CreateWorkspace(t.Context(), &session.Workspace{ID: op.WorkspaceID, Name: "Not proof", FolderSlug: "not-proof"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.DB().ExecContext(t.Context(), `UPDATE workspace_creation_operations SET state='accepted' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		observed := assertCreationOperationState(t, h, id, "accepted")
		if observed["observed_workspace"] != nil || observed["result"] != nil {
			t.Fatalf("unproven reserved identity was adopted: %v", observed)
		}
	}
	if store.calls.Load() != 0 {
		t.Fatal("observation executed creation")
	}
}

func TestWorkspaceCreationOperation_KeepsPresenceSensitiveArraysAndRejectsGroupOwner(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	request := map[string]any{"name": "Explicitly empty", "role_staffing": []string{}, "existing_agent_names": []string{}}
	id, _ := reviewCreationOperation(t, h, request)
	op, err := h.readWorkspaceCreationOperation(t.Context(), "local", id)
	if err != nil {
		t.Fatal(err)
	}
	var decoded createWorkspaceRequest
	if err := json.Unmarshal([]byte(op.Request), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.roleStaffingPresent || decoded.roleStaffingNull || decoded.ExistingAgentNames == nil {
		t.Fatal("journal normalization erased explicit vacancies")
	}
	w, _ := creationOperationRequest(t, h, http.MethodPost, "", map[string]any{"id": uuid.NewString(), "request": map[string]any{
		"name": "Separate group owner", "group_review_token": "token", "idempotency_key": "key",
	}})
	if w.Code != http.StatusConflict {
		t.Fatalf("group commit acquired ordinary owner: %d %s", w.Code, w.Body)
	}
}

func TestWorkspaceCreationOperation_MalformedSuccessAndReservedIDAreNotProof(t *testing.T) {
	for _, body := range []string{`{}`, `{"success":true}`, `{"success":true,"folder":{"id":"unrelated"}}`, `not JSON`} {
		t.Run(body, func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			id, _ := reviewCreationOperation(t, h, map[string]any{"name": "No proof"})
			op, err := h.readWorkspaceCreationOperation(t.Context(), "local", id)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r = r.WithContext(context.WithValue(r.Context(), creationOperationContextKey{}, op))
			if !h.acceptWorkspaceCreationOperation(httptest.NewRecorder(), r, &session.Workspace{}, projecttemplates.Template{}, false) {
				t.Fatal("accept failed")
			}
			recorder := httptest.NewRecorder()
			recorder.WriteHeader(http.StatusCreated)
			if _, err := recorder.WriteString(body); err != nil {
				t.Fatal(err)
			}
			if err := h.finishWorkspaceCreationOperation(t.Context(), op, recorder); err != nil {
				t.Fatal(err)
			}
			assertCreationOperationState(t, h, id, "reconciliation_required")
		})
	}
}
