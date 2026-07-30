package setupwizard

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Setup wizard event names. They are the vocabulary for answering the questions
// this feature exists to answer — do people finish setup, where do they stop,
// what breaks afterwards — without recording anything about the person or their
// data.
const (
	EventApplicable       = "setup_wizard.applicable"
	EventFirstOpened      = "setup_wizard.first_opened"
	EventResumed          = "setup_wizard.resumed"
	EventDismissed        = "setup_wizard.dismissed"
	EventStepCompleted    = "setup_wizard.step_completed"
	EventStepFailed       = "setup_wizard.step_failed"
	EventCompleted        = "setup_wizard.completed"
	EventRegressed        = "setup_wizard.regressed"
	EventRepairOpened     = "setup_wizard.repair_opened"
	EventRepairCompleted  = "setup_wizard.repair_completed"
	EventMigrated         = "setup_wizard.migrated"
	EventSnapshotRefused  = "setup_wizard.snapshot_refused"
	eventFieldName        = "event"
	eventFieldBlueprint   = "blueprint_id"
	eventFieldVersion     = "wizard_version"
	eventFieldStepKind    = "step_kind"
	eventFieldAdapter     = "adapter"
	eventFieldCategory    = "error_category"
	eventFieldDurationSec = "duration_seconds"
)

// The fields above are the entire permitted vocabulary, and each is safe by
// construction:
//
//   - blueprint_id and adapter are authored identifiers, fixed in a manifest and
//     in this build's registry. They name a kind of workspace, never one.
//   - wizard_version and step_kind come from the schema.
//   - error_category is one of the five stable categories in registry.go.
//   - duration_seconds is elapsed time.
//
// Deliberately absent: workspace ID, workspace name, step summary text, folder
// path, calendar or mailbox name, account identifier, connector name, project
// filename, and every adapter-produced sentence. A summary is written for a
// person looking at their own screen and routinely names their folder or their
// account; it must never become a log line. redaction_test.go enforces this
// against the emitted fields rather than trusting the call sites.

// emitEvent is the single exit point for every setup event, indirected through
// a variable so the redaction test can capture exactly what would be written
// rather than asserting against the call sites that produce it.
var emitEvent = func(name string, fields logger.Fields) {
	logger.Info("Setup wizard event", fields)
}

// event emits one structured setup event. Emission is logging, not a metrics
// pipeline: this is the observability the PRD asks for, and putting it behind
// the existing logger keeps it in one place with one redaction rule.
func (s *Service) event(name string, fields logger.Fields) {
	if fields == nil {
		fields = logger.Fields{}
	}
	fields[eventFieldName] = name
	emitEvent(name, fields)
}

// wizardFields is the identity every event carries: which blueprint, which
// schema version. Both are authored constants.
func wizardFields(resolved resolvedWizard) logger.Fields {
	fields := logger.Fields{eventFieldVersion: resolved.wizard.Version}
	if resolved.provenance != nil {
		if id := strings.TrimSpace(resolved.provenance.TemplateID); id != "" {
			fields[eventFieldBlueprint] = id
		}
	}
	return fields
}

// emitTransitions reports what changed between two evaluations of one
// workspace's setup.
//
// It is derived from the persisted records rather than sprinkled through the
// call sites, so an event cannot drift from the state it claims to describe:
// if the workspace did not actually move, nothing is emitted.
func (s *Service) emitTransitions(resolved resolvedWizard, previous, next *workspace.SetupWizardProgress, readiness map[string]StepReadiness) {
	if s == nil || next == nil {
		return
	}
	base := func() logger.Fields { return wizardFields(resolved) }

	if previous == nil {
		if next.WasMigrated() {
			s.event(EventMigrated, base())
		} else {
			s.event(EventApplicable, base())
		}
	}

	if previous.HasBeenOpened() != next.HasBeenOpened() && next.HasBeenOpened() {
		if previousState(previous) == workspace.SetupWizardStateNeedsAttention {
			s.event(EventRepairOpened, base())
		} else {
			s.event(EventFirstOpened, base())
		}
	} else if wasDismissed(previous) && !next.IsDismissed() {
		s.event(EventResumed, base())
	}
	if !wasDismissed(previous) && next.IsDismissed() {
		s.event(EventDismissed, base())
	}

	for _, step := range resolved.wizard.Steps {
		before, _ := previous.Step(step.ID)
		after, ok := next.Step(step.ID)
		if !ok {
			continue
		}
		beforeStatus := workspace.NormalizeSetupStepStatus(before.Status)
		afterStatus := workspace.NormalizeSetupStepStatus(after.Status)
		if beforeStatus == afterStatus {
			continue
		}
		fields := base()
		fields[eventFieldStepKind] = step.Kind
		if adapter := strings.TrimSpace(step.Adapter); adapter != "" {
			fields[eventFieldAdapter] = adapter
		}
		switch afterStatus {
		case workspace.SetupStepStatusComplete:
			s.event(EventStepCompleted, fields)
		case workspace.SetupStepStatusBlocked:
			// The category, never the adapter's sentence: the category is a fixed
			// token, and the sentence is written for one person's screen.
			if verdict, evaluated := readiness[step.ID]; evaluated && verdict.ErrorCategory != "" {
				fields[eventFieldCategory] = verdict.ErrorCategory
			}
			s.event(EventStepFailed, fields)
		}
	}

	priorState := previousState(previous)
	if priorState == next.State {
		return
	}
	switch next.State {
	case workspace.SetupWizardStateReady:
		fields := base()
		if seconds, ok := elapsed(next); ok {
			fields[eventFieldDurationSec] = seconds
		}
		if priorState == workspace.SetupWizardStateNeedsAttention {
			s.event(EventRepairCompleted, fields)
		} else {
			s.event(EventCompleted, fields)
		}
	case workspace.SetupWizardStateNeedsAttention:
		s.event(EventRegressed, base())
	}
}

// previousState reads the state a workspace was last recorded in. A workspace
// with no record has not started.
func previousState(previous *workspace.SetupWizardProgress) string {
	if previous == nil {
		return workspace.SetupWizardStateNotStarted
	}
	return workspace.NormalizeSetupWizardState(previous.State)
}

func wasDismissed(previous *workspace.SetupWizardProgress) bool {
	return previous != nil && previous.IsDismissed()
}

// elapsed is how long setup took, measured from the wizard's own record. It is
// a duration, not a pair of timestamps: when someone set up their workspace is
// about them, how long the flow takes is about the flow.
func elapsed(progress *workspace.SetupWizardProgress) (float64, bool) {
	if progress == nil || progress.CompletedAt == nil {
		return 0, false
	}
	start := progress.CreatedAt
	if progress.FirstOpenedAt != nil {
		start = *progress.FirstOpenedAt
	}
	if start.IsZero() {
		return 0, false
	}
	seconds := progress.CompletedAt.Sub(start).Seconds()
	if seconds < 0 {
		return 0, false
	}
	return seconds, true
}

// eventFieldAllowlist is the complete set of keys a setup event may carry. The
// redaction test walks every emitted event against it, so adding a field means
// deciding, deliberately, that it is safe to log about every user.
var eventFieldAllowlist = map[string]bool{
	eventFieldName:        true,
	eventFieldBlueprint:   true,
	eventFieldVersion:     true,
	eventFieldStepKind:    true,
	eventFieldAdapter:     true,
	eventFieldCategory:    true,
	eventFieldDurationSec: true,
}
