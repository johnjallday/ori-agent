package agenthttp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// maxBulkBatchSize bounds one bulk request so a single call cannot start an
// unbounded set of filesystem mutations (PRD FR48/FR53).
const maxBulkBatchSize = 100

// bulkOperation is the discriminator for POST /api/agents/bulk (PRD FR47).
type bulkOperation string

const (
	bulkOpDelete      bulkOperation = "delete"
	bulkOpAddTags     bulkOperation = "add_tags"
	bulkOpRemoveTags  bulkOperation = "remove_tags"
	bulkOpSetFavorite bulkOperation = "set_favorite"
	// bulkOpSetRole assigns the catalog role (or clears it to Unspecialized/
	// "general") across a batch of agents. Metadata-only — never touches
	// model/prompt/skills (PRD FR9, Group 5.4).
	bulkOpSetRole bulkOperation = "set_role"
)

// bulkAssignableRoles are the values bulkOpSetRole accepts: the 6 catalog
// roles plus "general" (Unspecialized) to clear a role. cli_agent is
// deliberately excluded — CLI agents are read-only and already rejected by
// metadataMutationTarget.
var bulkAssignableRoles = map[types.AgentRole]bool{
	types.RoleGeneral:      true,
	types.RoleOrchestrator: true,
	types.RoleResearcher:   true,
	types.RoleAnalyzer:     true,
	types.RoleSynthesizer:  true,
	types.RoleValidator:    true,
	types.RoleSpecialist:   true,
}

// bulkStatus is the per-agent outcome (PRD FR49).
type bulkStatus string

const (
	bulkStatusSucceeded bulkStatus = "succeeded"
	bulkStatusSkipped   bulkStatus = "skipped"
	bulkStatusFailed    bulkStatus = "failed"
)

// Stable reason codes are part of the UI contract (PRD FR51). Business-rule
// outcomes (skipped) are distinguishable from unexpected server errors (failed).
const (
	reasonProtectedAgent    = "protected_agent" // reserved system assistant
	reasonReadOnlyAgent     = "read_only_agent" // built-in CLI agent
	reasonAttachedAgent     = "attached_agent"  // attached to >=1 workspace
	reasonAgentNotFound     = "agent_not_found" // no such user agent
	reasonSharedEditNeedsOK = "shared_edit_requires_confirmation"
	reasonInternalError     = "internal_error" // unexpected server failure
)

// bulkRequest is the POST /api/agents/bulk body (PRD FR46).
type bulkRequest struct {
	AgentNames        []string         `json:"agent_names"`
	Operation         bulkOperation    `json:"operation"`
	Tags              []string         `json:"tags,omitempty"`
	Favorite          *bool            `json:"favorite,omitempty"`
	Role              *types.AgentRole `json:"role,omitempty"`
	ConfirmSharedEdit bool             `json:"confirm_shared_edit,omitempty"`
}

// bulkResult is one per-agent outcome in the response (PRD FR49).
type bulkResult struct {
	Name       string     `json:"name"`
	Status     bulkStatus `json:"status"`
	ReasonCode string     `json:"reason_code,omitempty"`
	Message    string     `json:"message,omitempty"`
}

// bulkSummary carries the aggregate counts (PRD FR50).
type bulkSummary struct {
	Requested int `json:"requested"`
	Succeeded int `json:"succeeded"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

// bulkResponse is the full response shape (PRD FR49/FR50).
type bulkResponse struct {
	Summary bulkSummary  `json:"summary"`
	Results []bulkResult `json:"results"`
}

// HandleBulk serves POST /api/agents/bulk: a bounded, partial-success batch of
// one operation (delete / add_tags / remove_tags / set_favorite) across a set of
// named agents. It reuses the same guards, persistence, session cleanup, and
// activity logging as the single-agent handlers so the bulk path cannot become a
// weaker mutation path (PRD FR52).
func (h *Handler) HandleBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req bulkRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Deterministic result ordering: one result per unique name, in first-seen
	// request order. Duplicates collapse to a single result (PRD FR48/FR49).
	names := dedupeAgentNames(req.AgentNames)
	if len(names) == 0 {
		orihttp.BadRequest(w, "agent_names must contain at least one agent")
		return
	}
	if len(names) > maxBulkBatchSize {
		orihttp.BadRequest(w, fmt.Sprintf("too many agents in one request (max %d)", maxBulkBatchSize))
		return
	}

	switch req.Operation {
	case bulkOpDelete, bulkOpAddTags, bulkOpRemoveTags, bulkOpSetFavorite, bulkOpSetRole:
	default:
		orihttp.BadRequest(w, fmt.Sprintf("unsupported operation %q", req.Operation))
		return
	}

	// set_favorite requires an explicit boolean so the intent is unambiguous.
	if req.Operation == bulkOpSetFavorite && req.Favorite == nil {
		orihttp.BadRequest(w, "set_favorite requires a boolean \"favorite\" field")
		return
	}

	// set_role requires one of the 6 catalog roles or "general" (Unspecialized).
	if req.Operation == bulkOpSetRole {
		if req.Role == nil {
			orihttp.BadRequest(w, "set_role requires a \"role\" field")
			return
		}
		if !bulkAssignableRoles[*req.Role] {
			orihttp.BadRequest(w, fmt.Sprintf("unsupported role %q", *req.Role))
			return
		}
	}

	// Normalize tags once for tag operations; an empty resulting set is rejected
	// so callers get a clear 400 rather than a batch of no-op successes.
	var tags []string
	if req.Operation == bulkOpAddTags || req.Operation == bulkOpRemoveTags {
		tags = normalizeTags(req.Tags)
		if len(tags) == 0 {
			orihttp.BadRequest(w, "tags must contain at least one non-empty tag")
			return
		}
	}

	results := make([]bulkResult, 0, len(names))
	for _, name := range names {
		switch req.Operation {
		case bulkOpDelete:
			results = append(results, h.bulkDeleteOne(r, name))
		case bulkOpAddTags, bulkOpRemoveTags:
			results = append(results, h.bulkTagOne(name, req.Operation, tags, req.ConfirmSharedEdit))
		case bulkOpSetFavorite:
			results = append(results, h.bulkFavoriteOne(name, *req.Favorite))
		case bulkOpSetRole:
			results = append(results, h.bulkRoleOne(name, *req.Role, req.ConfirmSharedEdit))
		}
	}

	orihttp.WriteJSON(w, bulkResponse{Summary: summarize(results), Results: results})
}

// bulkDeleteOne applies the deletion guards and lifecycle to one name, returning
// a per-agent result. Eligibility is re-checked here (not trusted from the
// client preview) immediately before deletion (PRD FR40).
func (h *Handler) bulkDeleteOne(r *http.Request, name string) bulkResult {
	if code, msg := h.checkAgentDeletable(name); code != "" {
		return bulkResult{Name: name, Status: bulkStatusSkipped, ReasonCode: code, Message: msg}
	}
	if err := h.performAgentDeletion(r.Context(), name); err != nil {
		logger.Error("bulk delete failed", logger.Fields{"agent": name, "err": err})
		return bulkResult{Name: name, Status: bulkStatusFailed, ReasonCode: reasonInternalError, Message: "Failed to delete agent."}
	}
	return bulkResult{Name: name, Status: bulkStatusSucceeded}
}

// bulkTagOne adds or removes tags on one agent's latest stored metadata,
// preserving unrelated tags (PRD FR24/FR25/FR32).
func (h *Handler) bulkTagOne(name string, op bulkOperation, tags []string, confirmed bool) bulkResult {
	ag, code, msg := h.metadataMutationTarget(name, true, confirmed)
	if code != "" {
		return bulkResult{Name: name, Status: bulkStatusSkipped, ReasonCode: code, Message: msg}
	}

	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	var changed bool
	if op == bulkOpAddTags {
		changed = addTags(ag.Metadata, tags)
	} else {
		changed = removeTags(ag.Metadata, tags)
	}

	if !changed {
		// No-op is a success: the requested end state already holds.
		return bulkResult{Name: name, Status: bulkStatusSucceeded}
	}
	if res, ok := h.persistMetadataMutation(name, ag, []string{"tags"}); !ok {
		return res
	}
	return bulkResult{Name: name, Status: bulkStatusSucceeded}
}

// bulkFavoriteOne sets an explicit favorite boolean on one agent. Favorite is
// not a shared-definition field, so it never requires shared-edit confirmation
// (mirrors the single-agent PATCH path).
func (h *Handler) bulkFavoriteOne(name string, favorite bool) bulkResult {
	ag, code, msg := h.metadataMutationTarget(name, false, false)
	if code != "" {
		return bulkResult{Name: name, Status: bulkStatusSkipped, ReasonCode: code, Message: msg}
	}
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	if ag.Metadata.Favorite == favorite {
		return bulkResult{Name: name, Status: bulkStatusSucceeded}
	}
	ag.Metadata.Favorite = favorite
	if res, ok := h.persistMetadataMutation(name, ag, []string{"favorite"}); !ok {
		return res
	}
	return bulkResult{Name: name, Status: bulkStatusSucceeded}
}

// bulkRoleOne assigns a catalog role (or "general" to clear it back to
// Unspecialized) on one agent. Metadata-only: role is a shared-definition
// field like model/prompt, so a definition attached to >1 workspace requires
// confirm_shared_edit, matching the single-agent PATCH rules (PRD FR9).
func (h *Handler) bulkRoleOne(name string, role types.AgentRole, confirmed bool) bulkResult {
	ag, code, msg := h.metadataMutationTarget(name, true, confirmed)
	if code != "" {
		return bulkResult{Name: name, Status: bulkStatusSkipped, ReasonCode: code, Message: msg}
	}
	if ag.Role == role {
		return bulkResult{Name: name, Status: bulkStatusSucceeded}
	}
	ag.Role = role
	if res, ok := h.persistMetadataMutation(name, ag, []string{"role"}); !ok {
		return res
	}
	return bulkResult{Name: name, Status: bulkStatusSucceeded}
}

// persistMetadataMutation saves an in-memory agent mutation and writes an update
// activity event. Returns (failedResult, false) on persistence error.
func (h *Handler) persistMetadataMutation(name string, ag *agent.Agent, fields []string) (bulkResult, bool) {
	if ag.Statistics != nil {
		ag.Statistics.UpdatedAt = time.Now()
	}
	if err := h.State.SetAgent(name, ag); err != nil {
		logger.Error("bulk metadata save failed", logger.Fields{"agent": name, "err": err})
		return bulkResult{Name: name, Status: bulkStatusFailed, ReasonCode: reasonInternalError, Message: "Failed to update agent."}, false
	}
	if h.ActivityLogger != nil {
		if err := h.ActivityLogger.LogActivity(name, types.ActivityEventUpdated, map[string]any{"fields": fields}, ""); err != nil {
			logger.Error("Failed to log activity", logger.Fields{"err": err})
		}
	}
	return bulkResult{}, true
}

// checkAgentDeletable returns ("", "") when the named agent may be deleted, or a
// stable reason code + message identifying why it must be skipped. Shared by the
// single-agent DELETE handler's guard intent and the bulk delete path so the two
// cannot drift (PRD FR36–FR38).
func (h *Handler) checkAgentDeletable(name string) (reasonCode, message string) {
	if isSystemAssistantAgent(name) {
		return reasonProtectedAgent, "The system assistant cannot be deleted."
	}
	if h.isCLIAgent(name) {
		return reasonReadOnlyAgent, "CLI agents are built-in and cannot be deleted."
	}
	if _, ok := h.State.GetAgent(name); !ok {
		return reasonAgentNotFound, "Agent not found."
	}
	if m := workspace.WorkspaceMembershipFor(h.workspaceStore, name); m.Count > 0 {
		return reasonAttachedAgent, fmt.Sprintf("Attached to %d workspace(s); detach it before deleting.", m.Count)
	}
	return "", ""
}

// metadataMutationTarget resolves the agent to mutate for a bulk metadata
// operation, or returns a skip reason. CLI agents are read-only; missing agents
// are reported; tag mutations on a definition shared by >1 workspace require
// explicit confirmation, matching the single-agent PATCH rules (PRD FR30/FR31).
func (h *Handler) metadataMutationTarget(name string, touchesSharedDefinition, confirmed bool) (*agent.Agent, string, string) {
	if h.isCLIAgent(name) {
		return nil, reasonReadOnlyAgent, "CLI agents cannot be modified."
	}
	ag, ok := h.State.GetAgent(name)
	if !ok || ag == nil {
		return nil, reasonAgentNotFound, "Agent not found."
	}
	if touchesSharedDefinition && !isSystemAssistantAgent(name) {
		if m := workspace.WorkspaceMembershipFor(h.workspaceStore, name); m.Count > 1 && !confirmed {
			return nil, reasonSharedEditNeedsOK, fmt.Sprintf("Attached to %d workspaces — confirm the shared edit to proceed.", m.Count)
		}
	}
	return ag, "", ""
}

// dedupeAgentNames trims each name and returns the unique, non-empty names in
// first-seen order. Comparison is case-insensitive on the trimmed name so the
// same agent requested twice (or with stray whitespace) collapses to one result.
func dedupeAgentNames(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

// normalizeTags trims each tag and drops empties/case-insensitive duplicates,
// preserving first-seen order and original casing (matches the profile editor).
func normalizeTags(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// addTags merges tags into the metadata, preserving existing (unrelated) tags
// and skipping case-insensitive duplicates. Returns whether anything changed.
func addTags(md *types.AgentMetadata, tags []string) bool {
	existing := make(map[string]struct{}, len(md.Tags))
	for _, t := range md.Tags {
		existing[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	changed := false
	for _, t := range tags {
		key := strings.ToLower(strings.TrimSpace(t))
		if key == "" {
			continue
		}
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		md.Tags = append(md.Tags, t)
		changed = true
	}
	return changed
}

// removeTags deletes the named tags (case-insensitive) from the metadata while
// preserving all other tags. Returns whether anything changed.
func removeTags(md *types.AgentMetadata, tags []string) bool {
	remove := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		remove[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	kept := md.Tags[:0]
	changed := false
	for _, t := range md.Tags {
		if _, drop := remove[strings.ToLower(strings.TrimSpace(t))]; drop {
			changed = true
			continue
		}
		kept = append(kept, t)
	}
	md.Tags = kept
	return changed
}

// summarize tallies per-agent results into the response summary counts.
func summarize(results []bulkResult) bulkSummary {
	s := bulkSummary{Requested: len(results)}
	for _, r := range results {
		switch r.Status {
		case bulkStatusSucceeded:
			s.Succeeded++
		case bulkStatusSkipped:
			s.Skipped++
		case bulkStatusFailed:
			s.Failed++
		}
	}
	return s
}
