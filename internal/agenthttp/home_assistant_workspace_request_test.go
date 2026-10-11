package agenthttp

import (
	"context"
	"slices"
	"testing"
)

// requestsWorkspace is how the hired assistant's conversation reads a prompt:
// the classification every surface shares, or a request in other words.
func requestsWorkspace(prompt string) bool {
	return shouldClassifyWorkspaceCreate(prompt) || asksForWorkspaceInOtherWords(prompt)
}

// "Build a workspace for research about OK Go" means what "Create a workspace
// for research about OK Go" means. In the assistant's conversation it used to
// be general work, which sent it to the system assistant, whose answer to the
// user was an implementation brief for a developer.
func TestWorkspaceRequest_OtherVerbsMeanCreate(t *testing.T) {
	requests := []string{
		"Build a workspace for research about ok go",
		"Make a workspace for research about ok go",
		"Set up a workspace for research about ok go",
		"Setup a workspace about OK Go",
		"Let's set up a workspace for research about ok go",
		"Please build a workspace for research about ok go.",
		"Can you build me a workspace to track the album?",
		"Make us a new workspace for the tour",
		"Build a new workspace on OK Go's videos",
		"build a workspace",
		"Build a workspace called OK Go Research",
		// A polite opening no longer hides a plain "create".
		"Please create a workspace for research about ok go",
		"Could you create a workspace named Launch",
		// Unchanged.
		"create workspace called Q3 Roadmap",
		"Create a workspace for research about ok go",
		"new workspace for research about ok go",
	}
	for _, prompt := range requests {
		if !requestsWorkspace(prompt) {
			t.Errorf("a request for a workspace was missed: %q", prompt)
		}
	}

	notRequests := []string{
		// Something else is being built, made or set up.
		"Build a full stack web app with authentication and a database",
		"Make a workspace summary for me",
		"Build a workspace template doc",
		"Make a workspaces overview",
		"Build a workspace-wide search",
		"Set up the workspace settings",
		"Set up my email",
		"Make it warmer",
		// A question, a hypothetical, a negation and a quotation are not requests.
		"How do I build a workspace?",
		"Should we build a workspace for this?",
		"Don't build a workspace for this",
		`Explain "build a workspace for research"`,
		"What would it take to make a workspace for the tour?",
		"",
	}
	for _, prompt := range notRequests {
		if requestsWorkspace(prompt) {
			t.Errorf("not a request for a workspace, but classified as one: %q", prompt)
		}
	}
}

// Outside the assistant's conversation nothing changed. There the page reads
// "create a workspace …" itself before it asks for a route, so giving "build
// a workspace …" that intent would send it somewhere "create" never goes.
func TestWorkspaceRequest_OtherSurfacesRouteAsBefore(t *testing.T) {
	for _, prompt := range []string{
		"Build a workspace for research about ok go",
		"Make a workspace for research about ok go",
		"Set up a workspace for research about ok go",
		"Please create a workspace for research about ok go",
	} {
		if shouldClassifyWorkspaceCreate(prompt) {
			t.Errorf("the classification every surface shares changed for %q", prompt)
		}
	}

	handler, _ := newHiredRouteHandler(t, "active")
	for _, routeContext := range []*HomeAssistantRouteContext{nil, {Origin: "home", Surface: "home"}} {
		resp, err := handler.RoutePrompt(context.Background(), "Build a workspace for research about ok go", routeContext)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Intent == homeAssistantWorkspaceCreateIntent.Key {
			t.Errorf("context %+v: routed as workspace creation outside the assistant's conversation", routeContext)
		}
	}
}

// In the hired assistant's drawer every such phrasing takes the route that
// "Create a workspace for …" takes: the reviewed workspace proposal. None of
// them is handed to the system assistant.
func TestWorkspaceRequest_RoutesLikeCreateForAHiredAssistant(t *testing.T) {
	handler, _ := newHiredRouteHandler(t, "active")
	reference, err := handler.RoutePrompt(context.Background(), "Create a workspace for research about ok go", homePanelRouteContext())
	if err != nil {
		t.Fatal(err)
	}
	if reference.Intent != "workspace_create" || reference.RouteMode != "workspace_task" {
		t.Fatalf("premise: create routes as workspace creation, got intent=%q mode=%q", reference.Intent, reference.RouteMode)
	}
	for _, prompt := range []string{
		"Build a workspace for research about ok go",
		"Make a workspace for research about ok go",
		"Set up a workspace for research about ok go",
		"Let's set up a workspace for research about ok go",
		"Please create a workspace for research about ok go",
	} {
		resp, err := handler.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
		if err != nil {
			t.Fatalf("%q: %v", prompt, err)
		}
		if resp.Intent != reference.Intent || resp.RouteMode != reference.RouteMode || resp.TargetSurface != reference.TargetSurface ||
			resp.MatchedAgent != reference.MatchedAgent || resp.RequiresCreation != reference.RequiresCreation {
			t.Errorf("%q: intent=%q mode=%q surface=%q matched=%q; want the route of \"Create a workspace for …\" (%q / %q / %q / %q)",
				prompt, resp.Intent, resp.RouteMode, resp.TargetSurface, resp.MatchedAgent,
				reference.Intent, reference.RouteMode, reference.TargetSurface, reference.MatchedAgent)
		}
		if slices.Contains(resp.Reasons, "fallback to system assistant") {
			t.Errorf("%q fell back to the system assistant", prompt)
		}
	}

	// What already had an owner keeps it.
	for prompt, want := range map[string][2]string{
		"Build a full stack web app with authentication and a database": {"general_task", "workspace_task"},
		"Let's set up a workspace for this plan":                        {homeAssistantConversationIntent.Key, homeAssistantRouteModeInline},
		"Can we create a workspace for research about ok go?":           {homeAssistantConversationIntent.Key, homeAssistantRouteModeInline},
		"create a workspace called Launch":                              {"workspace_create", "workspace_task"},
	} {
		resp, err := handler.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
		if err != nil {
			t.Fatalf("%q: %v", prompt, err)
		}
		if resp.Intent != want[0] || resp.RouteMode != want[1] {
			t.Errorf("%q: intent=%q mode=%q; want %q / %q", prompt, resp.Intent, resp.RouteMode, want[0], want[1])
		}
	}
}
