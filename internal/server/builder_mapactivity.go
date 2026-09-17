package server

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mapactivity"
	"github.com/johnjallday/ori-agent/internal/mapactivityhttp"
)

// wireMapActivity builds the task-run show's activity tracker and its HTTP
// handler (tasks/prd-task-run-show.md §4.1).
//
// It runs right after initializeEventSystem (Phase 19) because the tracker's
// whole input is the bus built there. Called any earlier it would capture a nil
// bus and silently show nothing, which is the Phase-19 wiring trap this repo
// has hit before; TestServerBuilder_Build_Integration asserts it through
// MapActivityWired.
//
// The feature flag is read per request by the handler rather than here, so the
// tracker is always built: turning ORI_MAP_SHOW_ENABLED back on needs no rewire,
// only a restart-free flag read.
func (b *ServerBuilder) wireMapActivity() {
	if b.eventBus == nil {
		logger.Warn("Map activity feed not wired: no event bus", logger.Fields{})
		b.mapActivityHandler = mapactivityhttp.NewHandler(nil)
		return
	}
	b.mapActivityTracker = mapactivity.NewTracker(b.eventBus)
	b.mapActivityHandler = mapactivityhttp.NewHandler(b.mapActivityTracker)
}

// MapActivityWired reports whether the activity tracker was built over the real
// event bus and its handler can serve it (builder integration check).
func (b *ServerBuilder) MapActivityWired() bool {
	return b.mapActivityTracker != nil && b.mapActivityHandler.Wired()
}
