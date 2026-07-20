package personalhqhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// resolveEmailOpsWorkspaceID returns the user's Email Ops workspace ID, or "" if
// none exists (or the workspace source is not wired). Follow-ups are keyed to
// this workspace so the hub-and-spoke split works: management lives in Email Ops
// and HQ surfaces them read-only via the same resolution rule.
func (h *Handler) resolveEmailOpsWorkspaceID(userID string) string {
	if h == nil || h.watchtowerSources == nil {
		return ""
	}
	id, err := workspace.ResolveEmailOpsWorkspace(h.watchtowerSources().Workspaces, userID)
	if err != nil {
		return ""
	}
	return id
}

// FollowUpAPI is the follow-up surface this handler needs, implemented by
// *followup.Service. Kept as an interface for testability.
type FollowUpAPI interface {
	List(ctx context.Context, f followup.Filter) ([]*followup.FollowUp, error)
	HomeProjection(ctx context.Context, userID string) ([]*followup.FollowUp, error)
	Get(ctx context.Context, userID, id string) (*followup.FollowUp, error)
	Capture(ctx context.Context, in followup.CaptureInput) (*followup.FollowUp, error)
	ConfirmCandidate(ctx context.Context, userID, id string) (*followup.FollowUp, error)
	Edit(ctx context.Context, userID, id, title, detail string, dueAt *time.Time) (*followup.FollowUp, error)
	Snooze(ctx context.Context, userID, id string, until time.Time) (*followup.FollowUp, error)
	Complete(ctx context.Context, userID, id string) (*followup.FollowUp, error)
	Dismiss(ctx context.Context, userID, id string) (*followup.FollowUp, error)
	Reopen(ctx context.Context, userID, id string) (*followup.FollowUp, error)
}

// SetFollowUps wires the follow-up service.
func (h *Handler) SetFollowUps(api FollowUpAPI) { h.followups = api }

func (h *Handler) followUpsReady(w http.ResponseWriter, r *http.Request, method string) bool {
	if !orihttp.RequireMethod(w, r, method) {
		return false
	}
	if h == nil || h.followups == nil {
		orihttp.ServiceUnavailable(w, "personal hq follow-ups are unavailable")
		return false
	}
	return true
}

// ListFollowUps handles GET /api/personal-hq/followups?status=active,candidate.
func (h *Handler) ListFollowUps(w http.ResponseWriter, r *http.Request) {
	if !h.followUpsReady(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	filter := followup.Filter{UserID: userID}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		if strings.EqualFold(raw, "open") {
			filter.OpenOnly = true
		} else {
			for _, s := range strings.Split(raw, ",") {
				if s = strings.TrimSpace(s); s != "" {
					filter.Statuses = append(filter.Statuses, followup.Status(s))
				}
			}
		}
	}
	items, err := h.followups.List(r.Context(), filter)
	if err != nil {
		orihttp.InternalError(w, "Failed to list follow-ups: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"followups": items})
}

// HomeFollowUps handles GET /api/personal-hq/followups/home: the bounded Home
// projection (due/stale first, capped).
func (h *Handler) HomeFollowUps(w http.ResponseWriter, r *http.Request) {
	if !h.followUpsReady(w, r, http.MethodGet) {
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	items, err := h.followups.HomeProjection(r.Context(), userID)
	if err != nil {
		orihttp.InternalError(w, "Failed to load follow-ups: "+err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"followups": items})
}

// CreateFollowUp handles POST /api/personal-hq/followups: manual create.
func (h *Handler) CreateFollowUp(w http.ResponseWriter, r *http.Request) {
	if !h.followUpsReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		Category     string `json:"category"`
		Direction    string `json:"direction"`
		Title        string `json:"title"`
		Detail       string `json:"detail"`
		Counterparty string `json:"counterparty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		orihttp.BadRequest(w, "title is required")
		return
	}
	cat := followup.Category(strings.TrimSpace(req.Category))
	if !followup.ValidCategory(cat) {
		orihttp.BadRequest(w, "category must be one of i_owe, waiting_on, needs_decision, recurring_check_in")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	created, err := h.followups.Capture(r.Context(), followup.CaptureInput{
		UserID: userID, WorkspaceID: h.resolveEmailOpsWorkspaceID(userID),
		Category: cat, Direction: followup.Direction(strings.TrimSpace(req.Direction)),
		Title: req.Title, Detail: req.Detail, Counterparty: req.Counterparty,
		Source: followup.SourceRef{Type: "manual"}, Provenance: followup.ProvenanceManual,
	})
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	orihttp.Success(w, map[string]any{"followup": created})
}

// followUpAction dispatches a status transition identified by the URL suffix.
type followUpAction func(ctx context.Context, userID, id string) (*followup.FollowUp, error)

func (h *Handler) handleFollowUpAction(w http.ResponseWriter, r *http.Request, action followUpAction) {
	if !h.followUpsReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	f, err := action(r.Context(), userID, req.ID)
	if err != nil {
		respondFollowUpError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"followup": f})
}

func (h *Handler) ConfirmFollowUp(w http.ResponseWriter, r *http.Request) {
	h.handleFollowUpAction(w, r, h.followups.ConfirmCandidate)
}
func (h *Handler) CompleteFollowUp(w http.ResponseWriter, r *http.Request) {
	h.handleFollowUpAction(w, r, h.followups.Complete)
}
func (h *Handler) DismissFollowUp(w http.ResponseWriter, r *http.Request) {
	h.handleFollowUpAction(w, r, h.followups.Dismiss)
}
func (h *Handler) ReopenFollowUp(w http.ResponseWriter, r *http.Request) {
	h.handleFollowUpAction(w, r, h.followups.Reopen)
}

// SnoozeFollowUp handles POST /api/personal-hq/followups/snooze {id, until}.
func (h *Handler) SnoozeFollowUp(w http.ResponseWriter, r *http.Request) {
	if !h.followUpsReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID    string `json:"id"`
		Until string `json:"until"` // RFC3339
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Until))
	if err != nil {
		orihttp.BadRequest(w, "until must be an RFC3339 timestamp")
		return
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	f, err := h.followups.Snooze(r.Context(), userID, req.ID, until)
	if err != nil {
		respondFollowUpError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"followup": f})
}

// EditFollowUp handles POST /api/personal-hq/followups/edit.
func (h *Handler) EditFollowUp(w http.ResponseWriter, r *http.Request) {
	if !h.followUpsReady(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		DueAt  string `json:"due_at"` // RFC3339 or empty to clear
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		orihttp.BadRequest(w, "id is required")
		return
	}
	var due *time.Time
	if s := strings.TrimSpace(req.DueAt); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			orihttp.BadRequest(w, "due_at must be an RFC3339 timestamp")
			return
		}
		due = &t
	}
	userID, ok := h.resolveUser(w, r)
	if !ok {
		return
	}
	f, err := h.followups.Edit(r.Context(), userID, req.ID, req.Title, req.Detail, due)
	if err != nil {
		respondFollowUpError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"followup": f})
}

func respondFollowUpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, followup.ErrNotFound):
		orihttp.NotFound(w, "That follow-up was not found")
	default:
		orihttp.BadRequest(w, err.Error())
	}
}
