package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

const uiSmokeTestBodyLimit = 2 << 20

type uiSmokeTestRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type uiSmokeTestResponse struct {
	Status     string             `json:"status"`
	Summary    string             `json:"summary"`
	Total      int                `json:"total"`
	Passed     int                `json:"passed"`
	Failed     int                `json:"failed"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at"`
	DurationMS int64              `json:"duration_ms"`
	Checks     []uiSmokeTestCheck `json:"checks"`
}

type uiSmokeTestCheck struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code"`
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail"`
}

type uiSmokeCheckSpec struct {
	name             string
	path             string
	expectedStatuses []int
	requiredSnippets []string
}

func (s *Server) handleUISmokeTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var req uiSmokeTestRequest
	if r.Body != nil {
		defer func() { _ = r.Body.Close() }()
		limitedBody := io.LimitReader(r.Body, uiSmokeTestBodyLimit)
		if err := json.NewDecoder(limitedBody).Decode(&req); err != nil && err != io.EOF {
			orihttp.BadRequest(w, "Invalid JSON: "+err.Error())
			return
		}
	}

	result := s.runUISmokeTest(strings.TrimSpace(req.WorkspaceID))
	orihttp.Success(w, result)
}

func (s *Server) runUISmokeTest(workspaceID string) uiSmokeTestResponse {
	startedAt := time.Now()
	handler := s.Handler()
	specs := []uiSmokeCheckSpec{
		{
			name:             "Health endpoint",
			path:             "/health",
			expectedStatuses: []int{http.StatusOK},
			requiredSnippets: []string{`"status":"ok"`},
		},
		{
			name:             "Dashboard JavaScript",
			path:             "/js/modules/dashboard.js",
			expectedStatuses: []int{http.StatusOK},
			requiredSnippets: []string{"handleHomeAssistantPrompt", "buildWorkspacePromptModeCommand"},
		},
		{
			name:             "Common stylesheet",
			path:             "/css/common.css",
			expectedStatuses: []int{http.StatusOK},
			requiredSnippets: []string{".modern-card", ".home-assistant-workspace-mode-switch"},
		},
	}

	if workspaceID != "" {
		specs = append(specs,
			uiSmokeCheckSpec{
				name:             "Workspace detail page",
				path:             "/workspaces/" + workspaceID,
				expectedStatuses: []int{http.StatusOK},
				requiredSnippets: []string{"homeAssistantForm", "homeAssistantWorkspaceModeSwitch", "open-diagnostics-btn"},
			},
			uiSmokeCheckSpec{
				name:             "Workspace diagnostics page",
				path:             "/workspaces/" + workspaceID + "/diagnostics",
				expectedStatuses: []int{http.StatusOK},
				requiredSnippets: []string{"workspace-detail-health-panel", "workspace-detail-ui-smoke-test"},
			},
		)
	}

	checks := make([]uiSmokeTestCheck, 0, len(specs))
	passed := 0
	for _, spec := range specs {
		check := runUISmokeCheck(handler, spec)
		if check.Status == "passed" {
			passed++
		}
		checks = append(checks, check)
	}

	finishedAt := time.Now()
	failed := len(checks) - passed
	status := "passed"
	if failed > 0 {
		status = "failed"
	}

	return uiSmokeTestResponse{
		Status:     status,
		Summary:    buildUISmokeSummary(passed, failed),
		Total:      len(checks),
		Passed:     passed,
		Failed:     failed,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
		Checks:     checks,
	}
}

func runUISmokeCheck(handler http.Handler, spec uiSmokeCheckSpec) (check uiSmokeTestCheck) {
	startedAt := time.Now()
	check = uiSmokeTestCheck{
		Name:   spec.name,
		Path:   spec.path,
		Status: "failed",
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			check.Status = "failed"
			check.Detail = "Check panicked while rendering target"
			logger.Error("UI smoke check panicked", logger.Fields{"path": spec.path, "panic": recovered})
		}
	}()

	req := httptest.NewRequest(http.MethodGet, spec.path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	statusCode := rr.Code
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	check.StatusCode = statusCode
	check.DurationMS = time.Since(startedAt).Milliseconds()

	if !statusCodeAllowed(statusCode, spec.expectedStatuses) {
		check.Detail = "Expected " + joinStatusCodes(spec.expectedStatuses) + ", got " + http.StatusText(statusCode)
		if check.Detail == "" {
			check.Detail = "Unexpected HTTP status"
		}
		return check
	}

	body := rr.Body.String()
	for _, snippet := range spec.requiredSnippets {
		if !strings.Contains(body, snippet) {
			check.Detail = "Missing expected content: " + snippet
			return check
		}
	}

	check.Status = "passed"
	check.Detail = "Rendered expected content"
	return check
}

func statusCodeAllowed(statusCode int, allowed []int) bool {
	if len(allowed) == 0 {
		return statusCode >= 200 && statusCode < 300
	}
	for _, candidate := range allowed {
		if statusCode == candidate {
			return true
		}
	}
	return false
}

func joinStatusCodes(statuses []int) string {
	if len(statuses) == 0 {
		return "2xx"
	}
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		label := http.StatusText(status)
		if label == "" {
			label = "status"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " or ")
}

func buildUISmokeSummary(passed, failed int) string {
	total := passed + failed
	if failed == 0 {
		return pluralizeCount(passed, "check") + " passed"
	}
	return pluralizeCount(passed, "check") + " passed, " + pluralizeCount(failed, "check") + " failed out of " + pluralizeCount(total, "check")
}

func pluralizeCount(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + singular + "s"
}
