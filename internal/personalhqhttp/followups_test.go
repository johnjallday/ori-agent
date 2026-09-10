package personalhqhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newFollowUpHTTPHandler(t *testing.T) (*Handler, *workspace.InMemoryStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workspaceStore := workspace.NewInMemoryStore()
	handler := NewHandler(nil, nil, nil, userprofile.LocalUserProvider{})
	handler.SetFollowUps(followup.NewService(followup.NewSQLiteStore(db)))
	handler.SetEmailOpsSource(func() workspace.EmailOpsWorkspaceSource { return workspaceStore })
	handler.SetWatchtowerSources(func() dailybrief.SnapshotSources {
		return dailybrief.SnapshotSources{Workspaces: workspaceStore}
	})
	return handler, workspaceStore
}

func createEmailOpsWorkspace(t *testing.T, store *workspace.InMemoryStore) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Email Ops"})
	ws.ID = "email-ops-id"
	ws.FolderSlug = "email-ops"
	ws.OwnerUserID = userprofile.LocalUserID
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save Email Ops: %v", err)
	}
	return ws
}

func createFollowUpHTTP(t *testing.T, handler *Handler, title string) *followup.FollowUp {
	t.Helper()
	body := fmt.Sprintf(`{"category":"needs_decision","direction":"inbound","title":%q,"counterparty":"Alex"}`, title)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/followups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.CreateFollowUp(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		FollowUp *followup.FollowUp `json:"followup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.FollowUp == nil {
		t.Fatalf("decode create: followup=%+v err=%v body=%s", payload.FollowUp, err, rec.Body.String())
	}
	return payload.FollowUp
}

func workspaceFollowUpsHTTP(t *testing.T, handler *Handler, workspaceID string) []*followup.FollowUp {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/followups", nil)
	req.SetPathValue("workspaceID", workspaceID)
	rec := httptest.NewRecorder()
	handler.WorkspaceFollowUps(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		FollowUps []*followup.FollowUp `json:"followups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode workspace list: %v", err)
	}
	return payload.FollowUps
}

func emailOpsStatusHTTP(t *testing.T, handler *Handler) EmailOpsStatus {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.EmailOpsStatusHandler(rec, httptest.NewRequest(http.MethodGet, "/api/personal-hq/email-ops", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("portal status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Status EmailOpsStatus `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode portal: %v", err)
	}
	return payload.Status
}

func runFollowUpActionHTTP(t *testing.T, path, id string, action func(http.ResponseWriter, *http.Request)) *followup.FollowUp {
	t.Helper()
	body := fmt.Sprintf(`{"id":%q}`, id)
	if path == "/api/personal-hq/followups/snooze" {
		body = fmt.Sprintf(`{"id":%q,"until":%q}`, id, time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339))
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	action(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("action %s status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	var payload struct {
		FollowUp *followup.FollowUp `json:"followup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.FollowUp == nil {
		t.Fatalf("decode action %s: followup=%+v err=%v", path, payload.FollowUp, err)
	}
	return payload.FollowUp
}

func TestFollowUpHTTP_EmailOpsOwnershipPanelCountAndLifecycleRemainAligned(t *testing.T) {
	handler, store := newFollowUpHTTPHandler(t)
	emailOps := createEmailOpsWorkspace(t, store)
	completeTarget := createFollowUpHTTP(t, handler, "Complete this")
	snoozeTarget := createFollowUpHTTP(t, handler, "Snooze this")
	dismissTarget := createFollowUpHTTP(t, handler, "Dismiss this")

	for _, item := range []*followup.FollowUp{completeTarget, snoozeTarget, dismissTarget} {
		if item.WorkspaceID != emailOps.ID || item.Source.Type != "manual" || item.Provenance != followup.ProvenanceManual {
			t.Fatalf("manual follow-up ownership/provenance changed: %+v", item)
		}
	}
	listed := workspaceFollowUpsHTTP(t, handler, emailOps.ID)
	status := emailOpsStatusHTTP(t, handler)
	if len(listed) != 3 || !status.Exists || status.WorkspaceID != emailOps.ID ||
		status.WorkspaceSlug != emailOps.FolderSlug || status.OpenFollowupCount != 3 {
		t.Fatalf("owner-local panel/count mismatch: listed=%+v status=%+v", listed, status)
	}

	completed := runFollowUpActionHTTP(t, "/api/personal-hq/followups/complete", completeTarget.ID, handler.CompleteFollowUp)
	snoozed := runFollowUpActionHTTP(t, "/api/personal-hq/followups/snooze", snoozeTarget.ID, handler.SnoozeFollowUp)
	dismissed := runFollowUpActionHTTP(t, "/api/personal-hq/followups/dismiss", dismissTarget.ID, handler.DismissFollowUp)
	if completed.WorkspaceID != emailOps.ID || completed.Status != followup.StatusCompleted ||
		snoozed.WorkspaceID != emailOps.ID || snoozed.Status != followup.StatusSnoozed ||
		dismissed.WorkspaceID != emailOps.ID || dismissed.Status != followup.StatusDismissed {
		t.Fatalf("lifecycle moved ownership: completed=%+v snoozed=%+v dismissed=%+v", completed, snoozed, dismissed)
	}

	listed = workspaceFollowUpsHTTP(t, handler, emailOps.ID)
	status = emailOpsStatusHTTP(t, handler)
	if len(listed) != 1 || listed[0].ID != snoozeTarget.ID || listed[0].WorkspaceID != emailOps.ID ||
		status.OpenFollowupCount != 1 {
		t.Fatalf("next owner-local read/count ignored lifecycle: listed=%+v status=%+v", listed, status)
	}
}
