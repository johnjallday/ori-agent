package usagehttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/utilitytelemetry"
)

func TestGetUtilityMetrics_WhenTelemetryDisabled(t *testing.T) {
	handler := NewHandler(llm.NewCostTracker(t.TempDir()))

	req := httptest.NewRequest(http.MethodGet, "/api/usage/utility", nil)
	rr := httptest.NewRecorder()
	handler.GetUtilityMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if enabled, _ := payload["enabled"].(bool); enabled {
		t.Fatalf("expected enabled=false when telemetry tracker is nil")
	}
}

func TestGetUtilityMetrics_WhenTelemetryEnabled(t *testing.T) {
	handler := NewHandler(llm.NewCostTracker(t.TempDir()))
	tracker := utilitytelemetry.NewTracker(10)
	tracker.RecordRouteDecision("utility_direct", "matched utility request")
	tracker.RecordToolInvocation("time", "system-clock")
	tracker.RecordToolResult("time", "system-clock", true, 25*time.Millisecond, "")
	handler.SetUtilityTelemetry(tracker)

	req := httptest.NewRequest(http.MethodGet, "/api/usage/utility", nil)
	rr := httptest.NewRecorder()
	handler.GetUtilityMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if enabled, _ := payload["enabled"].(bool); !enabled {
		t.Fatalf("expected enabled=true when telemetry tracker is configured")
	}

	metrics, ok := payload["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("expected metrics object in payload")
	}
	if _, exists := metrics["totals"]; !exists {
		t.Fatalf("expected metrics.totals field")
	}
}

func TestGetSummary_IncludesUtilityMetricsWhenAvailable(t *testing.T) {
	handler := NewHandler(llm.NewCostTracker(t.TempDir()))
	tracker := utilitytelemetry.NewTracker(10)
	tracker.RecordToolInvocation("time", "system-clock")
	tracker.RecordToolResult("time", "system-clock", true, 10*time.Millisecond, "")
	handler.SetUtilityTelemetry(tracker)

	req := httptest.NewRequest(http.MethodGet, "/api/usage/summary", nil)
	rr := httptest.NewRecorder()
	handler.GetSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := payload["utility"]; !ok {
		t.Fatalf("expected utility field in summary payload")
	}
}
