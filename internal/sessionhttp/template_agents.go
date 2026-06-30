package sessionhttp

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// seedAgentsResult is the outcome of seeding a template's agent roster.
// Per-agent tool binding for the created agents is a follow-up (PRD FR14); this
// result will grow to carry the created agents + specs when that lands.
type seedAgentsResult struct {
	Warnings []string
	EntrySet bool
}

// validAgentTypes canonicalizes a template-declared agent type to the real
// vocabulary; an empty/unrecognized value maps to "" so the store applies its
// own default (PRD FR8).
var validAgentTypes = map[string]string{
	agent.TypeToolCalling: agent.TypeToolCalling,
	agent.TypeGeneral:     agent.TypeGeneral,
	agent.TypeResearch:    agent.TypeResearch,
}

// validAgentRoles canonicalizes a template-declared role. cli_agent is
// intentionally omitted: CLI agents are a v1 non-goal, so a template cannot mint
// one — an unrecognized role falls back to the store default.
var validAgentRoles = map[string]string{
	string(types.RoleOrchestrator): string(types.RoleOrchestrator),
	string(types.RoleResearcher):   string(types.RoleResearcher),
	string(types.RoleAnalyzer):     string(types.RoleAnalyzer),
	string(types.RoleSynthesizer):  string(types.RoleSynthesizer),
	string(types.RoleValidator):    string(types.RoleValidator),
	string(types.RoleSpecialist):   string(types.RoleSpecialist),
	string(types.RoleGeneral):      string(types.RoleGeneral),
}

func canonicalAgentType(s string) string {
	return validAgentTypes[strings.ToLower(strings.TrimSpace(s))]
}

func canonicalAgentRole(s string) string {
	return validAgentRoles[strings.ToLower(strings.TrimSpace(s))]
}

// seedTemplateAgents creates (or reuses) the agents a template declares and
// attaches them to ws in roster order. The first declared agent becomes the
// workspace entry agent; the rest are specialist sub-agents. It runs before the
// workspace is persisted so the roster is part of the stored agent list and the
// entry agent suppresses the mandatory "create an entry agent" prompt.
//
// Reuse-on-name-match (PRD FR13): a name that already exists as a global agent is
// attached as-is and never mutated — the template's prompt/model/tools for that
// entry are ignored. Only unmatched names create a new global agent.
//
// Failure handling (PRD FR15): a specialist that fails to create is recorded as a
// warning and skipped; if the entry agent (first) fails, seeding stops and the
// workspace is left agent-less so the mandatory-prompt fallback runs — no
// specialist is promoted in its place.
func (h *Handler) seedTemplateAgents(ws *session.Workspace, tpl projecttemplates.Template) seedAgentsResult {
	var result seedAgentsResult
	if h == nil || h.agentStore == nil || ws == nil || !tpl.HasAgents() {
		return result
	}

	for i, spec := range tpl.Agents {
		isEntry := i == 0
		if _, exists := h.agentStore.GetAgent(spec.Name); !exists {
			cfg := &store.CreateAgentConfig{
				Type:         canonicalAgentType(spec.Type),
				Role:         types.AgentRole(canonicalAgentRole(spec.Role)),
				Model:        spec.Model, // empty -> the store's global default model
				SystemPrompt: spec.SystemPrompt,
			}
			if err := h.agentStore.CreateAgent(spec.Name, cfg); err != nil {
				if isEntry {
					logger.Warn("Failed to seed template entry agent; falling back to entry-agent prompt",
						logger.Fields{"workspace": ws.ID, "agent": spec.Name, "error": err})
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("Entry agent %q could not be created — you'll be prompted to add one.", spec.Name))
					return result
				}
				logger.Warn("Failed to seed template specialist agent (skipped)",
					logger.Fields{"workspace": ws.ID, "agent": spec.Name, "error": err})
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("Specialist agent %q could not be created and was skipped.", spec.Name))
				continue
			}
		}

		if isEntry {
			setWorkspaceEntryAgent(ws, spec.Name)
			result.EntrySet = true
		} else {
			attachWorkspaceSpecialist(ws, spec.Name)
		}
	}

	return result
}

// attachWorkspaceSpecialist adds a non-entry agent to the workspace — a fresh
// AgentInstance plus the legacy Agents entry — mirroring the add-agent endpoint.
// It is a no-op if an instance for the agent already exists.
func attachWorkspaceSpecialist(ws *session.Workspace, name string) {
	name = strings.TrimSpace(name)
	if ws == nil || name == "" {
		return
	}
	for _, inst := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(inst.Name), name) {
			return
		}
	}
	ws.AgentInstances = append(ws.AgentInstances, session.AgentInstance{
		ID:             uuid.New().String(),
		Name:           name,
		InstanceNumber: 1,
		NodeID:         fmt.Sprintf("%s-1-node-%s", name, uuid.New().String()[:8]),
		CreatedAt:      time.Now(),
	})
	ensureLegacyWorkspaceAgentName(ws, name)
}
