package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/progressionhttp"
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

	engine := progression.New(b.onboardingMgr, progression.WithOnComplete(func(q progression.Quest) {
		logger.Info("Onboarding quest completed", logger.Fields{"quest": q.ID, "tier": q.Tier})
	}))
	b.progressionEngine = engine

	// Live detection: forward every event to the engine. Publish already
	// delivers on its own goroutine, so this never blocks producers.
	b.eventBus.Subscribe(func(ev workspace.Event) {
		engine.HandleEvent(ev)
	}, nil)

	// Renaming the assistant is not an event; complete the quest directly.
	if b.onboardingHandler != nil {
		b.onboardingHandler.SetOnNamesSaved(func(assistantName string) {
			if assistantName != "" && assistantName != onboarding.DefaultAssistantName {
				engine.Complete("t1-personalize")
			}
		})
	}

	// One-time backfill so established installs are grandfathered silently.
	if err := engine.Backfill(progression.ScannerFunc(b.scanProgression)); err != nil {
		logger.Warn("Onboarding progression backfill failed", logger.Fields{"error": err})
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

	if _, assistantName := b.onboardingMgr.GetNames(); assistantName != "" && assistantName != onboarding.DefaultAssistantName {
		snap.AssistantRenamed = true
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
