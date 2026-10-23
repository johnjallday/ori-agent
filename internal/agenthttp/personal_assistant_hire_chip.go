package agenthttp

import (
	"context"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// On a path whose provider cannot call tools, the assistant cannot propose a
// hire. The host then offers a chip under the reply instead, for a request
// that asks for one in so many words: it opens the Agents page's Hire card
// with the request as the brief, and no role chosen. The model's prose is told
// to claim nothing (hireProposalManualInstructions).

// hireRequestLeads are the openings that ask for someone to take work on.
// They are a route hint beside isAssistantWorkspaceReviewRequest's, not a
// judgement of the request; the model makes that where it has the tool.
var hireRequestLeads = []string{
	"hire someone to", "hire an agent to", "hire a ", "have someone", "get an agent to", "get someone to", "delegate",
}

// hireProposalFolderInstructions is the rule for a turn with a recognized
// project folder attached: the folder's own card is the offer.
const hireProposalFolderInstructions = ` A folder Ori recognizes as a project is attached to this turn, and the folder's own setup card is the offer: do not propose a hire for it, and do not claim that a card, a hire, an agent or a workspace exists.`

// hireProposalWithheldKey marks a turn whose attached folder is the offer.
type hireProposalWithheldKey struct{}

func hireProposalWithheld(ctx context.Context) bool {
	withheld, _ := ctx.Value(hireProposalWithheldKey{}).(bool)
	return withheld
}

// folderOwnsTheOffer reports a folder turn whose folder Ori recognizes: a
// review already open, a project it hires for at once, or a setup review of
// a kind it knows (a software project, a plugin's capability, a blueprint's
// shape, a collection), even while that setup waits on a plugin. The
// request's hire tool is withheld on such a turn. A folder Ori knows nothing
// about, whose only option is a blank workspace, does not stand in the way,
// like a link does.
func (h *HomeAssistantAskHandler) folderOwnsTheOffer(ctx context.Context, turn *preparedFolderTurn) bool {
	if turn == nil || turn.observation == nil {
		return false
	}
	if turn.offerID != "" {
		return true
	}
	state := &PersonalAssistantFolderState{Observation: turn.observation, OfferID: turn.offerID, Historical: turn.ref.Historical}
	for _, option := range h.folderReviewOptions(ctx, turn.target, state) {
		if option.Presentation != nil && option.Presentation.State == "unavailable" {
			continue // Listed only to say it cannot be set up.
		}
		if option.Structure != nil || option.WorkspaceType != "Blank workspace" {
			return true
		}
	}
	return h.folderHireCandidate(ctx, turn.target, turn.observation).CandidateID != ""
}

// isHireRequestHint reports a request that asks, in so many words, for an
// agent to take work on. Like the workspace review hint it is a route hint
// for the host's own chip, never an authorization.
func isHireRequestHint(prompt string) bool {
	text := stripCompositionPolitePrefixes(strings.ToLower(strings.TrimSpace(prompt)))
	text = strings.TrimPrefix(text, "yes, ")
	for _, lead := range hireRequestLeads {
		if text == strings.TrimSpace(lead) {
			return false // "delegate" alone names no work.
		}
		if strings.HasPrefix(text, lead) {
			return true
		}
	}
	return false
}

// hireChipBrief is the request as the Agents page's card will show it: the
// user's own words, with the opening that asked for a hire taken off, one
// line, within the brief's bound.
func hireChipBrief(prompt string) string {
	text := strings.Join(strings.Fields(prompt), " ")
	lowered := strings.ToLower(text)
	for _, lead := range []string{
		"please ", "could you ", "can you ", "hire someone to ", "hire an agent to ", "get an agent to ", "get someone to ", "have someone ",
		"delegate ", "prepare a workspace review for ", "set up a workspace for ", "setup a workspace for ", "create a workspace for ",
	} {
		if lowered == strings.TrimSpace(lead) {
			return "" // The opening alone names no work.
		}
		if strings.HasPrefix(lowered, lead) {
			text, lowered = text[len(lead):], lowered[len(lead):]
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(text)
	text = string(unicode.ToUpper(first)) + text[size:]
	return oneLineHireText(text, personalassistant.WorkspaceGoalMaxRunes)
}

// hireChipHref is the Agents page with the Hire card open on that brief.
func hireChipHref(brief string) string {
	values := url.Values{"hire": {"1"}}
	if brief != "" {
		values.Set("brief", brief)
	}
	return "/agents?" + values.Encode()
}

// hireChipForRequest is the host's "Hire an agent for this" chip for a reply
// on a path without tools, or nil: the path has tools (the model proposes
// there), the request did not ask for a hire or a workspace, or the request
// has no words to make a brief of.
func (h *HomeAssistantAskHandler) hireChipForRequest(prompt string) *HomeAction {
	if !isAssistantWorkspaceReviewRequest(prompt) && !isHireRequestHint(prompt) {
		return nil
	}
	provider, _, err := h.resolveProvider()
	if err != nil || provider == nil || provider.Capabilities().SupportsTools {
		return nil
	}
	brief := hireChipBrief(prompt)
	if brief == "" {
		return nil
	}
	return &HomeAction{ID: "hire-for-this", Type: HomeActionNavigate, Label: "Hire an agent for this", Href: hireChipHref(brief)}
}
