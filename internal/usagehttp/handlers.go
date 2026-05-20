package usagehttp

import (
	"net/http"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/utilitytelemetry"
)

// Handler handles usage and cost tracking HTTP requests
type Handler struct {
	costTracker      *llm.CostTracker
	utilityTelemetry *utilitytelemetry.Tracker
}

// NewHandler creates a new usage HTTP handler
func NewHandler(costTracker *llm.CostTracker) *Handler {
	return &Handler{
		costTracker: costTracker,
	}
}

// SetUtilityTelemetry sets the optional utility telemetry tracker.
func (h *Handler) SetUtilityTelemetry(tracker *utilitytelemetry.Tracker) {
	h.utilityTelemetry = tracker
}

// GetAllTimeStats returns all-time usage statistics
// GET /api/usage/stats/all
func (h *Handler) GetAllTimeStats(w http.ResponseWriter, r *http.Request) {
	stats := h.costTracker.GetAllTimeStats()
	orihttp.WriteJSON(w, stats)
}

// GetTodayStats returns today's usage statistics
// GET /api/usage/stats/today
func (h *Handler) GetTodayStats(w http.ResponseWriter, r *http.Request) {
	stats := h.costTracker.GetTodayStats()
	orihttp.WriteJSON(w, stats)
}

// GetThisMonthStats returns this month's usage statistics
// GET /api/usage/stats/month
func (h *Handler) GetThisMonthStats(w http.ResponseWriter, r *http.Request) {
	stats := h.costTracker.GetThisMonthStats()
	orihttp.WriteJSON(w, stats)
}

// GetCustomRangeStats returns usage statistics for a custom time range
// GET /api/usage/stats/range?start=2024-01-01T00:00:00Z&end=2024-12-31T23:59:59Z
func (h *Handler) GetCustomRangeStats(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	if startStr == "" || endStr == "" {
		orihttp.BadRequest(w, "start and end parameters are required")
		return
	}

	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		orihttp.BadRequest(w, "invalid start time format, use RFC3339")
		return
	}

	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		orihttp.BadRequest(w, "invalid end time format, use RFC3339")
		return
	}

	stats := h.costTracker.GetStats(start, end)
	orihttp.WriteJSON(w, stats)
}

// GetPricingModels returns all pricing models
// GET /api/usage/pricing
func (h *Handler) GetPricingModels(w http.ResponseWriter, r *http.Request) {
	models := h.costTracker.GetPricingModels()
	orihttp.WriteJSON(w, map[string]any{
		"pricing_models": models,
	})
}

// UpdatePricingModel updates a pricing model
// PUT /api/usage/pricing
func (h *Handler) UpdatePricingModel(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethods(w, r, http.MethodPut, http.MethodPost) {
		return
	}

	var model llm.PricingModel
	if !orihttp.ParseJSONBody(w, r, &model) {
		return
	}

	h.costTracker.UpdatePricingModel(model)

	orihttp.WriteJSON(w, map[string]any{
		"success": true,
		"message": "Pricing model updated successfully",
	})
}

// GetSummary returns a quick summary of usage stats
// GET /api/usage/summary
func (h *Handler) GetSummary(w http.ResponseWriter, r *http.Request) {
	todayStats := h.costTracker.GetTodayStats()
	monthStats := h.costTracker.GetThisMonthStats()
	allTimeStats := h.costTracker.GetAllTimeStats()

	summary := map[string]any{
		"today": map[string]any{
			"requests": todayStats.TotalRequests,
			"tokens":   todayStats.TotalTokens,
			"cost":     todayStats.TotalCost,
			"currency": todayStats.Currency,
		},
		"this_month": map[string]any{
			"requests": monthStats.TotalRequests,
			"tokens":   monthStats.TotalTokens,
			"cost":     monthStats.TotalCost,
			"currency": monthStats.Currency,
		},
		"all_time": map[string]any{
			"requests": allTimeStats.TotalRequests,
			"tokens":   allTimeStats.TotalTokens,
			"cost":     allTimeStats.TotalCost,
			"currency": allTimeStats.Currency,
		},
	}

	if h.utilityTelemetry != nil {
		utilitySnapshot := h.utilityTelemetry.Snapshot()
		summary["utility"] = map[string]any{
			"generated_at": utilitySnapshot.GeneratedAt,
			"totals":       utilitySnapshot.Totals,
			"route_counts": utilitySnapshot.RouteCounts,
			"event_counts": utilitySnapshot.EventCounts,
		}
	}

	orihttp.WriteJSON(w, summary)
}

// GetUtilityMetrics returns aggregate utility routing + tool telemetry.
// GET /api/usage/utility
func (h *Handler) GetUtilityMetrics(w http.ResponseWriter, r *http.Request) {
	if h.utilityTelemetry == nil {
		orihttp.WriteJSON(w, map[string]any{
			"enabled": false,
			"message": "utility telemetry is not enabled",
		})
		return
	}

	snapshot := h.utilityTelemetry.Snapshot()
	orihttp.WriteJSON(w, map[string]any{
		"enabled": true,
		"metrics": snapshot,
	})
}
