package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newBacklogHandlerTestStore(t *testing.T) workspace.Store {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newBacklogHandlerTestWorkspace(t *testing.T, store workspace.Store, name string) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return ws
}

type backlogItemEnvelope struct {
	Success bool                      `json:"success"`
	Item    workspace.BacklogItemView `json:"item"`
}

type backlogListEnvelope struct {
	Success bool                        `json:"success"`
	Items   []workspace.BacklogItemView `json:"items"`
	Count   int                         `json:"count"`
}

func TestBacklogHandler_CreateAndList(t *testing.T) {
	store := newBacklogHandlerTestStore(t)
	ws := newBacklogHandlerTestWorkspace(t, store, "Alpha")
	bh := NewBacklogHandler(workspace.NewBacklogService(store))

	t.Run("create requires description", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/orchestration/backlog",
			strings.NewReader(`{"workspace_id":"`+ws.ID+`"}`))
		rec := httptest.NewRecorder()
		bh.BacklogListHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("create then list returns the item", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/orchestration/backlog",
			strings.NewReader(`{"workspace_id":"`+ws.ID+`","description":"investigate flaky test","priority":1}`))
		rec := httptest.NewRecorder()
		bh.BacklogListHandler(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
		}
		var created backlogItemEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if created.Item.Task.Status != workspace.TaskStatusBacklog {
			t.Fatalf("Status = %q, want Backlog", created.Item.Task.Status)
		}
		if created.Item.OwningWorkspaceName != "Alpha" {
			t.Fatalf("OwningWorkspaceName = %q, want Alpha", created.Item.OwningWorkspaceName)
		}

		listReq := httptest.NewRequest(http.MethodGet, "/api/orchestration/backlog?workspace_id="+ws.ID, nil)
		listRec := httptest.NewRecorder()
		bh.BacklogListHandler(listRec, listReq)
		if listRec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
		}
		var listed backlogListEnvelope
		if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if listed.Count != 1 || len(listed.Items) != 1 {
			t.Fatalf("expected 1 item, got %+v", listed)
		}
	})

	t.Run("list requires workspace_id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/orchestration/backlog", nil)
		rec := httptest.NewRecorder()
		bh.BacklogListHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestBacklogHandler_GetUpdateDelete(t *testing.T) {
	store := newBacklogHandlerTestStore(t)
	ws := newBacklogHandlerTestWorkspace(t, store, "Alpha")
	svc := workspace.NewBacklogService(store)
	bh := NewBacklogHandler(svc)

	item, err := svc.Create(workspace.BacklogCreateInput{WorkspaceID: ws.ID, Description: "original"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Run("get detail", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/orchestration/backlog/"+item.ID+"?workspace_id="+ws.ID, nil)
		rec := httptest.NewRecorder()
		bh.BacklogItemPathHandler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("get detail rejects wrong owning workspace", func(t *testing.T) {
		other := newBacklogHandlerTestWorkspace(t, store, "Other")
		req := httptest.NewRequest(http.MethodGet, "/api/orchestration/backlog/"+item.ID+"?workspace_id="+other.ID, nil)
		rec := httptest.NewRecorder()
		bh.BacklogItemPathHandler(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (item not owned by that workspace); body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("update supported fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/orchestration/backlog/"+item.ID+"?workspace_id="+ws.ID,
			strings.NewReader(`{"description":"revised"}`))
		rec := httptest.NewRecorder()
		bh.BacklogItemPathHandler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var updated backlogItemEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if updated.Item.Task.Description != "revised" {
			t.Fatalf("Description = %q, want revised", updated.Item.Task.Description)
		}
	})

	t.Run("delete removes the item", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/orchestration/backlog/"+item.ID+"?workspace_id="+ws.ID, nil)
		rec := httptest.NewRecorder()
		bh.BacklogItemPathHandler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}

		getReq := httptest.NewRequest(http.MethodGet, "/api/orchestration/backlog/"+item.ID+"?workspace_id="+ws.ID, nil)
		getRec := httptest.NewRecorder()
		bh.BacklogItemPathHandler(getRec, getReq)
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 after delete", getRec.Code)
		}
	})
}

func TestBacklogHandler_Promote(t *testing.T) {
	store := newBacklogHandlerTestStore(t)
	ws := newBacklogHandlerTestWorkspace(t, store, "Alpha")
	svc := workspace.NewBacklogService(store)
	bh := NewBacklogHandler(svc)

	item, err := svc.Create(workspace.BacklogCreateInput{WorkspaceID: ws.ID, Description: "promote me"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	promote := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/orchestration/backlog/"+item.ID+"/promote?workspace_id="+ws.ID, nil)
		rec := httptest.NewRecorder()
		bh.BacklogItemPathHandler(rec, req)
		return rec
	}

	rec := promote()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var promoted backlogItemEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &promoted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if promoted.Item.Task.Status != workspace.TaskStatusPending {
		t.Fatalf("Status = %q, want Pending (Ready)", promoted.Item.Task.Status)
	}
	if promoted.Item.Task.To != "" {
		t.Fatalf("To = %q, want unassigned", promoted.Item.Task.To)
	}

	t.Run("repeated promotion is idempotent, not an error", func(t *testing.T) {
		rec := promote()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 on repeated promotion; body=%s", rec.Code, rec.Body.String())
		}
	})

	// The item must have left the Backlog projection entirely.
	listReq := httptest.NewRequest(http.MethodGet, "/api/orchestration/backlog?workspace_id="+ws.ID, nil)
	listRec := httptest.NewRecorder()
	bh.BacklogListHandler(listRec, listReq)
	var listed backlogListEnvelope
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if listed.Count != 0 {
		t.Fatalf("expected promoted item removed from Backlog list, got %+v", listed)
	}
}

func TestBacklogHandler_Reorder(t *testing.T) {
	store := newBacklogHandlerTestStore(t)
	ws := newBacklogHandlerTestWorkspace(t, store, "Alpha")
	svc := workspace.NewBacklogService(store)
	bh := NewBacklogHandler(svc)

	a, err := svc.Create(workspace.BacklogCreateInput{WorkspaceID: ws.ID, Description: "a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	b, err := svc.Create(workspace.BacklogCreateInput{WorkspaceID: ws.ID, Description: "b"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	body := `{"workspace_id":"` + ws.ID + `","ordered_ids":["` + b.ID + `","` + a.ID + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/backlog/reorder", strings.NewReader(body))
	rec := httptest.NewRecorder()
	bh.BacklogItemPathHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var result struct {
		Items []workspace.Task `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].ID != b.ID || result.Items[1].ID != a.ID {
		t.Fatalf("unexpected order: %+v", result.Items)
	}
}

func TestBacklogHandler_SyncNow(t *testing.T) {
	store := newBacklogHandlerTestStore(t)
	ws := newBacklogHandlerTestWorkspace(t, store, "Alpha")
	bh := NewBacklogHandler(workspace.NewBacklogService(store))

	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/backlog/sync",
		strings.NewReader(`{"workspace_id":"`+ws.ID+`"}`))
	rec := httptest.NewRecorder()
	bh.BacklogItemPathHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
