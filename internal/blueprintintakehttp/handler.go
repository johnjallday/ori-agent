package blueprintintakehttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
	sources      *blueprintintake.SourceService
	lookup       WorkspaceLookup
	provider     userprofile.UserProvider
	profileStore userprofile.UserStore
	workflow     *blueprintintake.Service
}

func NewHandler(sources *blueprintintake.SourceService, lookup WorkspaceLookup, provider userprofile.UserProvider) *Handler {
	if provider == nil {
		provider = userprofile.LocalUserProvider{}
	}
	return &Handler{sources: sources, lookup: lookup, provider: provider}
}

func (h *Handler) SetWorkflow(workflow *blueprintintake.Service) {
	if h != nil {
		h.workflow = workflow
	}
}

func (h *Handler) SetUserStore(store userprofile.UserStore) {
	if h != nil {
		h.profileStore = store
	}
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
	payload := map[string]any{"requirement": requirement, "files": files, "consent": consent}
	if h.workflow != nil {
		if skill, skillErr := h.workflow.SkillReadiness(workspaceID, intakeKey); skillErr == nil {
			payload["skill"] = skill
		}
		if bundled, exists, bundledErr := h.workflow.BundledSkillReview(workspaceID, intakeKey); bundledErr == nil && exists {
			payload["bundled_skill"] = bundled
		}
		if run, runErr := h.workflow.Status(workspaceID, intakeKey); runErr == nil {
			payload["run"] = run
		}
		if proposal, proposalErr := h.workflow.Proposal(workspaceID, intakeKey); proposalErr == nil {
			payload["proposal"] = proposal
		}
	}
	_ = orihttp.RespondSuccess(w, payload)
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

func (h *Handler) TrustBundledSkill(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.resolveWorkflowWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	var request struct {
		Choice string `json:"choice"`
	}
	if r.Body != nil {
		decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
		if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			_ = orihttp.RespondError(w, http.StatusBadRequest, "The skill choice could not be read.")
			return
		}
	}
	review, err := h.workflow.TrustBundledSkill(workspaceID, strings.TrimSpace(r.PathValue("intakeKey")), request.Choice)
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusConflict, err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"bundled_skill": review})
}

func (h *Handler) StartRun(w http.ResponseWriter, r *http.Request) {
	workspaceID, actor, ok := h.resolveWorkflowWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	intakeKey := strings.TrimSpace(r.PathValue("intakeKey"))
	readiness, err := h.workflow.SkillReadiness(workspaceID, intakeKey)
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusBadRequest, "The intake skill could not be checked.")
		return
	}
	if !readiness.Ready {
		w.WriteHeader(http.StatusConflict)
		orihttp.WriteJSON(w, map[string]any{"success": false, "message": "The intake skill is not ready.", "skill": readiness})
		return
	}
	location := time.Local
	if h.profileStore != nil {
		if profile, profileErr := h.profileStore.Get(r.Context(), actor); profileErr == nil && profile != nil && strings.TrimSpace(profile.Timezone) != "" {
			if configured, zoneErr := time.LoadLocation(strings.TrimSpace(profile.Timezone)); zoneErr == nil {
				location = configured
			}
		}
	}
	run, err := h.workflow.Start(workspaceID, intakeKey, location)
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusConflict, err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"run": run})
}

func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.resolveWorkflowWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	run, err := h.workflow.Status(workspaceID, strings.TrimSpace(r.PathValue("intakeKey")))
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusNotFound, "No intake run has started.")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"run": run})
}

func (h *Handler) CancelRun(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.resolveWorkflowWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	run, err := h.workflow.Cancel(workspaceID, strings.TrimSpace(r.PathValue("intakeKey")))
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusConflict, "No intake run is active.")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"run": run})
}

func (h *Handler) GetProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.resolveWorkflowWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	proposal, err := h.workflow.Proposal(workspaceID, strings.TrimSpace(r.PathValue("intakeKey")))
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusNotFound, "No intake proposal is ready.")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"proposal": proposal})
}

func (h *Handler) ApplyProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID, actor, ok := h.resolveWorkflowWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	var request blueprintintake.ApplyRequest
	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}
	request.Actor = actor
	proposal, err := h.workflow.ApplyContext(r.Context(), workspaceID, strings.TrimSpace(r.PathValue("intakeKey")), request)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, blueprintintake.ErrProposalStale) {
			status = http.StatusConflict
		}
		_ = orihttp.RespondError(w, status, err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"proposal": proposal})
}

func (h *Handler) SkipProposal(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.resolveWorkflowWorkspace(r.Context(), w, r)
	if !ok {
		return
	}
	var request struct {
		ProposalHash string `json:"proposal_hash"`
	}
	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}
	proposal, err := h.workflow.Skip(workspaceID, strings.TrimSpace(r.PathValue("intakeKey")), request.ProposalHash)
	if err != nil {
		_ = orihttp.RespondError(w, http.StatusConflict, err.Error())
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"proposal": proposal})
}

func (h *Handler) resolveWorkflowWorkspace(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if h == nil || h.workflow == nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "Blueprint intake runs are unavailable.")
		return "", "", false
	}
	return h.resolveWorkspace(ctx, w, r)
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
