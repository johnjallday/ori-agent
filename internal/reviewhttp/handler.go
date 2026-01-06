// Package reviewhttp provides HTTP handlers for the conversation review system.
package reviewhttp

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/review"
)

// Handler provides HTTP endpoints for the review system.
type Handler struct {
	runner *review.Runner
	store  review.Store
}

// NewHandler creates a new review HTTP handler.
func NewHandler(runner *review.Runner, store review.Store) *Handler {
	return &Handler{
		runner: runner,
		store:  store,
	}
}

// TriggerRequest is the request body for POST /api/review/trigger
type TriggerRequest struct {
	AgentName   string `json:"agent_name,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Since       string `json:"since,omitempty"` // RFC3339 format
	Sensitivity string `json:"sensitivity,omitempty"`
}

// TriggerResponse is the response for POST /api/review/trigger
type TriggerResponse struct {
	JobID   string `json:"job_id"`
	Message string `json:"message"`
}

// HandleTrigger handles POST /api/review/trigger
// Starts a new review job with optional filters.
func (h *Handler) HandleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TriggerRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			http.Error(w, "Invalid JSON body", http.StatusBadRequest)
			return
		}
	}

	// Parse since time if provided
	var since *time.Time
	if req.Since != "" {
		t, err := time.Parse(time.RFC3339, req.Since)
		if err != nil {
			http.Error(w, "Invalid since format (use RFC3339)", http.StatusBadRequest)
			return
		}
		since = &t
	}

	opts := review.ReviewOptions{
		AgentName:   req.AgentName,
		SessionID:   req.SessionID,
		Since:       since,
		Sensitivity: req.Sensitivity,
	}

	jobID, err := h.runner.StartReview(r.Context(), opts)
	if err != nil {
		http.Error(w, "Failed to start review: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := TriggerResponse{
		JobID:   jobID,
		Message: "Review job started",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

// StatusResponse is the response for GET /api/review/status/{job_id}
type StatusResponse struct {
	ID               string    `json:"id"`
	Status           string    `json:"status"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	SessionsReviewed int       `json:"sessions_reviewed"`
	IssuesFound      int       `json:"issues_found"`
	ErrorMessage     string    `json:"error_message,omitempty"`
}

// HandleStatus handles GET /api/review/status/{job_id}
// Returns the current status of a review job.
func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract job_id from path
	path := strings.TrimPrefix(r.URL.Path, "/api/review/status/")
	jobID := strings.TrimPrefix(path, "/")

	if jobID == "" {
		http.Error(w, "Missing job_id", http.StatusBadRequest)
		return
	}

	run, err := h.runner.GetStatus(r.Context(), jobID)
	if err != nil {
		http.Error(w, "Failed to get status: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if run == nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	resp := StatusResponse{
		ID:               run.ID,
		Status:           string(run.Status),
		StartedAt:        run.StartedAt,
		CompletedAt:      run.CompletedAt,
		SessionsReviewed: run.SessionsReviewed,
		IssuesFound:      run.IssuesFound,
		ErrorMessage:     run.ErrorMessage,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// IssuesResponse is the response for GET /api/review/issues
type IssuesResponse struct {
	Issues []review.Issue `json:"issues"`
	Count  int            `json:"count"`
}

// HandleIssues handles GET /api/review/issues
// Returns a list of detected issues with optional filtering.
func (h *Handler) HandleIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query()

	// Parse query parameters
	opts := review.IssueQueryOptions{
		AgentName: query.Get("agent_name"),
		SessionID: query.Get("session_id"),
	}

	if t := query.Get("issue_type"); t != "" {
		opts.IssueType = review.IssueType(t)
	}

	if since := query.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			http.Error(w, "Invalid since format (use RFC3339)", http.StatusBadRequest)
			return
		}
		opts.Since = t
	}

	if limit := query.Get("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil && l > 0 {
			opts.Limit = l
		}
	}

	issues, err := h.store.GetIssues(r.Context(), opts)
	if err != nil {
		http.Error(w, "Failed to get issues: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if issues == nil {
		issues = []review.Issue{}
	}

	resp := IssuesResponse{
		Issues: issues,
		Count:  len(issues),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleExport handles GET /api/review/export
// Exports issues in JSON or JSONL format for training purposes.
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query()
	format := query.Get("format")
	if format == "" {
		format = "json"
	}

	// Parse filtering options (same as HandleIssues)
	opts := review.IssueQueryOptions{
		AgentName: query.Get("agent_name"),
		SessionID: query.Get("session_id"),
	}

	if t := query.Get("issue_type"); t != "" {
		opts.IssueType = review.IssueType(t)
	}

	if since := query.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			http.Error(w, "Invalid since format (use RFC3339)", http.StatusBadRequest)
			return
		}
		opts.Since = t
	}

	issues, err := h.store.GetIssues(r.Context(), opts)
	if err != nil {
		http.Error(w, "Failed to get issues: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if issues == nil {
		issues = []review.Issue{}
	}

	switch format {
	case "jsonl":
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", "attachment; filename=review_issues.jsonl")

		bw := bufio.NewWriter(w)
		defer func() { _ = bw.Flush() }()

		for _, issue := range issues {
			line, err := json.Marshal(issue)
			if err != nil {
				continue
			}
			_, _ = bw.Write(line)
			_ = bw.WriteByte('\n')
		}

	default: // json
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=review_issues.json")
		_ = json.NewEncoder(w).Encode(issues)
	}
}

// HandleRuns handles GET /api/review/runs
// Returns recent review runs.
func (h *Handler) HandleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query()
	limit := 10
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	runs, err := h.store.GetReviewRuns(r.Context(), limit)
	if err != nil {
		http.Error(w, "Failed to get runs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if runs == nil {
		runs = []review.ReviewRun{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"runs":  runs,
		"count": len(runs),
	})
}
