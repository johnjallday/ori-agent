package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
)

type updateReviewReaderFunc func(context.Context, string, string) (filejanitor.FilingReview, error)

func (f updateReviewReaderFunc) ReadFilingReview(ctx context.Context, owner, id string) (filejanitor.FilingReview, error) {
	return f(ctx, owner, id)
}

func aggregateRequest(h *Handler, method, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ListWorkspaceUpdates(w, httptest.NewRequest(method, "/api/workspace-updates"+query, nil))
	return w
}

func TestWorkspaceUpdateAggregateHTTPPagesOwnersAndIndependentSources(t *testing.T) {
	h, input := updateHTTPFixture(t)
	adapter := session.NewWorkspaceStoreAdapter(h.store)
	if _, err := h.store.DB().ExecContext(t.Context(), `INSERT INTO users (id,created_at,updated_at) VALUES ('other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i := range 107 {
		id := fmt.Sprintf("page-%03d", i)
		ws := &workspace.Workspace{ID: id, Name: "Desk", FolderSlug: "route-" + id, OwnerUserID: "local", Status: workspace.StatusActive,
			AgentInstances: []workspace.AgentInstance{{ID: "lead-" + id, Name: "Mira", EntryPoint: true}}}
		switch i {
		case 104:
			ws.OwnerUserID = "other"
		case 105:
			ws.Status = workspace.StatusTrashed
		case 106:
			ws.Status = workspace.StatusMissing
		}
		if err := adapter.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"ws", "page-050"} {
		entry := input
		if target != "ws" {
			ws, err := adapter.Get(target)
			if err != nil {
				t.Fatal(err)
			}
			entry.WorkspaceID, entry.WorkspaceVersion, entry.AgentInstanceID = target, ws.Version, "lead-"+target
		}
		if _, err := h.store.(workspaceupdates.Store).EnsureLeadConversation(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := adapter.Update("page-050", func(ws *workspace.Workspace) error { ws.AgentInstances[0].ID = "replacement"; return nil }); err != nil {
		t.Fatal(err)
	}
	reads := 0
	h.workspaceUpdates.SetReviewReader(updateReviewReaderFunc(func(_ context.Context, owner, id string) (filejanitor.FilingReview, error) {
		reads++
		if owner != "local" {
			t.Fatal("wrong projection owner", owner)
		}
		if id == "page-051" {
			return filejanitor.FilingReview{}, errors.New("PRIVATE path/token")
		}
		if id == "page-000" || id == "page-050" {
			return filejanitor.FilingReview{State: "ready", BatchID: "batch-" + id, ReviewCounts: &filejanitor.ReviewCounts{Pending: 10, NeedsReview: 2}}, nil
		}
		return filejanitor.FilingReview{State: "not_applicable"}, nil
	}))
	seen := map[string]workspaceupdates.Summary{}
	after := ""
	for pageIndex := range 2 {
		w := aggregateRequest(h, http.MethodGet, "?limit=100&after="+after)
		var page workspaceupdates.Page
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("page: %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "PRIVATE") || strings.Contains(w.Body.String(), "Private transcript") || strings.Contains(w.Body.String(), "needs_attention_count") {
			t.Fatal("private data or ticket counters entered updates", w.Body)
		}
		want := 100
		if pageIndex == 1 {
			want = 5
		}
		if len(page.Items) != want || page.HasMore != (pageIndex == 0) || (page.HasMore && page.NextAfter != "page-099") || (!page.HasMore && page.NextAfter != "") {
			t.Fatalf("silently truncated page: %+v", page)
		}
		for _, item := range page.Items {
			if _, exists := seen[item.WorkspaceID]; exists {
				t.Fatal("duplicate", item.WorkspaceID)
			}
			seen[item.WorkspaceID] = item
			if item.WorkspaceID != "ws" && item.FolderSlug != "route-"+item.WorkspaceID {
				t.Fatal("guessed slug", item)
			}
		}
		after = page.NextAfter
	}
	if reads != 105 || len(seen) != 105 {
		t.Fatal("unbounded projections or missed page", reads, len(seen))
	}
	if seen["ws"].Welcome.Unread != 1 || seen["page-050"].Welcome.State != "unavailable" || seen["page-050"].Review.Pending != 10 || seen["page-051"].Review.State != "unavailable" || seen["page-051"].Welcome.State != "ready" {
		t.Fatal("sources were merged or failed together")
	}
	for _, id := range []string{"page-104", "page-105", "page-106"} {
		if _, ok := seen[id]; ok {
			t.Fatal("foreign/hidden row leaked", id)
		}
	}
	var writes int
	if err := h.store.DB().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM workspace_welcome_enrollments)+(SELECT COUNT(*) FROM workspace_welcome_messages WHERE seen_at IS NOT NULL)`).Scan(&writes); err != nil || writes != 0 {
		t.Fatal("GET enrolled/acknowledged", writes, err)
	}
	h.currentUserID = func(context.Context) (string, error) { return "other", nil }
	h.workspaceUpdates.SetReviewReader(nil)
	w := aggregateRequest(h, http.MethodGet, "")
	var other workspaceupdates.Page
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &other) != nil || len(other.Items) != 1 || other.Items[0].WorkspaceID != "page-104" {
		t.Fatal("owner isolation", w.Code, w.Body)
	}
}

func TestWorkspaceUpdateAggregateHTTPRejectsSpoofingAndMalformedPages(t *testing.T) {
	h, _ := updateHTTPFixture(t)
	for _, query := range []string{"?owner_user_id=other", "?workspace_id=ws", "?limit=0", "?limit=101", "?limit=-1", "?limit=01", "?limit=%2B1", "?limit=", "?limit=one", "?limit=1&limit=2", "?after=x&after=y", "?after=%0A", "?after=%ff", "?after=%zz", "?after=x;y", "?after=" + strings.Repeat("x", 129), "?after=" + strings.Repeat("x", 600)} {
		w := aggregateRequest(h, http.MethodGet, query)
		if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if w := aggregateRequest(h, method, ""); w.Code != http.StatusMethodNotAllowed {
			t.Fatal(method, w.Code)
		}
	}
	h.currentUserID = func(context.Context) (string, error) { return "", errors.New("PRIVATE") }
	w := aggregateRequest(h, http.MethodGet, "")
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal(w.Code, w.Body)
	}
}
