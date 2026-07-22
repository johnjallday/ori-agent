package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
)

type fakeCalendarOpsPreference struct {
	agentName string
	ok        bool
}

func (f fakeCalendarOpsPreference) PreferredCalendarAgent(context.Context) (string, bool) {
	return f.agentName, f.ok
}

func TestCalendarOpsPreferredMatch_PrefersSchedulerForPersonalCalendarPrompt(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Scheduler", &store.CreateAgentConfig{Type: "tool-calling"}, "", nil, nil)
	addHomeRouteTestAgent(t, st, "Generic Helper", &store.CreateAgentConfig{Type: "general"}, "handles calendar and scheduling requests", nil, nil)

	h := NewHomeAssistantRouteHandler(st)
	h.SetWorkspaceResolver(newHomeWorkspaceResolverForTest(t, st))
	h.SetCalendarOpsPreference(fakeCalendarOpsPreference{agentName: "Scheduler", ok: true})

	resp, err := h.RoutePrompt(context.Background(), "am I free this afternoon?", nil)
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	if resp.Intent != "calendar_check" {
		t.Fatalf("intent = %q, want calendar_check", resp.Intent)
	}
	if resp.MatchedAgent != "Scheduler" {
		t.Fatalf("matched agent = %q, want Scheduler (Calendar Ops preference should win over generic scoring)", resp.MatchedAgent)
	}
}

func TestCalendarOpsPreferredMatch_NoOpWithoutPreferenceWired(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Scheduler", &store.CreateAgentConfig{Type: "tool-calling"}, "", nil, nil)

	h := NewHomeAssistantRouteHandler(st)
	h.SetWorkspaceResolver(newHomeWorkspaceResolverForTest(t, st))
	// CalendarOpsPreference intentionally left nil.

	resp, err := h.RoutePrompt(context.Background(), "am I free this afternoon?", nil)
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	if resp.Intent != "calendar_check" {
		t.Fatalf("intent = %q, want calendar_check", resp.Intent)
	}
	// Scheduler's generic keyword/summary score is 0 (no description, no
	// routing profile), so it should NOT be matched without a preference.
	if resp.MatchedAgent == "Scheduler" {
		t.Fatal("expected no Calendar Ops preference to be applied when CalendarOpsPreference is nil")
	}
}

func TestCalendarOpsPreferredMatch_FallsBackWhenPreferenceSaysNo(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Scheduler", &store.CreateAgentConfig{Type: "tool-calling"}, "", nil, nil)

	h := NewHomeAssistantRouteHandler(st)
	h.SetWorkspaceResolver(newHomeWorkspaceResolverForTest(t, st))
	h.SetCalendarOpsPreference(fakeCalendarOpsPreference{ok: false})

	resp, err := h.RoutePrompt(context.Background(), "am I free this afternoon?", nil)
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	if resp.MatchedAgent == "Scheduler" {
		t.Fatal("expected no preference applied when PreferredCalendarAgent reports ok=false")
	}
}

func TestCalendarOpsPreferredMatch_UnknownAgentNameFallsBackToGeneric(t *testing.T) {
	st := newHomeRouteTestStore(t)
	h := NewHomeAssistantRouteHandler(st)
	h.SetWorkspaceResolver(newHomeWorkspaceResolverForTest(t, st))
	// Preference names an agent that doesn't exist in the store (e.g. a stale
	// name or a race with agent deletion) -- must not panic or fabricate a match.
	h.SetCalendarOpsPreference(fakeCalendarOpsPreference{agentName: "Scheduler", ok: true})

	resp, err := h.RoutePrompt(context.Background(), "am I free this afternoon?", nil)
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	if resp.MatchedAgent == "Scheduler" {
		t.Fatal("expected no match for a preferred agent name absent from the store")
	}
}

func TestCalendarOpsPreferredMatch_NotAppliedForNonCalendarIntent(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Scheduler", &store.CreateAgentConfig{Type: "tool-calling"}, "", nil, nil)

	h := NewHomeAssistantRouteHandler(st)
	h.SetWorkspaceResolver(newHomeWorkspaceResolverForTest(t, st))
	h.SetCalendarOpsPreference(fakeCalendarOpsPreference{agentName: "Scheduler", ok: true})

	resp, err := h.RoutePrompt(context.Background(), "check my unread inbox", nil)
	if err != nil {
		t.Fatalf("RoutePrompt: %v", err)
	}
	if resp.Intent != "email_check" {
		t.Fatalf("intent = %q, want email_check", resp.Intent)
	}
	if resp.MatchedAgent == "Scheduler" {
		t.Fatal("expected the calendar preference to be ignored for a non-calendar intent")
	}
}

// TestCalendarOpsPreferredMatch_WorkspaceScheduleAmbiguityIsNotPreferred calls
// calendarOpsPreferredMatch directly (rather than through the full RoutePrompt
// pipeline) to isolate its gating from generic agent scoring -- an agent
// literally named "Scheduler" would otherwise also win generic keyword
// matching against a "schedule"-heavy prompt, confounding an end-to-end
// assertion on RoutePrompt's MatchedAgent.
func TestCalendarOpsPreferredMatch_WorkspaceScheduleAmbiguityIsNotPreferred(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Scheduler", &store.CreateAgentConfig{Type: "tool-calling"}, "", nil, nil)

	h := NewHomeAssistantRouteHandler(st)
	h.SetCalendarOpsPreference(fakeCalendarOpsPreference{agentName: "Scheduler", ok: true})

	routeContext := normalizedHomeAssistantRouteContext{WorkspaceID: "ws-1", Surface: "workspace"}
	if match := h.calendarOpsPreferredMatch(context.Background(), homeAssistantCalendarIntent, "workspace_schedule", routeContext); match != nil {
		t.Fatalf("expected no preferred match for workspace-schedule ambiguity inside a workspace, got %+v", match)
	}

	// Outside a workspace, "workspace_schedule" wording alone doesn't gate --
	// the preference still applies (this is what actually differentiates
	// personal-calendar handoff from workspace routing: workspace *context*,
	// not just phrasing).
	if match := h.calendarOpsPreferredMatch(context.Background(), homeAssistantCalendarIntent, "personal_calendar", normalizedHomeAssistantRouteContext{}); match == nil || match.Name != "Scheduler" {
		t.Fatalf("expected the preference to apply for personal_calendar with no workspace context, got %+v", match)
	}
}
