package sessionhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

const creationOperationIDKey = "workspace_creation_operation_id"
const creationOperationDigestKey = "workspace_creation_operation_digest"

// This is a delivery journal around createWorkspace, not a second creator or
// a resumable setup engine. An accepted operation never re-enters creation.
type workspaceCreationOperation struct {
	ID             string `json:"id"`
	State          string `json:"state"`
	Digest         string `json:"review_digest"`
	SourceDigest   string `json:"-"`
	WorkspaceID    string `json:"-"` // reserved identity is not creation proof
	Owner          string `json:"-"`
	Request        string `json:"-"`
	ResponseStatus int    `json:"-"`
	Response       string `json:"-"`
	claimed        bool
}

type creationOperationContextKey struct{}

func creationOperationFrom(ctx context.Context) *workspaceCreationOperation {
	op, _ := ctx.Value(creationOperationContextKey{}).(*workspaceCreationOperation)
	return op
}

func (h *Handler) creationOperationOwner(ctx context.Context) (string, error) {
	if h.currentUserID == nil {
		return userprofile.LocalUserID, nil
	}
	owner, err := h.currentUserID(ctx)
	if err != nil || strings.TrimSpace(owner) == "" {
		return "", errors.New("creation owner unavailable")
	}
	return strings.TrimSpace(owner), nil
}

func validCreationOperationID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func creationOperationError(w http.ResponseWriter, status int, code, message string) {
	_ = orihttp.RespondAPIError(w, status, &orihttp.APIError{Code: code, Message: message})
}

func (h *Handler) handleWorkspaceCreationOperations(w http.ResponseWriter, r *http.Request, rest string) {
	w.Header().Set("Cache-Control", "no-store")
	if h.store == nil || h.store.DB() == nil {
		creationOperationError(w, http.StatusServiceUnavailable, "creation_unavailable", "Reviewed creation storage is unavailable.")
		return
	}
	owner, err := h.creationOperationOwner(r.Context())
	if err != nil {
		creationOperationError(w, http.StatusServiceUnavailable, "owner_unavailable", "The creation owner could not be verified.")
		return
	}
	if r.URL.RawQuery != "" {
		_ = orihttp.RespondBadRequest(w, "creation operations do not accept query parameters")
		return
	}
	if rest == "" {
		if !orihttp.RequireMethod(w, r, http.MethodPost) {
			return
		}
		h.reviewWorkspaceCreationOperation(w, r, owner)
		return
	}
	parts := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	if !validCreationOperationID(parts[0]) || len(parts) > 2 || (len(parts) == 2 && parts[1] != "confirm" && parts[1] != "abandon") {
		_ = orihttp.RespondNotFound(w, "creation operation not found")
		return
	}
	confirm := len(parts) == 2 && parts[1] == "confirm"
	abandon := len(parts) == 2 && parts[1] == "abandon"
	method := http.MethodGet
	if confirm || abandon {
		method = http.MethodPost
	}
	if !orihttp.RequireMethod(w, r, method) {
		return
	}
	op, err := h.readWorkspaceCreationOperation(r.Context(), owner, parts[0])
	if err != nil {
		h.respondCreationOperationReadError(w, err)
		return
	}
	if abandon {
		var input struct{}
		if !decodeBuildBody(w, r, &input) {
			return
		}
		if op.State != "reviewed" && op.State != "abandoned" {
			creationOperationError(w, http.StatusConflict, "creation_not_cancellable", "This operation is no longer an unaccepted review. Closing cannot undo it.")
			return
		}
		if _, err := h.store.DB().ExecContext(r.Context(), `UPDATE workspace_creation_operations SET state='abandoned',updated_at=CURRENT_TIMESTAMP
			WHERE id=? AND owner_user_id=? AND state='reviewed'`, op.ID, owner); err != nil {
			creationOperationError(w, http.StatusServiceUnavailable, "creation_uncertain", "The review could not be abandoned. Read its saved status.")
			return
		}
		op, err = h.readWorkspaceCreationOperation(r.Context(), owner, op.ID)
		if err != nil {
			h.respondCreationOperationReadError(w, err)
			return
		}
		if op.State != "abandoned" {
			creationOperationError(w, http.StatusConflict, "creation_not_cancellable", "The operation was accepted concurrently. Closing is not undo; read its status.")
			return
		}
	}
	if !confirm {
		h.respondWorkspaceCreationOperation(w, r, op, false)
		return
	}
	var input struct {
		Digest string `json:"review_digest"`
	}
	if !decodeBuildBody(w, r, &input) {
		return
	}
	if input.Digest != op.Digest {
		creationOperationError(w, http.StatusConflict, "creation_review_changed", "Confirm only the exact reviewed request. Nothing new was accepted.")
		return
	}
	if op.State != "reviewed" {
		h.respondWorkspaceCreationOperation(w, r, op, true)
		return
	}

	// The normal pipeline validates again. It claims the journal immediately
	// before its first domain effect, rather than on entry to this wrapper.
	request := r.Clone(context.WithValue(r.Context(), creationOperationContextKey{}, op))
	request.Body = io.NopCloser(bytes.NewBufferString(op.Request))
	recorder := httptest.NewRecorder()
	h.createWorkspace(recorder, request)
	if err := h.finishWorkspaceCreationOperation(r.Context(), op, recorder); err != nil {
		creationOperationError(w, http.StatusServiceUnavailable, "creation_uncertain", "The original creation receipt could not be saved. Read its status; do not create again.")
		return
	}
	current, err := h.readWorkspaceCreationOperation(r.Context(), owner, op.ID)
	if err != nil {
		creationOperationError(w, http.StatusServiceUnavailable, "creation_uncertain", "The original creation outcome is unreadable. Read its saved status; do not create again.")
		return
	}
	h.respondWorkspaceCreationOperation(w, r, current, true)
}

func (h *Handler) reviewWorkspaceCreationOperation(w http.ResponseWriter, r *http.Request, owner string) {
	var input struct {
		ID      string          `json:"id"`
		Request json.RawMessage `json:"request"`
	}
	if !decodeBuildBody(w, r, &input) {
		return
	}
	var material map[string]json.RawMessage
	var request createWorkspaceRequest
	if !validCreationOperationID(input.ID) || json.Unmarshal(input.Request, &material) != nil || material == nil ||
		json.Unmarshal(input.Request, &request) != nil || strings.TrimSpace(request.Name) == "" {
		_ = orihttp.RespondBadRequest(w, "a canonical operation UUID and named material create request are required")
		return
	}
	if request.GroupRequirementReview || request.GroupReviewToken != "" || request.IdempotencyKey != "" ||
		request.CreateRequiredHome || request.GroupComposition != "" {
		creationOperationError(w, http.StatusConflict, "creation_owner_mismatch", "This setup must use its existing group review and commit owner.")
		return
	}
	// Stable JSON keys/whitespace, without marshaling the request struct (which
	// would erase presence-sensitive empty role_staffing and existing-agent arrays).
	body, err := json.Marshal(material)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "invalid material request")
		return
	}
	if existing, readErr := h.readWorkspaceCreationOperation(r.Context(), owner, input.ID); readErr == nil {
		if existing.Request != string(body) {
			creationOperationError(w, http.StatusConflict, "creation_review_changed", "This operation already belongs to another reviewed request.")
			return
		}
		h.respondWorkspaceCreationOperation(w, r, existing, false)
		return
	} else if !errors.Is(readErr, sql.ErrNoRows) {
		h.respondCreationOperationReadError(w, readErr)
		return
	}
	var template projecttemplates.Template
	resolved := request.TemplateID != "" || request.TemplatePath != ""
	if resolved {
		template, err = h.resolveProjectTemplate(request.TemplateID, request.TemplatePath)
		if err != nil {
			creationOperationError(w, http.StatusConflict, "creation_blueprint_unavailable", "The reviewed blueprint is unavailable. Nothing was created.")
			return
		}
		if template.GroupRequirement != nil {
			creationOperationError(w, http.StatusConflict, "creation_owner_mismatch", "This blueprint requires its existing group review and commit owner.")
			return
		}
	}
	sourceDigest, err := workspaceCreationSourceDigest(request, template, resolved)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, "the blueprint review could not be serialized")
		return
	}
	digest := sha256.Sum256(append(append(body, '\n'), []byte(sourceDigest)...))
	digestText := hex.EncodeToString(digest[:])
	_, err = h.store.DB().ExecContext(r.Context(), `INSERT INTO workspace_creation_operations
		(id,owner_user_id,request_digest,source_digest,request_json,workspace_id,state)
		SELECT ?,?,?,?,?,?,'reviewed' WHERE
		(SELECT COUNT(*) FROM workspace_creation_operations WHERE owner_user_id=? AND state='reviewed') < 64
		ON CONFLICT(id) DO NOTHING`, input.ID, owner, digestText, sourceDigest, string(body), uuid.NewString(), owner)
	if err != nil {
		creationOperationError(w, http.StatusServiceUnavailable, "creation_unavailable", "The review could not be saved. Nothing was created.")
		return
	}
	op, err := h.readWorkspaceCreationOperation(r.Context(), owner, input.ID)
	if err != nil {
		// No adoption by name or fallback to an unjournaled create on an
		// unreadable/foreign identity or exhausted review budget.
		h.respondCreationOperationReadError(w, err)
		return
	}
	if op.Digest != digestText {
		creationOperationError(w, http.StatusConflict, "creation_review_changed", "This operation already belongs to another reviewed request.")
		return
	}
	h.respondWorkspaceCreationOperation(w, r, op, false)
}

func (h *Handler) readWorkspaceCreationOperation(ctx context.Context, owner, id string) (*workspaceCreationOperation, error) {
	op := &workspaceCreationOperation{ID: id, Owner: owner}
	err := h.store.DB().QueryRowContext(ctx, `SELECT state,request_digest,source_digest,request_json,workspace_id,response_status,response_json
		FROM workspace_creation_operations WHERE id=? AND owner_user_id=?`, id, owner).
		Scan(&op.State, &op.Digest, &op.SourceDigest, &op.Request, &op.WorkspaceID, &op.ResponseStatus, &op.Response)
	return op, err
}

func (h *Handler) respondCreationOperationReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		_ = orihttp.RespondNotFound(w, "creation operation not found or review limit reached")
		return
	}
	creationOperationError(w, http.StatusServiceUnavailable, "creation_unavailable", "The original creation operation could not be read.")
}

func workspaceCreationSourceDigest(request createWorkspaceRequest, template projecttemplates.Template, resolved bool) (string, error) {
	if !resolved && request.Blank {
		template = blankWorkspaceTemplate()
	} else if !resolved && request.Kind == "group" {
		template = groupRosterTemplate(request.Name)
	}
	body, err := json.Marshal(template)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// Called only after normal create validation and before seeding any profiles.
func (h *Handler) acceptWorkspaceCreationOperation(w http.ResponseWriter, r *http.Request, ws *session.Workspace, tmpl projecttemplates.Template, resolved bool) bool {
	op := creationOperationFrom(r.Context())
	if op == nil {
		return true
	}
	if (resolved && tmpl.GroupRequirement != nil) || buildPreparationContextFrom(r.Context()) != nil {
		creationOperationError(w, http.StatusConflict, "creation_owner_mismatch", "This setup must use its existing reviewed action owner.")
		return false
	}
	result, err := h.store.DB().ExecContext(r.Context(), `UPDATE workspace_creation_operations SET state='accepted',updated_at=CURRENT_TIMESTAMP
		WHERE id=? AND owner_user_id=? AND request_digest=? AND state='reviewed'`, op.ID, op.Owner, op.Digest)
	if err != nil {
		creationOperationError(w, http.StatusServiceUnavailable, "creation_unavailable", "Creation could not be accepted. Read the original operation before retrying.")
		return false
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		creationOperationError(w, http.StatusConflict, "creation_already_accepted", "Read the original operation; this request cannot create again.")
		return false
	}
	op.claimed = true
	ws.ID, ws.OwnerUserID = op.WorkspaceID, op.Owner
	if ws.SharedData == nil {
		ws.SharedData = make(map[string]any)
	}
	ws.SharedData[creationOperationIDKey] = op.ID
	ws.SharedData[creationOperationDigestKey] = op.Digest
	return true
}

func (h *Handler) provesWorkspaceCreationOperation(ctx context.Context, op *workspaceCreationOperation, body []byte) bool {
	var response struct {
		Success bool               `json:"success"`
		Folder  *session.Workspace `json:"folder"`
	}
	if json.Unmarshal(body, &response) != nil || !response.Success || response.Folder == nil || response.Folder.ID != op.WorkspaceID {
		return false
	}
	ws := h.observeWorkspaceCreationResource(ctx, op)
	return ws != nil && response.Folder.Name != "" && response.Folder.Name == ws.Name &&
		response.Folder.ParentID == ws.ParentID && response.Folder.OwnerUserID == op.Owner &&
		response.Folder.SharedData[creationOperationIDKey] == op.ID && response.Folder.SharedData[creationOperationDigestKey] == op.Digest
}

func (h *Handler) observeWorkspaceCreationResource(ctx context.Context, op *workspaceCreationOperation) *session.Workspace {
	ws, err := h.store.GetWorkspace(ctx, op.WorkspaceID)
	if err != nil || ws == nil || ws.OwnerUserID != op.Owner ||
		ws.SharedData[creationOperationIDKey] != op.ID || ws.SharedData[creationOperationDigestKey] != op.Digest {
		return nil
	}
	return ws
}

func (h *Handler) finishWorkspaceCreationOperation(ctx context.Context, op *workspaceCreationOperation, recorder *httptest.ResponseRecorder) error {
	// A disconnected client must not discard the durable outcome. This bounded
	// write records evidence only, never continues domain work.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	state, prior := "refused", "reviewed"
	if op.claimed {
		state, prior = "reconciliation_required", "accepted"
		if recorder.Code == http.StatusCreated && h.provesWorkspaceCreationOperation(ctx, op, recorder.Body.Bytes()) {
			state = "complete"
		}
	} else if recorder.Code < 400 {
		return nil
	}
	// CAS prevents a losing validator or duplicate confirmation from changing
	// the receipt of the operation that actually won acceptance.
	if !op.claimed {
		var currentState string
		if err := h.store.DB().QueryRowContext(ctx, `SELECT state FROM workspace_creation_operations WHERE id=? AND owner_user_id=?`, op.ID, op.Owner).Scan(&currentState); err != nil {
			return err
		}
		if currentState != "reviewed" {
			return nil
		}
	}
	_, err := h.store.DB().ExecContext(ctx, `UPDATE workspace_creation_operations
		SET state=?,response_status=?,response_json=?,updated_at=CURRENT_TIMESTAMP
		WHERE id=? AND owner_user_id=? AND state=?`, state, recorder.Code, recorder.Body.String(), op.ID, op.Owner, prior)
	// A failed receipt write leaves accepted/reviewed state, never permission
	// to repeat accepted effects. Observation truthfully retains uncertainty.
	return err
}

func (h *Handler) respondWorkspaceCreationOperation(w http.ResponseWriter, r *http.Request, op *workspaceCreationOperation, confirm bool) {
	view := *op
	var result map[string]any
	if op.State == "complete" && !h.provesWorkspaceCreationOperation(r.Context(), op, []byte(op.Response)) {
		view.State = "reconciliation_required"
	}
	if view.State == "complete" || view.State == "refused" {
		if json.Unmarshal([]byte(op.Response), &result) != nil || result == nil {
			view.State = "reconciliation_required"
		}
	}
	if !confirm {
		body := map[string]any{"operation": view}
		switch view.State {
		case "complete":
			body["result"] = result
		case "refused":
			// Read-only recovery retains the original readiness/team diagnostic;
			// observation must not POST again merely to recover lost error copy.
			body["refusal"] = result
		case "accepted", "reconciliation_required":
			// Canonical existence is useful partial evidence, but it never
			// upgrades an interrupted operation to complete or resumes work.
			if ws := h.observeWorkspaceCreationResource(r.Context(), op); ws != nil {
				body["observed_workspace"] = ws
			}
		}
		_ = orihttp.RespondJSON(w, http.StatusOK, body)
		return
	}
	status := http.StatusAccepted
	if view.State == "complete" {
		status = http.StatusCreated
	} else if view.State == "refused" && result != nil {
		status = op.ResponseStatus
	} else if view.State == "reconciliation_required" || view.State == "abandoned" {
		status = http.StatusConflict
	}
	if result == nil || (view.State != "complete" && view.State != "refused") {
		result = map[string]any{"error": "The original creation is accepted or uncertain. Read its saved status; do not create again."}
	}
	if view.State == "abandoned" {
		result = map[string]any{"error": "This review was abandoned. Nothing can be created from it; start a new review."}
	}
	result["creation_operation"] = view
	_ = orihttp.RespondJSON(w, status, result)
}
