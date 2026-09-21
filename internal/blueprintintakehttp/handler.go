package blueprintintakehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/blueprintintake"
	"github.com/johnjallday/ori-agent/internal/fileparser"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxUploadRequestBytes = int64(blueprintintake.MaxFilesPerIntake+1)*fileparser.MaxFileSize + 10*1024*1024

type WorkspaceLookup interface {
	Get(id string) (*workspace.Workspace, error)
}

type Handler struct {
	sources  *blueprintintake.SourceService
	lookup   WorkspaceLookup
	provider userprofile.UserProvider
}

func NewHandler(sources *blueprintintake.SourceService, lookup WorkspaceLookup, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{sources: sources, lookup: lookup, provider: provider}
}

// UploadFiles accepts one multipart request containing one or more "files"
// parts. Every part gets an independent result; an unreadable or unsupported
// file never prevents later parts from being processed.
func (h *Handler) UploadFiles(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.sources == nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "Blueprint intake is unavailable.")
		return
	}
	workspaceID, _, ok := h.resolveWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	intakeKey := strings.TrimSpace(r.PathValue("intakeKey"))
	requirement, err := h.sources.Requirement(workspaceID, intakeKey)
	if err != nil {
		switch {
		case errors.Is(err, blueprintintake.ErrIntakeNotFound):
			_ = orihttp.RespondError(w, http.StatusNotFound, "Intake not found.")
		default:
			_ = orihttp.RespondError(w, http.StatusNotFound, "Workspace not found.")
		}
		return
	}
	if !requirement.Sources.Files {
		_ = orihttp.RespondError(w, http.StatusBadRequest, "This intake does not accept files.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusBadRequest, "Expected a multipart file upload.")
		return
	}

	results := make([]blueprintintake.FileResult, 0)
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = orihttp.RespondError(w, http.StatusBadRequest, "The file upload could not be read.")
			return
		}
		if part.FileName() == "" || (part.FormName() != "files" && part.FormName() != "file") {
			_ = part.Close()
			continue
		}
		result, addErr := h.sources.AddFile(r.Context(), workspaceID, requirement.Key, part.FileName(), part)
		_ = part.Close()
		if addErr != nil && !errors.Is(addErr, blueprintintake.ErrFileLimit) {
			name := strings.TrimSpace(part.FileName())
			if name == "" {
				name = "unnamed file"
			}
			result = blueprintintake.FileResult{Name: name, Status: blueprintintake.SourceStatusUnreadable, Message: "Ori could not add this file."}
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		_ = orihttp.RespondError(w, http.StatusBadRequest, "Add at least one file.")
		return
	}

	_ = orihttp.RespondSuccess(w, map[string]any{
		"intake_key": requirement.Key,
		"files":      results,
		"count":      len(results),
		"limit":      blueprintintake.MaxFilesPerIntake,
	})
}

func (h *Handler) GetIntake(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.resolveWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	intakeKey := strings.TrimSpace(r.PathValue("intakeKey"))
	requirement, err := h.sources.Requirement(workspaceID, intakeKey)
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusNotFound, "Intake not found.")
		return
	}
	sources, err := h.sources.ListSources(workspaceID, intakeKey)
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusInternalServerError, "Intake sources could not be read.")
		return
	}
	consent, err := h.sources.ConsentStatus(r.Context(), workspaceID, intakeKey)
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "This workspace's model provider is unavailable.")
		return
	}
	files := make([]blueprintintake.FileResult, 0, len(sources))
	for _, source := range sources {
		files = append(files, blueprintintake.FileResult{ID: source.ID, Name: source.Name, Status: source.Status, Size: source.Size, ContentHash: source.ContentHash})
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"requirement": requirement, "files": files, "consent": consent})
}

func (h *Handler) AcceptConsent(w http.ResponseWriter, r *http.Request) {
	workspaceID, actor, ok := h.resolveWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	status, err := h.sources.AcceptConsent(r.Context(), workspaceID, strings.TrimSpace(r.PathValue("intakeKey")), actor)
	if err != nil {
		if errors.Is(err, blueprintintake.ErrIntakeNotFound) {
			_ = orihttp.RespondError(w, http.StatusNotFound, "Intake not found.")
			return
		}
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "Consent could not be recorded.")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"consent": status})
}

func (h *Handler) resolveWorkspace(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if h == nil || h.sources == nil || h.lookup == nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "Blueprint intake is unavailable.")
		return "", "", false
	}
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	ws, err := h.lookup.Get(workspaceID)
	if err != nil || ws == nil {
		_ = orihttp.RespondError(w, http.StatusNotFound, "Workspace not found.")
		return "", "", false
	}
	actor, err := h.provider.CurrentUserID(ctx)
	if err != nil || strings.TrimSpace(actor) == "" {
		actor = userprofile.LocalUserID
	}
	owner := strings.TrimSpace(ws.OwnerUserID)
	if owner == "" {
		owner = userprofile.LocalUserID
	}
	if !strings.EqualFold(owner, actor) {
		_ = orihttp.RespondError(w, http.StatusNotFound, "Workspace not found.")
		return "", "", false
	}
	return workspaceID, actor, true
}

func uploadRoute(workspaceID, intakeKey string) string {
	return fmt.Sprintf("/api/workspaces/%s/blueprint-intakes/%s/sources/files", workspaceID, intakeKey)
}
