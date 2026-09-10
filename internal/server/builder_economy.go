package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/economyhttp"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// initializeEconomy wires the City Economy: the ledger over the shared database,
// the service that owns the earning and pricing rules, the event subscription
// that feeds it, and the one-time backfill that grandfathers an existing install.
//
// It runs in the event-system phase because the subscription needs the bus, and
// because by then the composed workspace store exists — the service reads Farms
// through it, and capturing a nil store earlier would have produced an economy
// that silently reported no Farms and grandfathered nothing.
//
// Failure is contained rather than fatal. Without a database there is nowhere to
// keep a ledger, so nothing is wired: the HTTP routes answer 404, the price check
// waves every save through, and the Home HUD does not render. That is the same
// state as ORI_ECONOMY_ENABLED=false, and it is a much better outcome than
// refusing to start the server over a game mechanic.
func (b *ServerBuilder) initializeEconomy() {
	if !featureflags.EconomyEnabled() {
		logger.Info("City Economy disabled by ORI_ECONOMY_ENABLED", logger.Fields{})
		return
	}
	if b.sessionStore == nil {
		logger.Warn("City Economy not wired: no database", logger.Fields{})
		return
	}
	db := b.sessionStore.DB()
	if db == nil {
		logger.Warn("City Economy not wired: session store has no database", logger.Fields{})
		return
	}

	b.economyStore = economy.NewSQLiteStore(db)
	b.economyService = economy.NewService(b.economyStore, nil, nil, nil)

	// Every dependency below is attached with a setter AFTER both sides exist.
	// Passing them to the constructor would mean capturing whatever they happen
	// to be at this point in the build, and a nil captured there is a silent
	// no-op rather than an error.
	if b.workspaceStore != nil {
		b.economyService.SetTaskSource(economy.NewWorkspaceTasks(b.workspaceStore))
	} else {
		logger.Warn("City Economy has no workspace store: Farms will not be listed", logger.Fields{})
	}

	b.economyHandler = economyhttp.NewHandler(b.economyService)

	// Live earning. Publish already delivers on its own goroutine, so a slow
	// ledger write never holds up chat or a task run, and every write the
	// service makes is idempotent, so a redelivered event costs nothing.
	if b.eventBus != nil {
		service := b.economyService
		b.eventBus.SubscribeToEventTypes(economy.SubscribedEventTypes(), func(ev workspace.Event) {
			service.HandleEvent(ev)
		})
	}

	// One-time backfill, so an install that predates the feature starts with a
	// stock of Craft proportional to work already done and keeps every Farm it
	// already had (FR32, FR33).
	//
	// It counts completed tasks itself rather than reusing scanProgression:
	// that scanner deliberately leaves AgentTasksDone and ChatMessages at zero
	// ("deeper per-workspace counts are not loaded here"), which would have made
	// every grant zero and defeated the point. Populating them there instead
	// would change which onboarding quests an existing install completes on its
	// next restart, which is not this feature's call to make.
	if err := b.economyService.Backfill(context.Background()); err != nil {
		logger.Warn("City Economy backfill failed", logger.Fields{"error": err})
	}
}
