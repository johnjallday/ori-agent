package workspacecapabilityhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

type countedBackupRuntime struct{ status, remove int }

func (r *countedBackupRuntime) CapabilityStatus(string) (workspacecapability.Status, error) {
	r.status++
	return workspacecapability.Status{State: workspacecapability.StatusReady}, nil
}
func (r *countedBackupRuntime) OnCapabilityRemove(string) error { r.remove++; return nil }
func TestBackupGenericCapabilityBoundaryRejectsBeforeContextFreeCallbacks(t *testing.T) {
	for _, scenario := range []string{"folded owner", "folded actor", "remote catalog", "cross port removal", "missing action", "unknown body", "duplicate body"} {
		t.Run(scenario, func(t *testing.T) {
			view := ownedWorkspace("backup", "local")
			view.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityBackupRecovery, Version: 1, InstalledAt: time.Now().UTC(), Source: workspace.InstallSourceInPlace}}
			actor := "local"
			method, path, body := http.MethodGet, "/api/workspaces/backup/capabilities", ""
			if scenario == "folded owner" {
				view.OwnerUserID = "LOCAL"
			}
			if scenario == "folded actor" {
				actor = "LOCAL"
			}
			if scenario != "folded owner" && scenario != "folded actor" && scenario != "remote catalog" {
				method = http.MethodDelete
				path += "/backup-recovery"
				body = "{}"
			}
			if scenario == "unknown body" {
				body = `{"command":"run"}`
			}
			if scenario == "duplicate body" {
				body = `{"remove_companion":false,"Remove_Companion":true}`
			}
			h, service := newTestHandler(t, newMemStore(view), stubUserProvider{id: actor})
			runtime := &countedBackupRuntime{}
			if err := service.Registry().BindRuntime(workspace.CapabilityBackupRecovery, runtime); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(method, "http://localhost:8765"+path, strings.NewReader(body))
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Origin", "http://localhost:8765")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Ori-Backup-Action", "user-control")
			if scenario == "remote catalog" {
				request.RemoteAddr = "192.0.2.1:1234"
			}
			if scenario == "cross port removal" {
				request.Header.Set("Origin", "http://localhost:9999")
			}
			if scenario == "missing action" {
				request.Header.Del("X-Ori-Backup-Action")
			}
			response := httptest.NewRecorder()
			newTestMux(t, h).ServeHTTP(response, request)
			if response.Code < 400 || runtime.status != 0 || runtime.remove != 0 {
				t.Fatal("alternate boundary dispatched", response.Code, runtime, response.Body.String())
			}
		})
	}
}
