package types

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The global agent's Default Toolbox: its explicit skill selection for direct,
// non-workspace chat (PRD FR-24–FR-27).
//
// This type exists to give direct chat the same property workspace Toolboxes
// give workspace runs — an explicit, inspectable answer to "what is active" —
// without letting the two leak into each other. A reusable agent used in three
// workspaces has three independent workspace Toolboxes and exactly one Default
// Toolbox, and editing any of the four must not touch the other three.
//
// The isolation is enforced BY CONSTRUCTION rather than by a check: this type
// has no field capable of naming a workspace binding, credential, scope, or
// agent instance (FR-25). There is no code path that could put one here,
// because there is nowhere to put it. Validate additionally rejects identities
// shaped like workspace runtime references, so a hand-edited agent settings
// file cannot smuggle one in through the skill-identity field.

// MaxDefaultToolboxNameLength bounds the user-visible name.
const MaxDefaultToolboxNameLength = 60

// DefaultToolboxName is the name a freshly created Default Toolbox carries.
const DefaultToolboxName = "Default Toolbox"

// DefaultToolboxSkillRef is one skill the agent activates in direct chat.
//
// Note what is absent: no source field, no binding ID. Every entry here is
// agent-learned by definition — a Default Toolbox cannot draw on a workspace,
// so there is no second source to disambiguate (FR-25).
type DefaultToolboxSkillRef struct {
	// CapabilityID is the normalized (lower-cased) skill identity.
	CapabilityID string `json:"capability_id"`
	// DisplayName preserves the exact-case skill name for the UI and for
	// resolving the skill against the skill manager.
	DisplayName string `json:"display_name,omitempty"`
}

// AgentDefaultToolbox is a global agent's explicit skill-only Toolbox for
// direct chat.
type AgentDefaultToolbox struct {
	Name   string                   `json:"name,omitempty"`
	Skills []DefaultToolboxSkillRef `json:"skills,omitempty"`
	// Version increases on every edit. It is not a run-snapshot pin — direct
	// chats are not snapshotted in V1 — but it gives the API a lost-write check
	// and the audit trail a stable reference (FR-160).
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// NewAgentDefaultToolbox returns an empty Default Toolbox at version 1.
func NewAgentDefaultToolbox() *AgentDefaultToolbox {
	return &AgentDefaultToolbox{
		Name:      DefaultToolboxName,
		Version:   1,
		UpdatedAt: time.Now(),
	}
}

// Clone returns a deep copy.
func (t *AgentDefaultToolbox) Clone() *AgentDefaultToolbox {
	if t == nil {
		return nil
	}
	cp := *t
	if len(t.Skills) > 0 {
		cp.Skills = append([]DefaultToolboxSkillRef(nil), t.Skills...)
	}
	return &cp
}

// SkillNames returns the display names of the active skills, in stored order.
// This is what the direct-chat runtime resolves against the skill manager.
func (t *AgentDefaultToolbox) SkillNames() []string {
	if t == nil || len(t.Skills) == 0 {
		return nil
	}
	names := make([]string, 0, len(t.Skills))
	for _, skill := range t.Skills {
		name := strings.TrimSpace(skill.DisplayName)
		if name == "" {
			name = skill.CapabilityID
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Has reports whether the Default Toolbox activates a skill, compared
// case-insensitively.
func (t *AgentDefaultToolbox) Has(skillName string) bool {
	if t == nil {
		return false
	}
	key := normalizeDefaultToolboxCapabilityID(skillName)
	if key == "" {
		return false
	}
	for _, skill := range t.Skills {
		if skill.CapabilityID == key {
			return true
		}
	}
	return false
}

// SetSkills replaces the active skill selection, normalizing and
// case-insensitively deduplicating it, and bumps the version.
func (t *AgentDefaultToolbox) SetSkills(skills []DefaultToolboxSkillRef) error {
	if t == nil {
		return fmt.Errorf("default toolbox is nil")
	}
	normalized, err := NormalizeDefaultToolboxSkills(skills)
	if err != nil {
		return err
	}
	t.Skills = normalized
	t.Version++
	t.UpdatedAt = time.Now()
	if strings.TrimSpace(t.Name) == "" {
		t.Name = DefaultToolboxName
	}
	return nil
}

// NormalizeDefaultToolboxSkills canonicalizes, deduplicates, sorts, and
// validates a Default Toolbox skill selection.
func NormalizeDefaultToolboxSkills(skills []DefaultToolboxSkillRef) ([]DefaultToolboxSkillRef, error) {
	out := make([]DefaultToolboxSkillRef, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		display := strings.TrimSpace(skill.DisplayName)
		identity := normalizeDefaultToolboxCapabilityID(skill.CapabilityID)
		if identity == "" {
			identity = normalizeDefaultToolboxCapabilityID(display)
		}
		if identity == "" {
			continue
		}
		if display == "" {
			display = identity
		}
		if err := validateDefaultToolboxIdentity(identity); err != nil {
			return nil, err
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		out = append(out, DefaultToolboxSkillRef{CapabilityID: identity, DisplayName: display})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CapabilityID < out[j].CapabilityID })
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeDefaultToolboxCapabilityID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// validateDefaultToolboxIdentity rejects an identity that is not a plain skill
// name.
//
// The rejected shape is `ws:{workspace}:mcp:{server}:{binding}`, the
// materialized runtime name for a workspace MCP binding. It can only arrive
// here through a hand-edited settings file or a confused caller, and accepting
// it would put a workspace reference on a global record — exactly what FR-25
// forbids.
func validateDefaultToolboxIdentity(identity string) error {
	if strings.HasPrefix(identity, "ws:") || strings.Contains(identity, ":mcp:") {
		return fmt.Errorf("default toolbox may not reference the workspace binding %q; it holds agent-learned skills only", identity)
	}
	return nil
}

// ValidateDefaultToolbox reports why a Default Toolbox cannot be saved, or nil.
func ValidateDefaultToolbox(t *AgentDefaultToolbox) error {
	if t == nil {
		return nil
	}
	if len([]rune(strings.TrimSpace(t.Name))) > MaxDefaultToolboxNameLength {
		return fmt.Errorf("default toolbox name must be %d characters or fewer", MaxDefaultToolboxNameLength)
	}
	for _, skill := range t.Skills {
		identity := normalizeDefaultToolboxCapabilityID(skill.CapabilityID)
		if identity == "" {
			return fmt.Errorf("default toolbox skill entry requires a capability identity")
		}
		if err := validateDefaultToolboxIdentity(identity); err != nil {
			return err
		}
	}
	return nil
}
