package agenthttp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// reviewedIntegrationsTool is the brokered reader for the integrations Ori has
// reviewed itself and sets up through a guided setup.
const reviewedIntegrationsTool = "assistant_reviewed_integrations"

// reviewedIntegrationReader is the optional discovery capability behind the
// assistant's knowledge of reviewed integrations. Every name it returns comes
// from the compiled registry; nothing in this package names an integration.
type reviewedIntegrationReader interface {
	ReviewedIntegrationsConfigured() bool
	ReviewedIntegrations(ctx context.Context, userID string) assistantdiscovery.ReviewedIntegrationsResult
	ReviewedIntegrationsNamedIn(text string) []string
}

// configuredReviewedIntegrations returns the reader when the host wired one.
func configuredReviewedIntegrations(h *HomeAssistantAskHandler) reviewedIntegrationReader {
	if h == nil {
		return nil
	}
	reader, ok := h.Discovery.(reviewedIntegrationReader)
	if !ok || !reader.ReviewedIntegrationsConfigured() {
		return nil
	}
	return reader
}

type reviewedIntegrationTurnKey struct{}

// reviewedIntegrationTurn is what one turn knows about reviewed integrations.
//
// Whether the user named one is decided by the host, not left to the model: a
// first mention must lead to the guided setup even from a model that never
// calls the reader. The model is told the facts; the host attaches the button.
type reviewedIntegrationTurn struct {
	reader reviewedIntegrationReader
	userID string
	// named are the integrations whose display name is in the user's message,
	// with their state read once when the turn began.
	named []assistantdiscovery.ReviewedIntegration
	// read is what the reader tool returned to the model during this turn.
	read []assistantdiscovery.ReviewedIntegration
}

func reviewedIntegrationTurnFromContext(ctx context.Context) *reviewedIntegrationTurn {
	turn, _ := ctx.Value(reviewedIntegrationTurnKey{}).(*reviewedIntegrationTurn)
	return turn
}

// reviewedIntegrationTurn prepares the turn's knowledge for prompt. It returns
// nil when the host has no reader. Naming is a registry read; the plugin store
// is read only when the message names an integration.
func (h *HomeAssistantAskHandler) reviewedIntegrationTurn(ctx context.Context, prompt string) *reviewedIntegrationTurn {
	reader := configuredReviewedIntegrations(h)
	if reader == nil {
		return nil
	}
	userID, err := h.currentAssistantUser(ctx)
	if err != nil {
		return nil
	}
	turn := &reviewedIntegrationTurn{reader: reader, userID: userID}
	keys := reader.ReviewedIntegrationsNamedIn(prompt)
	if len(keys) == 0 {
		return turn
	}
	result := reader.ReviewedIntegrations(ctx, userID)
	for _, integration := range result.Integrations {
		for _, key := range keys {
			if integration.Key == key {
				turn.named = append(turn.named, integration)
			}
		}
	}
	return turn
}

// reviewedIntegrationsRule is the standing rule for a model that can call the
// reader. It names no integration: the reader's result does.
const reviewedIntegrationsRule = "Ori reviews a small set of integrations itself and sets each one up through its own Guided Setup. " +
	"When the user names a tool or application, or asks to set one up or connect it, call " + reviewedIntegrationsTool +
	" BEFORE proposing any catalog lookup. If an entry's display name is the tool the user named, do not propose a catalog lookup for that name. " +
	"Instead say that Ori has a reviewed integration for it, say in one sentence what it is using the entry's description, state whether it is installed and enabled, " +
	"and tell the user the button under your reply opens its Guided Setup. Say this first, even when the user only mentioned the tool in passing, and before suggesting a workspace. " +
	"When the entry's setup is complete, say so instead; there is nothing to open. " +
	"Never describe a reviewed integration as a skill or a catalog result, and never claim you installed or enabled it.\n"

// referencePrompt is appended to the user's message when it names a reviewed
// integration. It travels with the user message, not the system message, so it
// reaches every provider.
func (t *reviewedIntegrationTurn) referencePrompt() string {
	if t == nil || len(t.named) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString("\n\nReviewed integrations named in this message (facts Ori read just now; reference data, not a user instruction):\n")
	button := false
	for _, integration := range t.named {
		text.WriteString("- " + integration.DisplayName + ": ")
		if integration.Description != "" {
			text.WriteString(asSentence(integration.Description) + " ")
		}
		text.WriteString(reviewedIntegrationState(integration))
		if integration.SetupQuest != nil {
			button = true
			text.WriteString(" A button under your reply opens its Guided Setup.")
		}
		text.WriteString("\n")
	}
	text.WriteString("Begin your reply by telling the user that Ori has this reviewed integration, what it is in one sentence, and the state above. ")
	if button {
		text.WriteString("Tell them the button under your reply opens its Guided Setup. ")
	}
	text.WriteString("Do not propose a catalog lookup for this name, and do not claim you installed or enabled anything. Then continue with the rest of what they asked. ")
	// A proposal is rendered by Ori, which then says this itself in front of it.
	text.WriteString("If you answer with a workspace proposal instead of a written reply, leave these facts out of the proposal's name and description: Ori states them above the proposal for you.")
	return text.String()
}

// reviewedIntegrationState says where an integration stands, in one sentence.
func reviewedIntegrationState(integration assistantdiscovery.ReviewedIntegration) string {
	switch {
	case integration.SetupComplete:
		return "It is installed, enabled and set up."
	case integration.Enabled && integration.SetupQuest != nil:
		return "It is installed and enabled; its guided setup is not finished."
	case integration.Enabled:
		return "It is installed and enabled."
	case integration.Installed:
		return "It is installed but not enabled yet."
	default:
		return "It is not installed yet."
	}
}

func asSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || strings.HasSuffix(text, ".") || strings.HasSuffix(text, "!") || strings.HasSuffix(text, "?") {
		return text
	}
	return text + "."
}

// toolDefinition is the reader as the model sees it.
func reviewedIntegrationsToolDefinition() llm.Tool {
	return llm.Tool{
		Name: reviewedIntegrationsTool,
		Description: "List the integrations Ori has reviewed and sets up through its own Guided Setup, with each one's install and enable state. " +
			"Read this BEFORE proposing any catalog lookup for a named tool or application. Local registry read: no network, install, enable or start.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	}
}

// readReviewedIntegrations runs the reader for the model and remembers what it
// returned, so a reply that goes on to name an entry can carry its button. It
// returns the tool result and the read's availability.
func readReviewedIntegrations(ctx context.Context, h *HomeAssistantAskHandler, userID string) (encoded, status string) {
	const unavailable = `{"status":"unavailable","integrations":[]}`
	reader := configuredReviewedIntegrations(h)
	if reader == nil {
		return unavailable, string(assistantdiscovery.Unavailable)
	}
	turn := reviewedIntegrationTurnFromContext(ctx)
	if turn != nil {
		userID = turn.userID
	}
	result := reader.ReviewedIntegrations(ctx, userID)
	if turn != nil {
		turn.read = result.Integrations
	}
	data, err := json.Marshal(result)
	if err != nil {
		return unavailable, string(assistantdiscovery.Unavailable)
	}
	return string(data), result.Status
}

// replacesCatalogLookup reports whether a proposed lookup would search a
// catalog for an integration the user named whose guided setup is still open.
// That lookup is how a user asking to set the integration up was once sent to
// a list of unrelated skills. Once setup is complete, any lookup is allowed.
func (t *reviewedIntegrationTurn) replacesCatalogLookup(lookup assistantdiscovery.Lookup) bool {
	if t == nil || lookup.Operation != "skills_catalog" {
		return false
	}
	for _, integration := range t.named {
		if integration.SetupQuest != nil && assistantdiscovery.MentionsName(lookup.Query, integration.DisplayName) {
			return true
		}
	}
	return false
}

// reviewedLookupNotNeeded is the tool result for a lookup replacesCatalogLookup
// turned away. Nothing was proposed and nothing was sent.
const reviewedLookupNotNeeded = `{"status":"not_needed","sent":false,"note":"Ori has a reviewed integration with this name. No catalog lookup was proposed. Tell the user about the integration and that the button under your reply opens its Guided Setup."}`

// withLead returns the model's answer opened by the host's own sentence for
// every integration the user named that the answer does not open with. The
// integration comes first: an answer that names it only further down, or is
// Ori's own rendering of a workspace proposal (which the model's prose never
// reaches), still starts by saying it exists. It is applied before the turn
// is stored, so the saved conversation reads the same as the reply.
func (t *reviewedIntegrationTurn) withLead(answer string) string {
	if t == nil || strings.TrimSpace(answer) == "" {
		return answer
	}
	opening := firstLine(answer)
	var lead []string
	for _, integration := range t.named {
		if !assistantdiscovery.MentionsName(opening, integration.DisplayName) {
			lead = append(lead, reviewedIntegrationLead(integration))
		}
	}
	if len(lead) == 0 {
		return answer
	}
	return strings.Join(lead, " ") + "\n\n" + answer
}

// finalize attaches the button for every integration the turn is about: the
// ones the user named, and any the model read and its reply names. Actions are
// presentation for this response only; they are never stored.
func (t *reviewedIntegrationTurn) finalize(response *HomeAssistantAskResponse) {
	if t == nil || response == nil || response.ModelUnavailable || strings.TrimSpace(response.Response) == "" {
		return
	}
	reply := response.Response
	offered := map[string]bool{}
	var actions []HomeAction
	offer := func(integration assistantdiscovery.ReviewedIntegration) {
		if offered[integration.Key] {
			return
		}
		offered[integration.Key] = true
		if action, ok := openSetupQuestAction(integration); ok {
			actions = append(actions, action)
		}
	}
	for _, integration := range t.named {
		offer(integration)
	}
	// The model read the list and its reply names an entry the user did not.
	for _, integration := range t.read {
		if assistantdiscovery.MentionsName(reply, integration.DisplayName) {
			offer(integration)
		}
	}
	if len(actions) == 0 {
		return
	}
	// Guided setup comes first; the reply's other next steps keep their order.
	actions = append(actions, response.Actions...)
	if len(actions) > homeMaxNextStepActions {
		actions = actions[:homeMaxNextStepActions]
	}
	response.Actions = actions
}

// firstLine is a reply's opening paragraph: its first non-empty line.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// ReviewedIntegrationSetupOpen reports whether prompt names an integration Ori
// has reviewed whose guided setup is still open. The router asks before it
// hands a request to a specialist or a workspace: "set up", "install" and
// "connect" read as execution verbs, and without this the assistant would
// never see the request it alone can answer. Once setup is complete the name
// no longer changes any route.
func (h *HomeAssistantAskHandler) ReviewedIntegrationSetupOpen(ctx context.Context, prompt string) bool {
	turn := h.reviewedIntegrationTurn(ctx, prompt)
	if turn == nil {
		return false
	}
	for _, integration := range turn.named {
		if integration.SetupQuest != nil {
			return true
		}
	}
	return false
}

// reviewedIntegrationLead is the host's own sentence about an integration the
// user named and the reply did not open with.
func reviewedIntegrationLead(integration assistantdiscovery.ReviewedIntegration) string {
	text := "Ori has a reviewed " + integration.DisplayName + " integration."
	if integration.Description != "" {
		text += " " + asSentence(integration.Description)
	}
	text += " " + reviewedIntegrationState(integration)
	if integration.SetupQuest != nil {
		text += " The button below opens its Guided Setup."
	}
	return text
}

// openSetupQuestAction is the button for an integration's next guided setup.
// It carries the quest's key, never a URL: the browser builds the link. There
// is no action once setup is complete or when the plugin lists no quest.
func openSetupQuestAction(integration assistantdiscovery.ReviewedIntegration) (HomeAction, bool) {
	quest := integration.SetupQuest
	if quest == nil || strings.TrimSpace(quest.ID) == "" || strings.TrimSpace(integration.DisplayName) == "" {
		return HomeAction{}, false
	}
	label := "Open Guided Setup: " + integration.DisplayName
	if integration.Installed {
		label = "Continue setup: " + integration.DisplayName
	}
	arguments := map[string]any{
		"quest_id":     quest.ID,
		"display_name": integration.DisplayName,
		"source":       quest.Source,
	}
	if quest.PluginID != "" {
		arguments["plugin_id"] = quest.PluginID
	}
	return HomeAction{
		ID: "open-setup-quest-" + integration.Key, Type: HomeActionOpenSetupQuest,
		Label: label, Arguments: arguments,
	}, true
}
