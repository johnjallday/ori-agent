package usagehttp

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// Handler handles usage and cost tracking HTTP requests
type Handler struct {
	costTracker *llm.CostTracker
}

// NewHandler creates a new usage HTTP handler
func NewHandler(costTracker *llm.CostTracker) *Handler {
	return &Handler{
		costTracker: costTracker,
	}
}

// GetAllTimeStats returns all-time usage statistics
// GET /api/usage/stats/all
func (h *Handler) GetAllTimeStats(w http.ResponseWriter, r *http.Request) {
	stats := h.costTracker.GetAllTimeStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GetTodayStats returns today's usage statistics
// GET /api/usage/stats/today
func (h *Handler) GetTodayStats(w http.ResponseWriter, r *http.Request) {
	stats := h.costTracker.GetTodayStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GetThisMonthStats returns this month's usage statistics
// GET /api/usage/stats/month
func (h *Handler) GetThisMonthStats(w http.ResponseWriter, r *http.Request) {
	stats := h.costTracker.GetThisMonthStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GetCustomRangeStats returns usage statistics for a custom time range
// GET /api/usage/stats/range?start=2024-01-01T00:00:00Z&end=2024-12-31T23:59:59Z
func (h *Handler) GetCustomRangeStats(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")

	if startStr == "" || endStr == "" {
		http.Error(w, "start and end parameters are required", http.StatusBadRequest)
		return
	}

	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		http.Error(w, "invalid start time format, use RFC3339", http.StatusBadRequest)
		return
	}

	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		http.Error(w, "invalid end time format, use RFC3339", http.StatusBadRequest)
		return
	}

	stats := h.costTracker.GetStats(start, end)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GetPricingModels returns all pricing models
// GET /api/usage/pricing
func (h *Handler) GetPricingModels(w http.ResponseWriter, r *http.Request) {
	models := h.costTracker.GetPricingModels()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pricing_models": models,
	})
}

// UpdatePricingModel updates a pricing model
// PUT /api/usage/pricing
func (h *Handler) UpdatePricingModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var model llm.PricingModel
	if err := json.NewDecoder(r.Body).Decode(&model); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	h.costTracker.UpdatePricingModel(model)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
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

	summary := map[string]interface{}{
		"today": map[string]interface{}{
			"requests": todayStats.TotalRequests,
			"tokens":   todayStats.TotalTokens,
			"cost":     todayStats.TotalCost,
			"currency": todayStats.Currency,
		},
		"this_month": map[string]interface{}{
			"requests": monthStats.TotalRequests,
			"tokens":   monthStats.TotalTokens,
			"cost":     monthStats.TotalCost,
			"currency": monthStats.Currency,
		},
		"all_time": map[string]interface{}{
			"requests": allTimeStats.TotalRequests,
			"tokens":   allTimeStats.TotalTokens,
			"cost":     allTimeStats.TotalCost,
			"currency": allTimeStats.Currency,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}
