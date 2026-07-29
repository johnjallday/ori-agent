package calendarhttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/calendar"
	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// adapterStep builds the wizard request for one of the Calendar blueprint's
// steps, shaped as the service hands it to the adapter.
func adapterStep(kind string) setupwizard.StepRequest {
	return setupwizard.StepRequest{
		WorkspaceID: "ws-1",
		Step: agentworkspace.SetupWizardStep{
			ID:       kind,
			Kind:     kind,
			Required: true,
			Adapter:  SetupAdapterID,
		},
	}
}

// adapterFixture builds a Calendar Ops workspace whose derived setup state is
// driven entirely by the injected connector status and binding — the same seams
// the setup card's own tests use, so both read one state machine.
func adapterFixture(t *testing.T, status connectorStatus, bind bool, mappingValid, validated bool) *SetupAdapter {
	t.Helper()
	ws := newCalendarOpsWorkspace("ws-1")
	if bind {
		settings := calendar.BindingSettings{Validated: validated}
		binding := agentworkspace.MCPBinding{
			ID:         "binding-1",
			ServerName: "google-calendar",
			Enabled:    true,
			Config:     calendar.WriteBindingSettings(nil, settings),
		}
		if mappingValid {
			binding.CapabilityMappings = []agentworkspace.CapabilityMapping{googleShapedMappingForTest()}
		} else {
			binding.CapabilityMappings = []agentworkspace.CapabilityMapping{{Capability: calendar.CapabilityKey}}
		}
		if err := ws.UpsertMCPBinding(binding); err != nil {
			t.Fatal(err)
		}
	}
	store := newFakeFolderStore()
	store.workspaces["ws-1"] = ws
	handler := NewHandler(store, &fakeWorkspaceLister{}, nil, nil, userprofile.LocalUserProvider{})
	handler.WithConnectorStatusFn(func(string) connectorStatus { return status })
	return NewSetupAdapter(handler, store)
}

func evaluate(t *testing.T, adapter *SetupAdapter, kind string) setupwizard.StepReadiness {
	t.Helper()
	readiness, err := adapter.Evaluate(context.Background(), adapterStep(kind))
	if err != nil {
		t.Fatalf("Evaluate(%s): %v", kind, err)
	}
	return readiness
}

// TestSetupAdapter_EveryCalendarStateMapsToAStep walks the domain's own state
// machine and pins where each state leaves the user (FR-93). The table is the
// contract: a new Calendar state that nobody maps here would otherwise surface
// as a silently pending step.
func TestSetupAdapter_EveryCalendarStateMapsToAStep(t *testing.T) {
	present := connectorStatus{Present: true}
	connected := connectorStatus{Present: true, Connected: true}

	cases := []struct {
		name     string
		adapter  *SetupAdapter
		state    calendar.SetupState
		connect  string // "ready", "pending" or "blocked"
		config   string
		overall  string
		category string
	}{
		{
			name:    "no connector",
			adapter: adapterFixture(t, connectorStatus{}, false, false, false),
			state:   calendar.SetupConnectorMissing,
			connect: "pending", config: "pending", overall: "pending",
		},
		{
			name:    "connector added but not signed in",
			adapter: adapterFixture(t, present, true, true, false),
			state:   calendar.SetupAuthRequired,
			connect: "pending", config: "pending", overall: "pending",
			category: setupwizard.ErrorCategoryPermissionRequired,
		},
		{
			name:    "signed in but unmapped",
			adapter: adapterFixture(t, connected, true, false, false),
			state:   calendar.SetupMappingRequired,
			connect: "ready", config: "pending", overall: "pending",
		},
		{
			name:    "mapped but untested",
			adapter: adapterFixture(t, connected, true, true, false),
			state:   calendar.SetupValidationFailed,
			connect: "ready", config: "blocked", overall: "pending",
		},
		{
			name:    "ready",
			adapter: adapterFixture(t, connected, true, true, true),
			state:   calendar.SetupReady,
			connect: "ready", config: "ready", overall: "ready",
		},
		{
			name:    "degraded",
			adapter: adapterFixture(t, connectorStatus{Present: true, Connected: true, Degraded: true}, true, true, true),
			state:   calendar.SetupDegraded,
			connect: "blocked", config: "blocked", overall: "blocked",
		},
	}
	if len(cases) != len(calendar.AllSetupStates()) {
		t.Fatalf("this table must cover all %d Calendar states, it covers %d", len(calendar.AllSetupStates()), len(cases))
	}

	classify := func(readiness setupwizard.StepReadiness) string {
		switch {
		case readiness.Ready:
			return "ready"
		case readiness.Blocked:
			return "blocked"
		default:
			return "pending"
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connect := evaluate(t, tc.adapter, agentworkspace.SetupStepKindCapabilityConnect)
			configure := evaluate(t, tc.adapter, agentworkspace.SetupStepKindCapabilityConfigure)
			overall := evaluate(t, tc.adapter, agentworkspace.SetupStepKindReadiness)

			if got := classify(connect); got != tc.connect {
				t.Errorf("connect step = %s, want %s (%+v)", got, tc.connect, connect)
			}
			if got := classify(configure); got != tc.config {
				t.Errorf("configure step = %s, want %s (%+v)", got, tc.config, configure)
			}
			if got := classify(overall); got != tc.overall {
				t.Errorf("readiness step = %s, want %s (%+v)", got, tc.overall, overall)
			}
			if tc.category != "" && connect.ErrorCategory != tc.category {
				t.Errorf("connect category = %q, want %q", connect.ErrorCategory, tc.category)
			}
			// Every unfinished step says what is missing, in words a user can act
			// on rather than a state name.
			for name, readiness := range map[string]setupwizard.StepReadiness{
				"connect": connect, "configure": configure, "readiness": overall,
			} {
				if readiness.Summary == "" {
					t.Errorf("%s step gives the user nothing to act on", name)
				}
				if !readiness.Ready && readiness.ErrorCategory == "" {
					t.Errorf("%s step is unfinished but carries no safe category", name)
				}
			}
		})
	}
}

// TestSetupAdapter_DegradedIsRepairNotUnfinishedSetup pins the distinction the
// domain draws deliberately: a connection that worked and stopped is a repair,
// not a workspace that was never set up.
func TestSetupAdapter_DegradedIsRepairNotUnfinishedSetup(t *testing.T) {
	adapter := adapterFixture(t, connectorStatus{Present: true, Connected: true, Degraded: true}, true, true, true)

	connect := evaluate(t, adapter, agentworkspace.SetupStepKindCapabilityConnect)
	if !connect.Blocked {
		t.Fatalf("a degraded connector blocks its step: %+v", connect)
	}
	authRequired := adapterFixture(t, connectorStatus{Present: true}, true, true, false)
	pending := evaluate(t, authRequired, agentworkspace.SetupStepKindCapabilityConnect)
	if pending.Blocked {
		t.Fatalf("an unauthenticated connector is unfinished, not broken: %+v", pending)
	}
}

func TestSetupAdapter_EvaluateAndConfirmCommitNothing(t *testing.T) {
	adapter := adapterFixture(t, connectorStatus{}, false, false, false)
	ctx := context.Background()

	before, err := adapter.workspaces.GetFolderWorkspace("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	bindingsBefore := len(before.GetMCPBindings())

	for range 3 {
		if _, err := adapter.Evaluate(ctx, adapterStep(agentworkspace.SetupStepKindCapabilityConnect)); err != nil {
			t.Fatal(err)
		}
		// Confirming a Calendar step is a re-read: every real mutation belongs
		// to an endpoint that validates it.
		if _, err := adapter.Confirm(ctx, adapterStep(agentworkspace.SetupStepKindCapabilityConnect), setupwizard.StepAction{}); err != nil {
			t.Fatal(err)
		}
	}

	after, err := adapter.workspaces.GetFolderWorkspace("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.GetMCPBindings()) != bindingsBefore {
		t.Fatalf("the wizard created a connector binding: %d -> %d", bindingsBefore, len(after.GetMCPBindings()))
	}
}

func TestSetupAdapter_SummariesNameNoCalendarsOrAccounts(t *testing.T) {
	adapter := adapterFixture(t, connectorStatus{Present: true, Connected: true}, true, true, true)
	for _, kind := range []string{
		agentworkspace.SetupStepKindCapabilityConnect,
		agentworkspace.SetupStepKindCapabilityConfigure,
		agentworkspace.SetupStepKindReadiness,
	} {
		readiness := evaluate(t, adapter, kind)
		// Safe by construction: these strings travel into logs and analytics, so
		// they describe state, never the user's calendars, addresses, or server
		// names.
		for _, leak := range []string{"google-calendar", "@", "ws-1"} {
			if readiness.Summary != "" && contains(readiness.Summary, leak) {
				t.Errorf("step %s leaked %q: %q", kind, leak, readiness.Summary)
			}
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestSetupAdapter_UnavailableDependenciesBlockRatherThanPanic(t *testing.T) {
	var adapter *SetupAdapter
	readiness, err := adapter.Evaluate(context.Background(), adapterStep(agentworkspace.SetupStepKindReadiness))
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.Blocked || readiness.ErrorCategory != setupwizard.ErrorCategoryUnavailable {
		t.Fatalf("an unwired adapter blocks with a safe category: %+v", readiness)
	}

	// A workspace that cannot be read is a blocked step, not a crash.
	handler := NewHandler(newFakeFolderStore(), &fakeWorkspaceLister{}, nil, nil, userprofile.LocalUserProvider{})
	missing := NewSetupAdapter(handler, newFakeFolderStore())
	readiness, err = missing.Evaluate(context.Background(), adapterStep(agentworkspace.SetupStepKindReadiness))
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.Blocked {
		t.Fatalf("an unreadable workspace blocks its step: %+v", readiness)
	}
}
