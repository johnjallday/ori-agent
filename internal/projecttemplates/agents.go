package projecttemplates

import "strings"

// MaxTemplateAgents caps how many agents a single template may declare. Extra
// entries beyond the cap are dropped during normalization rather than failing
// the load: a roster this large would slow workspace creation and is an easy
// abuse vector for an imported template. The cap is a soft limit (drop-extra),
// not a load error.
const MaxTemplateAgents = 10

// AgentSpec declares one agent a template seeds onto a workspace created from
// it. Like ToolDefaults, this package carries the spec as data only — it never
// creates or resolves agents. Role/Type/Model are trimmed and carried verbatim;
// canonicalizing them against the real agent enums and resolving empty values to
// defaults happens in the workspace-creation (seeding) layer, keeping this
// file-copy engine domain-blind. The first surviving entry in a template's
// roster is the workspace entry agent; the rest are specialist sub-agents.
type AgentSpec struct {
	Name         string       `json:"name"`
	Role         string       `json:"role,omitempty"`
	Type         string       `json:"type,omitempty"`
	SystemPrompt string       `json:"system_prompt,omitempty"`
	Model        string       `json:"model,omitempty"`
	Tools        ToolDefaults `json:"tools"`
}

// normalizeAgentSpecs cleans a raw roster: it trims string fields, drops entries
// with a blank name, de-duplicates by case-insensitive name (keeping the first
// occurrence), preserves declaration order (the first survivor is the entry
// agent — order is never sorted), normalizes each agent's tool lists, and caps
// the roster at MaxTemplateAgents. A nil/empty input (or one that normalizes
// away entirely) returns nil so a template with no agents stays a no-op.
func normalizeAgentSpecs(specs []AgentSpec) []AgentSpec {
	if len(specs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(specs))
	out := make([]AgentSpec, 0, len(specs))
	for _, s := range specs {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, AgentSpec{
			Name:         name,
			Role:         strings.TrimSpace(s.Role),
			Type:         strings.TrimSpace(s.Type),
			SystemPrompt: strings.TrimSpace(s.SystemPrompt),
			Model:        strings.TrimSpace(s.Model),
			Tools:        normalizeToolDefaults(s.Tools),
		})
		if len(out) >= MaxTemplateAgents {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
