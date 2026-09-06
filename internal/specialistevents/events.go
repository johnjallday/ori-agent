// Package specialistevents defines the local-only, redacted event vocabulary
// shared by specialist setup journeys and their canonical consequence owners.
package specialistevents

import (
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Name is one closed local specialist-setup event.
type Name string

const (
	JourneyOpened           Name = "specialist_setup.opened"
	JourneyDismissed        Name = "specialist_setup.dismissed"
	JourneyResumed          Name = "specialist_setup.resumed"
	IntegrationReviewOpened Name = "specialist_setup.integration_review_opened"
	IntegrationOutcome      Name = "specialist_setup.integration_outcome"
	ProjectRouteSelected    Name = "specialist_setup.project_route_selected"
	ProjectOutcome          Name = "specialist_setup.project_outcome"
	ModeSelected            Name = "specialist_setup.mode_selected"
	LiveVerifyOutcome       Name = "specialist_setup.live_verify_outcome"
	HomeRoleOutcome         Name = "specialist_setup.home_role_outcome"
	ProjectTeamOutcome      Name = "specialist_setup.project_team_outcome"
	SampleAddonOutcome      Name = "specialist_setup.sample_addon_outcome"
	SampleCapabilityOutcome Name = "specialist_setup.sample_capability_outcome"
	SampleRootOutcome       Name = "specialist_setup.sample_root_outcome"
	SampleAnalysisOutcome   Name = "specialist_setup.sample_analysis_outcome"
	SampleHandoffOutcome    Name = "specialist_setup.sample_handoff_outcome"
	JourneyCompleted        Name = "specialist_setup.completed"
	JourneyRegressed        Name = "specialist_setup.regressed"
)

// Outcome is a closed event result. It is deliberately less detailed than a
// user-facing error, which may contain names or paths.
type Outcome string

const (
	OutcomeReviewOpened      Outcome = "review_opened"
	OutcomeSelected          Outcome = "selected"
	OutcomeSucceeded         Outcome = "succeeded"
	OutcomeFailed            Outcome = "failed"
	OutcomeReconcileRequired Outcome = "reconcile_required"
	OutcomeDeclined          Outcome = "declined"
	OutcomeRevoked           Outcome = "revoked"
)

// Fields is the complete permitted event payload. It has no free-text field,
// user/relationship identity, path, project/folder/agent/sample name, prompt,
// credential, file/audio value, runtime-state body, or plugin manifest body.
type Fields struct {
	JourneyID          string
	StepID             string
	ActionID           string
	ResourceID         string
	RoleID             string
	RouteToken         string
	ModeToken          string
	RunKind            string
	Lifecycle          string
	Outcome            Outcome
	ReasonCode         string
	SchemaVersion      int
	DeclarationVersion int
	DurationSeconds    int64
	Count              int
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:-]{0,127}$`)

var forbiddenTokenFragments = []string{
	"secret", "password", "credential", "api_key", "apikey", "bearer", "token=", "sk-",
}

var knownEvents = map[Name]struct{}{
	JourneyOpened: {}, JourneyDismissed: {}, JourneyResumed: {},
	IntegrationReviewOpened: {}, IntegrationOutcome: {}, ProjectRouteSelected: {}, ProjectOutcome: {},
	ModeSelected: {}, LiveVerifyOutcome: {}, HomeRoleOutcome: {}, ProjectTeamOutcome: {},
	SampleAddonOutcome: {}, SampleCapabilityOutcome: {}, SampleRootOutcome: {}, SampleAnalysisOutcome: {},
	SampleHandoffOutcome: {}, JourneyCompleted: {}, JourneyRegressed: {},
}

var knownOutcomes = map[Outcome]struct{}{
	OutcomeReviewOpened: {}, OutcomeSelected: {}, OutcomeSucceeded: {}, OutcomeFailed: {},
	OutcomeReconcileRequired: {}, OutcomeDeclined: {}, OutcomeRevoked: {},
}

// emitEvent is indirected so tests inspect the exact map sent to the existing
// local logger. This package does not add an outbound analytics transport.
var emitEvent = func(fields logger.Fields) {
	logger.Info("Specialist setup event", fields)
}

// Record emits one bounded local event. Invalid token-like values are omitted
// rather than logging untrusted data.
func Record(name Name, fields Fields) {
	if _, ok := knownEvents[name]; !ok {
		return
	}
	values := logger.Fields{"event": string(name)}
	addToken(values, "journey_id", fields.JourneyID)
	addToken(values, "step_id", fields.StepID)
	addToken(values, "action_id", fields.ActionID)
	addToken(values, "resource_id", fields.ResourceID)
	addToken(values, "role_id", fields.RoleID)
	addToken(values, "route_token", fields.RouteToken)
	addToken(values, "mode_token", fields.ModeToken)
	addToken(values, "run_kind", fields.RunKind)
	addToken(values, "lifecycle", fields.Lifecycle)
	addToken(values, "reason_code", fields.ReasonCode)
	if _, ok := knownOutcomes[fields.Outcome]; ok {
		values["outcome"] = string(fields.Outcome)
	}
	if fields.SchemaVersion > 0 && fields.SchemaVersion <= 1_000_000 {
		values["schema_version"] = fields.SchemaVersion
	}
	if fields.DeclarationVersion > 0 && fields.DeclarationVersion <= 1_000_000 {
		values["declaration_version"] = fields.DeclarationVersion
	}
	if fields.DurationSeconds >= 0 && fields.DurationSeconds <= 31_536_000 {
		values["duration_seconds"] = fields.DurationSeconds
	}
	if fields.Count >= 0 && fields.Count <= 1_000_000 {
		values["count"] = fields.Count
	}
	emitEvent(values)
}

func addToken(fields logger.Fields, key, value string) {
	value = strings.TrimSpace(value)
	if !safeToken.MatchString(value) {
		return
	}
	lower := strings.ToLower(value)
	for _, forbidden := range forbiddenTokenFragments {
		if strings.Contains(lower, forbidden) {
			return
		}
	}
	fields[key] = value
}
