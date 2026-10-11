package agenthttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// Fictional conversation/action pairs: words describing possible work are not
// instructions to set it up. Each row retains a genuine current action owner.
func TestAssistantDiscoveryRoute_DiscussionAndActionPairs(t *testing.T) {
	cases := []struct {
		name, discussion, action, actionMode string
	}{
		{"saas brainstorming", "I have access to musicians. Let's brainstorm whether to build a SaaS platform for them.", "Build a SaaS platform for musicians with authentication and a database", "workspace_task"},
		{"access is not email triage", "How could access to musicians help us? Would email or a community app make sense?", "Check my email inbox for unread messages", "specialist_handoff"},
		{"correction is not coaching", "No, I don't want to develop anyone's talent. I want to discuss community ideas.", "Create an agent named Talent Researcher", "specialist_handoff"},
		{"ori possibility", "Could Ori help people build communities?", "Build a community platform from scratch", "workspace_task"},
		{"skill inquiry", "Is there a Telegram skill?", "Set up a Telegram integration", "specialist_handoff"},
		{"hypothetical build", "Should we build a community platform?", "Create a workspace called Community", "workspace_task"},
		{"negated setup", "Don't create a workspace called Community; let's compare options first.", "Create a workspace called Community", "workspace_task"},
		{"composition mentions implementation", "Write a short proposal explaining how we could implement a community platform", "Build a community platform with an API", "workspace_task"},
		{"quoted instruction", `What do you think about the instruction "create a workspace called Community"?`, "Create a workspace called Community", "workspace_task"},
		{"uncertain referent", "Yes, use that", "Create a workspace called Community", "workspace_task"},
		{"uncertain build target", "Build that platform", "Build a community platform with an API", "workspace_task"},
		{"prose mentions implementation", "Please explain how to build a platform with email notifications", "Check my email inbox", "specialist_handoff"},
		{"compose list", "Make a list of options for a community platform", "Build a community platform with an API", "workspace_task"},
	}
	for _, state := range []string{"active", "paused"} {
		for _, surface := range []string{"home", "workspace"} {
			for _, tc := range cases {
				t.Run(state+"/"+surface+"/"+tc.name, func(t *testing.T) {
					route, st := newHiredRouteHandler(t, state)
					refs := homePanelRouteContext()
					if surface == "workspace" {
						refs.Surface, refs.PagePath, refs.WorkspaceID = "workspace", "/workspaces/fictional-project", "project-not-hq"
					}
					before := len(st.ListAgents())
					resp, err := route.RoutePrompt(context.Background(), tc.discussion, refs)
					if err != nil {
						t.Fatal(err)
					}
					requireConversationRoute(t, resp)
					if resp.WorkspaceRecommended || resp.WorkspaceResolution != nil {
						t.Fatalf("discussion opened setup: %+v", resp)
					}

					f := newConversationFixture(t, "Let's compare the options; no setup is needed to discuss them.")
					f.context.State = state
					f.store.seed("same-thread", f.context.HQWorkspaceID, f.context.ConversationAgent,
						PersonalAssistantConversationMessage{ID: "u1", Role: llm.RoleUser, Content: "I want community ideas, not talent coaching."},
						PersonalAssistantConversationMessage{ID: "a1", Role: llm.RoleAssistant, Content: "We can compare community options."})
					answer := f.handler.Ask(context.Background(), HomeAssistantAskRequest{
						Prompt: tc.discussion, Intent: resp.Intent, Context: refs,
						Conversation: &HomeAssistantConversationRef{ID: "same-thread"},
					})
					if answer.RequiresConfirmation || answer.DraftReview != nil || answer.MemoryReview != nil || len(answer.Actions) != 0 {
						t.Fatalf("discussion initiated action: %+v", answer)
					}
					if answer.Conversation == nil || answer.Conversation.ID != "same-thread" || !answer.Conversation.Stored {
						t.Fatalf("lost canonical conversation: %+v", answer.Conversation)
					}
					if len(f.provider.requests) != 1 || !strings.Contains(f.provider.last().Messages[1].Content, "not talent coaching") {
						t.Fatal("discussion did not use canonical correction in one model call")
					}
					if f.mutator.writes != 0 || len(f.memory.requests) != 0 || len(f.store.sessions) != 1 || len(st.ListAgents()) != before {
						t.Fatal("discussion created business state or a second conversation")
					}

					// Action characterization is app-wide; browsing alone must not
					// supply the eventual action's target or approval.
					actual, err := route.RoutePrompt(context.Background(), tc.action, homePanelRouteContext())
					if err != nil || actual == nil || actual.RouteMode != tc.actionMode || actual.Intent == homeAssistantConversationIntent.Key {
						t.Fatalf("explicit action lost its owner: %+v %v", actual, err)
					}
					if f.mutator.writes != 0 || len(st.ListAgents()) != before {
						t.Fatal("routing an action executed it")
					}
				})
			}
		}
	}
}

func TestAssistantDiscoveryRoute_ExplicitCreateStillRequiresReview(t *testing.T) {
	f := newConversationFixture(t)
	answer := f.say("Create a workspace called Community", "")
	if !answer.RequiresConfirmation || answer.Confirmation == nil || answer.Confirmation.ActionType != HomeActionCreateWorkspace {
		t.Fatalf("missing actual workspace review: %+v", answer)
	}
	if len(f.provider.requests) != 0 || f.mutator.writes != 0 || f.store.messageCount() != 0 {
		t.Fatal("opening review called a model, executed a mutation or stored approval")
	}
}
