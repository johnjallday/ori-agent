package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/progressionhttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// initializeProgression wires the onboarding quest-log: it builds the engine
// (persisting through the onboarding manager), subscribes it to the event bus,
// and connects the hooks whose owners already exist in Phase 19 (personalize
// and Personal HQ designation).
//
// The personal assistant, its first-assignment handler, the setup journey,
// and the Daily Brief are built later, in initializeDailyBrief (Phase 22.6).
// Everything that reads or hooks them, including the one-time backfill, runs
// in completeProgressionWiring after that phase. Wiring them here bound nil:
// the first-day hook and its reconcile were silently never installed on a
// real server.
func (b *ServerBuilder) initializeProgression() {
	if b.onboardingMgr == nil || b.eventBus == nil {
		return
	}

	engine := progression.New(
		b.onboardingMgr,
		progression.WithGraph(progression.PersonalAssistantGraph()),
		// The starter missions resolve their card from per-user state read at
		// status time (focus areas, File Janitor setup, email setup, model).
		progression.WithMissionContext(b.starterMissionContext),
		// What each quest pays, for the quest log to display. The amounts live
		// in the economy's own tuning file; progression only renders them.
		progression.WithRewards(economy.StarterQuestCraft),
		progression.WithOnComplete(func(q progression.Quest) {
			logger.Info("Onboarding quest completed", logger.Fields{"quest": q.ID, "tier": q.Tier})
			// The early quests pay Craft, which is what makes the City Economy
			// reachable on a fresh install (city-economy Group 7).
			//
			// b.economyService is read HERE rather than captured, because the
			// economy is initialized after this function runs — capturing it now
			// would bind a nil forever. A quest completes long after the build,
			// so by then it is set, and a nil is a safe no-op besides.
			//
			// This fires only for a LIVE completion: the engine's Backfill marks
			// existing state complete without calling back, so an established
			// install is never paid twice for history the economy's own backfill
			// already counted.
			if awarded, ok := b.economyService.AwardQuestCraft(context.Background(), q.ID); ok {
				logger.Info("Quest reward paid", logger.Fields{"quest": q.ID, "craft": awarded})
			}
		}),
	)
	b.progressionEngine = engine

	// Live detection: forward every event to the engine. Publish already
	// delivers on its own goroutine, so this never blocks producers.
	b.eventBus.Subscribe(func(ev workspace.Event) {
		engine.HandleEvent(ev)
	}, nil)

	// Filling out the profile is not an event; complete the quest directly.
	if b.smartOnboardingHandler != nil {
		b.smartOnboardingHandler.SetOnPersonalized(func() {
			engine.Complete("t1-personalize")
		})
	}

	// A workspace becoming the user's Personal HQ is not an event either;
	// this fires for both Build My HQ (a new workspace) and designating an
	// existing workspace (PRD FR48/FR49), so t2-build-hq completes either
	// way without replaying unrelated quests.
	//
	// Designation is the ONLY thing that completes this quest. Hiring, creating
	// the assistant profile, opening the guided quest, selecting the reserved
	// site, and opening the build form all deliberately leave it open.
	if b.personalHQService != nil {
		b.personalHQService.SetOnDesignated(func(ctx context.Context, userID, workspaceID string) {
			engine.Complete(progression.BuildHQQuestID)
		})
	}

	b.progressionHandler = progressionhttp.NewHandler(engine)
}

// starterMissionsReconcileKey names the one-time grandfathering pass for the
// starter missions. Never change it: a new key re-runs the pass.
const starterMissionsReconcileKey = "starter-missions-v1"

// meetAssistantReconcileKey names the one-time grandfathering pass for Meet
// your assistant. Never change it: a new key re-runs the pass.
const meetAssistantReconcileKey = "meet-assistant-v1"

// completeProgressionWiring installs the progression hooks whose owners are
// built in initializeDailyBrief, then runs the one-time backfill and the
// startup reconcile. Call it after that phase. Safe when progression was not
// initialized.
func (b *ServerBuilder) completeProgressionWiring() {
	engine := b.progressionEngine
	if engine == nil {
		return
	}

	// Meet your assistant (Mission 01) completes from the durable hire, never
	// from a browser claim: the request that made the hire durable, or a repair
	// that reconnected a hired assistant. Bound here, not in
	// initializeProgression, because the handler does not exist until 22.6.
	//
	// Connect one source (Mission 04) completes from ANY branch (PRD FR14).
	//
	// Plan: a first-assignment apply has its own atomic durability boundary.
	// Progression observes only the successful result.
	if b.personalAssistantHandler != nil {
		b.personalAssistantHandler.SetOnHired(func() {
			engine.Complete(progression.MeetAssistantQuestID)
		})
		b.personalAssistantHandler.SetOnFirstAssignmentCompleted(func() {
			engine.Complete(progression.ConnectSourceQuestID)
		})
	}
	// Email: the guided Email Ops setup first reaching ready.
	if b.setupJourneyService != nil {
		b.setupJourneyService.SetOnFirstReady(onEmailSetupFirstReady(engine))
	}
	// Calendar: a ready calendar binding on a Calendar Ops workspace. Project:
	// the engine's own Match on workspace.created.
	if b.eventBus != nil {
		b.eventBus.SubscribeToEventType(workspace.EventWorkspaceUpdated, func(ev workspace.Event) {
			if calendarBindingConnected(b.starterWorkspaces(), ev) {
				engine.Complete(progression.ConnectSourceQuestID)
			}
		})
	}

	// Read your first Daily Brief (Mission 05): Today served with a brief.
	if b.personalAssistantToday != nil {
		b.personalAssistantToday.SetOnBriefSeen(func(string) {
			engine.Complete(progression.FirstBriefQuestID)
		})
	}

	// Installs whose backfill ran before the starter missions existed get one
	// silent grandfathering pass for them (PRD FR44): a ready File Janitor, a
	// connected source, or an existing brief shows as done, with no toast
	// storm and no Craft paid for past work. Runs before Backfill, which covers
	// a fresh install on its own.
	scanner := progression.ScannerFunc(b.scanProgression)
	if marked, err := engine.ReconcileOnce(starterMissionsReconcileKey, scanner,
		progression.TidyDownloadsQuestID, progression.ConnectSourceQuestID, progression.FirstBriefQuestID,
	); err != nil {
		logger.Warn("Starter missions reconcile failed", logger.Fields{"error": err})
	} else if marked > 0 {
		logger.Info("Starter missions grandfathered", logger.Fields{"quests": marked})
	}
	// Installs that hired their assistant before Meet your assistant existed
	// see it complete, silently and without Craft (PRD FR34).
	if marked, err := engine.ReconcileOnce(meetAssistantReconcileKey, scanner, progression.MeetAssistantQuestID); err != nil {
		logger.Warn("Meet your assistant reconcile failed", logger.Fields{"error": err})
	} else if marked > 0 {
		logger.Info("Meet your assistant grandfathered", logger.Fields{"quests": marked})
	}

	// One-time backfill so established installs are grandfathered silently.
	if err := engine.Backfill(scanner); err != nil {
		logger.Warn("Onboarding progression backfill failed", logger.Fields{"error": err})
	}
	// Meet your assistant gates every other mission, so it must never stay open
	// while an assistant is hired: after a quest reset, or if the process died
	// between the hire's commit and its hook. The hire cannot be repeated, so
	// nothing else could ever complete it. The passes above ran first, so an
	// upgraded install is already marked here and is not paid; the economy
	// pays a quest at most once besides.
	if b.assistantHired() {
		engine.Complete(progression.MeetAssistantQuestID)
	}
	// Reconcile installs whose one-time progression backfill predates this quest.
	// Complete is idempotent, and the widget suppresses announcements on its first
	// status load, so a restart cannot replay the first-day flow or toast old work.
	if b.personalAssistantService != nil {
		if state, err := b.personalAssistantService.Get(context.Background(), userprofile.LocalUserID); err == nil && state.FirstAssignment == personalassistant.FirstAssignmentCompleted {
			engine.Complete(progression.ConnectSourceQuestID)
		}
	}
}

// scanProgression gathers a best-effort Snapshot of existing state for the
// backfill scan. It covers the cheap, high-value counts (workspaces, agents,
// notes, assistant rename); deeper per-workspace counts are not loaded here, so
// those quests complete live for everyone going forward.
func (b *ServerBuilder) scanProgression() progression.Snapshot {
	var snap progression.Snapshot

	var workspaceIDs []string
	if b.workspaceStore != nil {
		if ids, err := b.workspaceStore.List(); err == nil {
			workspaceIDs = ids
			snap.Workspaces = len(ids)
		}
	}

	if b.st != nil {
		snap.Agents = len(b.st.ListAgents())
	}

	if profile := b.onboardingMgr.GetUserProfile(); profile != nil && !profile.PersonalizedAt.IsZero() {
		snap.Personalized = true
	}

	hqWorkspaceID := ""
	if b.personalHQService != nil {
		if status, err := b.personalHQService.Status(context.Background(), userprofile.LocalUserID); err == nil && status.Valid {
			snap.HasPersonalHQ = true
			hqWorkspaceID = status.WorkspaceID
		}
	}

	if b.personalAssistantService != nil {
		if state, err := b.personalAssistantService.Get(context.Background(), userprofile.LocalUserID); err == nil {
			snap.FirstAssignmentCompleted = state.FirstAssignment == personalassistant.FirstAssignmentCompleted
		}
	}

	// Mission 01: the assistant is already hired.
	snap.AssistantHired = b.assistantHired()

	// Mission 03: a File Janitor workspace whose setup already reached ready.
	if _, ready, ok := findJanitorWorkspace(b.starterWorkspaces()); ok {
		snap.FileJanitorReady = ready
	}

	// Mission 04: any source already connected, on any branch.
	snap.EmailOpsReady = b.emailSetupEverReady()
	snap.CalendarReady, snap.ProjectWorkspaces = scanStarterWorkspaces(b.starterWorkspaces(), hqWorkspaceID)
	if b.progressionEngine != nil {
		snap.LegacyFirstDayCompleted = b.progressionEngine.HasCompleted(progression.PersonalAssistantFirstDayQuestID)
	}

	// Mission 05: HQ already has a Daily Brief revision.
	if b.dailyBriefService != nil {
		snap.HasBriefRevision = briefRevisionExists(b.dailyBriefService, hqWorkspaceID)
	}

	// Count notes only until we find one — the quest just needs "> 0".
	if b.sessionStore != nil {
		ctx := context.Background()
		for _, id := range workspaceIDs {
			notes, err := b.sessionStore.ListNotesByWorkspace(ctx, id)
			if err != nil {
				continue
			}
			if len(notes) > 0 {
				snap.Notes = len(notes)
				break
			}
		}
	}

	return snap
}

// assistantHired reports whether the local user's relationship owns a hired
// assistant profile (awaiting_hq, provisioning_hq, active, or paused).
//
// It reads the persisted relationship rather than the read projection: an
// active assistant whose HQ link fails validation projects as repair_needed,
// but it was still hired, and Meet your assistant must not reopen and lock
// every other mission because of it.
func (b *ServerBuilder) assistantHired() bool {
	if b.personalAssistantStore == nil {
		return false
	}
	state, err := b.personalAssistantStore.GetState(context.Background(), userprofile.LocalUserID)
	return err == nil && state != nil && state.Status.HasOwnedProfile()
}
