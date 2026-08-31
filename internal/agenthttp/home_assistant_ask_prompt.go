package agenthttp

import (
	"fmt"
	"strings"
)

const homeMaxNextStepActions = 4

// buildHomeSystemPrompt is the engineered system prompt for the home harness,
// the app-scoped analogue of buildTaskSystemPrompt (PRD FR #16).
//
// When firstRun is true (a brand-new user with no workspaces yet), a greeting
// behavior is appended so Ori guides the user toward creating their first
// workspace — the onboarding progression "first contact" continuation. The
// gate is the caller's responsibility; once a workspace exists this is off.
func buildHomeSystemPrompt(firstRun bool) string {
	return buildHomeSystemPromptWithAssistant(firstRun, nil)
}

func buildHomeSystemPromptWithAssistant(firstRun bool, workContext *PersonalAssistantWorkContext) string {
	var b strings.Builder
	if workContext != nil && workContext.ReadyForWork() {
		name := boundedContextText(workContext.DisplayName, 100)
		if name == "" {
			name = "the user's personal assistant"
		}
		fmt.Fprintf(&b, "You are the user's hired personal assistant, displayed as %q with the role Personal Assistant. Speak as that display identity. Never expose or mention internal assistant IDs, system assistants, agent profile keys, or implementation names. ", name)
		if workContext.State == "paused" {
			b.WriteString("The proactive relationship is paused: you may answer this direct user request and prepare confirm-gated actions, but must not claim a routine or autonomous run is active. ")
		}
	} else {
		b.WriteString("You are Ori's home assistant. ")
	}
	b.WriteString("You answer questions about the user's own Ori app — their agents, workspaces, tasks, sessions, Action Center opportunities, and usage — and you help them navigate the app. ")
	b.WriteString("You are given a \"Home Snapshot\" of the user's app-wide state and a \"Navigation Catalog\" of the app's pages. ")
	b.WriteString("Treat the Home Snapshot as the source of truth for questions about the user's activity and data; use the exact counts and names from it. ")
	b.WriteString("The snapshot includes the agent roster (each agent's type, role, model, and which workspaces use it), so you can answer \"what agents do I have\", \"what can agent X do\", and \"which agents aren't used anywhere\". ")
	b.WriteString("When a snapshot section is marked truncated, or the user asks for detail beyond it, call the read-only home_* tools (home_workspaces, home_tasks, home_sessions, home_opportunities, home_usage, home_agents) to read full state. ")
	b.WriteString("Never invent agents, workspaces, tasks, sessions, opportunities, or activity. If a section is empty or marked degraded (data unavailable), say so plainly instead of guessing. ")
	b.WriteString("For navigation questions, only reference destinations that appear in the Navigation Catalog; never invent a URL or page name. ")
	b.WriteString("Be concise and skimmable: lead with the key counts, then a few highlights. ")
	b.WriteString("Beyond answering, you can act on the user's behalf: create a workspace, create a task in an existing workspace, start (run) an existing task, create a new agent, and add or remove one of the user's agents to/from a workspace. ")
	b.WriteString("These actions always require the user's explicit confirmation before anything happens, so when a user asks you to create or start something, confirm you can and let the confirmation step handle it. ")
	b.WriteString("When helpful, end with a brief suggestion of a concrete next step. ")
	b.WriteString("Do not output raw JSON or tool results as your final answer; write a short natural-language summary.")
	if firstRun {
		b.WriteString("\n\nThis is a brand-new user who has not created any workspace yet. Treat this as their first contact with Ori. ")
		b.WriteString("If their message is just a greeting or an unfocused opener, do NOT give a generic \"how can I help\". Instead: (1) in one or two sentences, orient them on what Ori does in outcome terms — it gets real work done by spinning up workspaces, delegating tasks to agents, and automating the repetitive; (2) ask a single question: what are they working on right now (a project, a song, some research — anything); then, based on their answer, (3) offer to create a workspace for it (the create-workspace action, which seeds a couple of starter tasks) and (4) suggest the next step, like opening it or adding an agent. ")
		b.WriteString("Never assume they already know what a \"workspace\" is — teach by doing it through the conversation. ")
		b.WriteString("If their very first message is already a concrete request (e.g. \"create a workspace for my thesis\"), skip the question and act on it directly. ")
		b.WriteString("Keep it warm and brief, not a wall of text.")
	}
	return b.String()
}

// buildHomeUserPrompt assembles the user turn: the request plus the injected
// snapshot and navigation catalog.
func buildHomeUserPrompt(prompt, intent string, snapshot HomeSnapshot) string {
	return buildHomeUserPromptWithAssistant(prompt, intent, snapshot, nil)
}

func buildHomeUserPromptWithAssistant(prompt, intent string, snapshot HomeSnapshot, workContext *PersonalAssistantWorkContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "User request: %s\n\n", strings.TrimSpace(prompt))
	fmt.Fprintf(&b, "(classified intent: %s)\n\n", intent)
	b.WriteString(snapshot.PromptText())
	b.WriteString("\n\n## Navigation Catalog\n\n")
	b.WriteString("These are the only app destinations you may reference for navigation:\n")
	for _, entry := range homeNavCatalog {
		fmt.Fprintf(&b, "- %s (%s): %s\n", entry.Label, entry.Href, entry.Description)
	}
	b.WriteString(renderPersonalAssistantPromptContext(workContext))
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
		add(HomeAction{ID: "nav-create-first-ws", Type: HomeActionNavigate, Label: "Create your first workspace", Href: "/?create=1"})
		return actions
	}

	// Introspection: surface the most recently active workspaces.
	if intent == homeAssistantAppIntrospectionIntent.Key {
		for _, ws := range snapshot.Workspaces {
			if strings.TrimSpace(ws.Slug) == "" {
				continue
			}
			add(HomeAction{ID: "open-ws-" + ws.ID, Type: HomeActionOpenWorkspace, Label: "Open " + ws.Name, Href: workspaceHref(ws.Slug), WorkspaceID: ws.ID})
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

func workspaceHref(slug string) string {
	return "/workspaces/" + slug
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
