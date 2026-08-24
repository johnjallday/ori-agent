// Package agentcatalog defines the static Role Catalog: a small, curated set
// of agent presets (display identity, model tier, starter prompt, starter
// skills) offered as the default way to create an agent. See
// tasks/prd-agent-role-catalog.md section A.
package agentcatalog

import (
	"github.com/johnjallday/ori-agent/internal/types"
)

// ModelTier is a coarse capability tier a catalog entry declares. It is
// resolved to a concrete model through the existing model-category system
// (internal/store.ModelCategoryStore), never hard-coded to a specific model.
type ModelTier string

const (
	TierFast     ModelTier = "fast"     // cost-optimized, tool-calling oriented
	TierBalanced ModelTier = "balanced" // general purpose
	TierDeep     ModelTier = "deep"     // deepest reasoning tier
)

// defaultCategoryIDs maps a tier to the built-in model-category ID it
// resolves through. These IDs come from types.DefaultCategories(); the
// mapping is 1:1 by design (tool-calling/general-purpose/research).
var defaultCategoryIDs = map[ModelTier]string{
	TierFast:     "cat_default_tool_calling",
	TierBalanced: "cat_default_general_purpose",
	TierDeep:     "cat_default_research",
}

// DefaultCategoryID returns the built-in model-category ID a tier resolves
// through, or "" for an unknown tier.
func DefaultCategoryID(tier ModelTier) string {
	return defaultCategoryIDs[tier]
}

// Entry describes one curated catalog role: a display identity plus the
// presets a "Catalog" creation applies (role, model tier, starter prompt,
// starter skills). Display fields are presentation-only — Slug is the only
// value persisted on the created agent's Role.
type Entry struct {
	Slug        types.AgentRole `json:"slug"`
	DisplayName string          `json:"display_name"`
	Emblem      string          `json:"emblem"`       // Bootstrap Icons name, no "bi-" prefix
	AccentColor string          `json:"accent_color"` // hex color
	Tagline     string          `json:"tagline"`
	Description string          `json:"description"`
	ModelTier   ModelTier       `json:"model_tier"`

	StarterPrompt string `json:"starter_prompt"`
	// StarterSkills lists built-in skill names to enable at creation. Must
	// stay within the spark-stage slot cap (2, see FR10) so no agent is born
	// over-cap. Empty in v1: the repo ships no universal built-in skill set
	// for non-CLI agents to draw from (skills are user/data-dir scoped); the
	// application path is fully wired for whenever one is added.
	StarterSkills []string `json:"starter_skills"`

	// SupportsDomain is true only for the Specialist entry: creation accepts
	// an optional free-text domain that feeds the created agent's
	// RoutingProfile.Domains and is appended to its starter prompt.
	SupportsDomain bool `json:"supports_domain"`
}

// registry is the static, ordered Role Catalog (FR A.1): exactly one entry
// per types.AgentRole value, excluding RoleGeneral and RoleCLIAgent. Order is
// display order in the catalog card grid.
var registry = []Entry{
	{
		Slug:        types.RoleOrchestrator,
		DisplayName: "Commander",
		Emblem:      "diagram-3",
		AccentColor: "#f59e0b",
		Tagline:     "Leads the workspace — receives requests, dispatches tasks, coordinates the team",
		Description: "Front door for the workspace. Triages incoming work and dispatches it to the right specialist instead of doing domain work itself.",
		ModelTier:   TierBalanced,
		StarterPrompt: "You are the Commander for this workspace. You receive incoming requests, decide whether " +
			"to handle them directly or dispatch them to the right specialist agent, and keep the team's work " +
			"moving. Prefer delegating domain-specific work to a specialist over doing it yourself; step in " +
			"directly only for coordination, triage, and short administrative tasks.",
		StarterSkills: []string{},
	},
	{
		Slug:        types.RoleResearcher,
		DisplayName: "Researcher",
		Emblem:      "search",
		AccentColor: "#0ea5e9",
		Tagline:     "Gathers information from the web and files",
		Description: "Finds and summarizes information from the web, documents, and other sources, with clear sourcing.",
		ModelTier:   TierDeep,
		StarterPrompt: "You are a Researcher. Your job is to gather information — from the web, files, and other " +
			"sources — and report back clearly sourced findings. Prioritize accuracy and say where information " +
			"came from.",
		StarterSkills: []string{},
	},
	{
		Slug:        types.RoleAnalyzer,
		DisplayName: "Analyzer",
		Emblem:      "graph-up",
		AccentColor: "#8b5cf6",
		Tagline:     "Digs into data and code to explain what's going on",
		Description: "Investigates data, code, and logs and explains what's actually happening, precisely and with reasoning shown.",
		ModelTier:   TierDeep,
		StarterPrompt: "You are an Analyzer. You dig into data, code, and logs to explain what is actually going " +
			"on. Be precise, show your reasoning, and flag uncertainty rather than guessing.",
		StarterSkills: []string{},
	},
	{
		Slug:        types.RoleSynthesizer,
		DisplayName: "Writer",
		Emblem:      "pencil-square",
		AccentColor: "#22c55e",
		Tagline:     "Turns findings into briefs, docs, and emails",
		Description: "Turns findings, notes, and raw material into clear briefs, documents, and emails.",
		ModelTier:   TierBalanced,
		StarterPrompt: "You are a Writer. You turn findings, notes, and raw material into clear briefs, " +
			"documents, and emails. Prioritize clarity and the right tone for the audience over exhaustive detail.",
		StarterSkills: []string{},
	},
	{
		Slug:        types.RoleValidator,
		DisplayName: "Reviewer",
		Emblem:      "shield-check",
		AccentColor: "#ef4444",
		Tagline:     "Reviews, fact-checks, and catches mistakes",
		Description: "Checks work for mistakes, verifies facts, and flags problems before they ship.",
		ModelTier:   TierBalanced,
		StarterPrompt: "You are a Reviewer. You check other people's and agents' work for mistakes, verify " +
			"facts, and flag anything that looks wrong before it ships. Be specific about what you checked and " +
			"what you found.",
		StarterSkills: []string{},
	},
	{
		Slug:        types.RoleSpecialist,
		DisplayName: "Specialist",
		Emblem:      "gem",
		AccentColor: "#14b8a6",
		Tagline:     "Deep expert in one domain you define",
		Description: "A focused expert in a single domain you name at creation (e.g. \"audio\", \"tax\").",
		ModelTier:   TierFast,
		StarterPrompt: "You are a Specialist with deep expertise in one domain. Focus on that domain, use the " +
			"tools and context available to you, and be explicit when a request falls outside your area.",
		StarterSkills:  []string{},
		SupportsDomain: true,
	},
}

// Registry returns a copy of the static Role Catalog, safe for callers to
// range over or serialize without risking mutation of package state.
func Registry() []Entry {
	out := make([]Entry, len(registry))
	copy(out, registry)
	return out
}

// Find returns the catalog entry for the given role slug.
func Find(slug types.AgentRole) (Entry, bool) {
	for _, e := range registry {
		if e.Slug == slug {
			return e, true
		}
	}
	return Entry{}, false
}
