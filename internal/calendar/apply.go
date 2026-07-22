package calendar

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Collection resolves an operation's ResultCollection pointer against a
// decoded MCP tool result and returns the items to apply per-record field
// mapping to. The empty pointer means the whole result is the collection;
// a result that isn't an array at the resolved location is an error (the
// connector's response shape doesn't match what the mapping claims).
func Collection(result any, op workspace.OperationMapping) ([]any, error) {
	node, ok := ResolvePointer(result, op.ResultCollection)
	if !ok {
		return nil, fmt.Errorf("result_collection %q did not resolve against the connector's response", op.ResultCollection)
	}
	items, ok := node.([]any)
	if !ok {
		return nil, fmt.Errorf("result_collection %q did not resolve to an array", op.ResultCollection)
	}
	return items, nil
}

// FieldStrings extracts every canonical field's raw resolved value from one
// result item using op.Fields, keyed by canonical field name. Missing
// fields are simply absent from the map -- callers combine this with
// RequiredFieldsFor to decide whether an absence is acceptable.
func FieldValues(item any, op workspace.OperationMapping) map[string]any {
	out := make(map[string]any, len(op.Fields))
	for field, ptr := range op.Fields {
		if val, ok := ResolvePointer(item, ptr); ok {
			out[field] = val
		}
	}
	return out
}

// ApplyEvent deterministically builds a canonical Event from one decoded
// result item using op.Fields. It never guesses or invents a value: a field
// whose pointer doesn't resolve, or whose resolved value has the wrong JSON
// type, is simply left at its zero value. Use ValidateOperationResult (in
// validate.go) against the same item to find out which required fields were
// actually missing/invalid before trusting the returned Event.
func ApplyEvent(item any, op workspace.OperationMapping) Event {
	values := FieldValues(item, op)
	return Event{
		ID:             stringField(values, "id"),
		CalendarID:     stringField(values, "calendar_id"),
		Title:          stringField(values, "title"),
		Description:    stringField(values, "description"),
		Location:       stringField(values, "location"),
		StartTime:      stringField(values, "start_time"),
		EndTime:        stringField(values, "end_time"),
		TimeZone:       stringField(values, "time_zone"),
		AllDay:         boolField(values, "all_day"),
		Private:        boolField(values, "private"),
		Canceled:       boolField(values, "canceled"),
		ResponseStatus: stringField(values, "response_status"),
		Attendees:      attendeesField(values, "attendees"),
		ConferenceLink: stringField(values, "conference_link"),
		SourceLink:     stringField(values, "source_link"),
		Recurring:      boolField(values, "recurring"),
	}
}

// ApplyCalendar deterministically builds a canonical Calendar from one
// decoded result item using op.Fields.
func ApplyCalendar(item any, op workspace.OperationMapping) Calendar {
	values := FieldValues(item, op)
	return Calendar{
		ID:       stringField(values, "id"),
		Name:     stringField(values, "name"),
		Primary:  boolField(values, "primary"),
		TimeZone: stringField(values, "time_zone"),
		Color:    stringField(values, "color"),
	}
}

// ApplyAccount deterministically builds a canonical Account from one decoded
// result item using op.Fields.
func ApplyAccount(item any, op workspace.OperationMapping) Account {
	values := FieldValues(item, op)
	return Account{
		ID:    stringField(values, "id"),
		Label: stringField(values, "label"),
		Email: stringField(values, "email"),
	}
}

// ApplyTimeSlot deterministically builds a canonical TimeSlot from one
// decoded result item using op.Fields (used for freebusy/suggest_time).
func ApplyTimeSlot(item any, op workspace.OperationMapping) TimeSlot {
	values := FieldValues(item, op)
	return TimeSlot{
		StartTime: stringField(values, "start_time"),
		EndTime:   stringField(values, "end_time"),
	}
}

// BuildArguments deterministically builds an MCP tool-call argument object
// from canonical input values using op.Arguments. Only fields present in
// both `input` and op.Arguments are placed; a canonical field the mapping
// doesn't reference is silently dropped rather than guessed at.
func BuildArguments(input map[string]any, op workspace.OperationMapping) (map[string]any, error) {
	args := make(map[string]any)
	for field, ptr := range op.Arguments {
		value, present := input[field]
		if !present {
			continue
		}
		if err := SetPointer(args, ptr, value); err != nil {
			return nil, fmt.Errorf("argument %q: %w", field, err)
		}
	}
	return args, nil
}

func stringField(values map[string]any, key string) string {
	v, ok := values[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func boolField(values map[string]any, key string) bool {
	v, ok := values[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func attendeesField(values map[string]any, key string) []Attendee {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]Attendee, 0, len(list))
	for _, entry := range list {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, Attendee{
			Email:          stringFromAny(obj["email"]),
			DisplayName:    stringFromAny(obj["display_name"]),
			ResponseStatus: stringFromAny(obj["response_status"]),
			Organizer:      boolFromAny(obj["organizer"]),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func boolFromAny(v any) bool {
	b, _ := v.(bool)
	return b
}
