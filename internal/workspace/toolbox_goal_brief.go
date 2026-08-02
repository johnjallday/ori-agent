package workspace

import (
	"sort"
	"strings"
	"time"
)

// Goal briefs: the visible, editable statement of what a Goal actually needs
// (PRD FR-92–FR-94).
//
// A Goal today is a sentence of prose. That is fine for a human and useless for
// ranking: "keep an eye on the release notes" does not say whether it needs to
// read the web, write a file, or send anything. The brief turns that prose into
// structured requirements a deterministic ranker can score against.
//
// The rule that makes this safe is that a PROPOSED brief controls nothing. Ori
// may use an LLM to draft one, but the draft is inspectable and editable, and
// only an ACCEPTED brief feeds recommendations (FR-94). Without that split, a
// model's guess about what a goal needs would quietly become the basis for
// which capabilities get recommended — and a wrong guess would recommend the
// wrong permissions.

// Autonomy ceilings a Goal brief may declare. These mirror AutonomyPolicy
// rather than replacing it: the brief states what the goal SHOULD be allowed to
// do, and the existing autonomy gate remains what actually enforces it.
const (
	// GoalAutonomyRead is observation only.
	GoalAutonomyRead = "read"
	// GoalAutonomyWrite permits workspace-internal changes.
	GoalAutonomyWrite = "write"
	// GoalAutonomyExternal permits effects outside the workspace.
	GoalAutonomyExternal = "external"
)

// GoalBrief is the structured requirement manifest for one Goal (FR-93).
type GoalBrief struct {
	// Summary restates the goal in one line, for the user to check the rest
	// against.
	Summary string `json:"summary,omitempty"`
	// ExpectedOutput describes what the goal produces — a report, a set of
	// filed items, a list of findings.
	ExpectedOutput string `json:"expected_output,omitempty"`
	// SourceTypes name where the information comes from ("workspace files",
	// "public web", "the team's calendar").
	SourceTypes []string `json:"source_types,omitempty"`
	// Operations are the semantic things the goal must be able to do —
	// "search", "read", "summarize". They are matched against Toolbox
	// capabilities, so they are deliberately capability-shaped rather than
	// tool-shaped: a goal needs to search, not to call `web_query`.
	Operations []string `json:"operations,omitempty"`
	// MaxAutonomy is the highest side-effect class this goal should reach. It
	// is a CEILING the ranker penalizes exceeding, never a grant (FR-99).
	MaxAutonomy string `json:"max_autonomy,omitempty"`
	// RequiredCapabilities name capabilities the goal cannot run without,
	// using the same normalized skill identities a Toolbox uses.
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`

	// Source records how this brief came to exist: "proposed" (an LLM draft
	// nobody has accepted), "user" (hand-written or edited), or "fallback"
	// (derived deterministically because no proposal service was available).
	Source string `json:"source,omitempty"`
	// AcceptedAt is set only when the user accepted it. A brief with a zero
	// AcceptedAt must not drive recommendations (FR-94).
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	// Version increases on every accepted edit, so a recommendation can name
	// the brief it was computed from.
	Version   int64     `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Brief sources.
const (
	GoalBriefSourceProposed = "proposed"
	GoalBriefSourceUser     = "user"
	GoalBriefSourceFallback = "fallback"
)

// Accepted reports whether this brief may drive recommendations.
func (b *GoalBrief) Accepted() bool {
	return b != nil && b.AcceptedAt != nil
}

// Clone returns a deep copy.
func (b *GoalBrief) Clone() *GoalBrief {
	if b == nil {
		return nil
	}
	cp := *b
	cp.SourceTypes = append([]string(nil), b.SourceTypes...)
	cp.Operations = append([]string(nil), b.Operations...)
	cp.RequiredCapabilities = append([]string(nil), b.RequiredCapabilities...)
	if b.AcceptedAt != nil {
		accepted := *b.AcceptedAt
		cp.AcceptedAt = &accepted
	}
	return &cp
}

// Normalize canonicalizes the brief's list fields so two equivalent briefs
// compare equal — which is what makes ranking deterministic (FR-95).
func (b *GoalBrief) Normalize() {
	if b == nil {
		return
	}
	b.Summary = strings.TrimSpace(b.Summary)
	b.ExpectedOutput = strings.TrimSpace(b.ExpectedOutput)
	b.SourceTypes = normalizeBriefList(b.SourceTypes)
	b.Operations = normalizeBriefList(b.Operations)
	b.RequiredCapabilities = normalizeBriefList(b.RequiredCapabilities)
	b.MaxAutonomy = normalizeGoalAutonomy(b.MaxAutonomy)
}

func normalizeBriefList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeGoalAutonomy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case GoalAutonomyExternal:
		return GoalAutonomyExternal
	case GoalAutonomyWrite:
		return GoalAutonomyWrite
	case GoalAutonomyRead:
		return GoalAutonomyRead
	default:
		return ""
	}
}

func goalAutonomyRank(value string) int {
	switch normalizeGoalAutonomy(value) {
	case GoalAutonomyExternal:
		return 3
	case GoalAutonomyWrite:
		return 2
	case GoalAutonomyRead:
		return 1
	default:
		return 0
	}
}

// GoalToolboxPolicy records which Toolbox a Goal runs with, and whether that
// choice is pinned (FR-103, FR-104).
type GoalToolboxPolicy struct {
	// EntryAgentInstanceID is the stable instance that carries out the goal.
	// V1 recommends for this instance only; multi-agent arrangements are
	// Phase 3 (FR-106).
	EntryAgentInstanceID string `json:"entry_agent_instance_id,omitempty"`
	// ToolboxID and ToolboxVersion name the pinned recipe.
	ToolboxID      string `json:"toolbox_id,omitempty"`
	ToolboxVersion int64  `json:"toolbox_version,omitempty"`
	// UseCurrentAtStart is the deliberate alternative to pinning: the goal
	// resolves whatever the instance is using when it starts.
	//
	// Pinning is the DEFAULT because a recurring goal that silently changed
	// behavior when someone edited a toolbox would be the worst kind of
	// surprise — it would only show up in results, days later (FR-104).
	UseCurrentAtStart bool `json:"use_current_at_start,omitempty"`

	// NeedsAttention and NeedsAttentionReason are set by the preflight when a
	// pinned toolbox has become unusable, so the goal stops before the model is
	// invoked rather than running with the wrong capabilities (FR-105).
	NeedsAttention       bool      `json:"needs_attention,omitempty"`
	NeedsAttentionReason string    `json:"needs_attention_reason,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

// Clone returns a copy.
func (p *GoalToolboxPolicy) Clone() *GoalToolboxPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// Pinned reports whether this policy fixes an exact version.
func (p *GoalToolboxPolicy) Pinned() bool {
	return p != nil && !p.UseCurrentAtStart && strings.TrimSpace(p.ToolboxID) != ""
}

// ProposeGoalBrief derives a brief deterministically from a goal's prose and
// the workspace's own state.
//
// This is the FALLBACK path, used when no LLM proposal service is configured or
// when one fails. It is deliberately conservative: it infers only what the text
// plainly says, and it never invents a capability requirement, because an
// invented requirement would push the ranker toward recommending permissions
// the goal does not need (FR-94, FR-99).
func ProposeGoalBrief(goalText string, autonomy AutonomyPolicy) GoalBrief {
	text := strings.ToLower(strings.TrimSpace(goalText))
	brief := GoalBrief{
		Summary:   strings.TrimSpace(goalText),
		Source:    GoalBriefSourceFallback,
		UpdatedAt: time.Now(),
	}

	// The autonomy ceiling comes from the workspace's existing policy rather
	// than from reading the prose. The policy is a decision the user already
	// made; guessing from words would be free to contradict it.
	switch autonomy {
	case AutonomyWatch:
		brief.MaxAutonomy = GoalAutonomyRead
	case AutonomyPropose:
		brief.MaxAutonomy = GoalAutonomyWrite
	default:
		brief.MaxAutonomy = GoalAutonomyRead
	}

	// Operations are matched from the same vocabulary Focus uses for overlap,
	// so a brief's "search" and a Toolbox's search operations mean the same
	// thing.
	for fragment, operation := range nameHeuristicOperations {
		if strings.Contains(text, fragment) {
			brief.Operations = append(brief.Operations, operation)
		}
	}

	for phrase, source := range map[string]string{
		"file":     "workspace files",
		"folder":   "workspace files",
		"note":     "workspace notes",
		"web":      "public web",
		"email":    "email",
		"inbox":    "email",
		"calendar": "calendar",
		"task":     "workspace tasks",
	} {
		if strings.Contains(text, phrase) {
			brief.SourceTypes = append(brief.SourceTypes, source)
		}
	}

	brief.Normalize()
	return brief
}
