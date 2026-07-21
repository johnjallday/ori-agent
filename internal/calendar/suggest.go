package calendar

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// DiscoveredTool is the minimal, MCP-SDK-independent shape this package
// needs from a connector's discovered tool list -- kept decoupled from
// internal/mcp.Tool so the guided-suggestion engine stays independently
// testable. Callers (the setup HTTP handler) convert mcp.Tool -> this.
type DiscoveredTool struct {
	Name        string
	Description string
	// InputSchemaProperties are the discovered tool's declared JSON Schema
	// input property names (the schema's "properties" object keys). Nil/empty
	// is fine -- argument-pointer suggestions are simply skipped.
	InputSchemaProperties []string
}

// OperationSuggestion is a prefillable, unconfirmed guess at how one
// semantic calendar operation might map onto a discovered tool. Per FR12/13
// and this file's contract, a suggestion is never activated automatically --
// it exists only to prefill a mapping editor the user must confirm.
type OperationSuggestion struct {
	Operation string `json:"operation"`
	Tool      string `json:"tool"`
	// Confidence is the number of synonym phrases that matched the tool's
	// name/description; higher is a stronger guess. Purely for UI ranking/
	// display, never used to decide anything automatically.
	Confidence int `json:"confidence"`
	// Arguments suggests canonical-input-field -> tool-argument-property
	// pointers, guessed by matching normalized field-synonym tokens against
	// the tool's declared input schema property names. Output Fields are
	// intentionally never guessed: MCP tools don't reliably declare a result
	// schema this package can introspect, so field mapping for read
	// operations is always a deliberate, example-response-driven step in the
	// advanced editor (see PRD Design Considerations).
	Arguments map[string]string `json:"arguments,omitempty"`
}

// operationSynonyms maps each calendar operation to phrases (already
// normalized: lowercase, hyphens/underscores as spaces) whose presence in a
// tool's normalized name+description counts as a match.
var operationSynonyms = map[string][]string{
	OpListCalendars:  {"list calendars", "calendars list", "get calendars", "calendar list", "fetch calendars"},
	OpListEvents:     {"list events", "events list", "get events", "search events", "fetch events", "query events"},
	OpGetEvent:       {"get event", "fetch event", "event detail", "event by id", "read event"},
	OpFreeBusy:       {"freebusy", "free busy", "availability", "busy times"},
	OpSuggestTime:    {"suggest time", "find time", "find a time", "suggest meeting time", "propose time"},
	OpCreateEvent:    {"create event", "add event", "new event", "insert event", "schedule event", "book event"},
	OpUpdateEvent:    {"update event", "edit event", "modify event", "patch event", "reschedule event"},
	OpListAccounts:   {"list accounts", "connected accounts", "get accounts"},
	OpConnectAccount: {"connect account", "add account", "authorize account", "link account"},
}

// argumentFieldSynonyms maps each canonical argument-side field (used by
// list_events/create_event/update_event) to normalized property-name
// synonyms a connector's input schema might use.
var argumentFieldSynonyms = map[string][]string{
	"calendar_id": {"calendar id", "calendarid", "calendar"},
	"id":          {"event id", "eventid", "id"},
	"title":       {"title", "summary", "subject", "name"},
	"description": {"description", "notes", "body"},
	"location":    {"location", "place", "venue"},
	"start_time":  {"start time", "starttime", "start", "time min", "timemin", "from"},
	"end_time":    {"end time", "endtime", "end", "time max", "timemax", "to"},
	"time_zone":   {"time zone", "timezone", "tz"},
	"attendees":   {"attendees", "guests", "participants", "invitees"},
}

// normalizeTokenString lower-cases s and replaces hyphens/underscores with
// spaces, matching this file's "hyphen/underscore/case normalized" synonym
// matching contract.
func normalizeTokenString(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(s), " ")
}

// SuggestMappings proposes an unconfirmed OperationSuggestion for every
// calendar operation that has at least one plausible tool match among
// discovered. Suggestions are sorted by operation (required operations
// first, matching AllOperations' order) for stable, predictable UI
// rendering.
func SuggestMappings(discovered []DiscoveredTool) []OperationSuggestion {
	normalizedTools := make([]struct {
		tool DiscoveredTool
		text string
	}, len(discovered))
	for i, t := range discovered {
		normalizedTools[i].tool = t
		normalizedTools[i].text = normalizeTokenString(t.Name + " " + t.Description)
	}

	var out []OperationSuggestion
	for _, operation := range AllOperations() {
		synonyms := operationSynonyms[operation]
		best := DiscoveredTool{}
		bestScore := 0
		for _, nt := range normalizedTools {
			score := 0
			for _, phrase := range synonyms {
				if strings.Contains(nt.text, phrase) {
					score++
				}
			}
			if score > bestScore {
				bestScore = score
				best = nt.tool
			}
		}
		if bestScore == 0 {
			continue
		}
		out = append(out, OperationSuggestion{
			Operation:  operation,
			Tool:       best.Name,
			Confidence: bestScore,
			Arguments:  suggestArguments(operation, best.InputSchemaProperties),
		})
	}
	return out
}

func suggestArguments(operation string, properties []string) map[string]string {
	requiredFields, isWrite, ok := RequiredFieldsFor(operation)
	if !ok || !isWrite || len(properties) == 0 {
		return nil
	}

	normalizedProps := make(map[string]string, len(properties)) // normalized -> original
	for _, p := range properties {
		normalizedProps[normalizeTokenString(p)] = p
	}

	args := make(map[string]string)
	for _, field := range requiredFields {
		for _, synonym := range argumentFieldSynonyms[field] {
			if original, ok := normalizedProps[synonym]; ok {
				args[field] = "/" + escapeToken(original)
				break
			}
		}
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

// SuggestionsForOperation filters suggestions down to the entries for a
// single operation name (there is at most one, since SuggestMappings emits
// one best guess per operation), returning ok=false if none matched.
func SuggestionForOperation(suggestions []OperationSuggestion, operation string) (OperationSuggestion, bool) {
	for _, s := range suggestions {
		if s.Operation == operation {
			return s, true
		}
	}
	return OperationSuggestion{}, false
}

// ToCapabilityMapping converts confirmed suggestions into a
// workspace.CapabilityMapping. This is only ever called after explicit user
// confirmation (per FR13) -- SuggestMappings itself never produces anything
// that gets persisted directly.
func ToCapabilityMapping(confirmed []OperationSuggestion) workspace.CapabilityMapping {
	ops := make(map[string]workspace.OperationMapping, len(confirmed))
	for _, s := range confirmed {
		ops[s.Operation] = workspace.OperationMapping{
			Tool:      s.Tool,
			Arguments: s.Arguments,
		}
	}
	mapping := workspace.CapabilityMapping{Capability: CapabilityKey, Operations: ops}
	sortedCopy := workspace.NormalizeCapabilityMappings([]workspace.CapabilityMapping{mapping})
	if len(sortedCopy) == 0 {
		return workspace.CapabilityMapping{Capability: CapabilityKey}
	}
	return sortedCopy[0]
}
