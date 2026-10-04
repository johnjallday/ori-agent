package agenthttp

import (
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Remembering a fact from a conversation is a reviewed action, separate from
// saving a draft. A typed request ("remember that…", "remember this") never
// writes: it opens the same editable review the Remember… message action
// opens, and the user's final wording is saved through the existing reviewed
// Personal HQ memory API. Nothing here calls a model.

// PersonalAssistantMemoryReview opens the editable fact review in the panel.
// Text is the user's own statement when the request carried one, and empty
// when it did not — the review then asks instead of guessing.
type PersonalAssistantMemoryReview struct {
	Text        string `json:"text"`
	Destination string `json:"destination"`
	MaxBytes    int    `json:"max_bytes"`
}

const (
	personalAssistantMemoryDestination = "Personal HQ memory"
	memoryReviewPrompt                 = "Review this fact before it is remembered. Nothing is saved yet."
	memoryReviewAsk                    = "Tell me what to remember: write one fact in your own words below. Nothing is saved yet."
)

var (
	memoryStatementLead = regexp.MustCompile(`^remember that\s+\S`)
	memoryStatementBody = regexp.MustCompile(`(?i)\bremember that (.+)$`)
	memoryBareRequest   = regexp.MustCompile(`^remember (this|that|it)( fact)?( please| for me| too)?[.!]?$`)
)

// assistantMemoryRequest describes a typed request to remember something.
type assistantMemoryRequest struct {
	// memory: the prompt asks for something to be remembered.
	memory bool
	// statement: it states the fact itself ("remember that …"). A bare
	// "remember this" does not, and is never resolved by guessing.
	statement bool
}

// detectAssistantMemoryRequest recognizes an explicit request to remember. It
// is deliberately narrow: "remember to call Sam" is a reminder, not a fact, and
// ordinary conversation is never mined for facts.
func detectAssistantMemoryRequest(prompt string) assistantMemoryRequest {
	text := stripCompositionPolitePrefixes(normalizeRouteToken(prompt))
	switch {
	case memoryStatementLead.MatchString(text):
		return assistantMemoryRequest{memory: true, statement: true}
	case memoryBareRequest.MatchString(text):
		return assistantMemoryRequest{memory: true}
	}
	return assistantMemoryRequest{}
}

// isAssistantMemoryRequest reports whether the prompt asks to remember
// something. The router uses it: remembering is the assistant's own reviewed
// action, never a handoff to a specialist that matched a word in the fact.
func isAssistantMemoryRequest(prompt string) bool {
	return detectAssistantMemoryRequest(prompt).memory
}

// memoryStatementText returns the fact stated after "remember that", with
// whitespace collapsed and trailing punctuation trimmed. It does not truncate.
func memoryStatementText(prompt string) string {
	// Whitespace is collapsed first, so a tab or a line break after "that" is
	// the same request as a space.
	match := memoryStatementBody.FindStringSubmatch(strings.Join(strings.Fields(prompt), " "))
	if match == nil {
		return ""
	}
	return strings.Trim(match[1], " .")
}

// handleMemoryRequest answers a typed memory request without a model.
//
//   - A statement that maps to an allowlisted global preference keeps the
//     existing confirmation, unchanged.
//   - In a hired-assistant conversation, any other request opens the editable
//     review. Opening it writes nothing.
//   - Outside a conversation the existing confirmation behavior is preserved.
//
// handled is false when the prompt is not a memory request or the relationship
// cannot remember anything.
func (h *HomeAssistantAskHandler) handleMemoryRequest(prompt, intent string, identity *HomeAssistantIdentity, workContext *PersonalAssistantWorkContext, inConversation bool) (HomeAssistantAskResponse, bool) {
	request := detectAssistantMemoryRequest(prompt)
	if !request.memory || workContext == nil || !workContext.ReadyForWork() || workContext.StateVersion < 1 {
		return HomeAssistantAskResponse{}, false
	}
	var confirmation *HomeActionConfirmation
	if request.statement {
		confirmation = detectPersonalAssistantRememberRequest(prompt, workContext)
	}
	confirm := func() (HomeAssistantAskResponse, bool) {
		return HomeAssistantAskResponse{
			Response: confirmation.Summary, Intent: intent, Identity: identity,
			RequiresConfirmation: true, Confirmation: confirmation,
		}, true
	}
	if confirmation != nil && actionArgString(confirmation.Arguments, "destination") == "profile" {
		return confirm()
	}
	if !inConversation {
		if confirmation != nil {
			return confirm()
		}
		return HomeAssistantAskResponse{}, false
	}
	review := &PersonalAssistantMemoryReview{
		Destination: personalAssistantMemoryDestination, MaxBytes: workspace.MemoryEntryMaxLen,
	}
	response := memoryReviewAsk
	if request.statement {
		review.Text = memoryStatementText(prompt)
		response = memoryReviewPrompt
	}
	return HomeAssistantAskResponse{Response: response, Intent: intent, Identity: identity, MemoryReview: review}, true
}
