package agenthttp

import (
	"fmt"
	"strings"
)

const homeMaxNextStepActions = 4

// buildHomeSystemPrompt is the engineered system prompt for the home harness,
// the app-scoped analogue of buildTaskSystemPrompt (PRD FR #16).
func buildHomeSystemPrompt() string {
	var b strings.Builder
	b.WriteString("You are Ori's home assistant. You answer questions about the user's own Ori app — their agents, workspaces, tasks, sessions, Action Center opportunities, and usage — and you help them navigate the app. ")
	b.WriteString("You are given a \"Home Snapshot\" of the user's app-wide state and a \"Navigation Catalog\" of the app's pages. ")
	b.WriteString("Treat the Home Snapshot as the source of truth for questions about the user's activity and data; use the exact counts and names from it. ")
	b.WriteString("The snapshot includes the agent roster (each agent's type, role, model, and which workspaces use it), so you can answer \"what agents do I have\", \"what can agent X do\", and \"which agents aren't used anywhere\". ")
	b.WriteString("When a snapshot section is marked truncated, or the user asks for detail beyond it, call the read-only home_* tools (home_workspaces, home_tasks, home_sessions, home_opportunities, home_usage, home_agents) to read full state. ")
	b.WriteString("Never invent agents, workspaces, tasks, sessions, opportunities, or activity. If a section is empty or marked degraded (data unavailable), say so plainly instead of guessing. ")
	b.WriteString("For navigation questions, only reference destinations that appear in the Navigation Catalog; never invent a URL or page name. ")
	b.WriteString("Be concise and skimmable: lead with the key counts, then a few highlights. ")
	b.WriteString("Beyond answering, you can act on the user's behalf: create a workspace, create a task in an existing workspace, and start (run) an existing task. ")
	b.WriteString("These actions always require the user's explicit confirmation before anything happens, so when a user asks you to create or start something, confirm you can and let the confirmation step handle it. ")
	b.WriteString("When helpful, end with a brief suggestion of a concrete next step. ")
	b.WriteString("Do not output raw JSON or tool results as your final answer; write a short natural-language summary.")
	return b.String()
}

// buildHomeUserPrompt assembles the user turn: the request plus the injected
// snapshot and navigation catalog.
func buildHomeUserPrompt(prompt, intent string, snapshot HomeSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "User request: %s\n\n", strings.TrimSpace(prompt))
	fmt.Fprintf(&b, "(classified intent: %s)\n\n", intent)
	b.WriteString(snapshot.PromptText())
	b.WriteString("\n\n## Navigation Catalog\n\n")
	b.WriteString("These are the only app destinations you may reference for navigation:\n")
	for _, entry := range homeNavCatalog {
		fmt.Fprintf(&b, "- %s (%s): %s\n", entry.Label, entry.Href, entry.Description)
	}
	return b.String()
}

// buildNextStepActions deterministically derives grounded next-step actions from
// real data (PRD 4.5). Actions are not produced by the model; navigation/open_*
// actions are read-only, and any create_* action carries requires_confirmation.
func (h *HomeAssistantAskHandler) buildNextStepActions(intent, prompt string, snapshot HomeSnapshot) []HomeAction {
	var actions []HomeAction
	seen := map[string]bool{}
	add := func(a HomeAction) {
		if len(actions) >= homeMaxNextStepActions {
			return
		}
		key := a.Type + "|" + a.Href + "|" + a.WorkspaceID
		if seen[key] {
			return
		}
		seen[key] = true
		actions = append(actions, a)
	}

	if intent == homeAssistantAppNavigationIntent.Key {
		if entry, ok := MatchNavEntryInPrompt(prompt); ok {
			add(HomeAction{ID: "nav-" + entry.Key, Type: HomeActionNavigate, Label: "Open " + entry.Label, Href: entry.Href})
		}
		if id, name, ok := matchWorkspaceInPrompt(h.Sources.Workspaces, prompt); ok {
			add(HomeAction{ID: "open-ws-" + id, Type: HomeActionOpenWorkspace, Label: "Open " + name, Href: workspaceHref(id), WorkspaceID: id})
		}
	}

	// Empty state: guide first-run users to create a workspace.
	if snapshot.Meta.WorkspaceCount == 0 {
		add(HomeAction{ID: "nav-create-first-ws", Type: HomeActionNavigate, Label: "Create your first workspace", Href: "/workspaces"})
		return actions
	}

	// Introspection: surface the most recently active workspaces.
	if intent == homeAssistantAppIntrospectionIntent.Key {
		for _, ws := range snapshot.Workspaces {
			add(HomeAction{ID: "open-ws-" + ws.ID, Type: HomeActionOpenWorkspace, Label: "Open " + ws.Name, Href: workspaceHref(ws.ID), WorkspaceID: ws.ID})
			if len(actions) >= 3 {
				break
			}
		}
		if snapshot.Meta.OpportunityCount > 0 {
			add(HomeAction{ID: "nav-action-center", Type: HomeActionNavigate, Label: fmt.Sprintf("Review %d opportunit%s", snapshot.Meta.OpportunityCount, plural(snapshot.Meta.OpportunityCount, "y", "ies")), Href: "/action-center"})
		}
	}

	return actions
}

func workspaceHref(id string) string {
	return "/workspaces/" + id
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
