package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// WorkspaceGoalMaxRunes bounds the goal a person saves from Goal Settings or
// the quick-edit Goal modal: room for a five-part charter. The hire card's
// one-line brief has its own, much smaller bound (personalassistant.BriefMaxRunes).
const WorkspaceGoalMaxRunes = 4000

// GoalCharterPart is one part of a goal charter: its heading name and the
// one-line meaning the editor hints, the review prompt and the docs share.
type GoalCharterPart struct {
	Name    string `json:"name"`
	Meaning string `json:"hint"`
}

// The five parts of a goal charter, in canonical order.
const (
	GoalPartOutcome    = "Outcome"
	GoalPartMaintain   = "Maintain"
	GoalPartBuild      = "Build"
	GoalPartWatch      = "Watch"
	GoalPartBoundaries = "Boundaries"
)

// GoalCharterParts is the one source of the five part names and meanings.
var GoalCharterParts = []GoalCharterPart{
	{Name: GoalPartOutcome, Meaning: "What this workspace exists to produce or keep true."},
	{Name: GoalPartMaintain, Meaning: "What the agent keeps in good order on its own initiative."},
	{Name: GoalPartBuild, Meaning: "What the agent should work towards that does not exist yet."},
	{Name: GoalPartWatch, Meaning: "What the agent looks for and reports when it changes."},
	{Name: GoalPartBoundaries, Meaning: "What the agent must not do or touch, and what needs the user's say."},
}

// GoalHintLine is the placeholder line the editor's "Use the charter shape"
// fill writes under a part's heading; the save path drops it when untouched.
func GoalHintLine(part GoalCharterPart) string {
	return "_" + part.Meaning + "_"
}

// GoalCharter is goal text read as a charter.
type GoalCharter struct {
	// IsCharter reports whether the text has at least one part heading.
	IsCharter bool
	// Present names the parts with text, in canonical order. Empty for a
	// plain goal.
	Present []string
	// Parts holds each part's text, keyed by canonical name. A plain goal's
	// whole text is its Outcome.
	Parts map[string]string
}

// ParseGoalCharter reads goal text as a charter. A part heading is a line of
// `#`, `##` or `###` followed by a part name, or a `**Part**` line, matched
// case-insensitively with an optional trailing colon; parts may come in any
// order and any subset. Text before the first heading belongs to Outcome. Any
// other line, an unknown heading included, stays text of the part it follows.
func ParseGoalCharter(text string) GoalCharter {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := make(map[string][]string, len(GoalCharterParts))
	current := GoalPartOutcome
	isCharter := false
	for _, line := range strings.Split(text, "\n") {
		if name, ok := goalPartHeading(line); ok {
			current = name
			isCharter = true
			continue
		}
		lines[current] = append(lines[current], line)
	}

	charter := GoalCharter{IsCharter: isCharter, Parts: make(map[string]string, len(lines))}
	for _, part := range GoalCharterParts {
		body := strings.TrimSpace(strings.Join(lines[part.Name], "\n"))
		if body == "" {
			continue
		}
		charter.Parts[part.Name] = body
		if isCharter {
			charter.Present = append(charter.Present, part.Name)
		}
	}
	return charter
}

// goalPartHeading reports the canonical part name a heading line names.
func goalPartHeading(line string) (string, bool) {
	label := strings.TrimSpace(line)
	markdownHeading := strings.HasPrefix(label, "#")
	if markdownHeading {
		hashes := len(label) - len(strings.TrimLeft(label, "#"))
		if hashes > 3 {
			return "", false
		}
		label = strings.TrimSpace(label[hashes:])
	}
	label = strings.TrimSpace(strings.TrimSuffix(label, ":"))
	if len(label) > 4 && strings.HasPrefix(label, "**") && strings.HasSuffix(label, "**") {
		label = strings.TrimSpace(label[2 : len(label)-2])
	} else if !markdownHeading {
		return "", false
	}
	label = strings.TrimSpace(strings.TrimSuffix(label, ":"))
	for _, part := range GoalCharterParts {
		if strings.EqualFold(label, part.Name) {
			return part.Name, true
		}
	}
	return "", false
}

// GoalOutcomeLine is the one line every one-line reader of a goal shows and
// injects: for a charter, the first line of its Outcome (or, without one, its
// first line of text); for a plain goal, the goal itself, exactly as before
// charters existed. Flattened to one line and bounded by
// WorkspaceGoalPromptLimit; empty when there is no goal.
func GoalOutcomeLine(mission string) string {
	charter := ParseGoalCharter(mission)
	if !charter.IsCharter {
		return sanitizeTaskPromptText(mission, WorkspaceGoalPromptLimit)
	}
	if line := firstGoalTextLine(charter.Parts[GoalPartOutcome]); line != "" {
		return sanitizeTaskPromptText(line, WorkspaceGoalPromptLimit)
	}
	for _, name := range charter.Present {
		if line := firstGoalTextLine(charter.Parts[name]); line != "" {
			return sanitizeTaskPromptText(line, WorkspaceGoalPromptLimit)
		}
	}
	return ""
}

func firstGoalTextLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !isGoalHintLine(line) {
			return line
		}
	}
	return ""
}

// GoalDigest is a short fingerprint of the goal text, so a run's suggested
// charter is offered only while the goal is still the one the run read.
// Empty when there is no goal.
func GoalDigest(mission string) string {
	mission = strings.TrimSpace(strings.ReplaceAll(mission, "\r\n", "\n"))
	if mission == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(mission))
	return hex.EncodeToString(sum[:8])
}

// StripGoalHints drops every line that is exactly one of the five untouched
// hint lines, so a half-filled charter never carries hint text into the goal.
func StripGoalHints(text string) string {
	if !strings.Contains(text, "_") {
		return text
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !isGoalHintLine(strings.TrimSpace(line)) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func isGoalHintLine(line string) bool {
	for _, part := range GoalCharterParts {
		if line == GoalHintLine(part) {
			return true
		}
	}
	return false
}
