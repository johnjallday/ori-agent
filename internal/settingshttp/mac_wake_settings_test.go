package settingshttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/macwake"
)

type fakeMacWakeService struct {
	status         macwake.Status
	updateEnabled  *bool
	updateLead     *int
	updateFallback *string
	updateErr      error
	permissionErr  error
	permissionHit  bool
}

func (f *fakeMacWakeService) Status() macwake.Status {
	return f.status
}

func (f *fakeMacWakeService) UpdateSettings(enabled *bool, leadMinutes *int, fallbackPolicy *string) (macwake.Status, error) {
	f.updateEnabled = enabled
	f.updateLead = leadMinutes
	f.updateFallback = fallbackPolicy
	return f.status, f.updateErr
}

func (f *fakeMacWakeService) RequestAdminApproval() (macwake.Status, error) {
	f.permissionHit = true
	return f.status, f.permissionErr
}

func newMacWakeSettingsTestHandler(t *testing.T, service macWakeService) *Handler {
	t.Helper()

	configManager := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := configManager.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	handler := NewHandler(nil, configManager, nil, llm.NewFactory())
	handler.SetMacWakeService(service)
	return handler
}

func TestMacWakeSettingsHandler_Get(t *testing.T) {
	nextWake := time.Date(2026, 5, 6, 8, 55, 0, 0, time.UTC)
	service := &fakeMacWakeService{
		status: macwake.Status{
			Supported:          true,
			Enabled:            true,
			PermissionState:    "ready",
			PermissionLabel:    "Ready",
			DefaultLeadMinutes: 5,
			FallbackPolicy:     "run_on_next_wake",
			NextWakeAt:         &nextWake,
			NextWakeTaskID:     "task-1",
		},
	}
	handler := newMacWakeSettingsTestHandler(t, service)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/mac-wake", nil)
	rec := httptest.NewRecorder()
	handler.MacWakeSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		MacWake macwake.Status `json:"mac_wake"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.MacWake.Enabled || resp.MacWake.NextWakeTaskID != "task-1" {
		t.Fatalf("unexpected mac wake status: %#v", resp.MacWake)
	}
}

func TestMacWakeSettingsHandler_Post(t *testing.T) {
	service := &fakeMacWakeService{status: macwake.Status{Supported: true, Enabled: true}}
	handler := newMacWakeSettingsTestHandler(t, service)

	body := bytes.NewBufferString(`{"enabled":true,"default_lead_minutes":15,"fallback_policy":"skip"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/mac-wake", body)
	rec := httptest.NewRecorder()
	handler.MacWakeSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if service.updateEnabled == nil || !*service.updateEnabled {
		t.Fatal("expected enabled update")
	}
	if service.updateLead == nil || *service.updateLead != 15 {
		t.Fatalf("expected lead update 15, got %#v", service.updateLead)
	}
	if service.updateFallback == nil || *service.updateFallback != "skip" {
		t.Fatalf("expected fallback update skip, got %#v", service.updateFallback)
	}
}

func TestMacWakePermissionHandler(t *testing.T) {
	service := &fakeMacWakeService{
		status: macwake.Status{
			Supported:       true,
			PermissionState: "ready",
		},
	}
	handler := newMacWakeSettingsTestHandler(t, service)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/mac-wake/permission", nil)
	rec := httptest.NewRecorder()
	handler.MacWakePermissionHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !service.permissionHit {
		t.Fatal("expected permission request to be forwarded")
	}
}

func TestMacWakePermissionHandler_Error(t *testing.T) {
	service := &fakeMacWakeService{permissionErr: fmt.Errorf("denied")}
	handler := newMacWakeSettingsTestHandler(t, service)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/mac-wake/permission", nil)
	rec := httptest.NewRecorder()
	handler.MacWakePermissionHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
