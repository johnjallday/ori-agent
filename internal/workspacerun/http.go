package workspacerun

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

type Handler struct {
	store   Store
	service *Service
}

func NewHandler(store Store, service *Service) *Handler {
	return &Handler{store: store, service: service}
}

func (h *Handler) CreateRun(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return
	}
	var req CreateRunRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	run, err := h.service.CreateRun(r.Context(), workspaceID, req)
	if err != nil {
		writeRunError(w, err)
		return
	}
	go func() {
		_ = h.service.ExecuteRun(context.Background(), workspaceID, run.ID)
	}()
	orihttp.Created(w, run)
}

func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return
	}
	runs, err := h.store.ListRuns(r.Context(), workspaceID)
	if err != nil {
		writeRunError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"runs": runs})
}

func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, runID := requireWorkspaceAndRunID(w, r)
	if workspaceID == "" || runID == "" {
		return
	}
	run, err := h.store.GetRun(r.Context(), workspaceID, runID)
	if err != nil {
		writeRunError(w, err)
		return
	}
	orihttp.Success(w, run)
}

func (h *Handler) StopRun(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, runID := requireWorkspaceAndRunID(w, r)
	if workspaceID == "" || runID == "" {
		return
	}
	if err := h.service.StopRun(r.Context(), workspaceID, runID); err != nil {
		writeRunError(w, err)
		return
	}
	orihttp.Success(w, map[string]string{"status": string(RunStatusCancelled), "run_id": runID})
}

func (h *Handler) ApproveRun(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, runID := requireWorkspaceAndRunID(w, r)
	if workspaceID == "" || runID == "" {
		return
	}
	var req struct {
		Comment string `json:"comment,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
	}
	if err := h.service.ApproveRun(r.Context(), workspaceID, runID); err != nil {
		writeRunError(w, err)
		return
	}
	orihttp.Success(w, map[string]string{"status": string(RunStatusSucceeded), "run_id": runID})
}

func (h *Handler) RejectRun(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	workspaceID, runID := requireWorkspaceAndRunID(w, r)
	if workspaceID == "" || runID == "" {
		return
	}
	var req struct {
		Reason string `json:"reason,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
	}
	if err := h.service.RejectRun(r.Context(), workspaceID, runID, req.Reason); err != nil {
		writeRunError(w, err)
		return
	}
	orihttp.Success(w, map[string]string{"status": string(RunStatusRejected), "run_id": runID})
}

func (h *Handler) ListArtifacts(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, runID := requireWorkspaceAndRunID(w, r)
	if workspaceID == "" || runID == "" {
		return
	}
	artifacts, err := h.store.ListArtifacts(r.Context(), workspaceID, runID)
	if err != nil {
		writeRunError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"artifacts": artifacts})
}

func (h *Handler) ListTrace(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	workspaceID, runID := requireWorkspaceAndRunID(w, r)
	if workspaceID == "" || runID == "" {
		return
	}
	since, err := ParseSince(r.URL.Query().Get("since"))
	if err != nil {
		orihttp.BadRequest(w, "invalid since")
		return
	}
	limit := DefaultTracePageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			orihttp.BadRequest(w, "invalid limit")
			return
		}
		limit = parsed
	}
	page, err := h.store.ListTrace(r.Context(), workspaceID, runID, since, limit)
	if err != nil {
		writeRunError(w, err)
		return
	}
	orihttp.Success(w, page)
}

func requireWorkspaceID(w http.ResponseWriter, r *http.Request) string {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspaceID is required")
	}
	return workspaceID
}

func requireWorkspaceAndRunID(w http.ResponseWriter, r *http.Request) (string, string) {
	workspaceID := requireWorkspaceID(w, r)
	if workspaceID == "" {
		return "", ""
	}
	runID := strings.TrimSpace(r.PathValue("runID"))
	if runID == "" {
		orihttp.BadRequest(w, "runID is required")
		return "", ""
	}
	return workspaceID, runID
}

func writeRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRunNotFound):
		orihttp.NotFound(w, "Workspace run not found")
	case errors.Is(err, ErrRunExists):
		orihttp.Conflict(w, "Workspace run already exists")
	case errors.Is(err, ErrProfileNotFound), errors.Is(err, ErrExecutorNotRegistered):
		orihttp.BadRequest(w, err.Error())
	default:
		orihttp.InternalError(w, err.Error())
	}
}
