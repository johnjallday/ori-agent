package workspace

import "strings"

// readPrefixes are tool-name prefixes that strongly suggest the tool only
// observes state (no mutation). Used by SuggestSideEffect when the user is
// classifying bindings for the first time — purely advisory; the user always
// confirms before the suggestion takes effect.
var readPrefixes = []string{
	"read_",
	"get_",
	"list_",
	"search_",
	"find_",
	"describe_",
	"inspect_",
	"fetch_",
	"query_",
	"show_",
}

// SuggestSideEffect returns a heuristic classification for the given tool name
// based on its prefix. Returns SideEffectRead for names matching any of
// readPrefixes (case-insensitive). Returns the empty SideEffect for anything
// else — callers should treat "no suggestion" as "leave it to the user."
//
// Write and external classifications are deliberately not heuristic-suggested:
// the cost of false-positives (auto-classifying a tool as write/external when
// it really shouldn't be) is much lower than auto-classifying as read.
func SuggestSideEffect(toolName string) SideEffect {
	lower := strings.ToLower(toolName)
	for _, p := range readPrefixes {
		if strings.HasPrefix(lower, p) {
			return SideEffectRead
		}
	}
	return ""
}

// ResolveSideEffect determines the effective SideEffect classification for a
// tool call: per-tool override wins, then binding default, then empty (meaning
// "unclassified" — the autonomy gate must treat this as denied until the user
// classifies). This helper does NOT consult SuggestSideEffect at gate time so
// that the gate is fully deterministic and driven by user-confirmed state.
func ResolveSideEffect(defaultSE SideEffect, overrides map[string]SideEffect, toolName string) SideEffect {
	if override, ok := overrides[toolName]; ok && override != "" {
		return override
	}
	return defaultSE
}

// isValidSideEffect reports whether se is one of the known SideEffect values.
// Empty is also accepted by callers as "unclassified" — this helper only rejects
// strings that look like SideEffect but aren't one of the defined constants.
func isValidSideEffect(se SideEffect) bool {
	switch se {
	case SideEffectRead, SideEffectWrite, SideEffectExternal:
		return true
	}
	return false
}

// UnclassifiedBindings returns the IDs of bindings enabled on the workspace
// that lack a DefaultSideEffect — the per-tool overrides alone are not enough
// here, since any tool that isn't listed in overrides would fall through to an
// empty default. Used by:
//   - the autonomy gate (defense-in-depth) to deny calls into unclassified bindings
//   - the mission-enable flow to surface the one-time classification prompt
//   - the run setup path to refuse to start a mission run while bindings are
//     still unclassified
//
// Disabled bindings are ignored; a user who has explicitly turned off a binding
// shouldn't be blocked from running a mission just because that binding was
// never classified.
func UnclassifiedBindings(ws *Workspace) (mcpIDs, skillIDs []string) {
	if ws == nil {
		return nil, nil
	}
	for _, b := range ws.MCPBindings {
		if !b.Enabled {
			continue
		}
		if !isValidSideEffect(b.DefaultSideEffect) {
			mcpIDs = append(mcpIDs, b.ID)
		}
	}
	for _, b := range ws.SkillBindings {
		if !b.Enabled {
			continue
		}
		if !isValidSideEffect(b.DefaultSideEffect) {
			skillIDs = append(skillIDs, b.ID)
		}
	}
	return mcpIDs, skillIDs
}

// MissionBindingsReady reports whether all enabled bindings on a workspace
// have a valid DefaultSideEffect. Mission runs must not start when this is
// false; callers should surface the "classify your bindings" prompt instead.
func MissionBindingsReady(ws *Workspace) bool {
	mcp, sk := UnclassifiedBindings(ws)
	return len(mcp) == 0 && len(sk) == 0
}

// IsAllowedUnderPolicy reports whether a tool call classified with `se` is
// permitted under the given autonomy policy. The empty SideEffect (unclassified)
// is always denied. Watch allows only read; Propose allows read and write.
// Higher policies (Act-with-approval, Autopilot) land in v1.5+.
func IsAllowedUnderPolicy(policy AutonomyPolicy, se SideEffect) bool {
	if se == "" {
		return false
	}
	switch policy {
	case AutonomyWatch:
		return se == SideEffectRead
	case AutonomyPropose:
		return se == SideEffectRead || se == SideEffectWrite
	}
	return false
}
