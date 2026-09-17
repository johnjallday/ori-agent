package server

import (
	"errors"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mapactivity"
	"github.com/johnjallday/ori-agent/internal/mapactivityhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// taskXPAwarder adapts the evolution service to the executor's narrow awarder,
// so the workspace package never imports evolution. It carries the award report
// through unchanged (task-run-show FR38).
type taskXPAwarder struct {
	service *evolution.Service
}

func (a taskXPAwarder) AwardTaskXP(agentName string) (workspace.TaskXPAward, error) {
	award, err := a.service.AwardTaskXP(agentName)
	return workspace.TaskXPAward{
		Amount:         award.Amount,
		LevelBefore:    award.LevelBefore,
		LevelAfter:     award.LevelAfter,
		ProgressBefore: award.ProgressBefore,
		ProgressAfter:  award.ProgressAfter,
		StageBefore:    string(award.StageBefore),
		StageAfter:     string(award.StageAfter),
	}, err
}

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
	b.wireResultParcels()
}

// wireResultParcels gives the tracker somewhere to keep finished runs' results
// (task-run-show §4.6). Without a database there is nowhere to keep them: the
// feed still lights buildings, and no parcels are made.
func (b *ServerBuilder) wireResultParcels() {
	if b.sessionStore == nil || b.sessionStore.DB() == nil {
		logger.Warn("Result parcels not wired: no database", logger.Fields{})
		return
	}
	b.mapActivityTracker.SetParcels(mapactivity.ParcelOptions{
		Store: mapactivity.NewSQLiteParcelStore(b.sessionStore.DB()),
		// Read through the builder at call time: the store is final by then,
		// and a closure cannot capture a store that is replaced later.
		Tasks: parcelTaskFacts{builder: b},
		EconomyEnabled: func() bool {
			return featureflags.EconomyEnabled() && b.economyService.Available()
		},
		WorkspaceIDs: func() ([]string, error) {
			if b.workspaceStore == nil {
				return nil, errors.New("workspace store is not wired")
			}
			return b.workspaceStore.List()
		},
	})
	if b.economyService != nil {
		b.mapActivityHandler.SetCraftLookup(b.economyService)
	}
}

// parcelTaskFacts reads one task's title, summary and start for its parcel.
type parcelTaskFacts struct {
	builder *ServerBuilder
}

func (f parcelTaskFacts) TaskFacts(workspaceID, taskID string) (mapactivity.TaskFacts, bool) {
	store := f.builder.workspaceStore
	if store == nil {
		return mapactivity.TaskFacts{}, false
	}
	ws, err := store.Get(workspaceID)
	if err != nil || ws == nil {
		return mapactivity.TaskFacts{}, false
	}
	task, err := ws.GetTask(taskID)
	if err != nil || task == nil {
		return mapactivity.TaskFacts{}, false
	}
	return mapactivity.TaskFactsFrom(task), true
}

// briefActivityPublisher puts a Daily Brief generation on the workspace bus as
// an activity (task-run-show FR8, FR9). Only ids, the trigger and the local date
// travel; brief content and failure text never do.
type briefActivityPublisher struct {
	bus *workspace.EventBus
}

func (p briefActivityPublisher) PublishBriefActivity(activity dailybrief.Activity) {
	data := map[string]any{
		"trigger":    string(activity.Trigger),
		"local_date": activity.LocalDate,
	}
	eventType := workspace.EventActivityStarted
	if activity.Phase == dailybrief.ActivityFinished {
		eventType = workspace.EventActivityFinished
		data["outcome"] = activity.Outcome
		if activity.RevisionID != "" {
			data["ref_id"] = activity.RevisionID
		}
	}
	p.bus.Publish(workspace.NewActivityEvent(eventType, activity.WorkspaceID, string(mapactivity.KindDailyBrief), activity.ActivityID, data))
}

// janitorActivityPublisher does the same for a File Janitor watcher or daily
// scan (FR10). The count is how many new proposals wait for review.
type janitorActivityPublisher struct {
	bus *workspace.EventBus
}

func (p janitorActivityPublisher) PublishJanitorActivity(activity filejanitor.Activity) {
	data := map[string]any{}
	eventType := workspace.EventActivityStarted
	if activity.Phase == filejanitor.ActivityFinished {
		eventType = workspace.EventActivityFinished
		data["outcome"] = activity.Outcome
		data["count"] = activity.Count
		if activity.BatchID != "" {
			data["ref_id"] = activity.BatchID
		}
	}
	p.bus.Publish(workspace.NewActivityEvent(eventType, activity.WorkspaceID, string(mapactivity.KindFileJanitor), activity.ActivityID, data))
}

// DailyBriefActivityWired reports whether Daily Brief generations reach the map.
// The brief is built in Phase 22.6, long after the bus; binding it anywhere
// earlier would capture a nil service.
func (b *ServerBuilder) DailyBriefActivityWired() bool {
	return b.dailyBriefService != nil && b.dailyBriefService.ActivityPublisherWired()
}

// FileJanitorActivityWired reports whether unattended janitor scans reach the map.
func (b *ServerBuilder) FileJanitorActivityWired() bool {
	return b.fileJanitorAutomation != nil && b.fileJanitorAutomation.ActivityPublisherWired()
}

// MapActivityParcelsWired reports whether finished runs become parcels.
func (b *ServerBuilder) MapActivityParcelsWired() bool {
	return b.mapActivityTracker != nil && b.mapActivityTracker.ParcelsWired()
}

// MapActivityWired reports whether the activity tracker was built over the real
// event bus and its handler can serve it (builder integration check).
func (b *ServerBuilder) MapActivityWired() bool {
	return b.mapActivityTracker != nil && b.mapActivityHandler.Wired()
}
