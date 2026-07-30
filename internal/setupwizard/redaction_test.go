package setupwizard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// captureEvents swaps the event sink for the duration of a test and returns
// everything that would have been logged.
func captureEvents(t *testing.T) *[]logger.Fields {
	t.Helper()
	var captured []logger.Fields
	original := emitEvent
	emitEvent = func(_ string, fields logger.Fields) {
		copied := logger.Fields{}
		for key, value := range fields {
			copied[key] = value
		}
		captured = append(captured, copied)
	}
	t.Cleanup(func() { emitEvent = original })
	return &captured
}

// secrets are the values that must never reach a log line, a persisted state
// file, or an analytics field. Each is planted somewhere a careless
// implementation would pick it up: the adapter's own summary, the requirement
// the step references, the workspace's name.
var secrets = []string{
	"ya29.a0AfB_by-ACCESS-TOKEN",
	"1//04-refresh-token",
	"4/0AVHEtk-oauth-code",
	"/Users/realperson/Downloads/tax-return-2025.pdf",
	"realperson@example.com",
	"Family Calendar (private)",
	"acct_1234567890",
	"Untitled Song Draft.rpp",
	"postgres://user:hunter2@localhost/db",
}

// leakyAdapter says everything it should not. Its summary names a token, a
// path, an address, and a filename — which is not far-fetched: an adapter
// summary is written for one person looking at their own screen, and the
// mistake this guards against is that sentence being logged verbatim.
type leakyAdapter struct{ id string }

func (a *leakyAdapter) ID() string { return a.id }

func (a *leakyAdapter) Evaluate(context.Context, StepRequest) (StepReadiness, error) {
	return StepReadiness{
		Blocked:       true,
		Summary:       strings.Join(secrets, " "),
		ErrorCategory: ErrorCategoryPermissionRequired,
	}, nil
}

func (a *leakyAdapter) Confirm(context.Context, StepRequest, StepAction) (StepReadiness, error) {
	return StepReadiness{}, fmt.Errorf("failed for %s", secrets[0])
}

// TestEvents_CarryNothingIdentifying is the whole redaction requirement in one
// place: whatever the domain says, the events describe the flow and not the
// user.
func TestEvents_CarryNothingIdentifying(t *testing.T) {
	captured := captureEvents(t)
	service, _ := newTestService(t, downloadsWizard(), &leakyAdapter{id: "downloads_janitor"})
	ws := workspaceUnderTest(t, service)
	ws.Name = secrets[3]

	ctx := context.Background()
	if _, err := service.Status(ctx, "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := service.Open(ctx, "ws-1"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := service.Dismiss(ctx, "ws-1"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if _, err := service.Recheck(ctx, "ws-1"); err != nil {
		t.Fatalf("Recheck: %v", err)
	}

	if len(*captured) == 0 {
		t.Fatal("no events were emitted, so this test proves nothing")
	}
	for _, fields := range *captured {
		for key, value := range fields {
			if !eventFieldAllowlist[key] {
				t.Errorf("event carries field %q, which is not on the allowlist: %v", key, value)
			}
			rendered := fmt.Sprint(value)
			for _, secret := range secrets {
				if strings.Contains(rendered, secret) {
					t.Errorf("field %q leaked %q", key, secret)
				}
			}
			// Nothing that looks like a path or an address, whatever its source.
			if strings.ContainsAny(rendered, "/@") && key != eventFieldName {
				t.Errorf("field %q = %q looks like a path or an address", key, rendered)
			}
		}
	}
}

// TestEvents_ReportTheFlowTheyClaimTo is the other half of usefulness: fields
// that never leak but never say anything are not observability. This pins the
// events a full journey must produce.
func TestEvents_ReportTheFlowTheyClaimTo(t *testing.T) {
	captured := captureEvents(t)
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, _ := newTestService(t, downloadsWizard(), adapter)

	ctx := context.Background()
	if _, err := service.Open(ctx, "ws-1"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := service.Dismiss(ctx, "ws-1"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if _, err := service.Open(ctx, "ws-1"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The folder step passes, then the acknowledgement step, which completes it.
	adapter.ready = true
	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{Type: ActionConfirm}); err != nil {
		t.Fatalf("Confirm folder: %v", err)
	}
	if _, err := service.Confirm(ctx, "ws-1", "automation", StepAction{Type: ActionConfirm}); err != nil {
		t.Fatalf("Confirm automation: %v", err)
	}

	seen := map[string]int{}
	for _, fields := range *captured {
		seen[fmt.Sprint(fields[eventFieldName])]++
	}
	for _, want := range []string{EventApplicable, EventFirstOpened, EventDismissed, EventResumed, EventStepCompleted, EventCompleted} {
		if seen[want] == 0 {
			t.Errorf("no %s event in %v", want, seen)
		}
	}
	if seen[EventCompleted] != 1 {
		t.Errorf("completion was reported %d times; it happened once", seen[EventCompleted])
	}

	// Completion says how long the flow took, which is the number the feature is
	// judged on — and it is a duration, not a timestamp about the user.
	for _, fields := range *captured {
		if fmt.Sprint(fields[eventFieldName]) != EventCompleted {
			continue
		}
		if _, ok := fields[eventFieldDurationSec]; !ok {
			t.Errorf("completion event has no duration: %v", fields)
		}
		if fields[eventFieldBlueprint] != "downloads-janitor" {
			t.Errorf("completion event does not say which blueprint: %v", fields)
		}
	}
}

// TestEvents_FailureIsCountedByCategoryOnly pins the one field that carries
// anything about what went wrong.
func TestEvents_FailureIsCountedByCategoryOnly(t *testing.T) {
	captured := captureEvents(t)
	service, _ := newTestService(t, downloadsWizard(), &leakyAdapter{id: "downloads_janitor"})

	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	found := false
	for _, fields := range *captured {
		if fmt.Sprint(fields[eventFieldName]) != EventStepFailed {
			continue
		}
		found = true
		if fields[eventFieldCategory] != ErrorCategoryPermissionRequired {
			t.Errorf("failure category = %v, want the adapter's stable category", fields[eventFieldCategory])
		}
		if _, ok := fields["summary"]; ok {
			t.Error("the adapter's sentence was attached to the event")
		}
	}
	if !found {
		t.Fatal("a blocked step produced no failure event")
	}
}

// TestPersistedState_KeepsNoDomainDetail checks the other place this data could
// escape: workspace.json is synced, backed up, and read by other tools, so the
// progress record must be as free of domain detail as the logs are.
func TestPersistedState_KeepsNoDomainDetail(t *testing.T) {
	service, store := newTestService(t, downloadsWizard(), &leakyAdapter{id: "downloads_janitor"})

	if _, err := service.Status(context.Background(), "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	progress := store.ws.GetSetupWizardProgress()
	if progress == nil {
		t.Fatal("nothing was persisted, so this test proves nothing")
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("persisted progress leaked %q: %s", secret, encoded)
		}
	}
	// Positively: the record is step IDs, statuses, and timestamps. An adapter
	// summary living in here would be a copy of a live domain fact that goes
	// stale the moment the domain changes.
	// (The literal step id "summary" is fine; a `"summary":` *field* would mean
	// a live domain sentence had been copied into the record, where it goes
	// stale the moment the domain changes.)
	if strings.Contains(string(encoded), `"summary":`) {
		t.Errorf("persisted progress carries adapter prose: %s", encoded)
	}
}

// TestStatusPayload_CarriesOnlyWhatTheDialogRenders documents the deliberate
// asymmetry: the HTTP response *does* carry the adapter's sentence, because it
// is what the user reads on their own screen. What it must not carry is
// anything the dialog does not render — and it must stay free of the request
// plumbing an attacker could use.
func TestStatusPayload_CarriesOnlyWhatTheDialogRenders(t *testing.T) {
	service, _ := newTestService(t, downloadsWizard(), &fakeAdapter{id: "downloads_janitor"})

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, forbidden := range []string{"adapter_source", "command", "plugin_source", "connector", "endpoint", "token", "path"} {
		if _, present := payload[forbidden]; present {
			t.Errorf("status payload exposes %q", forbidden)
		}
	}
	steps, _ := payload["steps"].([]any)
	if len(steps) == 0 {
		t.Fatal("status payload has no steps")
	}
	allowed := map[string]bool{
		"id": true, "kind": true, "required": true, "title": true, "description": true,
		"disclosure": true, "adapter": true, "status": true, "action": true, "summary": true,
		"error_category": true, "options": true, "selected_option": true, "completed_at": true,
		"directory_label": true, "directory_suggested_path": true, "directory_access_disclosure": true,
		"capability_key": true, "plugin_name": true,
	}
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		for key := range step {
			if !allowed[key] {
				t.Errorf("step payload carries unexpected field %q", key)
			}
		}
	}
}

// workspaceUnderTest exposes the fake store's workspace so a test can plant a
// hostile value on it.
func workspaceUnderTest(t *testing.T, service *Service) *workspace.Workspace {
	t.Helper()
	store, ok := service.store.(*fakeStore)
	if !ok {
		t.Fatalf("service is not backed by the fake store")
	}
	return store.ws
}
