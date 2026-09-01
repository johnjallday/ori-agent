package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/progressionhttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// initializeProgression wires the onboarding quest-log: it builds the engine
// (persisting through the onboarding manager), subscribes it to the event bus,
// runs the one-time backfill scan, and connects the "personalize" rename hook.
// Safe to call once the event bus and onboarding manager exist.
func (b *ServerBuilder) initializeProgression() {
	if b.onboardingMgr == nil || b.eventBus == nil {
		return
	}

	engine := progression.New(
		b.onboardingMgr,
		progression.WithQuests(progression.PersonalAssistantQuests()),
		progression.WithOnComplete(func(q progression.Quest) {
			logger.Info("Onboarding quest completed", logger.Fields{"quest": q.ID, "tier": q.Tier})
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
	if b.personalHQService != nil {
		b.personalHQService.SetOnDesignated(func(ctx context.Context, userID, workspaceID string) {
			engine.Complete("t2-build-hq")
		})
	}

	// A first-assignment apply has its own atomic durability boundary. Progression
	// observes only the successful result and remains safe to retry independently.
	if b.personalAssistantHandler != nil {
		b.personalAssistantHandler.SetOnFirstAssignmentCompleted(func() {
			engine.Complete(progression.PersonalAssistantFirstDayQuestID)
		})
	}

	// One-time backfill so established installs are grandfathered silently.
	if err := engine.Backfill(progression.ScannerFunc(b.scanProgression)); err != nil {
		logger.Warn("Onboarding progression backfill failed", logger.Fields{"error": err})
	}
	// Reconcile installs whose one-time progression backfill predates this quest.
	// Complete is idempotent, and the widget suppresses announcements on its first
	// status load, so a restart cannot replay the first-day flow or toast old work.
	if b.personalAssistantService != nil {
		if state, err := b.personalAssistantService.Get(context.Background(), userprofile.LocalUserID); err == nil && state.FirstAssignment == personalassistant.FirstAssignmentCompleted {
			engine.Complete(progression.PersonalAssistantFirstDayQuestID)
		}
	}

	b.progressionHandler = progressionhttp.NewHandler(engine)
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

	if b.personalHQService != nil {
		if status, err := b.personalHQService.Status(context.Background(), userprofile.LocalUserID); err == nil && status.Valid {
			snap.HasPersonalHQ = true
		}
	}

	if b.personalAssistantService != nil {
		if state, err := b.personalAssistantService.Get(context.Background(), userprofile.LocalUserID); err == nil {
			snap.FirstAssignmentCompleted = state.FirstAssignment == personalassistant.FirstAssignmentCompleted
		}
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
