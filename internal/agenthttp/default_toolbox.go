package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Read/update API for a global agent's Default Toolbox — its explicit skill
// selection for direct, non-workspace chat (PRD FR-24–FR-27, task 1.14).
//
// The safety property this endpoint carries is NEGATIVE: it must be incapable
// of touching workspace state. It accepts skill identities and nothing else,
// writes only the agent record, and rejects an identity shaped like a workspace
// binding reference (types.ValidateDefaultToolbox). A workspace Toolbox edit
// likewise cannot reach here — the two live in different stores and share no
// write path (FR-26, FR-27).

// DefaultToolboxSkillPayload is one skill in a Default Toolbox request or
// response, with the availability the collection currently reports.
type DefaultToolboxSkillPayload struct {
	CapabilityID string `json:"capability_id,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	// Available reports whether the agent's Skill Collection currently offers
	// this skill. Response-only; ignored on write.
	Available bool `json:"available,omitempty"`
}

type defaultToolboxRequest struct {
	Name   *string                      `json:"name,omitempty"`
	Skills []DefaultToolboxSkillPayload `json:"skills"`
}

// HandleDefaultToolbox routes GET and PUT/PATCH for
// /api/agents/{name}/default-toolbox.
func (h *Handler) HandleDefaultToolbox(w http.ResponseWriter, r *http.Request) {
	agentName := agentNameFromDefaultToolboxPath(r.URL.Path)
	if agentName == "" {
		orihttp.BadRequest(w, "Agent name is required")
		return
	}

	ag, ok := h.State.GetAgent(agentName)
	if !ok || ag == nil {
		orihttp.NotFound(w, "Agent not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getDefaultToolbox(w, agentName, ag)
	case http.MethodPut, http.MethodPatch:
		h.updateDefaultToolbox(w, r, agentName)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (h *Handler) getDefaultToolbox(w http.ResponseWriter, agentName string, ag *agent.Agent) {
	collection := h.skillCollectionFor(agentName)
	toolbox := ag.DefaultToolbox

	writeDefaultToolboxJSON(w, map[string]any{
		"agent":            agentName,
		"default_toolbox":  toolbox,
		"skills":           annotateDefaultToolboxSkills(toolbox, collection),
		"skill_collection": collection,
		// A nil Default Toolbox means this agent predates the field and still
		// uses every enabled skill in direct chat. Saying so beats presenting
		// an empty selection the user never made (FR-28).
		"migrated": toolbox != nil,
	})
}

func (h *Handler) updateDefaultToolbox(w http.ResponseWriter, r *http.Request, agentName string) {
	var req defaultToolboxRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	selection := make([]types.DefaultToolboxSkillRef, 0, len(req.Skills))
	for _, skill := range req.Skills {
		selection = append(selection, types.DefaultToolboxSkillRef{
			CapabilityID: skill.CapabilityID,
			DisplayName:  skill.DisplayName,
		})
	}

	var saved *types.AgentDefaultToolbox
	err := h.State.UpdateAgent(agentName, func(ag *agent.Agent) error {
		ag.InitializeDefaultToolbox()
		if req.Name != nil {
			ag.DefaultToolbox.Name = strings.TrimSpace(*req.Name)
		}
		if setErr := ag.DefaultToolbox.SetSkills(selection); setErr != nil {
			return setErr
		}
		if validateErr := types.ValidateDefaultToolbox(ag.DefaultToolbox); validateErr != nil {
			return validateErr
		}
		saved = ag.DefaultToolbox.Clone()
		return nil
	})
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}
	if saved == nil {
		orihttp.InternalError(w, "default toolbox was not saved")
		return
	}

	// Provenance for the audit trail: who changed the Default Toolbox, when,
	// and to which version (FR-160).
	if h.ActivityLogger != nil {
		if logErr := h.ActivityLogger.LogActivity(agentName, types.ActivityEventUpdated, map[string]any{
			"change":                  "default_toolbox",
			"default_toolbox_version": saved.Version,
			"skill_count":             len(saved.Skills),
		}, ""); logErr != nil {
			logger.Warn("Failed to record the default toolbox change", logger.Fields{
				"agent": agentName,
				"error": logErr.Error(),
			})
		}
	}

	collection := h.skillCollectionFor(agentName)
	writeDefaultToolboxJSON(w, map[string]any{
		"message":         "Default toolbox updated",
		"agent":           agentName,
		"default_toolbox": saved,
		"skills":          annotateDefaultToolboxSkills(saved, collection),
		"migrated":        true,
	})
}

// skillCollectionFor lists the skills this agent may choose from — its Skill
// Collection. Collection membership is not activation: a skill listed here is
// available to select, and only the Default Toolbox decides what is active in
// direct chat (FR-3).
func (h *Handler) skillCollectionFor(agentName string) []DefaultToolboxSkillPayload {
	if h.skillsManager == nil {
		return nil
	}
	available, err := h.skillsManager.ListSkills(agentName)
	if err != nil {
		logger.Warn("Failed to list the agent's skill collection", logger.Fields{
			"agent": agentName,
			"error": err.Error(),
		})
		return nil
	}

	out := make([]DefaultToolboxSkillPayload, 0, len(available))
	for _, skill := range available {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		out = append(out, DefaultToolboxSkillPayload{
			CapabilityID: strings.ToLower(name),
			DisplayName:  name,
			Available:    true,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CapabilityID < out[j].CapabilityID })
	return out
}

// annotateDefaultToolboxSkills reports each selected skill alongside whether
// the collection still offers it, so the UI can show a truthful `Missing
// capability` rather than silently dropping the entry (FR-14, FR-46).
func annotateDefaultToolboxSkills(toolbox *types.AgentDefaultToolbox, collection []DefaultToolboxSkillPayload) []DefaultToolboxSkillPayload {
	if toolbox == nil || len(toolbox.Skills) == 0 {
		return nil
	}
	availableIDs := make(map[string]struct{}, len(collection))
	for _, skill := range collection {
		availableIDs[skill.CapabilityID] = struct{}{}
	}

	out := make([]DefaultToolboxSkillPayload, 0, len(toolbox.Skills))
	for _, skill := range toolbox.Skills {
		_, available := availableIDs[skill.CapabilityID]
		out = append(out, DefaultToolboxSkillPayload{
			CapabilityID: skill.CapabilityID,
			DisplayName:  skill.DisplayName,
			// With no skills manager wired there is nothing to check against,
			// so availability is not asserted either way.
			Available: available || len(collection) == 0,
		})
	}
	return out
}

func agentNameFromDefaultToolboxPath(path string) string {
	rest := strings.TrimPrefix(path, "/api/agents/")
	rest = strings.TrimSuffix(rest, "/default-toolbox")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(rest); err == nil {
		return decoded
	}
	return rest
}

func writeDefaultToolboxJSON(w http.ResponseWriter, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("Failed to encode default toolbox response", logger.Fields{"error": err})
	}
}
