package agenthttp

import "strings"

// This file is the server-owned vocabulary of everything Ori is allowed to talk
// about and point at. Nothing here is generated at request time and nothing here
// comes from a model: a topic exists because it was written down, reviewed, and
// bound to a route that home_nav_catalog_test.go proves is registered.
//
// That is the whole safety story for explanations. A model may later rephrase
// one of these answers, but it cannot introduce a topic, a destination, or a
// coachmark target that is not already in this file (PRD FR-34/FR-41/FR-46).

// CoachmarkKey names a control the guide may visually mark. It is a typed token,
// never a CSS selector: the browser maps a key to a local element through its own
// allowlist, so neither the server nor a model can aim the guide at arbitrary
// page structure (FR-41).
type CoachmarkKey string

const (
	CoachmarkNewAgent        CoachmarkKey = "new_agent"
	CoachmarkWorkspaceManger CoachmarkKey = "workspace_manager"
	CoachmarkQuickCapture    CoachmarkKey = "quick_capture"
	CoachmarkViewToggle      CoachmarkKey = "view_toggle"
	CoachmarkNewWorkspace    CoachmarkKey = "new_workspace"
	CoachmarkAgentToolbox    CoachmarkKey = "agent_toolbox"
	CoachmarkActionCenter    CoachmarkKey = "action_center_review"
	CoachmarkAddMCPServer    CoachmarkKey = "add_mcp_server"

	// The guided Personal HQ walkthrough marks exactly two controls, in order:
	// the reserved site on the Map, then the Build My HQ action inside the
	// site's context dialog. Both are focus-only, like every other coachmark —
	// marking the site must not select it, and marking Build My HQ must not
	// open the form. The user performs both actions.
	CoachmarkPersonalHQSite  CoachmarkKey = "personal_hq_site"
	CoachmarkPersonalHQBuild CoachmarkKey = "personal_hq_build"
)

// registeredCoachmarkKeys is the complete set the browser knows how to resolve.
// It mirrors the REGISTRY in ori-guide-coachmarks.js; the two are checked
// against each other by tests on both sides, so a key added to one and not the
// other fails rather than silently producing a coachmark that never appears.
var registeredCoachmarkKeys = []CoachmarkKey{
	CoachmarkNewAgent,
	CoachmarkWorkspaceManger,
	CoachmarkQuickCapture,
	CoachmarkViewToggle,
	CoachmarkNewWorkspace,
	CoachmarkAgentToolbox,
	CoachmarkActionCenter,
	CoachmarkAddMCPServer,
	CoachmarkPersonalHQSite,
	CoachmarkPersonalHQBuild,
}

// SetupStep is the closed set of app-setup actions Ori may offer.
//
// This is the one place the guide reaches beyond navigation, and it is
// deliberately an enumeration rather than a capability: each value maps to an
// existing setup endpoint that keeps its own confirmation, and the guide handler
// itself never performs any of them — it only returns the token. Adding a value
// here is a reviewed decision, not a configuration change.
//
// Note what cannot be expressed: running a workspace task, invoking an agent,
// calling a connected service, or reading a secret. Those are not omissions to
// be filled in later; the guide has no vocabulary for them by design (FR-39).
type SetupStep string

const (
	// SetupOpenModelSettings sends the user to the model/API-key form. It opens
	// a form; it does not write a key.
	SetupOpenModelSettings SetupStep = "open_model_settings"
	// SetupOpenConnections sends the user to account connection management.
	SetupOpenConnections SetupStep = "open_connections"
	// SetupOpenToolCatalog sends the user to the MCP/tool configuration page.
	SetupOpenToolCatalog SetupStep = "open_tool_catalog"
)

// setupStepRoutes binds each setup step to a navigation-catalog key, so a setup
// action resolves to the same validated route as any other destination and
// cannot point somewhere unregistered.
var setupStepRoutes = map[SetupStep]string{
	SetupOpenModelSettings: "settings",
	SetupOpenConnections:   "vaults",
	SetupOpenToolCatalog:   "mcp",
}

// GuideTopic is one thing Ori can explain.
type GuideTopic struct {
	// Key is stable and is what analytics/tests refer to; the label is what a
	// user reads. Renaming a label never changes the key.
	Key   string `json:"key"`
	Label string `json:"label"`
	// Explanation is the canonical, human-reviewed answer. This is the text a
	// user sees when no model is configured, so it has to stand on its own
	// (FR-47).
	Explanation string `json:"explanation"`
	// NavKey binds the topic to a navigation-catalog entry. Empty means the
	// concept has no single destination (e.g. "Agent" as an idea).
	NavKey string `json:"nav_key,omitempty"`
	// Coachmark is an optional typed control to spotlight once the user is on
	// the right route.
	Coachmark CoachmarkKey `json:"coachmark,omitempty"`
	// Setup marks a topic that offers a bounded setup action.
	Setup SetupStep `json:"setup,omitempty"`
	// Aliases are phrases that resolve to this topic. Matching is exact-ish and
	// deterministic; an unmatched question becomes an honest "I don't know"
	// rather than a guess (FR-48).
	Aliases []string `json:"-"`
}

// guideTopics is the approved concept catalog (FR-33). Every NavKey must exist
// in homeNavCatalog; ori_guide_topics_test.go enforces that.
var guideTopics = []GuideTopic{
	{
		Key:   "home",
		Label: "Home",
		Explanation: "Home is your workspace map. It shows every workspace you can open and " +
			"what needs attention today. Ask Ori is available on every page for questions and work alike.",
		NavKey:  "home",
		Aliases: []string{"home", "dashboard", "the map", "workspace map", "start page"},
	},
	{
		Key:   "workspace",
		Label: "Workspace",
		Explanation: "A workspace is a place for one body of work. It holds its own tasks, notes, " +
			"files, sessions, and the agents assigned to it. Opening a workspace scopes everything you do to it.",
		NavKey:    "workspaces",
		Coachmark: CoachmarkNewWorkspace,
		Aliases:   []string{"workspace", "workspaces", "what is a workspace", "projects"},
	},
	{
		Key:   "agent",
		Label: "Agent",
		Explanation: "An agent is a configured worker: a model, a system prompt, a set of tools, and " +
			"the workspaces it belongs to. When you describe work here, I route it to the right agent and " +
			"ask you to confirm anything consequential.",
		NavKey:    "agents",
		Coachmark: CoachmarkNewAgent,
		Aliases:   []string{"agent", "agents", "what is an agent", "workers"},
	},
	{
		// The topic key stays "workspace-manager" on purpose: the controller uses
		// it as the signal that a request is work rather than navigation, and the
		// aliases keep answering for users who still call it by its old name.
		// Only the rendered copy changed (FR61/FR62).
		Key:   "workspace-manager",
		Label: "Getting work done",
		Explanation: "Ask Ori plans work, routes it to the right agent or workspace, and asks you to " +
			"confirm anything consequential. Describe what you want done in the same box you ask questions in — " +
			"nothing runs until you say so.",
		NavKey:    "home",
		Coachmark: CoachmarkWorkspaceManger,
		Aliases: []string{
			"workspace manager", "the work surface", "command box",
			"how do i run something", "who does the work",
		},
	},
	{
		Key:   "toolbox",
		Label: "Toolbox",
		Explanation: "An agent's Toolbox is the exact set of skills and tools that agent can use. " +
			"It is per-agent: giving one agent a tool does not give it to the others.",
		NavKey:    "agents",
		Coachmark: CoachmarkAgentToolbox,
		Aliases:   []string{"toolbox", "tools an agent has", "agent tools", "capabilities"},
	},
	{
		Key:   "skill",
		Label: "Skill",
		Explanation: "A skill is a reusable instruction set an agent can load — a packaged way of doing " +
			"a particular kind of task. Skills are bound per agent and per workspace.",
		NavKey:  "agents",
		Aliases: []string{"skill", "skills", "what is a skill"},
	},
	{
		Key:   "tool",
		Label: "Tool",
		Explanation: "A tool is a concrete capability an agent can call, usually provided by an MCP server. " +
			"Tools are configured once and then bound to the agents and workspaces that should have them.",
		NavKey:    "mcp",
		Setup:     SetupOpenToolCatalog,
		Coachmark: CoachmarkAddMCPServer,
		Aliases:   []string{"tool", "tools", "mcp", "mcp server", "connectors", "what is a tool"},
	},
	{
		Key:   "vault",
		Label: "Vault",
		Explanation: "A Vault stores credentials that agents and connectors need. Values are write-only: " +
			"once saved, a secret can be used but never displayed back — including to me. I can show you where " +
			"to manage them, but I cannot read them.",
		NavKey: "vaults",
		// Vault owns *stored credential* language. Provider-key setup language
		// ("api key", "openai key") belongs to model-setup, because someone
		// asking that is almost always trying to get Ori working rather than
		// looking for the credential store.
		Aliases: []string{"vault", "vaults", "secret", "secrets", "credentials", "stored credentials"},
	},
	{
		Key:   "connection",
		Label: "Connection",
		Explanation: "A connection links an external account, such as email or calendar, so agents can work " +
			"with it. You grant a connection per workspace, and you can revoke it at any time.",
		NavKey: "vaults",
		Setup:  SetupOpenConnections,
		Aliases: []string{
			"connection", "connections", "connect account", "connect my account",
			"connect my email", "connect email", "link account",
			"gmail", "google", "email account", "calendar",
		},
	},
	{
		Key:   "action-center",
		Label: "Action Center",
		Explanation: "Action Center collects findings your workspaces surfaced and lets you triage them in " +
			"one place, so nothing important stays buried in a single workspace.",
		NavKey:    "action-center",
		Coachmark: CoachmarkActionCenter,
		Aliases:   []string{"action center", "action centre", "actions", "opportunities", "triage"},
	},
	{
		Key:   "personal-hq",
		Label: "Personal HQ",
		Explanation: "Personal HQ is your own workspace for personal work — email, calendar, and daily " +
			"planning — kept separate from your project workspaces.",
		NavKey:  "home",
		Aliases: []string{"personal hq", "hq", "my workspace", "personal workspace"},
	},
	{
		Key:   "model-setup",
		Label: "Model setup",
		Explanation: "Ori needs a model provider and API key before agents can run. Settings is where you " +
			"choose the provider, add the key, and pick the system model.",
		NavKey: "settings",
		Setup:  SetupOpenModelSettings,
		Aliases: []string{
			"api key", "api keys", "model", "provider", "openai key", "anthropic key",
			"set up ori", "setup", "get started", "configure",
		},
	},
	{
		Key:   "usage",
		Label: "Usage & cost",
		Explanation: "Usage tracks what your agents have spent across providers and models, so you can see " +
			"where tokens and money are going.",
		NavKey:  "usage",
		Aliases: []string{"usage", "cost", "costs", "spend", "billing", "how much"},
	},
}

// GuideTopics returns a copy of the approved topic catalog.
func GuideTopics() []GuideTopic {
	out := make([]GuideTopic, len(guideTopics))
	copy(out, guideTopics)
	return out
}

// FindGuideTopic resolves a question to an approved topic.
//
// Matching is deliberately conservative: an exact key/label/alias hit, then a
// longest-alias containment pass. Anything else returns false so the caller says
// "I don't know that one" and offers the approved list, rather than steering the
// user somewhere plausible-but-wrong (FR-48).
func FindGuideTopic(query string) (GuideTopic, bool) {
	q := normalizeGuideQuery(query)
	if q == "" {
		return GuideTopic{}, false
	}

	for _, t := range guideTopics {
		if strings.EqualFold(t.Key, q) || strings.EqualFold(t.Label, q) {
			return t, true
		}
		for _, alias := range t.Aliases {
			if alias == q {
				return t, true
			}
		}
	}

	// Longest-alias containment, so "where do I put my openai api key" resolves
	// to model-setup rather than to whichever alias happened to be checked first.
	best := GuideTopic{}
	bestLen := 0
	for _, t := range guideTopics {
		for _, alias := range t.Aliases {
			if len(alias) > bestLen && strings.Contains(q, alias) {
				best, bestLen = t, len(alias)
			}
		}
	}
	if bestLen > 0 {
		return best, true
	}
	return GuideTopic{}, false
}

func normalizeGuideQuery(s string) string {
	q := strings.ToLower(strings.TrimSpace(s))
	q = strings.Trim(q, " .,!?:;\"'")
	q = strings.TrimPrefix(q, "the ")
	q = strings.TrimSuffix(q, " page")
	return strings.TrimSpace(q)
}

// suggestedTopicsFor returns the topics worth offering on a given route. Route
// context only re-orders approved topics; it never unlocks a new one.
func suggestedTopicsFor(route string) []GuideTopic {
	pick := func(keys ...string) []GuideTopic {
		out := make([]GuideTopic, 0, len(keys))
		for _, k := range keys {
			for _, t := range guideTopics {
				if t.Key == k {
					out = append(out, t)
					break
				}
			}
		}
		return out
	}

	switch {
	case route == "/" || route == "":
		return pick("workspace", "workspace-manager", "agent", "model-setup")
	case strings.HasPrefix(route, "/agents"):
		return pick("agent", "toolbox", "skill", "tool")
	case strings.HasPrefix(route, "/vaults"):
		return pick("vault", "connection", "model-setup")
	case strings.HasPrefix(route, "/action-center"):
		return pick("action-center", "workspace", "workspace-manager")
	case strings.HasPrefix(route, "/mcp"):
		return pick("tool", "toolbox", "skill")
	case strings.HasPrefix(route, "/skills"):
		return pick("skill", "toolbox", "agent")
	case strings.HasPrefix(route, "/plugins"):
		return pick("tool", "skill", "toolbox")
	case strings.HasPrefix(route, "/settings"):
		return pick("model-setup", "usage", "vault")
	case strings.HasPrefix(route, "/models"):
		return pick("model-setup", "usage", "agent")
	case strings.HasPrefix(route, "/usage"):
		return pick("usage", "model-setup", "agent")
	case strings.HasPrefix(route, "/profile"):
		return pick("home", "personal-hq", "workspace-manager")
	// Matches both /workspace/<id> and /workspaces.
	case strings.HasPrefix(route, "/workspace"):
		return pick("workspace", "agent", "workspace-manager", "action-center")
	default:
		return pick("home", "workspace", "agent", "workspace-manager")
	}
}
