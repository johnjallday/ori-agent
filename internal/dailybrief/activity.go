package dailybrief

// ActivityPhase is where a generation is in the map's show
// (tasks/prd-task-run-show.md FR9).
type ActivityPhase string

const (
	ActivityStarted  ActivityPhase = "started"
	ActivityFinished ActivityPhase = "finished"
)

// Activity is what the map is told about one generation: that it started, and
// how it finished. It carries ids and the local date only — never brief content
// or the reason a generation failed.
type Activity struct {
	Phase       ActivityPhase
	WorkspaceID string
	// ActivityID is the generation request id, the same on start and finish.
	ActivityID string
	Trigger    Trigger
	LocalDate  string
	// Outcome is set on finish: succeeded, partial, or failed.
	Outcome string
	// RevisionID is the revision the generation produced, when it produced one.
	RevisionID string
}

// ActivityPublisher receives a generation's start and finish. The server binds
// it to the workspace event bus; this package never imports the bus.
type ActivityPublisher interface {
	PublishBriefActivity(Activity)
}

// SetActivityPublisher wires the map's activity feed. nil publishes nothing.
func (s *Service) SetActivityPublisher(publisher ActivityPublisher) {
	s.activityPublisher = publisher
}

// ActivityPublisherWired reports whether a publisher is bound (builder check).
func (s *Service) ActivityPublisherWired() bool {
	return s != nil && s.activityPublisher != nil
}

func (s *Service) publishActivity(activity Activity) {
	if s == nil || s.activityPublisher == nil {
		return
	}
	s.activityPublisher.PublishBriefActivity(activity)
}

// activityOutcome is how a generation status reads on the map.
func activityOutcome(status GenerationStatus) string {
	switch status {
	case GenerationSucceeded:
		return "succeeded"
	case GenerationPartial:
		return "partial"
	default:
		return "failed"
	}
}
