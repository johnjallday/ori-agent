// Package progression implements the onboarding quest-log: a built-in,
// tiered set of "quests" that guide a new user from their first message to
// advanced multi-agent automation. Quests complete automatically when the
// server observes the real action (via the workspace event bus) or when a
// startup backfill scan finds the action was already done on an existing
// install. The package is purely additive — it never gates or hides any
// feature.
package progression

import (
	"strings"

	ws "github.com/johnjallday/ori-agent/internal/workspace"
)

// TotalTiers is the number of tiers in the built-in quest graph.
const TotalTiers = 6

// Quest is one objective in the onboarding quest graph.
type Quest struct {
	// ID is a stable identifier persisted in progression state. Never change
	// an existing ID — completions are keyed on it.
	ID    string
	Tier  int
	Title string
	// Why is the one-line "why this matters" shown for the next quest.
	Why string
	// Match reports whether a live event completes this quest. nil means the
	// quest is not event-driven (it completes only via backfill or a direct
	// Complete call from a non-event code path, e.g. renaming the assistant).
	Match func(ev ws.Event) bool
	// Satisfied reports whether the backfill snapshot of existing state already
	// satisfies this quest. nil means the quest cannot be backfilled and will
	// only ever complete live.
	Satisfied func(s Snapshot) bool
	// ActionURL, when set, is where the widget links an incomplete quest so the
	// user can act on it. ActionLabel is the link text. Empty means the action
	// happens inline (e.g. the home chat box) with no separate destination.
	ActionURL   string
	ActionLabel string
	// Optional marks a quest the user may explicitly skip via Engine.Skip
	// instead of completing. Skipping a non-optional quest is rejected. A
	// skipped optional quest counts as resolved for current-tier/TierView
	// completion so it never keeps later tiers locked, but it is never
	// recorded in CompletedQuests — only a real observed action (live event,
	// backfill, or direct Complete call) does that.
	Optional bool
}

// tierNames maps a tier number to its display name.
var tierNames = map[int]string{
	1: "First Contact",
	2: "Establish a Base",
	3: "Recruit",
	4: "Equip",
	5: "Automate",
	6: "Command",
}

// TierName returns the display name for a tier, or "" if unknown.
func TierName(tier int) string { return tierNames[tier] }

// onEvent builds a Match that fires on any of the given event types.
func onEvent(types ...ws.EventType) func(ws.Event) bool {
	return func(ev ws.Event) bool {
		for _, t := range types {
			if ev.Type == t {
				return true
			}
		}
		return false
	}
}

// dataString reads a string field from an event's data payload.
func dataString(ev ws.Event, key string) string {
	if ev.Data == nil {
		return ""
	}
	v, _ := ev.Data[key].(string)
	return strings.TrimSpace(v)
}

// onWorkspaceAction builds a Match for a workspace.updated event carrying a
// specific "action" in its data payload. Skill and MCP binding changes are
// published this way rather than as dedicated event types.
func onWorkspaceAction(action string) func(ws.Event) bool {
	return func(ev ws.Event) bool {
		return ev.Type == ws.EventWorkspaceUpdated && dataString(ev, "action") == action
	}
}

// BuiltinQuests returns the ordered built-in quest graph. The slice is freshly
// built on each call so callers can't mutate shared state.
//
// Some Match hooks are intentionally nil where the triggering action does not
// yet emit an event; those emissions are added in a follow-up group, at which
// point the Match is wired here. Backfill (Satisfied) still grandfathers those
// quests for existing installs in the meantime.
func BuiltinQuests() []Quest {
	return []Quest{
		// ---- Tier 1 — First Contact ----
		{
			ID: "t1-first-message", Tier: 1,
			Title: "Say hello to Ori",
			Why:   "Send Ori a message on the home page — it's the fastest way to see what it can do.",
			Match: onEvent(ws.EventMessageSent),
			// Any workspace implies the app has been used; grandfather first contact.
			Satisfied: func(s Snapshot) bool { return s.ChatMessages > 0 || s.Workspaces > 0 },
		},
		{
			ID: "t1-personalize", Tier: 1,
			Title: "Personalize Ori",
			Why:   "Tell Ori your interests and work style so it tailors its help to you.",
			// Filling out the profile is not an event; completed live by a direct
			// Complete call from the personalize handler and here via backfill.
			Satisfied:   func(s Snapshot) bool { return s.Personalized },
			ActionURL:   "/profile#personalization",
			ActionLabel: "Personalize on your Profile page",
		},

		// ---- Tier 2 — Establish a Base ----
		{
			ID: "t2-create-workspace", Tier: 2,
			Title:       "Create your first workspace",
			Why:         "A workspace is home base — where your projects, notes, and agents live.",
			Match:       onEvent(ws.EventWorkspaceCreated),
			Satisfied:   func(s Snapshot) bool { return s.Workspaces > 0 },
			ActionURL:   "/workspaces?create=1",
			ActionLabel: "Create a workspace",
		},
		{
			ID: "t2-build-hq", Tier: 2,
			Title:     "Build your Personal HQ",
			Why:       "Give Ori a home base — a place to prepare your daily brief, track follow-ups, and help you resume work.",
			Optional:  true,
			Satisfied: func(s Snapshot) bool { return s.HasPersonalHQ },
			// Mission 01 is featured from the Home progression panel even before
			// Tier 2 unlocks. Route straight into the guided HQ briefing instead
			// of the generic workspace launcher so the action is useful at every
			// progression tier.
			ActionURL:   "/workspaces?hq_onboarding=1",
			ActionLabel: "Build your Personal HQ",
		},
		{
			ID: "t2-create-note", Tier: 2,
			Title:     "Write a note",
			Why:       "Capture a thought or a plan — Ori can read and build on your notes.",
			Match:     onEvent(ws.EventNoteCreated),
			Satisfied: func(s Snapshot) bool { return s.Notes > 0 },
		},
		{
			ID: "t2-run-task", Tier: 2,
			Title:     "Run your first task",
			Why:       "Hand Ori a task and watch it work — this is where the real value starts.",
			Match:     onEvent(ws.EventTaskStarted),
			Satisfied: func(s Snapshot) bool { return s.TasksStarted > 0 },
		},

		// ---- Tier 3 — Recruit ----
		{
			ID: "t3-second-agent", Tier: 3,
			Title:     "Add a second agent",
			Why:       "Different agents bring different strengths — build a team, not a soloist.",
			Match:     onEvent(ws.EventAgentJoined),
			Satisfied: func(s Snapshot) bool { return s.Agents >= 2 },
		},
		{
			ID: "t3-delegate", Tier: 3,
			Title:     "Delegate a task to an agent",
			Why:       "Delegation is where Ori starts saving you real time.",
			Match:     onEvent(ws.EventTaskAssigned),
			Satisfied: func(s Snapshot) bool { return s.AgentTasksDone > 0 },
		},
		{
			ID: "t3-agent-task-done", Tier: 3,
			Title: "See an agent finish a task",
			Why:   "Close the loop — an agent completing work on its own is the core moment.",
			Match: func(ev ws.Event) bool {
				return ev.Type == ws.EventTaskCompleted && dataString(ev, "agent") != ""
			},
			Satisfied: func(s Snapshot) bool { return s.AgentTasksDone > 0 },
		},

		// ---- Tier 4 — Equip ----
		{
			ID: "t4-enable-skill", Tier: 4,
			Title:     "Enable a skill",
			Why:       "Skills teach an agent a reusable capability — equip one to level it up.",
			Match:     onWorkspaceAction("skill_binding_created"),
			Satisfied: func(s Snapshot) bool { return s.SkillsBound > 0 },
		},
		{
			ID: "t4-connect-mcp", Tier: 4,
			Title:     "Connect an MCP server",
			Why:       "MCP servers give agents real tools — files, APIs, apps on your machine.",
			Match:     onWorkspaceAction("mcp_binding_created"),
			Satisfied: func(s Snapshot) bool { return s.MCPServers > 0 },
		},
		{
			ID: "t4-tool-task", Tier: 4,
			Title: "Run a task that uses a tool",
			Why:   "Put the new capability to work in an actual task.",
			Match: onEvent(ws.EventTaskToolCall),
		},

		// ---- Tier 5 — Automate ----
		{
			ID: "t5-create-trigger", Tier: 5,
			Title:     "Set up a trigger or schedule",
			Why:       "Let Ori act on its own — on a schedule or when something happens.",
			Satisfied: func(s Snapshot) bool { return s.Triggers > 0 || s.ScheduledTasks > 0 },
		},
		{
			ID: "t5-unattended-run", Tier: 5,
			Title: "Get your first unattended result",
			Why:   "This is autonomy: work that happens without you kicking it off.",
			Match: onEvent(ws.EventScheduledTaskTriggered),
		},

		// ---- Tier 6 — Command ----
		{
			ID: "t6-orchestrate", Tier: 6,
			Title:     "Run a multi-agent orchestration",
			Why:       "Coordinate several agents on one goal — the deep end of what Ori does.",
			Match:     onEvent(ws.EventWorkflowStarted, ws.EventDelegationStarted),
			Satisfied: func(s Snapshot) bool { return s.OrchestrationRuns > 0 },
		},
		{
			ID: "t6-memory", Tier: 6,
			Title:     "Write to workspace memory",
			Why:       "Give Ori lasting context it carries across every run in the workspace.",
			Satisfied: func(s Snapshot) bool { return s.MemoryWrites > 0 },
		},
	}
}
