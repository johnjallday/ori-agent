package calendar

import (
	"context"
	"fmt"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestValidateConnection_Success(t *testing.T) {
	mapping := googleShapedMapping()
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		switch tool {
		case "calendars_list":
			return map[string]any{"items": []any{
				map[string]any{"id": "primary", "summary": "me@example.com"},
			}}, nil
		case "events_list":
			return map[string]any{"items": []any{
				map[string]any{
					"id":      "evt1",
					"summary": "Standup",
					"start":   map[string]any{"dateTime": "2026-07-20T10:00:00Z"},
					"end":     map[string]any{"dateTime": "2026-07-20T10:15:00Z"},
				},
			}}, nil
		}
		return nil, fmt.Errorf("unexpected tool %q", tool)
	}

	results := ValidateConnection(context.Background(), mapping, call, nil)
	if !AllSucceeded(results) {
		t.Fatalf("expected all operations to succeed, got: %+v", results)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (list_calendars, list_events), got %d", len(results))
	}
}

func TestValidateConnection_ReportsMissingRequiredField(t *testing.T) {
	mapping := googleShapedMapping()
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		switch tool {
		case "calendars_list":
			return map[string]any{"items": []any{map[string]any{"id": "primary", "summary": "me@example.com"}}}, nil
		case "events_list":
			// Missing "summary" (title) entirely -- the mapping's /summary pointer
			// will never resolve for this connector response.
			return map[string]any{"items": []any{
				map[string]any{
					"id":    "evt1",
					"start": map[string]any{"dateTime": "2026-07-20T10:00:00Z"},
					"end":   map[string]any{"dateTime": "2026-07-20T10:15:00Z"},
				},
			}}, nil
		}
		return nil, fmt.Errorf("unexpected tool %q", tool)
	}

	results := ValidateConnection(context.Background(), mapping, call, nil)
	if AllSucceeded(results) {
		t.Fatal("expected list_events to fail validation")
	}
	var eventsResult OperationValidationResult
	for _, r := range results {
		if r.Operation == OpListEvents {
			eventsResult = r
		}
	}
	if eventsResult.Success {
		t.Fatal("expected list_events result to report failure")
	}
	found := false
	for _, f := range eventsResult.MissingFields {
		if f == "title" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'title' in missing fields, got: %+v", eventsResult.MissingFields)
	}
}

func TestValidateConnection_ConnectorErrorReported(t *testing.T) {
	mapping := googleShapedMapping()
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		return nil, fmt.Errorf("connector unavailable")
	}
	results := ValidateConnection(context.Background(), mapping, call, nil)
	if AllSucceeded(results) {
		t.Fatal("expected failure when the connector call errors")
	}
	for _, r := range results {
		if r.Error == "" {
			t.Fatalf("expected a non-empty error on result: %+v", r)
		}
	}
}

func TestValidateConnection_EmptyListIsSuccess(t *testing.T) {
	mapping := googleShapedMapping()
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		return map[string]any{"items": []any{}}, nil
	}
	results := ValidateConnection(context.Background(), mapping, call, nil)
	if !AllSucceeded(results) {
		t.Fatalf("an empty (but well-formed) result should validate as success: %+v", results)
	}
}

func TestValidateConnection_UnmappedRequiredOperationFails(t *testing.T) {
	mapping := workspace.CapabilityMapping{Capability: CapabilityKey, Operations: map[string]workspace.OperationMapping{}}
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		t.Fatal("should not call the connector for an unmapped operation")
		return nil, nil
	}
	results := ValidateConnection(context.Background(), mapping, call, nil)
	if AllSucceeded(results) {
		t.Fatal("expected failure for unmapped required operations")
	}
}

func TestValidateConnection_NeverUsesLLMOrFreeText(t *testing.T) {
	// A connector that returns a plain string instead of structured JSON must
	// be reported as a failure, never silently reinterpreted.
	mapping := googleShapedMapping()
	call := func(ctx context.Context, tool string, args map[string]any) (any, error) {
		return "Sure! Here are your events: Standup at 10am.", nil
	}
	results := ValidateConnection(context.Background(), mapping, call, nil)
	if AllSucceeded(results) {
		t.Fatal("expected a free-text connector response to fail validation deterministically")
	}
}
