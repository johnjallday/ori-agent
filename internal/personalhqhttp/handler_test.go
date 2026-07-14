package personalhqhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func newTestHandler(t *testing.T) (*Handler, *session.SQLiteStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	profiles := userprofile.NewSQLiteStore(db)
	workspaces := session.NewSQLiteStore(db)
	service := personalhq.NewService(profiles, workspaces)
	return NewHandler(service, userprofile.LocalUserProvider{}), workspaces
}

func createWorkspace(t *testing.T, store *session.SQLiteStore, id string, kind session.WorkspaceKind, status session.WorkspaceStatus) {
	t.Helper()
	ws := &session.Workspace{
		ID:          id,
		Name:        id,
		Kind:        kind,
		OwnerUserID: userprofile.LocalUserID,
		Status:      status,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatalf("CreateWorkspace(%s): %v", id, err)
	}
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) personalhq.Status {
	t.Helper()
	var got struct {
		Status personalhq.Status `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return got.Status
}

func TestStatusReturnsNoDesignationInitially(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/status", nil)
	rec := httptest.NewRecorder()
	handler.Status(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	status := decodeStatus(t, rec)
	if status.HasDesignation() {
		t.Fatalf("expected no designation, got %#v", status)
	}
}

func TestDesignateRejectsInvalidWorkspaceWithActionableError(t *testing.T) {
	handler, workspaces := newTestHandler(t)
	createWorkspace(t, workspaces, "group-1", session.WorkspaceKindGroup, session.WorkspaceStatusActive)

	body := bytes.NewBufferString(`{"workspace_id":"group-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/designate", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Designate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a group workspace, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDesignateMissingWorkspaceReturnsNotFound(t *testing.T) {
	handler, _ := newTestHandler(t)

	body := bytes.NewBufferString(`{"workspace_id":"does-not-exist"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/designate", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Designate(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing workspace, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDesignateThenReplaceThenClear(t *testing.T) {
	handler, workspaces := newTestHandler(t)
	createWorkspace(t, workspaces, "ws-1", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)
	createWorkspace(t, workspaces, "ws-2", session.WorkspaceKindWorkspace, session.WorkspaceStatusActive)

	designateReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/designate", bytes.NewBufferString(`{"workspace_id":"ws-1"}`))
	designateReq.Header.Set("Content-Type", "application/json")
	designateRec := httptest.NewRecorder()
	handler.Designate(designateRec, designateReq)
	if designateRec.Code != http.StatusOK {
		t.Fatalf("designate status = %d body=%s", designateRec.Code, designateRec.Body.String())
	}
	if status := decodeStatus(t, designateRec); status.WorkspaceID != "ws-1" || !status.Valid {
		t.Fatalf("expected valid designation of ws-1, got %#v", status)
	}

	// A second Designate call without replace must be rejected (409) — the
	// UI should route to Replace with explicit confirmation instead.
	secondDesignateReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/designate", bytes.NewBufferString(`{"workspace_id":"ws-2"}`))
	secondDesignateReq.Header.Set("Content-Type", "application/json")
	secondDesignateRec := httptest.NewRecorder()
	handler.Designate(secondDesignateRec, secondDesignateReq)
	if secondDesignateRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for re-designation, got %d body=%s", secondDesignateRec.Code, secondDesignateRec.Body.String())
	}

	replaceReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/replace", bytes.NewBufferString(`{"workspace_id":"ws-2"}`))
	replaceReq.Header.Set("Content-Type", "application/json")
	replaceRec := httptest.NewRecorder()
	handler.Replace(replaceRec, replaceReq)
	if replaceRec.Code != http.StatusOK {
		t.Fatalf("replace status = %d body=%s", replaceRec.Code, replaceRec.Body.String())
	}
	if status := decodeStatus(t, replaceRec); status.WorkspaceID != "ws-2" {
		t.Fatalf("expected replacement to designate ws-2, got %#v", status)
	}

	clearReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/clear", nil)
	clearRec := httptest.NewRecorder()
	handler.Clear(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", clearRec.Code, clearRec.Body.String())
	}
	if status := decodeStatus(t, clearRec); status.HasDesignation() {
		t.Fatalf("expected designation cleared, got %#v", status)
	}
}

func TestSetOnboardingStateRoundTrips(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/onboarding-state", bytes.NewBufferString(`{"state":"skipped"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.SetOnboardingState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if status := decodeStatus(t, rec); status.OnboardingState != userprofile.HQOnboardingSkipped {
		t.Fatalf("expected skipped onboarding state, got %#v", status)
	}
}

func TestSetOnboardingStateRejectsUnknownValue(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/onboarding-state", bytes.NewBufferString(`{"state":"bogus"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.SetOnboardingState(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown state, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMethodContractRejectsWrongVerb(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/status", nil)
	rec := httptest.NewRecorder()
	handler.Status(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST to status, got %d", rec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/personal-hq/clear", nil)
	getRec := httptest.NewRecorder()
	handler.Clear(getRec, getReq)
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET to clear, got %d", getRec.Code)
	}
}

func TestStatusDegradesWhenServiceUnavailable(t *testing.T) {
	handler := NewHandler(nil, userprofile.LocalUserProvider{})

	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/status", nil)
	rec := httptest.NewRecorder()
	handler.Status(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when service is nil, got %d body=%s", rec.Code, rec.Body.String())
	}
}
