package dailybriefhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// fakeGenerator is a minimal dailybrief.Generator for HTTP-layer tests.
type fakeGenerator struct{}

func (fakeGenerator) Generate(ctx context.Context, req dailybrief.GenerationRequest, cfg dailybrief.Config) (dailybrief.GenerationResult, error) {
	return dailybrief.GenerationResult{Status: dailybrief.GenerationSucceeded, ContentJSON: `{"opening_summary":"ok"}`}, nil
}

func newTestHandler(t *testing.T) (*Handler, *personalhq.Service, *session.SQLiteStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	profiles := userprofile.NewSQLiteStore(db)
	workspaces := session.NewSQLiteStore(db)
	hq := personalhq.NewService(profiles, workspaces)

	briefStore := dailybrief.NewSQLiteStore(db)
	briefSvc := dailybrief.NewService(briefStore, fakeGenerator{})

	handler := NewHandler(briefSvc, hq, userprofile.LocalUserProvider{})
	return handler, hq, workspaces
}

func designateHQ(t *testing.T, hq *personalhq.Service, workspaces *session.SQLiteStore, workspaceID string) {
	t.Helper()
	ctx := context.Background()
	ws := &session.Workspace{
		ID: workspaceID, Name: workspaceID, Kind: session.WorkspaceKindWorkspace,
		OwnerUserID: userprofile.LocalUserID, Status: session.WorkspaceStatusActive,
	}
	if err := workspaces.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if _, err := hq.Designate(ctx, userprofile.LocalUserID, workspaceID); err != nil {
		t.Fatalf("Designate: %v", err)
	}
}

func TestGetConfig_NoValidHQReturns404(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/config", nil)
	rec := httptest.NewRecorder()
	handler.GetConfig(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 with no HQ designated, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetConfig_ReturnsDefaultsWhenUnconfigured(t *testing.T) {
	handler, hq, workspaces := newTestHandler(t)
	designateHQ(t, hq, workspaces, "ws-hq")

	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/config", nil)
	rec := httptest.NewRecorder()
	handler.GetConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Config     dailybrief.Config `json:"config"`
		Configured bool              `json:"configured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Configured {
		t.Fatal("expected configured=false before any config is saved")
	}
	if got.Config.Timezone != "UTC" {
		t.Fatalf("expected default timezone UTC, got %q", got.Config.Timezone)
	}
}

func TestUpdateConfigThenGetConfigRoundTrips(t *testing.T) {
	handler, hq, workspaces := newTestHandler(t)
	designateHQ(t, hq, workspaces, "ws-hq")

	putReq := httptest.NewRequest(http.MethodPut, "/api/personal-hq/brief/config", bytes.NewBufferString(`{"timezone":"America/New_York","notify_on_ready":true}`))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.UpdateConfig(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/config", nil)
	getRec := httptest.NewRecorder()
	handler.GetConfig(getRec, getReq)
	var got struct {
		Config     dailybrief.Config `json:"config"`
		Configured bool              `json:"configured"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Configured || got.Config.Timezone != "America/New_York" || !got.Config.NotifyOnReady {
		t.Fatalf("config did not round-trip: %#v", got)
	}
}

func TestGetCurrent_NoRevisionYetReturnsNil(t *testing.T) {
	handler, hq, workspaces := newTestHandler(t)
	designateHQ(t, hq, workspaces, "ws-hq")

	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/current", nil)
	rec := httptest.NewRecorder()
	handler.GetCurrent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Revision *dailybrief.Revision `json:"revision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Revision != nil {
		t.Fatalf("expected nil revision before any generation, got %#v", got.Revision)
	}
}

// TestRequestFirstOpenGeneratesInBackgroundThenCurrentReflectsIt covers task
// 7.4: the request returns immediately (never blocking), and the brief
// becomes available shortly after via polling.
func TestRequestFirstOpenGeneratesInBackgroundThenCurrentReflectsIt(t *testing.T) {
	handler, hq, workspaces := newTestHandler(t)
	designateHQ(t, hq, workspaces, "ws-hq")

	req := httptest.NewRequest(http.MethodPost, "/api/personal-hq/brief/open", nil)
	rec := httptest.NewRecorder()
	handler.RequestFirstOpen(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted immediately, got %d body=%s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		getReq := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/current", nil)
		getRec := httptest.NewRecorder()
		handler.GetCurrent(getRec, getReq)
		var got struct {
			Revision *dailybrief.Revision `json:"revision"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &got)
		if got.Revision != nil {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected the background generation to produce a current revision within the deadline")
}

func TestRequestRefresh_RejectsWrongVerb(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/refresh", nil)
	rec := httptest.NewRecorder()
	handler.RequestRefresh(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestGetStatus_IdleWhenNothingRunning(t *testing.T) {
	handler, hq, workspaces := newTestHandler(t)
	designateHQ(t, hq, workspaces, "ws-hq")

	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/status", nil)
	rec := httptest.NewRecorder()
	handler.GetStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "idle" {
		t.Fatalf("expected idle status, got %q", got.Status)
	}
}

func TestServiceUnavailableWhenNotConfigured(t *testing.T) {
	handler := NewHandler(nil, nil, userprofile.LocalUserProvider{})
	req := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/current", nil)
	rec := httptest.NewRecorder()
	handler.GetCurrent(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}
