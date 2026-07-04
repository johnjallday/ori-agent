// Package memoryhttp serves the workspace memory API: read, add, edit, and
// delete the curated MEMORY.md entries surfaced in the workspace Memory tab.
// The file on disk is canonical; this handler is a thin CRUD layer over
// workspace.MemoryStore.
package memoryhttp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// workspaceLookup resolves a workspace for existence checks. *workspace.FileStore
// satisfies it (and also satisfies workspace.WorkspaceFolderResolver).
type workspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

// Handler serves the workspace memory endpoints.
type Handler struct {
	lookup workspaceLookup
	memory *workspace.MemoryStore
}

// NewHandler builds the memory handler over a folder-backed store. Both
// arguments are typically the same *workspace.FileStore. Returns a handler
// whose endpoints 503 when the store is unavailable.
func NewHandler(lookup workspaceLookup, resolver workspace.FolderResolver) *Handler {
	h := &Handler{lookup: lookup}
	if resolver != nil {
		h.memory = workspace.NewMemoryStore(resolver)
	}
	return h
}

type entryDTO struct {
	Index      int    `json:"index"`
	Type       string `json:"type"`
	Date       string `json:"date"`
	Provenance string `json:"provenance"`
	Text       string `json:"text"`
}

type memoryResponse struct {
	Entries      []entryDTO `json:"entries"`
	Unstructured []string   `json:"unstructured"`
	RawSize      int        `json:"raw_size"`
	CharBudget   int        `json:"char_budget"`
	TokenBudget  int        `json:"token_budget"`
	OverBudget   bool       `json:"over_budget"`
}

type writeRequest struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

// GetMemory handles GET /api/workspaces/{workspaceID}/memory.
func (h *Handler) GetMemory(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	h.writeMemoryResponse(w, workspaceID)
}

// AddEntry handles POST /api/workspaces/{workspaceID}/memory/entries.
func (h *Handler) AddEntry(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	var req writeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	text, err := workspace.ValidateMemoryText(req.Text)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	entry := workspace.MemoryEntry{
		Type:       workspace.NormalizeMemoryEntryType(req.Type),
		Date:       time.Now().Format("2006-01-02"),
		Provenance: "user",
		Text:       text,
	}
	if err := h.memory.Append(workspaceID, entry); err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to save memory entry: "+err.Error())
		return
	}
	h.writeMemoryResponse(w, workspaceID)
}

// UpdateEntry handles PUT /api/workspaces/{workspaceID}/memory/entries/{index}.
func (h *Handler) UpdateEntry(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	index, ok := h.parseIndex(w, r)
	if !ok {
		return
	}
	var req writeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	text, err := workspace.ValidateMemoryText(req.Text)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}
	entry := workspace.MemoryEntry{
		Type:       workspace.NormalizeMemoryEntryType(req.Type),
		Date:       time.Now().Format("2006-01-02"),
		Provenance: "user",
		Text:       text,
	}
	if err := h.memory.EditAt(workspaceID, index, entry); err != nil {
		h.writeMutationError(w, err)
		return
	}
	h.writeMemoryResponse(w, workspaceID)
}

// DeleteEntry handles DELETE /api/workspaces/{workspaceID}/memory/entries/{index}.
func (h *Handler) DeleteEntry(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.resolveWorkspace(w, r)
	if !ok {
		return
	}
	index, ok := h.parseIndex(w, r)
	if !ok {
		return
	}
	if err := h.memory.DeleteAt(workspaceID, index); err != nil {
		h.writeMutationError(w, err)
		return
	}
	h.writeMemoryResponse(w, workspaceID)
}

// resolveWorkspace validates the store is wired and the workspace exists,
// returning the workspace ID. It writes the appropriate error response and
// returns ok=false otherwise.
func (h *Handler) resolveWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h == nil || h.memory == nil {
		_ = orihttp.RespondAPIError(w, http.StatusServiceUnavailable,
			orihttp.NewAPIError("unavailable", "Workspace memory storage is not available."))
		return "", false
	}
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		_ = orihttp.RespondBadRequest(w, "workspace id is required")
		return "", false
	}
	if h.lookup != nil {
		if ws, err := h.lookup.Get(workspaceID); err != nil || ws == nil {
			_ = orihttp.RespondNotFound(w, "workspace not found")
			return "", false
		}
	}
	return workspaceID, true
}

func (h *Handler) parseIndex(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.PathValue("index"))
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 {
		_ = orihttp.RespondBadRequest(w, "entry index must be a non-negative integer")
		return 0, false
	}
	return index, true
}

func (h *Handler) writeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, workspace.ErrMemoryIndexOutOfRange) {
		_ = orihttp.RespondNotFound(w, err.Error())
		return
	}
	_ = orihttp.RespondInternalError(w, "Failed to update memory: "+err.Error())
}

func (h *Handler) writeMemoryResponse(w http.ResponseWriter, workspaceID string) {
	raw, err := h.memory.ReadRaw(workspaceID)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "Failed to read memory: "+err.Error())
		return
	}
	doc := workspace.ParseMemoryDocument(raw)
	entries := doc.Entries()
	dtos := make([]entryDTO, len(entries))
	for i, e := range entries {
		dtos[i] = entryDTO{
			Index:      i,
			Type:       string(e.Type),
			Date:       e.Date,
			Provenance: e.Provenance,
			Text:       e.Text,
		}
	}
	unstructured := doc.UnstructuredLines()
	if unstructured == nil {
		unstructured = []string{}
	}
	resp := memoryResponse{
		Entries:      dtos,
		Unstructured: unstructured,
		RawSize:      len(raw),
		CharBudget:   workspace.MemoryPromptTokenBudget * 4,
		TokenBudget:  workspace.MemoryPromptTokenBudget,
		OverBudget:   len(raw) > workspace.MemoryPromptTokenBudget*4,
	}
	if err := orihttp.RespondJSON(w, http.StatusOK, resp); err != nil {
		logger.Debug("Failed to write memory response", logger.Fields{"workspace_id": workspaceID, "error": err})
	}
}
