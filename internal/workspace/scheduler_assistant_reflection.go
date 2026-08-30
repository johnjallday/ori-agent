package workspace

import (
	"strings"
	"time"
)

func (ts *TaskScheduler) assistantReflectionDue(station *Workspace, now time.Time) bool {
	if ts == nil || ts.assistantReflectionTrigger == nil || station == nil || station.Status != StatusActive {
		return false
	}
	state := station.GetAssistantProgramState()
	if state == nil || !state.Hired || !state.PluginAvailable || state.Declaration == nil ||
		strings.TrimSpace(state.Reflection.ScheduleTaskID) == "" || state.Reflection.NextEligibleAt == nil ||
		strings.TrimSpace(state.Reflection.InFlightRunID) != "" {
		return false
	}
	return !state.Reflection.NextEligibleAt.After(now)
}

func (ts *TaskScheduler) claimAssistantReflection(stationID string) bool {
	if ts == nil || strings.TrimSpace(stationID) == "" {
		return false
	}
	ts.reflectionMu.Lock()
	defer ts.reflectionMu.Unlock()
	if ts.reflectionInFlight == nil {
		ts.reflectionInFlight = make(map[string]bool)
	}
	if ts.reflectionInFlight[stationID] {
		return false
	}
	ts.reflectionInFlight[stationID] = true
	return true
}

func (ts *TaskScheduler) releaseAssistantReflection(stationID string) {
	if ts == nil {
		return
	}
	ts.reflectionMu.Lock()
	delete(ts.reflectionInFlight, stationID)
	ts.reflectionMu.Unlock()
}
