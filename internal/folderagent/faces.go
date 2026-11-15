package folderagent

import (
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentappearance"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// confirmedHireFaces validates the whole choice before the run can start.
// Unknown hire names confer no authority and are ignored, even with bad ids.
func confirmedHireFaces(rec Record, hires []HireEntry, chosen map[string]string) (map[string]*types.AgentAppearance, error) {
	if len(chosen) > maxReceiptItems {
		return nil, personalassistant.ErrValidation
	}
	faces := make(map[string]*types.AgentAppearance, len(hires))
	for _, hire := range hires {
		id, supplied := chosen[hire.Name]
		if !supplied {
			// An older client omits faces. Keep a retry's saved choice, but do
			// not silently opt a first confirmation into a suggested character.
			faces[hire.Name] = rec.HireAppearance(hire.Name)
			continue
		}
		appearance := types.NewAgentAppearance()
		if id = strings.TrimSpace(id); id != "" {
			version, err := agentappearance.ResolveAssignableCharacter(id)
			if err != nil {
				return nil, &Refusal{Code: "agent_character_unknown", Status: http.StatusBadRequest,
					Message: "That face is not available here. Choose another."}
			}
			appearance.SetCharacter(id, version)
		}
		faces[hire.Name] = appearance
	}
	return faces, nil
}

// HireAppearance is create-only input. Never lend a mutable saved appearance
// to a staffing adapter or use it to update an existing agent.
func (r Record) HireAppearance(name string) *types.AgentAppearance {
	return r.HireFaces[name].Clone()
}

func applyHireFaces(plan *Plan, faces map[string]*types.AgentAppearance) {
	for i := range plan.Hires {
		if appearance, confirmed := faces[plan.Hires[i].Name]; confirmed {
			plan.Hires[i].Character = ""
			if appearance != nil && appearance.Mode == types.AppearanceModeCharacter {
				plan.Hires[i].Character = appearance.CharacterCatalogID()
			}
		}
	}
}

// Execution equivalence retains every structural and authority check, ignoring
// only faces. Roster usage can change while a multi-step run hires its agents.
func withoutHireFaces(plan Plan) Plan {
	plan.Hires = append([]HireEntry(nil), plan.Hires...)
	for i := range plan.Hires {
		plan.Hires[i].Character = ""
	}
	return plan
}
