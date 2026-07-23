package calendar

import "testing"

func TestSuggestMappings_MatchesByNameAndDescription(t *testing.T) {
	tools := []DiscoveredTool{
		{Name: "calendars_list", Description: "List the user's calendars"},
		{Name: "events_list", Description: "Search events on a calendar"},
		{Name: "events_insert", Description: "Create a new event", InputSchemaProperties: []string{"calendarId", "summary", "start", "end"}},
		{Name: "unrelated_tool", Description: "Does something else entirely"},
	}

	suggestions := SuggestMappings(tools)

	listCalendars, ok := SuggestionForOperation(suggestions, OpListCalendars)
	if !ok || listCalendars.Tool != "calendars_list" {
		t.Fatalf("expected list_calendars to match calendars_list, got: %+v ok=%v", listCalendars, ok)
	}

	listEvents, ok := SuggestionForOperation(suggestions, OpListEvents)
	if !ok || listEvents.Tool != "events_list" {
		t.Fatalf("expected list_events to match events_list, got: %+v ok=%v", listEvents, ok)
	}

	createEvent, ok := SuggestionForOperation(suggestions, OpCreateEvent)
	if !ok || createEvent.Tool != "events_insert" {
		t.Fatalf("expected create_event to match events_insert, got: %+v ok=%v", createEvent, ok)
	}
	if createEvent.Arguments["calendar_id"] != "/calendarId" || createEvent.Arguments["title"] != "/summary" {
		t.Fatalf("expected argument pointers guessed from input schema properties, got: %+v", createEvent.Arguments)
	}

	if _, ok := SuggestionForOperation(suggestions, OpFreeBusy); ok {
		t.Fatal("expected no suggestion for an operation with no plausible tool match")
	}
}

func TestSuggestMappings_HyphenUnderscoreCaseNormalized(t *testing.T) {
	tools := []DiscoveredTool{
		{Name: "List-Calendars", Description: "LIST_CALENDARS for the account"},
	}
	suggestions := SuggestMappings(tools)
	got, ok := SuggestionForOperation(suggestions, OpListCalendars)
	if !ok || got.Tool != "List-Calendars" {
		t.Fatalf("expected hyphen/underscore/case-insensitive match, got: %+v ok=%v", got, ok)
	}
}

func TestSuggestMappings_EmptyDiscoveredToolsYieldsNoSuggestions(t *testing.T) {
	if suggestions := SuggestMappings(nil); len(suggestions) != 0 {
		t.Fatalf("expected no suggestions for no discovered tools, got: %+v", suggestions)
	}
}

func TestToCapabilityMapping_RequiresExplicitConfirmationStep(t *testing.T) {
	// SuggestMappings itself returns unconfirmed guesses; only an explicit
	// ToCapabilityMapping call (standing in for user confirmation in the UI)
	// produces something that would ever be persisted.
	tools := []DiscoveredTool{
		{Name: "calendars_list", Description: "List calendars"},
		{Name: "events_list", Description: "List events"},
	}
	suggestions := SuggestMappings(tools)
	mapping := ToCapabilityMapping(suggestions)
	if mapping.Capability != CapabilityKey {
		t.Fatalf("unexpected capability: %q", mapping.Capability)
	}
	if _, ok := mapping.Operation(OpListCalendars); !ok {
		t.Fatal("expected list_calendars in the confirmed mapping")
	}
	if _, ok := mapping.Operation(OpListEvents); !ok {
		t.Fatal("expected list_events in the confirmed mapping")
	}
}
