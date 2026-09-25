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

// PersonalAssistantFirstDayQuestID was the PAF cohort's featured first mission
// before the starter missions. It is no longer in any graph, but the ID stays
// because completions are persisted by it: a recorded completion is evidence
// that the user already connected a source (see ConnectSourceQuestID).
const PersonalAssistantFirstDayQuestID = "t1-plan-first-day"

// The starter missions' persisted IDs. Never change them once shipped.
const (
	// MeetAssistantQuestID is Mission 01: the personal assistant hired from the
	// Agents page. It completes from the durable hire, never from a browser
	// claim, and every later mission is locked until it does.
	MeetAssistantQuestID = "pa-meet-assistant"
	// TidyDownloadsQuestID was Mission 03 before Show your assistant a folder:
	// a File Janitor workspace whose setup wizard reached ready. It is no
	// longer in the graph, but persisted completions still carry the ID and
	// grandfather the new mission.
	TidyDownloadsQuestID = "pa-tidy-downloads"
	// ShowFolderQuestID is Mission 03: the user showed the assistant a folder
	// and accepted what it offered — a workspace linked to the folder, or a
	// tidy through File Janitor. It completes from the server-observed
	// outcome, never from a browser claim.
	ShowFolderQuestID = "pa-show-folder"
	// ConnectSourceQuestID is Mission 04: one source connected, chosen from the
	// hire's focus areas.
	ConnectSourceQuestID = "pa-connect-source"
	// FirstBriefQuestID is Mission 05: Today served with a Daily Brief.
	FirstBriefQuestID = "pa-first-brief"
)

// MeetAssistantActionURL starts Ori's deterministic Mission 01 walkthrough from
// its first step, on Home: Ori points at the Agents nav entry and the user
// clicks it, then the Agents page walks them through the New Agent panel's
// assistant preset. Starting where the user is, rather than jumping to the
// Agents page, is what shows them every click.
const MeetAssistantActionURL = "/?quest=meet-assistant"

// TidyDownloadsActionURL started the retired Tidy your Downloads walkthrough.
// Kept so persisted state that names it still loads.
const TidyDownloadsActionURL = "/?quest=tidy-downloads"

// ShowFolderActionURL opens Home with the assistant's folder chooser.
const ShowFolderActionURL = "/?quest=show-folder"

// PlanFirstDayActionURL opens the existing first-assignment flow, the plan
// branch of Mission 04.
const PlanFirstDayActionURL = "/?quest=plan-first-day"

// MissionContext is the per-user state a featured mission resolves its card
// from. The server fills it from the services that own each fact, so this
// package never imports them.
type MissionContext struct {
	// FocusAreas are the focus-area values the user chose at hire.
	FocusAreas []string
	// FileJanitor is the user's File Janitor workspace, or nil when none exists.
	FileJanitor *MissionWorkspace
	// EmailQuestStarted is true when the guided email setup exists but is not
	// ready yet.
	EmailQuestStarted bool
	// EmailQuestURL opens the guided email setup. The server passes it in so
	// this package does not import the host quest catalog.
	EmailQuestURL string
	// ModelConfigured is true when a model is available to generate a brief.
	ModelConfigured bool
}

// MissionWorkspace identifies a workspace a mission points at.
type MissionWorkspace struct {
	Slug        string
	WizardReady bool
}

// MissionPresentation is what a mission's Resolve returns. An empty string
// keeps the quest's static value, so a Resolve that has nothing to change
// returns the zero value.
type MissionPresentation struct {
	Title       string
	Why         string
	ActionURL   string
	ActionLabel string
	// InProgress is true when the user has started the mission but not
	// finished it. It is ignored once the quest is completed or skipped.
	InProgress bool
	// Hint is a sentence appended to the why line only while the mission is
	// open, for advice that stops being true once it is done.
	Hint string
}

// Graph is a quest graph together with its tier metadata. Tier names belong
// to the graph because a cohort can rename its tiers without touching another.
type Graph struct {
	Quests     []Quest
	TierNames  map[int]string
	TotalTiers int
}

// BuiltinGraph returns the built-in quests with their original tier names.
func BuiltinGraph() Graph {
	return Graph{Quests: BuiltinQuests(), TierNames: builtinTierNames(), TotalTiers: TotalTiers}
}

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
	// Featured marks a starter mission: the Quests card shows the first
	// unresolved featured quest, ordered by Order (1-based). Non-featured quests
	// carry Order 0.
	Featured bool
	Order    int
	// Retired omits this quest from the status view only. Its event matching,
	// completion, skip, and backfill history stay fully functional.
	Retired bool
	// LockedUntil names the quest that must be completed before this one is
	// offered. Until then the status view marks it Locked, with the reason
	// "<that quest's title> first". Locking is presentation only: Match,
	// Complete, and Backfill still run, so a user who somehow does the work
	// early still gets credit.
	LockedUntil string
	// Resolve, when set, fills the card's title, why, and action from per-user
	// state at status time. See MissionContext.
	Resolve func(MissionContext) MissionPresentation
}

// builtinTierNames maps a built-in tier number to its display name. It is
// built per call so no graph can mutate another's names.
func builtinTierNames() map[int]string {
	return map[int]string{
		1: "First Contact",
		2: "Establish a Base",
		3: "Recruit",
		4: "Equip",
		5: "Automate",
		6: "Command",
	}
}

// TierName returns the built-in graph's display name for a tier, or "" if
// unknown. A cohort graph's names come from its Graph.TierNames instead.
func TierName(tier int) string { return builtinTierNames()[tier] }

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

// BuildHQQuestID is the optional Personal HQ objective. It remains observable
// and completable, but the HQ confirm card replaces it on the mission board.
const BuildHQQuestID = "t2-build-hq"

// GuidedBuildHQActionURL opens Ori's deterministic Personal HQ walkthrough.
//
// It deliberately carries no focus parameter. A focus would preselect the
// reserved landmark; the quest highlights it and waits for the user's own
// selection, which is the first real interaction of the walkthrough.
const GuidedBuildHQActionURL = "/?quest=build-hq"

// PersonalAssistantQuests returns the cohort graph's quests. See
// PersonalAssistantGraph.
func PersonalAssistantQuests() []Quest { return PersonalAssistantGraph().Quests }

// PersonalAssistantGraph returns the personal-assistant cohort's graph.
//
// Tier 1, "Starter", presents four missions: Meet your assistant, Show your
// assistant a folder, Connect one source, Read your first Daily Brief. Every
// other quest is retired from presentation, but still detects and records real
// actions and backfill, preserving existing installs' history.
//
// Two built-in quests are dropped from this graph only. Plan my first day is
// now one branch of Connect one source, and Create your first workspace is
// what Mission 03 does. Their persisted completions stay harmlessly in place.
// BuiltinGraph is unchanged for any non-cohort caller.
func PersonalAssistantGraph() Graph {
	builtin := map[string]Quest{}
	for _, q := range BuiltinQuests() {
		builtin[q.ID] = q
	}
	retier := func(id string, tier int) Quest {
		q := builtin[id]
		q.Tier = tier
		q.Retired = true
		return q
	}

	// Only the tier and destination change. Completion still comes from a real
	// designation, never from opening the quest.
	buildHQ := retier(BuildHQQuestID, 1)
	buildHQ.ActionURL = GuidedBuildHQActionURL
	buildHQ.Why = "Give your assistant a home base — where it prepares your daily brief, tracks follow-ups, and helps you resume work."
	buildHQ.Featured, buildHQ.Order = false, 0
	buildHQ.LockedUntil = MeetAssistantQuestID

	quests := []Quest{
		{
			ID: MeetAssistantQuestID, Tier: 1, Featured: true, Order: 1, Optional: false,
			Title:       "Meet your assistant",
			Why:         "Your assistant is the one agent that owns your ongoing work. Make them yours.",
			ActionURL:   MeetAssistantActionURL,
			ActionLabel: "Start",
			// Live, the hire handler completes it once per durable hire or repair.
			// Here, any relationship that owns a hired profile grandfathers it.
			Satisfied: func(s Snapshot) bool { return s.AssistantHired },
		},
		buildHQ,
		{
			ID: ShowFolderQuestID, Tier: 1, Featured: true, Order: 2, Optional: true,
			LockedUntil: MeetAssistantQuestID,
			Title:       "Show your assistant a folder",
			Why:         "Point Ori at a folder and it will tell you what it can do with it.",
			ActionURL:   ShowFolderActionURL,
			ActionLabel: "Start",
			// Live, a workspace created from the assistant's offer is observed
			// here (FR42); the folder attaching and a tidy complete it from
			// server hooks, as does the File Janitor wizard reaching ready.
			Match: IsFolderDigestWorkspaceCreated,
			// Grandfathered by the retired Tidy mission's completion, a ready
			// File Janitor, or a workspace that already has an outside folder
			// as its primary project directory (FR43). A merely skipped Tidy
			// leaves it open.
			Satisfied: func(s Snapshot) bool {
				return s.LegacyTidyCompleted || s.FileJanitorReady || s.LinkedProjectWorkspaces > 0
			},
		},
		{
			ID: ConnectSourceQuestID, Tier: 1, Featured: true, Order: 3, Optional: true,
			LockedUntil: MeetAssistantQuestID,
			// The static copy is the plan branch, the fallback for every focus.
			Title:       "Plan my first day",
			Why:         "Give your assistant today's priorities and commitments so it can prepare a useful Daily Brief.",
			ActionURL:   PlanFirstDayActionURL,
			ActionLabel: "Start",
			// Any branch completes it, not only the one the card offers (FR14).
			// Live, a project workspace is observed here; email, calendar, and the
			// first-day plan complete it from server hooks.
			Match: IsProjectWorkspaceCreated,
			Satisfied: func(s Snapshot) bool {
				return s.FirstAssignmentCompleted || s.LegacyFirstDayCompleted ||
					s.EmailOpsReady || s.CalendarReady || s.ProjectWorkspaces > 0
			},
			Resolve: resolveConnectSource,
		},
		{
			ID: FirstBriefQuestID, Tier: 1, Featured: true, Order: 4, Optional: true,
			LockedUntil: MeetAssistantQuestID,
			Title:       "Read your first Daily Brief",
			Why:         firstBriefWhy,
			ActionURL:   "/",
			ActionLabel: "Open Today",
			Satisfied:   func(s Snapshot) bool { return s.HasBriefRevision },
			Resolve:     resolveFirstBrief,
		},
		retier("t1-first-message", 2),
		retier("t1-personalize", 2),
		retier("t2-create-note", 2),
		retier("t2-run-task", 2),
	}
	for _, q := range BuiltinQuests() {
		if q.Tier >= 3 {
			q.Retired = true
			quests = append(quests, q)
		}
	}

	names := builtinTierNames()
	names[1] = "Starter"
	names[2] = "Daily loop"
	return Graph{Quests: quests, TierNames: names, TotalTiers: 1}
}

// firstBriefWhy is Mission 05's static why line.
const firstBriefWhy = "Ori pulls your priorities, follow-ups, and anything you connected into one morning brief."

// resolveFirstBrief tells a user with no model that one is needed before a
// brief can be generated (PRD FR21). The mission stays optional, so this never
// locks the next tier.
func resolveFirstBrief(ctx MissionContext) MissionPresentation {
	if ctx.ModelConfigured {
		return MissionPresentation{}
	}
	return MissionPresentation{Hint: "Add a model in Settings to generate one."}
}

// ConnectSourceBranch is the destination Mission 04 offers a user.
type ConnectSourceBranch string

// The Mission 04 branches, in priority order.
const (
	BranchEmail    ConnectSourceBranch = "email"
	BranchCalendar ConnectSourceBranch = "calendar"
	BranchProject  ConnectSourceBranch = "project"
	BranchPlan     ConnectSourceBranch = "plan"
)

// CalendarOpsCreateURL opens the unified creator with Calendar Ops preselected.
const CalendarOpsCreateURL = "/?create=1&blueprint=calendar-ops"

// ProjectWorkspaceCreateURL opens the unified creator on its blueprint step.
const ProjectWorkspaceCreateURL = "/?create=1"

// ChooseConnectSourceBranch picks Mission 04's destination from the focus
// areas chosen at hire (PRD FR13). When several match, the source the user
// asked for help with and that brings the most outside signal wins: email,
// then calendar, then a project workspace. Everything else, including
// "something else" and no focus at all, plans the first day.
//
// Any branch completes the mission; this only decides which one the card
// offers.
func ChooseConnectSourceBranch(focus []string) ConnectSourceBranch {
	has := map[string]bool{}
	for _, value := range focus {
		has[strings.TrimSpace(value)] = true
	}
	switch {
	case has["help_with_email"]:
		return BranchEmail
	case has["prepare_for_meetings"]:
		return BranchCalendar
	case has["keep_projects_moving"]:
		return BranchProject
	default:
		return BranchPlan
	}
}

// resolveConnectSource presents Mission 04 as the branch the hire's focus
// chose. The plan branch is the quest's static copy, so it changes nothing.
func resolveConnectSource(ctx MissionContext) MissionPresentation {
	switch ChooseConnectSourceBranch(ctx.FocusAreas) {
	case BranchEmail:
		url := strings.TrimSpace(ctx.EmailQuestURL)
		if url == "" {
			// No guided email setup is wired: offer the plan rather than a dead link.
			return MissionPresentation{}
		}
		presentation := MissionPresentation{
			Title:       "Set up email",
			Why:         "So your brief can show what is waiting on you.",
			ActionURL:   url,
			ActionLabel: "Start",
		}
		if ctx.EmailQuestStarted {
			presentation.ActionLabel = "Resume"
			presentation.InProgress = true
		}
		return presentation
	case BranchCalendar:
		return MissionPresentation{
			Title:       "Connect your calendar",
			Why:         "So your brief can prepare you for today's meetings.",
			ActionURL:   CalendarOpsCreateURL,
			ActionLabel: "Start",
		}
	case BranchProject:
		// Starting a project workspace is what Show your assistant a folder
		// does now, so this branch presents the plan and still completes from
		// a project workspace (FR41).
		return MissionPresentation{}
	default:
		return MissionPresentation{}
	}
}

// starterTemplateIDs are the blueprints that are not a "project workspace" for
// Mission 04: Personal HQ and the other starter destinations.
var starterTemplateIDs = map[string]bool{
	"personal-ops":      true,
	"file-janitor":      true,
	"downloads-janitor": true,
	"email-ops":         true,
	"calendar-ops":      true,
}

// IsProjectWorkspaceCreated reports whether a workspace.created event is the
// project branch of Mission 04: a workspace (not a group) from the unified
// creator whose blueprint is blank or not a starter one. An event without a
// template_id key came from another producer and never counts.
func IsProjectWorkspaceCreated(ev ws.Event) bool {
	if ev.Type != ws.EventWorkspaceCreated || ev.Data == nil {
		return false
	}
	raw, present := ev.Data["template_id"]
	if !present {
		return false
	}
	templateID, isString := raw.(string)
	if !isString || starterTemplateIDs[strings.TrimSpace(templateID)] {
		return false
	}
	return dataString(ev, "kind") != "group"
}

// FolderDigestEntryPoint is the entry_point a workspace.created event carries
// when the workspace was created from the assistant's folder offer: the
// creator tags it only for a create that named an offer.
const FolderDigestEntryPoint = "folder_digest"

// IsFolderDigestWorkspaceCreated reports whether a workspace.created event is
// a workspace the user set up from the assistant's folder offer (FR42).
func IsFolderDigestWorkspaceCreated(ev ws.Event) bool {
	return ev.Type == ws.EventWorkspaceCreated && ev.Data != nil &&
		dataString(ev, "entry_point") == FolderDigestEntryPoint
}

// IsStarterTemplateID reports whether a template ID is one of the starter
// destinations that do not count as a project workspace.
func IsStarterTemplateID(templateID string) bool {
	return starterTemplateIDs[strings.TrimSpace(templateID)]
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
			// "Ori" elsewhere in this file means the product. Here it means the
			// assistant you message — which is now the same thing, since Issue
			// #350 merged the guide and the working assistant into Ask Ori.
			Title: "Send your first request",
			Why:   "Ask Ori for something from any page — it's the fastest way to see what Ori can do.",
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
			ActionURL:   "/?create=1",
			ActionLabel: "Create a workspace",
		},
		{
			ID: "t2-build-hq", Tier: 2,
			Title:     "Build My HQ",
			Why:       "Give Ori a home base — a place to prepare your daily brief, track follow-ups, and help you resume work.",
			Optional:  true,
			Satisfied: func(s Snapshot) bool { return s.HasPersonalHQ },
			// Build My HQ is featured from the Home progression panel even before
			// Tier 2 unlocks. Route to the Map's unbuilt HQ landmark so setup stays
			// grounded in the user's actual workspace landscape.
			ActionURL:   "/?focus=personal-hq",
			ActionLabel: "Build My HQ",
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
