package workspace

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
)

// The Goal **Prepare** step: propose a brief, accept it, and see explained
// Toolbox recommendations (PRD FR-92–FR-106).
//
// Three endpoints, split along the line that keeps this safe:
//
//   GET  .../goal/brief        propose (read-only, changes nothing)
//   PUT  .../goal/brief        accept an edited brief (the user's decision)
//   GET  .../goal/recommendations   rank against the ACCEPTED brief
//   PUT  .../goal/toolbox-policy    pin a version, or opt into current-at-start
//
// A proposed brief is never persisted by the propose call. If it were, a
// model's guess about what a goal needs would silently become the basis for
// which capabilities get recommended, and nobody would have agreed to it
// (FR-94).

type goalBriefPayload struct {
	Summary              string   `json:"summary,omitempty"`
	ExpectedOutput       string   `json:"expected_output,omitempty"`
	SourceTypes          []string `json:"source_types,omitempty"`
	Operations           []string `json:"operations,omitempty"`
	MaxAutonomy          string   `json:"max_autonomy,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}

// GetGoalBrief handles GET /api/workspaces/{workspaceID}/goal/brief
//
// Returns the accepted brief when one exists, plus a fresh proposal the user
// can compare against. Read-only: proposing writes nothing (FR-94).
func (h *HTTPHandler) GetGoalBrief(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	proposed := ProposeGoalBrief(ws.Mission, ws.AutonomyPolicy)
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"accepted": ws.GoalBrief,
		// The proposal is a suggestion the user may take, edit, or ignore. It
		// controls nothing until accepted.
		"proposed":          proposed,
		"policy":            ws.GoalToolboxPolicy,
		"goal":              ws.Mission,
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
	})
}

// UpdateGoalBrief handles PUT/PATCH /api/workspaces/{workspaceID}/goal/brief
//
// Accepting is the user's explicit act. Only what arrives here drives
// recommendations (FR-94).
func (h *HTTPHandler) UpdateGoalBrief(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	var req struct {
		goalBriefPayload
		ExpectedWorkspaceVersion int64 `json:"expected_workspace_version,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	var saved *GoalBrief
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		if versionErr := requireWorkspaceVersion(ws, req.ExpectedWorkspaceVersion); versionErr != nil {
			return versionErr
		}
		now := time.Now()
		brief := &GoalBrief{
			Summary:              req.Summary,
			ExpectedOutput:       req.ExpectedOutput,
			SourceTypes:          req.SourceTypes,
			Operations:           req.Operations,
			MaxAutonomy:          req.MaxAutonomy,
			RequiredCapabilities: req.RequiredCapabilities,
			Source:               GoalBriefSourceUser,
			AcceptedAt:           &now,
			UpdatedAt:            now,
		}
		brief.Normalize()
		// Version increases on every accepted edit so a recommendation can name
		// the brief it was computed from, and a stale one is recognizable.
		if ws.GoalBrief != nil {
			brief.Version = ws.GoalBrief.Version + 1
		} else {
			brief.Version = 1
		}
		ws.GoalBrief = brief
		ws.UpdatedAt = now
		saved = brief.Clone()
		return nil
	})
	if err != nil {
		toolboxWriteError(w, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "goal_brief_accepted", map[string]any{"brief": saved})
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"message":   "Goal brief accepted",
		"brief":     saved,
		"workspace": workspaceID,
	})
}

// GetGoalRecommendations handles
// GET /api/workspaces/{workspaceID}/goal/recommendations
//
// Query: agent_instance_id (optional; defaults to the policy's entry agent).
//
// Read-only. Ranking never selects, applies, installs, connects, widens a
// scope, enables Expert mode, or raises autonomy (FR-99).
func (h *HTTPHandler) GetGoalRecommendations(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	agentInstanceID := strings.TrimSpace(r.URL.Query().Get("agent_instance_id"))
	if agentInstanceID == "" && ws.GoalToolboxPolicy != nil {
		agentInstanceID = ws.GoalToolboxPolicy.EntryAgentInstanceID
	}
	if agentInstanceID == "" {
		// V1 recommends for the goal's entry agent only; multi-agent
		// arrangements are Phase 3 (FR-106).
		if entry := entryAgentInstance(ws); entry != nil {
			agentInstanceID = entry.ID
		}
	}
	if agentInstanceID == "" {
		writeToolboxJSON(w, http.StatusOK, map[string]any{
			"recommendations": ToolboxRecommendationResult{
				Message: "Choose the agent that will carry out this goal to see recommendations.",
			},
			"workspace": workspaceID,
		})
		return
	}

	instance, learned, capacity, expertMode, err := h.resolveToolboxContext(ws, agentInstanceID)
	if err != nil {
		orihttp.NotFound(w, err.Error())
		return
	}

	result := RecommendToolboxes(ws, instance, ws.GoalBrief, learned, capacity, expertMode, DefaultFocusThresholds())
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"recommendations":   result,
		"policy":            ws.GoalToolboxPolicy,
		"workspace":         workspaceID,
		"workspace_version": ws.Version,
	})
}

// entryAgentInstance resolves the workspace's entry agent instance, which is
// the one a Goal runs as.
func entryAgentInstance(ws *Workspace) *AgentInstance {
	instances := ws.GetAgentInstances()
	for i := range instances {
		if instances[i].EntryPoint {
			return &instances[i]
		}
	}
	entryName := strings.TrimSpace(ws.EntryAgentName())
	for i := range instances {
		if entryName != "" && strings.EqualFold(strings.TrimSpace(instances[i].Name), entryName) {
			return &instances[i]
		}
	}
	if len(instances) > 0 {
		return &instances[0]
	}
	return nil
}

// UpdateGoalToolboxPolicy handles
// PUT/PATCH /api/workspaces/{workspaceID}/goal/toolbox-policy
//
// Pinning is the default; current-at-start is the explicitly labeled
// alternative (FR-103, FR-104).
func (h *HTTPHandler) UpdateGoalToolboxPolicy(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	var req struct {
		EntryAgentInstanceID string `json:"entry_agent_instance_id"`
		ToolboxID            string `json:"toolbox_id,omitempty"`
		// ToolboxVersion pins an exact version; 0 pins the current one at the
		// moment of this call. It never tracks later edits (FR-104).
		ToolboxVersion           int64 `json:"toolbox_version,omitempty"`
		UseCurrentAtStart        bool  `json:"use_current_at_start,omitempty"`
		ExpectedWorkspaceVersion int64 `json:"expected_workspace_version,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	var saved *GoalToolboxPolicy
	err := h.store.Update(workspaceID, func(ws *Workspace) error {
		if versionErr := requireWorkspaceVersion(ws, req.ExpectedWorkspaceVersion); versionErr != nil {
			return versionErr
		}
		instanceID := strings.TrimSpace(req.EntryAgentInstanceID)
		if instanceID == "" {
			return fmt.Errorf("entry_agent_instance_id is required")
		}
		if findAgentInstanceByID(ws, instanceID) == nil {
			return fmt.Errorf("agent instance %s is not attached to this workspace", instanceID)
		}

		policy := &GoalToolboxPolicy{
			EntryAgentInstanceID: instanceID,
			UseCurrentAtStart:    req.UseCurrentAtStart,
			UpdatedAt:            time.Now(),
		}

		if !req.UseCurrentAtStart {
			toolboxID := strings.TrimSpace(req.ToolboxID)
			if toolboxID == "" {
				return fmt.Errorf("toolbox_id is required unless the goal uses the current toolbox when it starts")
			}
			definition, exists := ws.GetToolbox(toolboxID)
			if !exists {
				return fmt.Errorf("%w: %s", ErrToolboxNotFound, toolboxID)
			}
			version := req.ToolboxVersion
			if version == 0 {
				version = definition.Version
			}
			// Resolving now is what makes the pin real: a version that cannot
			// be resolved today would fail the preflight later, unattended.
			if _, resolveErr := definition.ResolveVersion(version); resolveErr != nil {
				return resolveErr
			}
			policy.ToolboxID = definition.ID
			policy.ToolboxVersion = version
		}

		ws.GoalToolboxPolicy = policy
		ws.UpdatedAt = policy.UpdatedAt
		saved = policy.Clone()
		return nil
	})
	if err != nil {
		toolboxWriteError(w, err)
		return
	}

	h.publishToolboxEvent(workspaceID, "goal_toolbox_policy_updated", map[string]any{"policy": saved})

	message := fmt.Sprintf("This goal is pinned to version %d.", saved.ToolboxVersion)
	if saved.UseCurrentAtStart {
		message = "This goal will use the current toolbox when it starts."
	}
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"message":   message,
		"policy":    saved,
		"workspace": workspaceID,
	})
}

// GetGoalPreflight handles GET /api/workspaces/{workspaceID}/goal/preflight
//
// The same check the scheduler runs, exposed so a manual start can refuse for
// the same reason and the Goal surface can explain a `Needs attention` without
// waiting for the next cadence tick (FR-105).
func (h *HTTPHandler) GetGoalPreflight(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}

	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, fmt.Sprintf("Workspace not found: %v", err))
		return
	}

	var learned []ResolvedSkill
	capacity, expertMode := 0, false
	if ws.GoalToolboxPolicy != nil {
		if instance := findAgentInstanceByID(ws, ws.GoalToolboxPolicy.EntryAgentInstanceID); instance != nil {
			if _, resolvedLearned, resolvedCapacity, resolvedExpert, ctxErr := h.resolveToolboxContext(ws, instance.ID); ctxErr == nil {
				learned, capacity, expertMode = resolvedLearned, resolvedCapacity, resolvedExpert
			}
		}
	}

	result := PreflightGoalToolbox(ws, learned, capacity, expertMode, DefaultFocusThresholds())
	writeToolboxJSON(w, http.StatusOK, map[string]any{
		"preflight": result,
		"policy":    ws.GoalToolboxPolicy,
		"workspace": workspaceID,
	})
}
