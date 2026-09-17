package filejanitor

import (
	"strconv"
)

// ActivityPhase is where an unattended scan is in the map's show
// (tasks/prd-task-run-show.md FR10).
type ActivityPhase string

const (
	ActivityStarted  ActivityPhase = "started"
	ActivityFinished ActivityPhase = "finished"
)

// Activity is what the map is told about one unattended scan: that it
// started, and how it finished. Ids and a count only — never a file name, a
// path, or an error message.
type Activity struct {
	Phase       ActivityPhase
	WorkspaceID string
	// ActivityID is the same on the scan's start and finish.
	ActivityID string
	Source     ScanSource
	// Outcome is set on finish: succeeded or failed.
	Outcome string
	// BatchID is the review batch the scan created, when it created one.
	BatchID string
	// Count is the number of new proposals waiting for review. Zero when the
	// scan found nothing new.
	Count int
}

// ActivityPublisher receives a scan's start and finish. The server binds it to
// the workspace event bus.
type ActivityPublisher interface {
	PublishJanitorActivity(Activity)
}

// SetActivityPublisher wires the map's activity feed. nil publishes nothing.
// Set before Start.
func (a *Automation) SetActivityPublisher(publisher ActivityPublisher) {
	if a == nil {
		return
	}
	a.activityPublisher = publisher
}

// ActivityPublisherWired reports whether a publisher is bound (builder check).
func (a *Automation) ActivityPublisherWired() bool {
	return a != nil && a.activityPublisher != nil
}

func (a *Automation) publishActivity(activity Activity) {
	if a == nil || a.activityPublisher == nil {
		return
	}
	a.activityPublisher.PublishJanitorActivity(activity)
}

// scanActivityID names one unattended scan. A workspace runs at most one scan
// at a time (FR-36), so the start time is enough to tell runs apart.
func (a *Automation) scanActivityID(workspaceID string) string {
	return workspaceID + ":" + strconv.FormatInt(a.now().UnixNano(), 10)
}
