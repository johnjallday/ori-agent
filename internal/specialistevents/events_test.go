package specialistevents

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/logger"
)

func TestVocabularyIsCompleteAndClosed(t *testing.T) {
	t.Parallel()
	wanted := []Name{
		JourneyOpened, JourneyDismissed, JourneyResumed,
		IntegrationReviewOpened, IntegrationOutcome,
		ProjectRouteSelected, ProjectOutcome, ModeSelected, LiveVerifyOutcome,
		HomeRoleOutcome, ProjectTeamOutcome,
		SampleAddonOutcome, SampleCapabilityOutcome, SampleRootOutcome,
		SampleAnalysisOutcome, SampleHandoffOutcome,
		JourneyCompleted, JourneyRegressed,
	}
	for _, name := range wanted {
		if _, ok := knownEvents[name]; !ok {
			t.Fatalf("event %q is outside the closed vocabulary", name)
		}
	}
	if len(knownEvents) != len(wanted) {
		t.Fatalf("closed vocabulary has %d events, want %d", len(knownEvents), len(wanted))
	}
}

func TestRecordEmitsOnlyBoundedRedactedFields(t *testing.T) {
	original := emitEvent
	t.Cleanup(func() { emitEvent = original })
	var captured logger.Fields
	emitEvent = func(fields logger.Fields) { captured = fields }

	Record(ProjectOutcome, Fields{
		JourneyID: "safe_journey", StepID: "project", ActionID: "connect_existing_project",
		ResourceID: "/Users/person/Secret Project/song.rpp",
		RoleID:     "Producer Name", RouteToken: "existing_project", ModeToken: "file_only",
		RunKind: "child", Lifecycle: "needs_attention", Outcome: OutcomeFailed,
		ReasonCode: "project_scope_invalid", SchemaVersion: 1, DeclarationVersion: 2,
		DurationSeconds: 3, Count: 1,
	})
	if captured == nil {
		t.Fatal("event was not emitted")
	}
	allowed := map[string]bool{
		"event": true, "journey_id": true, "step_id": true, "action_id": true,
		"resource_id": true, "role_id": true, "route_token": true, "mode_token": true,
		"run_kind": true, "lifecycle": true, "outcome": true, "reason_code": true,
		"schema_version": true, "declaration_version": true,
		"duration_seconds": true, "count": true,
	}
	for key, value := range captured {
		if !allowed[key] {
			t.Fatalf("unexpected event field %q", key)
		}
		encoded := fmt.Sprint(value)
		for _, forbidden := range []string{"/Users/", "Secret Project", "song.rpp", "Producer Name"} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("field %q leaked %q in %q", key, forbidden, encoded)
			}
		}
	}
	if _, ok := captured["resource_id"]; ok {
		t.Fatal("path-like resource identity was logged")
	}
	if _, ok := captured["role_id"]; ok {
		t.Fatal("free-text role name was logged")
	}
}

func TestRecordRejectsFilenameAndCredentialLikeTokens(t *testing.T) {
	original := emitEvent
	t.Cleanup(func() { emitEvent = original })
	for _, unsafe := range []string{
		"private.wav", "session.rpp", "sk-secretvalue", "api_key_private", "bearer-secret",
		"system prompt", `{"manifest":"private"}`,
	} {
		var captured logger.Fields
		emitEvent = func(fields logger.Fields) { captured = fields }
		Record(ProjectOutcome, Fields{ResourceID: unsafe, RoleID: unsafe})
		if _, ok := captured["resource_id"]; ok {
			t.Errorf("unsafe resource token %q was logged", unsafe)
		}
		if _, ok := captured["role_id"]; ok {
			t.Errorf("unsafe role token %q was logged", unsafe)
		}
	}
}

func TestFieldsCannotGrowFreeTextPayloadsUnnoticed(t *testing.T) {
	t.Parallel()
	typeOfFields := reflect.TypeOf(Fields{})
	want := []string{
		"JourneyID", "StepID", "ActionID", "ResourceID", "RoleID", "RouteToken", "ModeToken",
		"RunKind", "Lifecycle", "Outcome", "ReasonCode", "SchemaVersion", "DeclarationVersion",
		"DurationSeconds", "Count",
	}
	if typeOfFields.NumField() != len(want) {
		t.Fatalf("Fields has %d members; update the privacy review", typeOfFields.NumField())
	}
	for index, name := range want {
		if typeOfFields.Field(index).Name != name {
			t.Fatalf("Fields[%d] = %q, want %q", index, typeOfFields.Field(index).Name, name)
		}
	}
}
