package agenthttp

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Dynamic destinations: opening a real record the user named.
//
// "Open my Launch workspace" is only answerable if a workspace called Launch
// actually exists. Everything here resolves against the live store first and
// builds a URL from the resulting ID — never from the user's text — so the guide
// cannot offer a link to a record that is not there, and a name cannot become
// part of a path (PRD FR-32/FR-49).
//
// When several records match, the guide says so rather than picking one. A guess
// that silently opens the wrong workspace is worse than an honest question.

// maxDynamicMatches bounds how many candidates are offered before the guide
// stops listing and asks the user to be more specific.
const maxDynamicMatches = 3

// isLinkableRecordID reports whether an ID is safe to place in a URL path.
//
// Deliberately strict: letters, digits, hyphen, and underscore only. Real
// workspace IDs are generated tokens that satisfy this easily, so the rule costs
// nothing in practice while removing an entire class of path-traversal question.
func isLinkableRecordID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// dynamicMatch is one resolved record.
type dynamicMatch struct {
	ID    string
	Name  string
	Href  string
	Label string
}

// resolveWorkspaceDestinations returns the workspaces whose names appear in the
// question, longest name first.
//
// Matching requires the *stored* name to appear in the question, not the other
// way round, so a one-letter workspace cannot match everything. The ID is
// path-escaped when building the href because a workspace name — and therefore
// sometimes its ID — is user-controlled text (FR-49).
func resolveWorkspaceDestinations(store workspace.Store, question string) []dynamicMatch {
	if store == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return nil
	}

	ids, err := store.List()
	if err != nil {
		return nil
	}

	var matches []dynamicMatch
	for _, wsID := range ids {
		ws, getErr := store.Get(wsID)
		if getErr != nil || ws == nil {
			continue
		}
		name := strings.TrimSpace(ws.Name)
		lower := strings.ToLower(name)
		// Three characters is the same floor the Home router uses; below that a
		// name matches so much text that it stops being evidence.
		if len(lower) < 3 || !strings.Contains(q, lower) {
			continue
		}
		// Browser destinations use the globally unique folder slug. Keep the
		// stable UUID in ID for actions and route only a plain slug token.
		if !isLinkableRecordID(ws.FolderSlug) {
			continue
		}
		matches = append(matches, dynamicMatch{
			ID:    ws.ID,
			Name:  name,
			Href:  "/workspaces/" + url.PathEscape(ws.FolderSlug),
			Label: "Open " + name,
		})
	}

	// Longest name first: "Launch Planning" beats "Launch" when both appear.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && len(matches[j].Name) > len(matches[j-1].Name); j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}

	// A longer match subsumes a shorter one contained within it, so "Launch
	// Planning" does not also offer "Launch" as a separate destination.
	var kept []dynamicMatch
	for _, m := range matches {
		redundant := false
		for _, k := range kept {
			if strings.Contains(strings.ToLower(k.Name), strings.ToLower(m.Name)) {
				redundant = true
				break
			}
		}
		if !redundant {
			kept = append(kept, m)
		}
	}
	if len(kept) > maxDynamicMatches {
		kept = kept[:maxDynamicMatches]
	}
	return kept
}

// dynamicWorkspaceResponse builds the answer for a question that named real
// workspaces. Returns ok=false when nothing resolved, so the caller falls
// through to normal topic matching.
func (h *GuideHandler) dynamicWorkspaceResponse(question, route string) (GuideResponse, bool) {
	matches := resolveWorkspaceDestinations(h.workspaceStore, question)
	if len(matches) == 0 {
		return GuideResponse{}, false
	}

	resp := GuideResponse{
		Status:    "answered",
		TopicKey:  "workspace",
		Location:  locationLabelFor(route),
		Suggested: summarizeGuideTopics(suggestedTopicsFor(route)),
	}

	if len(matches) == 1 {
		resp.Answer = fmt.Sprintf(
			"%q is one of your workspaces. Opening it scopes tasks, notes, files, and agents to that work.",
			matches[0].Name,
		)
	} else {
		// Ambiguity is reported, not resolved by guessing (FR-32).
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, fmt.Sprintf("%q", m.Name))
		}
		resp.Answer = "More than one of your workspaces matches that: " +
			strings.Join(names, ", ") + ". Pick the one you meant."
	}

	for _, m := range matches {
		resp.Actions = append(resp.Actions, GuideAction{
			Type:  GuideActionNavigate,
			Label: m.Label,
			Href:  m.Href,
		})
	}
	return resp, true
}
