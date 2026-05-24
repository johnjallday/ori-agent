// Package actioncenterhttp serves the cross-workspace triage surface for
// workspace mission findings. Every mission produces opportunities; the
// Action Center aggregates them so the user has one place to dismiss,
// snooze, mark resolved, or open the source workspace.
//
// This package is deliberately small — most of the work is in:
//   - internal/workspace/opportunities.go (data + CRUD)
//   - internal/web/static/js/modules/action-center.js (UI)
//
// Routes are registered from internal/server/routes.go.
package actioncenterhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Handler serves Action Center endpoints.
type Handler struct {
	workspaces    workspace.Store
	opportunities workspace.OpportunityStore
}

// NewHandler constructs an Action Center handler. Both stores are required;
// passing nil for either is a programmer error and will panic on first use.
func NewHandler(ws workspace.Store, opps workspace.OpportunityStore) *Handler {
	return &Handler{workspaces: ws, opportunities: opps}
}

// AggregatedOpportunity is the row shape the Action Center list returns.
// Embeds the Opportunity record plus the source workspace's display name so
// the UI doesn't need a second round-trip per row.
type AggregatedOpportunity struct {
	workspace.Opportunity
	WorkspaceName string `json:"workspace_name"`
	WorkspaceKind string `json:"workspace_kind,omitempty"`
}

// listResponse wraps the slice so future top-level fields (counts, cursors)
// don't break clients that expect an object.
type listResponse struct {
	Items []AggregatedOpportunity `json:"items"`
	Total int                     `json:"total"`
}

// List handles GET /api/action-center/opportunities.
//
// Query parameters:
//   - status: comma-separated filter (default: new,snoozed — the "active" set).
//     Pass "all" to include resolved and dismissed.
//   - workspace: limit to one workspace ID.
//   - sort: "priority" (default) | "recency".
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.workspaces == nil || h.opportunities == nil {
		orihttp.ServiceUnavailable(w, "action center is not configured")
		return
	}

	statusFilter := parseStatusFilter(r.URL.Query().Get("status"))
	workspaceFilter := strings.TrimSpace(r.URL.Query().Get("workspace"))
	sortKey := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortKey == "" {
		sortKey = "priority"
	}
	now := time.Now()

	wsIDs, err := h.workspaces.List()
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("list workspaces: %v", err))
		return
	}

	items := make([]AggregatedOpportunity, 0, 32)
	for _, id := range wsIDs {
		if workspaceFilter != "" && id != workspaceFilter {
			continue
		}
		ws, err := h.workspaces.Get(id)
		if err != nil || ws == nil {
			continue
		}
		opps, err := h.opportunities.List(id)
		if err != nil {
			logger.Warn("action center: list opportunities", logger.Fields{"workspace_id": id, "error": err})
			continue
		}
		for _, o := range opps {
			// Promote an expired snooze back to `new` at read time. This is
			// the model's lighter-weight stand-in for a background job that
			// would flip the status when SnoozedUntil passes: a still-snoozed
			// item keeps Status=snoozed (and is hidden from the default view),
			// while an elapsed snooze re-surfaces as `new`. We mutate the loop
			// copy only — the stored record is untouched.
			eff := effectiveOpportunityStatus(o, now)
			if !statusMatches(eff, statusFilter) {
				continue
			}
			if eff != o.Status {
				o.Status = eff
				o.SnoozedUntil = nil
			}
			items = append(items, AggregatedOpportunity{
				Opportunity:   o,
				WorkspaceName: ws.Name,
				WorkspaceKind: ws.Kind,
			})
		}
	}

	sortItems(items, sortKey)

	resp := listResponse{Items: items, Total: len(items)}
	writeJSON(w, http.StatusOK, resp)
}

// Get handles GET /api/action-center/opportunities/{workspaceID}/{opportunityID}.
// Sets SeenAt on the opportunity as a side effect — opening counts as seeing
// (this is the documented "implicit read" UX).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		orihttp.ServiceUnavailable(w, "action center is not configured")
		return
	}
	wsID := r.PathValue("workspaceID")
	oppID := r.PathValue("opportunityID")
	if wsID == "" || oppID == "" {
		orihttp.BadRequest(w, "workspace ID and opportunity ID are required")
		return
	}

	// Mark seen first so even an immediate browser back+forward shows the
	// "read" state correctly. If MarkSeen fails (e.g. not found) we still
	// try Get below so the response reflects the actual error.
	_ = h.opportunities.MarkSeen(wsID, oppID)

	opp, err := h.opportunities.Get(wsID, oppID)
	if err != nil {
		if errors.Is(err, workspace.ErrOpportunityNotFound) {
			orihttp.NotFound(w, "opportunity not found")
			return
		}
		orihttp.InternalError(w, fmt.Sprintf("get opportunity: %v", err))
		return
	}
	ws, _ := h.workspaces.Get(wsID)
	resp := AggregatedOpportunity{Opportunity: opp}
	if ws != nil {
		resp.WorkspaceName = ws.Name
		resp.WorkspaceKind = ws.Kind
	}
	writeJSON(w, http.StatusOK, resp)
}

// dismissRequest is the body shape for the dismiss endpoint.
type dismissRequest struct {
	Reason workspace.DismissalReason `json:"reason,omitempty"`
}

// Dismiss handles POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/dismiss.
func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	wsID := r.PathValue("workspaceID")
	oppID := r.PathValue("opportunityID")
	if wsID == "" || oppID == "" {
		orihttp.BadRequest(w, "workspace ID and opportunity ID are required")
		return
	}
	var body dismissRequest
	// Body is optional — empty body is a dismiss with no reason.
	_ = json.NewDecoder(r.Body).Decode(&body)

	if body.Reason != "" && !validDismissalReason(body.Reason) {
		orihttp.BadRequest(w, fmt.Sprintf("invalid dismissal reason: %q", body.Reason))
		return
	}
	if err := h.opportunities.Dismiss(wsID, oppID, body.Reason); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "dismissed"})
}

// snoozeRequest is the body for the snooze endpoint. Accepts either a preset
// duration name (tomorrow / next_week / next_month) or a concrete ISO
// timestamp. Preset wins when both are supplied.
type snoozeRequest struct {
	Preset string `json:"preset,omitempty"`
	Until  string `json:"until,omitempty"` // RFC3339
}

// Snooze handles POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/snooze.
func (h *Handler) Snooze(w http.ResponseWriter, r *http.Request) {
	wsID := r.PathValue("workspaceID")
	oppID := r.PathValue("opportunityID")
	if wsID == "" || oppID == "" {
		orihttp.BadRequest(w, "workspace ID and opportunity ID are required")
		return
	}
	var body snoozeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		orihttp.BadRequest(w, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	until, err := resolveSnoozeUntil(body)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if err := h.opportunities.Snooze(wsID, oppID, until); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "snoozed",
		"snoozed_until": until.UTC(),
	})
}

// Resolve handles POST /api/action-center/opportunities/{workspaceID}/{opportunityID}/resolve.
func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	wsID := r.PathValue("workspaceID")
	oppID := r.PathValue("opportunityID")
	if wsID == "" || oppID == "" {
		orihttp.BadRequest(w, "workspace ID and opportunity ID are required")
		return
	}
	if err := h.opportunities.MarkResolved(wsID, oppID); err != nil {
		writeMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "resolved"})
}

// --- helpers ---

func parseStatusFilter(s string) map[workspace.OpportunityStatus]bool {
	s = strings.TrimSpace(s)
	if s == "" {
		// Default: the "active" inbox. Filtering runs against each item's
		// effective status (see effectiveOpportunityStatus), where a snooze
		// whose window has elapsed counts as `new` again — so matching `new`
		// here surfaces both genuinely-new items and expired snoozes while
		// still hiding those that are currently snoozed.
		return map[workspace.OpportunityStatus]bool{
			workspace.OpportunityNew: true,
		}
	}
	if strings.EqualFold(s, "all") {
		return nil // nil = match anything
	}
	out := make(map[workspace.OpportunityStatus]bool)
	for _, raw := range strings.Split(s, ",") {
		key := workspace.OpportunityStatus(strings.TrimSpace(raw))
		switch key {
		case workspace.OpportunityNew, workspace.OpportunitySnoozed,
			workspace.OpportunityResolved, workspace.OpportunityDismissed:
			out[key] = true
		}
	}
	return out
}

func statusMatches(s workspace.OpportunityStatus, filter map[workspace.OpportunityStatus]bool) bool {
	if filter == nil { // "all"
		return true
	}
	return filter[s]
}

// effectiveOpportunityStatus returns the status to filter/display by. A snoozed
// opportunity whose SnoozedUntil has elapsed (or was never set) counts as `new`
// again — it has re-surfaced for triage. Every other status is returned as-is.
func effectiveOpportunityStatus(o workspace.Opportunity, now time.Time) workspace.OpportunityStatus {
	if o.Status == workspace.OpportunitySnoozed && (o.SnoozedUntil == nil || !o.SnoozedUntil.After(now)) {
		return workspace.OpportunityNew
	}
	return o.Status
}

// sortItems sorts in-place by the requested key. priority sorts by
// (priority desc, recency desc); recency sorts by UpdatedAt desc.
func sortItems(items []AggregatedOpportunity, key string) {
	switch key {
	case "recency":
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
	default: // priority
		sort.SliceStable(items, func(i, j int) bool {
			pi := priorityRank(items[i].Priority)
			pj := priorityRank(items[j].Priority)
			if pi != pj {
				return pi > pj
			}
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
	}
}

// priorityRank duplicates the rank table from opportunities.go to avoid
// exporting that helper. Kept narrow so it stays in sync trivially.
func priorityRank(p string) int {
	switch p {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func validDismissalReason(r workspace.DismissalReason) bool {
	switch r {
	case workspace.DismissalNotUseful, workspace.DismissalDuplicate,
		workspace.DismissalOutOfScope, workspace.DismissalOther:
		return true
	}
	return false
}

// resolveSnoozeUntil turns a snooze request into a concrete time. Preset
// values map to common windows; "until" is an RFC3339 timestamp. At least
// one must be set.
func resolveSnoozeUntil(req snoozeRequest) (time.Time, error) {
	now := time.Now()
	preset := strings.TrimSpace(strings.ToLower(req.Preset))
	switch preset {
	case "tomorrow":
		// 9am tomorrow in the user's local-equivalent (server) zone.
		t := time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, now.Location())
		return t, nil
	case "next_week":
		return now.AddDate(0, 0, 7), nil
	case "next_month":
		return now.AddDate(0, 1, 0), nil
	case "":
		// fall through to custom timestamp
	default:
		return time.Time{}, fmt.Errorf("unknown snooze preset: %q", req.Preset)
	}
	if strings.TrimSpace(req.Until) == "" {
		return time.Time{}, errors.New("snooze requires either preset or until")
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Until))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid snooze timestamp: %v", err)
	}
	if !t.After(now) {
		return time.Time{}, errors.New("snooze target must be in the future")
	}
	return t, nil
}

func writeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, workspace.ErrOpportunityNotFound) {
		orihttp.NotFound(w, "opportunity not found")
		return
	}
	orihttp.InternalError(w, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("action center: encode response", logger.Fields{"error": err})
	}
}
