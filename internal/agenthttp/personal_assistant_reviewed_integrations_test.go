package agenthttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// reviewedDiscoveryFixture adds the reviewed-integration reader to the research
// source. Its registry is made up, so any name in a reply, a prompt or a button
// can only have come from the reader.
type reviewedDiscoveryFixture struct {
	*researchSourceFixture
	integrations []assistantdiscovery.ReviewedIntegration
	// reads counts full reads (registry joined with the plugin store).
	reads int
}

func (f *reviewedDiscoveryFixture) ReviewedIntegrationsConfigured() bool { return true }

func (f *reviewedDiscoveryFixture) ReviewedIntegrations(context.Context, string) assistantdiscovery.ReviewedIntegrationsResult {
	f.reads++
	return assistantdiscovery.ReviewedIntegrationsResult{Status: "available", Integrations: f.integrations}
}

func (f *reviewedDiscoveryFixture) ReviewedIntegrationsNamedIn(text string) []string {
	var keys []string
	for _, integration := range f.integrations {
		if assistantdiscovery.MentionsName(text, integration.DisplayName) {
			keys = append(keys, integration.Key)
		}
	}
	return keys
}

const loomDescription = "Ori's Loom Studio integration is a local integration for Ori, not a loom driver"

func loomNotInstalled() assistantdiscovery.ReviewedIntegration {
	return assistantdiscovery.ReviewedIntegration{
		Key: "ori_loom", DisplayName: "Loom Studio", Description: loomDescription, MinimumReviewedVersion: "1.2.0",
		SetupQuest: &assistantdiscovery.SetupQuestRef{Source: assistantdiscovery.SetupQuestSourceHost, ID: "install_ori_loom"},
	}
}

func newReviewedFixture(t *testing.T, integrations ...assistantdiscovery.ReviewedIntegration) (*readerFixture, *reviewedDiscoveryFixture) {
	t.Helper()
	f, _, source := newResearchHostFixture(t)
	discovery := &reviewedDiscoveryFixture{researchSourceFixture: source, integrations: integrations}
	f.handler.Discovery = discovery
	return f, discovery
}

func askReviewed(f *readerFixture, prompt string) HomeAssistantAskResponse {
	return f.handler.Ask(context.Background(), HomeAssistantAskRequest{
		Prompt: prompt, Intent: homeAssistantConversationIntent.Key,
		Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: f.refs,
	})
}

func setupQuestActions(response HomeAssistantAskResponse) []HomeAction {
	var actions []HomeAction
	for _, action := range response.Actions {
		if action.Type == HomeActionOpenSetupQuest {
			actions = append(actions, action)
		}
	}
	return actions
}

func lastUserMessage(request llm.ChatRequest) string {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == llm.RoleUser {
			return request.Messages[i].Content
		}
	}
	return ""
}

func toolNames(request llm.ChatRequest) map[string]bool {
	names := map[string]bool{}
	for _, tool := range request.Tools {
		names[tool.Name] = true
	}
	return names
}

// A first mention that is not a setup request still leads to the guided setup:
// the model is handed the facts with the user's message, and the host attaches
// the button whatever the model does with them.
func TestReviewedIntegration_FirstMentionIsNamedBackWithItsGuidedSetup(t *testing.T) {
	f, discovery := newReviewedFixture(t, loomNotInstalled())
	f.provider.script = []llm.ChatResponse{{Content: "Ori has a reviewed Loom Studio integration; it is not installed yet. The button below opens its Guided Setup. After that I can set up a home for your songs."}}

	resp := askReviewed(f, "I make music in loom studio, set up a home that knows my songs")

	actions := setupQuestActions(resp)
	if len(actions) != 1 || len(resp.Actions) == 0 || resp.Actions[0].Type != HomeActionOpenSetupQuest {
		t.Fatalf("actions=%+v, want the guided setup first", resp.Actions)
	}
	action := actions[0]
	if action.Label != "Open Guided Setup: Loom Studio" || action.ID != "open-setup-quest-ori_loom" || action.Href != "" ||
		action.RequiresConfirmation || homeMutatingActionTypes[action.Type] {
		t.Fatalf("action=%+v", action)
	}
	if len(action.Arguments) != 3 || action.Arguments["quest_id"] != "install_ori_loom" ||
		action.Arguments["display_name"] != "Loom Studio" || action.Arguments["source"] != "host" {
		t.Fatalf("arguments=%+v", action.Arguments)
	}
	// The model's own sentence stands; the host adds nothing in front of it.
	if !strings.HasPrefix(resp.Response, "Ori has a reviewed Loom Studio integration; it is not installed yet.") {
		t.Fatalf("response=%q", resp.Response)
	}

	request := f.provider.requests[0]
	user := lastUserMessage(request)
	for _, want := range []string{
		"Reviewed integrations named in this message",
		"- Loom Studio: " + loomDescription + ". It is not installed yet. A button under your reply opens its Guided Setup.",
		"Do not propose a catalog lookup for this name",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("user message lacks %q:\n%s", want, user)
		}
	}
	if !strings.Contains(request.Messages[0].Content, reviewedIntegrationsTool+" BEFORE proposing any catalog lookup") {
		t.Error("the standing rule is missing from the system message")
	}
	if !toolNames(request)[reviewedIntegrationsTool] {
		t.Errorf("tools=%v, want the reviewed-integration reader offered", toolNames(request))
	}
	if discovery.reads != 1 {
		t.Fatalf("full reads = %d, want one for the named integration", discovery.reads)
	}
}

// A reply that never names the integration the user named (a model that
// ignored the facts, or Ori's own rendering of a workspace proposal) still
// opens with the host's sentence about it.
func TestReviewedIntegration_HostNamesItWhenTheReplyDoesNot(t *testing.T) {
	f, store, source := newResearchHostFixture(t)
	f.handler.Discovery = &reviewedDiscoveryFixture{researchSourceFixture: source, integrations: []assistantdiscovery.ReviewedIntegration{loomNotInstalled()}}
	f.provider.script = []llm.ChatResponse{{Content: "I can set up a music production home for you."}}

	resp := askReviewed(f, "I make music in Loom Studio")

	want := "Ori has a reviewed Loom Studio integration. " + loomDescription + ". It is not installed yet. The button below opens its Guided Setup.\n\nI can set up a music production home for you."
	if resp.Response != want {
		t.Fatalf("response = %q\nwant       %q", resp.Response, want)
	}
	if len(setupQuestActions(resp)) != 1 {
		t.Fatalf("actions=%+v", resp.Actions)
	}
	// The saved conversation reads the same as the reply: a reload must not
	// lose the sentence that names the integration.
	messages := store.sessions["canonical"].messages
	if saved := messages[len(messages)-1]; saved.Role != "assistant" || saved.Content != want {
		t.Fatalf("saved reply = %q (%s), want the reply as shown", saved.Content, saved.Role)
	}
}

// The integration comes first. A reply that names it only further down, as a
// workspace proposal does when the model writes it into the proposal's own
// text, still opens with the host's sentence.
func TestReviewedIntegration_HostLeadsWhenTheReplyNamesItOnlyLater(t *testing.T) {
	f, _ := newReviewedFixture(t, loomNotInstalled())
	later := "Workspace proposal — not created\n\nMusic Production Home\nOri has a reviewed Loom Studio integration; this home keeps your songs."
	f.provider.script = []llm.ChatResponse{{Content: later}}

	resp := askReviewed(f, "I make music in Loom Studio, set up a home for my songs")

	if !strings.HasPrefix(resp.Response, "Ori has a reviewed Loom Studio integration. "+loomDescription+". It is not installed yet. The button below opens its Guided Setup.\n\n") ||
		!strings.HasSuffix(resp.Response, later) {
		t.Fatalf("response = %q", resp.Response)
	}

	// A reply that opens with it, after blank lines or not, is left alone.
	f, _ = newReviewedFixture(t, loomNotInstalled())
	opening := "\n\nLoom Studio has a reviewed integration in Ori.\n\nIt is not installed yet."
	f.provider.script = []llm.ChatResponse{{Content: opening}}
	if resp := askReviewed(f, "I make music in Loom Studio"); strings.Contains(resp.Response, "The button below opens") {
		t.Fatalf("the host repeated a reply that already led with the integration: %q", resp.Response)
	}
}

// "Set up", "install" and "connect" read as execution to the router, which
// hands them to a specialist or the workspace's own chat. While an integration's
// guided setup is open, naming it keeps the request with the assistant, from
// Home and from a workspace page alike.
func TestRoute_AReviewedIntegrationSetupRequestStaysWithTheAssistant(t *testing.T) {
	handler, _ := newHiredRouteHandler(t, "active")
	named := func(_ context.Context, prompt string) bool {
		return assistantdiscovery.MentionsName(prompt, "Loom Studio")
	}
	contexts := map[string]*HomeAssistantRouteContext{
		"home":      homePanelRouteContext(),
		"workspace": {Surface: "workspace", PagePath: "/workspaces/album", WorkspaceID: "ws-project", Origin: "personal_assistant_panel"},
	}
	for where, routeContext := range contexts {
		for _, prompt := range []string{"can you set up the Loom Studio integration?", "install Loom Studio", "connect Loom Studio"} {
			handler.ReviewedIntegrationSetup = nil
			before, err := handler.RoutePrompt(context.Background(), prompt, routeContext)
			if err != nil {
				t.Fatal(err)
			}
			if before.Intent == homeAssistantConversationIntent.Key {
				t.Fatalf("%s %q: already a conversation without the check; the test proves nothing", where, prompt)
			}
			handler.ReviewedIntegrationSetup = named
			after, err := handler.RoutePrompt(context.Background(), prompt, routeContext)
			if err != nil {
				t.Fatal(err)
			}
			requireConversationRoute(t, after)

			// A message that names nothing keeps the owner it had.
			other := strings.ReplaceAll(prompt, "Loom Studio", "label printer")
			handler.ReviewedIntegrationSetup = nil
			plain, err := handler.RoutePrompt(context.Background(), other, routeContext)
			if err != nil {
				t.Fatal(err)
			}
			handler.ReviewedIntegrationSetup = named
			same, err := handler.RoutePrompt(context.Background(), other, routeContext)
			if err != nil || same.Intent != plain.Intent || same.RouteMode != plain.RouteMode {
				t.Fatalf("%s %q: an unrelated request changed owner: %+v vs %+v (%v)", where, other, same, plain, err)
			}
		}
	}

	// The check is the panel's only.
	outsidePanel := &HomeAssistantRouteContext{Surface: "home", PagePath: "/"}
	handler.ReviewedIntegrationSetup = nil
	baseline, err := handler.RoutePrompt(context.Background(), "install Loom Studio", outsidePanel)
	if err != nil {
		t.Fatal(err)
	}
	handler.ReviewedIntegrationSetup = named
	outside, err := handler.RoutePrompt(context.Background(), "install Loom Studio", outsidePanel)
	if err != nil || outside.Intent != baseline.Intent || outside.RouteMode != baseline.RouteMode {
		t.Fatalf("outside the panel: %+v vs %+v (%v)", outside, baseline, err)
	}
}

// The router's question is answered from the same read the turn uses: named,
// and with a guided setup still to open.
func TestReviewedIntegrationSetupOpen(t *testing.T) {
	f, discovery := newReviewedFixture(t, loomNotInstalled())
	ctx := context.Background()
	if !f.handler.ReviewedIntegrationSetupOpen(ctx, "can you set up the Loom Studio integration?") {
		t.Fatal("a named integration with setup open was not reported")
	}
	if f.handler.ReviewedIntegrationSetupOpen(ctx, "can you set up my label printer?") {
		t.Fatal("a message that names nothing was reported")
	}
	if discovery.reads != 1 {
		t.Fatalf("full reads = %d, want one (only for the message that names an integration)", discovery.reads)
	}

	complete := loomNotInstalled()
	complete.Installed, complete.Enabled, complete.SetupComplete, complete.SetupQuest = true, true, true, nil
	f, _ = newReviewedFixture(t, complete)
	if f.handler.ReviewedIntegrationSetupOpen(ctx, "start the Loom Studio mix task") {
		t.Fatal("a fully set-up integration still diverted a request")
	}

	bare, _, _ := newResearchHostFixture(t)
	if bare.handler.ReviewedIntegrationSetupOpen(ctx, "install Loom Studio") {
		t.Fatal("a host without the reader reported an integration")
	}
}

// Without the name in the message, the model can still read the list; a reply
// that then names an entry carries its button.
func TestReviewedIntegration_ToolReadOffersTheEntryTheReplyNames(t *testing.T) {
	f, discovery := newReviewedFixture(t, loomNotInstalled())
	f.provider.script = []llm.ChatResponse{
		toolCall(reviewedIntegrationsTool, map[string]any{}),
		{Content: "Ori has a reviewed Loom Studio integration for that. The button below opens its Guided Setup."},
	}

	resp := askReviewed(f, "can you connect my recording software?")

	if strings.Contains(lastUserMessage(f.provider.requests[0]), "Reviewed integrations named in this message") {
		t.Fatal("the facts block was added for a message that names nothing")
	}
	toolText := ""
	for _, message := range f.provider.requests[1].Messages {
		if message.Role == llm.RoleTool {
			toolText += message.Content
		}
	}
	for _, want := range []string{`"status":"available"`, `"display_name":"Loom Studio"`, `"installed":false`, `"setup_quest":{"source":"host","id":"install_ori_loom"}`} {
		if !strings.Contains(toolText, want) {
			t.Errorf("tool result lacks %s: %s", want, toolText)
		}
	}
	if actions := setupQuestActions(resp); len(actions) != 1 || actions[0].Arguments["quest_id"] != "install_ori_loom" {
		t.Fatalf("actions=%+v", resp.Actions)
	}
	if discovery.reads != 1 || resp.Conversation == nil || !resp.Conversation.Stored {
		t.Fatalf("reads=%d conversation=%+v", discovery.reads, resp.Conversation)
	}

	// Read, but the reply names no entry: nothing is offered.
	f, _ = newReviewedFixture(t, loomNotInstalled())
	f.provider.script = []llm.ChatResponse{toolCall(reviewedIntegrationsTool, map[string]any{}), {Content: "Ori has no reviewed integration for that."}}
	if resp := askReviewed(f, "can you connect my label printer?"); len(setupQuestActions(resp)) != 0 {
		t.Fatalf("actions=%+v", resp.Actions)
	}
}

// A message that names no reviewed integration is the turn it always was: the
// catalog proposal still becomes a review, and the plugin store is not read.
func TestReviewedIntegration_NoMatchLeavesTheCatalogPathAlone(t *testing.T) {
	f, discovery := newReviewedFixture(t, loomNotInstalled())
	f.provider.script = []llm.ChatResponse{
		toolCall("assistant_propose_research_lookup", map[string]any{"operation": "skills_catalog", "query": "Telegram community management"}),
		{Content: "Let's compare existing tools before building."},
	}

	resp := askReviewed(f, "Is there a Telegram skill? I am looming on a deadline.")

	if resp.ResearchReview == nil || resp.ResearchReview.Lookup.Query != "Telegram community management" {
		t.Fatalf("review=%+v", resp.ResearchReview)
	}
	if len(setupQuestActions(resp)) != 0 || resp.Response != "Let's compare existing tools before building." {
		t.Fatalf("response=%q actions=%+v", resp.Response, resp.Actions)
	}
	if discovery.reads != 0 {
		t.Fatalf("the plugin store was read %d times for a message that names nothing", discovery.reads)
	}
}

// Asked to set the integration up, a model that reaches for the skills catalog
// is told the lookup is not needed, and no review is offered for it.
func TestReviewedIntegration_CatalogLookupForItsNameIsReplacedByGuidedSetup(t *testing.T) {
	f, _ := newReviewedFixture(t, loomNotInstalled())
	f.provider.script = []llm.ChatResponse{
		toolCall("assistant_propose_research_lookup", map[string]any{"operation": "skills_catalog", "query": "Loom Studio"}),
		{Content: "Ori has a reviewed Loom Studio integration. The button below opens its Guided Setup."},
	}

	resp := askReviewed(f, "can you set up the Loom Studio integration?")

	if resp.ResearchReview != nil {
		t.Fatalf("a catalog review was offered for a reviewed integration: %+v", resp.ResearchReview)
	}
	toolText := ""
	for _, message := range f.provider.requests[1].Messages {
		if message.Role == llm.RoleTool {
			toolText += message.Content
		}
	}
	if !strings.Contains(toolText, `"status":"not_needed"`) || !strings.Contains(toolText, `"sent":false`) {
		t.Fatalf("tool result = %s", toolText)
	}
	if len(setupQuestActions(resp)) != 1 {
		t.Fatalf("actions=%+v", resp.Actions)
	}

	// A lookup about something else in the same turn is still a review.
	f, _ = newReviewedFixture(t, loomNotInstalled())
	f.provider.script = []llm.ChatResponse{
		toolCall("assistant_propose_research_lookup", map[string]any{"operation": "skills_catalog", "query": "podcast editing"}),
		{Content: "Ori has a reviewed Loom Studio integration. I can also look for podcast editing skills."},
	}
	if resp := askReviewed(f, "set up Loom Studio and find a podcast editing skill"); resp.ResearchReview == nil {
		t.Fatal("an unrelated lookup lost its review")
	}
}

func TestReviewedIntegration_InstalledContinuesInThePluginsOwnSetup(t *testing.T) {
	installed := loomNotInstalled()
	installed.Installed, installed.Enabled, installed.InstalledVersion = true, true, "1.4.0"
	installed.SetupQuest = &assistantdiscovery.SetupQuestRef{Source: assistantdiscovery.SetupQuestSourcePlugin, PluginID: "loom-plugin", ID: "first-song"}
	f, _ := newReviewedFixture(t, installed)
	f.provider.script = []llm.ChatResponse{{Content: "Loom Studio is installed; continue its setup with the button below."}}

	resp := askReviewed(f, "is Loom Studio set up?")

	actions := setupQuestActions(resp)
	if len(actions) != 1 || actions[0].Label != "Continue setup: Loom Studio" {
		t.Fatalf("actions=%+v", resp.Actions)
	}
	args := actions[0].Arguments
	if len(args) != 4 || args["quest_id"] != "first-song" || args["source"] != "plugin" || args["plugin_id"] != "loom-plugin" || args["display_name"] != "Loom Studio" {
		t.Fatalf("arguments=%+v", args)
	}
	if !strings.Contains(lastUserMessage(f.provider.requests[0]), "It is installed and enabled; its guided setup is not finished.") {
		t.Fatalf("user message: %s", lastUserMessage(f.provider.requests[0]))
	}
}

// Finished setup: the model is told so, there is no button, and a catalog
// lookup about the integration is an ordinary lookup again.
func TestReviewedIntegration_CompleteSetupOffersNothingToOpen(t *testing.T) {
	complete := loomNotInstalled()
	complete.Installed, complete.Enabled, complete.SetupComplete, complete.SetupQuest = true, true, true, nil
	f, _ := newReviewedFixture(t, complete)
	f.provider.script = []llm.ChatResponse{
		toolCall("assistant_propose_research_lookup", map[string]any{"operation": "skills_catalog", "query": "Loom Studio mixing"}),
		{Content: "Loom Studio is installed and set up. I can look for mixing skills."},
	}

	resp := askReviewed(f, "find me a skill for Loom Studio mixing")

	if len(setupQuestActions(resp)) != 0 {
		t.Fatalf("a finished setup still offers a button: %+v", resp.Actions)
	}
	if resp.ResearchReview == nil {
		t.Fatal("a lookup about a fully set-up integration was turned away")
	}
	user := lastUserMessage(f.provider.requests[0])
	if !strings.Contains(user, "It is installed, enabled and set up.") || strings.Contains(user, "A button under your reply") {
		t.Fatalf("user message: %s", user)
	}
}

// No answer, no button: a turn the model never answered says only that.
func TestReviewedIntegration_ModelUnavailableAddsNothing(t *testing.T) {
	f, _ := newReviewedFixture(t, loomNotInstalled())
	f.handler.SystemModel = nil

	resp := askReviewed(f, "I make music in Loom Studio")

	if !resp.ModelUnavailable || len(setupQuestActions(resp)) != 0 || strings.Contains(resp.Response, "Loom Studio") {
		t.Fatalf("response=%+v", resp)
	}
}

// A host without the reader offers no tool and changes nothing.
func TestReviewedIntegration_AbsentReaderIsInvisible(t *testing.T) {
	f, _, _ := newResearchHostFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Sure."}}

	resp := askReviewed(f, "I make music in Loom Studio")

	if toolNames(f.provider.requests[0])[reviewedIntegrationsTool] || len(resp.Actions) != 0 || resp.Response != "Sure." {
		t.Fatalf("tools=%v response=%+v", toolNames(f.provider.requests[0]), resp)
	}
	if strings.Contains(f.provider.requests[0].Messages[0].Content, reviewedIntegrationsTool) {
		t.Fatal("the rule names a reader this host does not have")
	}
}

func TestOpenSetupQuestAction_NeedsAQuest(t *testing.T) {
	bare := loomNotInstalled()
	bare.SetupQuest = nil
	if _, ok := openSetupQuestAction(bare); ok {
		t.Fatal("an integration with nothing to open got a button")
	}
	blank := loomNotInstalled()
	blank.SetupQuest = &assistantdiscovery.SetupQuestRef{Source: "host", ID: "  "}
	if _, ok := openSetupQuestAction(blank); ok {
		t.Fatal("a blank quest id got a button")
	}
}
