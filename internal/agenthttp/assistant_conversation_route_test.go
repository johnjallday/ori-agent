package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// newHiredRouteHandler builds a route handler for a hired assistant ("Nova",
// profile key "nova-profile") with the protected system assistant present, the
// way a real install with a system model looks.
func newHiredRouteHandler(t *testing.T, state string) (*HomeAssistantRouteHandler, store.Store) {
	t.Helper()
	st := newHomeRouteTestStore(t)
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensure system assistant: %v", err)
	}
	addHomeRouteTestAgent(t, st, "nova-profile", nil, "Personal assistant", []string{"assistant"}, nil)
	handler := NewHomeAssistantRouteHandler(st)
	workContext := activePersonalAssistantContext()
	workContext.State = state
	workContext.ConversationAgent = "nova-profile"
	handler.SetPersonalAssistantContextProvider(&stubPersonalAssistantContextProvider{context: workContext}, "user-a")
	return handler, st
}

func homePanelRouteContext() *HomeAssistantRouteContext {
	return &HomeAssistantRouteContext{Surface: "home", PagePath: "/", Origin: "personal_assistant_panel"}
}

func requireConversationRoute(t *testing.T, resp *HomeAssistantRouteResponse) {
	t.Helper()
	if resp.Intent != homeAssistantConversationIntent.Key || resp.RouteMode != homeAssistantRouteModeInline || resp.TargetSurface != "current" {
		t.Fatalf("want an inline assistant conversation, got intent=%q mode=%q surface=%q", resp.Intent, resp.RouteMode, resp.TargetSurface)
	}
	if resp.MatchedAgent != "" || resp.RequiresCreation {
		t.Fatalf("a conversation names no handler and creates no agent: matched=%q requires_creation=%v", resp.MatchedAgent, resp.RequiresCreation)
	}
	if resp.RoutingPolicy != homeAssistantPolicyAssistantOnly || resp.AssistantName != "Nova" {
		t.Fatalf("policy=%q assistant=%q", resp.RoutingPolicy, resp.AssistantName)
	}
}

// A1: drafting, refining, and translating all stay with the hired assistant.
func TestRoute_EverydayDraftingIsAnAssistantConversation(t *testing.T) {
	for _, state := range []string{"active", "paused"} {
		for _, prompt := range []string{
			"Write a short birthday greeting for my friend Mina.",
			"make it warmer",
			"give it to me in Korean",
			"Help me organize my thoughts for tomorrow",
		} {
			t.Run(state+"/"+prompt, func(t *testing.T) {
				handler, _ := newHiredRouteHandler(t, state)
				resp, err := handler.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
				if err != nil {
					t.Fatalf("RoutePrompt: %v", err)
				}
				requireConversationRoute(t, resp)
				if resp.PersonalAssistantState != state {
					t.Fatalf("state=%q", resp.PersonalAssistantState)
				}
			})
		}
	}
}

// With no system model the system assistant does not exist. Drafting must not
// turn into "create an agent" for a hired user.
func TestRoute_DraftingNeverRequiresCreatingAnAgent(t *testing.T) {
	st := newHomeRouteTestStore(t)
	handler := NewHomeAssistantRouteHandler(st)
	workContext := activePersonalAssistantContext()
	workContext.ConversationAgent = "nova-profile"
	handler.SetPersonalAssistantContextProvider(&stubPersonalAssistantContextProvider{context: workContext}, "user-a")

	resp, err := handler.RoutePrompt(context.Background(), "Write a short birthday greeting for my friend Mina.", homePanelRouteContext())
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	requireConversationRoute(t, resp)
}

// The hired profile is in the agent store. Matching it is not a specialist
// handoff: the conversation is how the user talks to it.
func TestRoute_HiredProfileMatchStaysAConversation(t *testing.T) {
	handler, st := newHiredRouteHandler(t, "active")
	setHomeRouteTestAgentRoutingProfile(t, st, "nova-profile", &types.AgentRoutingProfile{
		MatchPhrases:    []string{"birthday greeting"},
		ExampleRequests: []string{"write a birthday greeting for a friend"},
	})
	resp, err := handler.RoutePrompt(context.Background(), "Write a short birthday greeting for my friend Mina.", homePanelRouteContext())
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	requireConversationRoute(t, resp)
}

// A7: routes that already had an owner keep it.
func TestRoute_ConversationLeavesOtherRoutesAlone(t *testing.T) {
	cases := []struct {
		name      string
		prompt    string
		context   *HomeAssistantRouteContext
		setup     func(t *testing.T, st store.Store)
		intent    string
		routeMode string
		matched   string
	}{
		{
			name: "app activity detour", prompt: "how many tasks do I have pending?",
			context: homePanelRouteContext(), intent: "app_introspection", routeMode: homeAssistantRouteModeInline,
		},
		{
			name: "utility lookup", prompt: "what time is it in Seoul?",
			context: homePanelRouteContext(), intent: "utility_direct", routeMode: "utility_direct",
		},
		{
			name: "explicit project context", prompt: "Write a short birthday greeting for my friend Mina.",
			context: &HomeAssistantRouteContext{Surface: "home", PagePath: "/", WorkspaceID: "ws-project", Origin: "personal_assistant_panel"},
			intent:  "general_task", routeMode: "workspace_task",
		},
		{
			name: "workspace creation", prompt: "create a workspace called Launch",
			context: homePanelRouteContext(), intent: "workspace_create", routeMode: "workspace_task",
		},
		{
			name: "complex project build", prompt: "Build a full stack web app with authentication and a database",
			context: homePanelRouteContext(), intent: "general_task", routeMode: "workspace_task",
		},
		{
			name: "specialist request", prompt: "check my email inbox for unread messages",
			context: homePanelRouteContext(), intent: "email_check", routeMode: "specialist_handoff", matched: "Mail Pilot",
			setup: func(t *testing.T, st store.Store) {
				addHomeRouteTestAgent(t, st, "Mail Pilot", nil, "Reads and triages the email inbox", []string{"email", "inbox"}, nil)
			},
		},
		{
			name: "composition a specialist can take", prompt: "write an email to my landlord about the leak",
			context: homePanelRouteContext(), intent: "email_check", routeMode: "specialist_handoff", matched: "Mail Pilot",
			setup: func(t *testing.T, st store.Store) {
				addHomeRouteTestAgent(t, st, "Mail Pilot", nil, "Reads and triages the email inbox", []string{"email", "inbox"}, nil)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, st := newHiredRouteHandler(t, "active")
			if tc.setup != nil {
				tc.setup(t, st)
			}
			resp, err := handler.RoutePrompt(context.Background(), tc.prompt, tc.context)
			if err != nil {
				t.Fatalf("RoutePrompt: %v", err)
			}
			if resp.Intent != tc.intent || resp.RouteMode != tc.routeMode {
				t.Fatalf("intent=%q mode=%q; want %q / %q", resp.Intent, resp.RouteMode, tc.intent, tc.routeMode)
			}
			if tc.matched != "" && resp.MatchedAgent != tc.matched {
				t.Fatalf("matched=%q; want %q", resp.MatchedAgent, tc.matched)
			}
		})
	}
}

// A composition request never forces an agent or a connection into existence:
// with no email specialist, "write an email…" is drafted in the conversation.
func TestRoute_CompositionWithoutASpecialistIsDraftedInConversation(t *testing.T) {
	for _, prompt := range []string{
		"write an email to my landlord about the leak",
		"Please draft a reply to the meeting invite",
		"convert it to a formal tone",
		"translate this into Korean and add today's date",
	} {
		t.Run(prompt, func(t *testing.T) {
			handler, _ := newHiredRouteHandler(t, "active")
			resp, err := handler.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
			if err != nil {
				t.Fatalf("RoutePrompt: %v", err)
			}
			requireConversationRoute(t, resp)
		})
	}
}

// Without a hired assistant nothing changes: the legacy handoff stays.
func TestRoute_ConversationRequiresAHiredAssistant(t *testing.T) {
	st := newHomeRouteTestStore(t)
	if err := ensureSystemAssistantAgent(st); err != nil {
		t.Fatalf("ensure system assistant: %v", err)
	}
	handler := NewHomeAssistantRouteHandler(st)
	resp, err := handler.RoutePrompt(context.Background(), "Write a short birthday greeting for my friend Mina.", homePanelRouteContext())
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	if resp.Intent != "general_task" || resp.RouteMode != "specialist_handoff" || resp.MatchedAgent != systemAssistantAgentName {
		t.Fatalf("legacy route changed: %+v", resp)
	}
}

func TestIsCompositionRequest(t *testing.T) {
	yes := []string{
		"Write a toast", "please draft a note to Sam", "Can you rewrite this more politely?",
		"could you translate it into Korean", "make it warmer", "Make this shorter",
		"give it to me in Korean", "turn it into a haiku", "convert this to bullet points",
		"proofread the paragraph below", "help me write a thank-you card",
	}
	no := []string{
		"", "what time is it in Seoul?", "check my email inbox", "how many tasks do I have?",
		"open Safari", "writer's block is annoying", "I make it a rule to convert currency daily",
		"create a workspace called Launch",
	}
	for _, prompt := range yes {
		if !isCompositionRequest(prompt) {
			t.Errorf("isCompositionRequest(%q) = false; want true", prompt)
		}
	}
	for _, prompt := range no {
		if isCompositionRequest(prompt) {
			t.Errorf("isCompositionRequest(%q) = true; want false", prompt)
		}
	}
}
