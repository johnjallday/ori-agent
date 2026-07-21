package calendar

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ToolCaller invokes a single MCP tool and returns its result decoded as a
// generic JSON tree (map[string]any / []any / scalars). It is the only seam
// between this package and a live connector, so ValidateConnection has no
// dependency on internal/mcp and can be exercised with a fake in tests. The
// real implementation (group 4's CalendarMCPGateway) decodes the MCP SDK's
// structured tool-call result the same way.
type ToolCaller func(ctx context.Context, toolName string, arguments map[string]any) (any, error)

// OperationValidationResult reports, for one mapped operation, whether the
// round trip through the connector produced a structurally valid result.
type OperationValidationResult struct {
	Operation     string   `json:"operation"`
	Tool          string   `json:"tool"`
	Success       bool     `json:"success"`
	Error         string   `json:"error,omitempty"`
	MissingFields []string `json:"missing_fields,omitempty"`
	ItemsChecked  int      `json:"items_checked"`
}

// AllSucceeded reports whether every result in results succeeded, i.e. the
// calendar capability is ready to activate.
func AllSucceeded(results []OperationValidationResult) bool {
	for _, r := range results {
		if !r.Success {
			return false
		}
	}
	return true
}

// ValidateConnection invokes every *required* read operation in mapping
// (list_calendars, list_events) through call, decodes the structured JSON
// result, and reports exactly which required canonical fields were missing
// from the first item(s) actually returned. It never uses an LLM and never
// accepts a plain-text response as event data: any non-JSON-object/array
// result is reported as a validation failure, not silently reinterpreted.
//
// listEventsArgs supplies the bounded arguments (e.g. calendar id, a narrow
// time range) used for the list_events probe call -- the caller is
// responsible for keeping that range small, per FR15/task 2.7's "bounded
// range" requirement; this function does not itself impose a range.
func ValidateConnection(ctx context.Context, mapping workspace.CapabilityMapping, call ToolCaller, listEventsArgs map[string]any) []OperationValidationResult {
	results := make([]OperationValidationResult, 0, len(requiredOperations))
	for _, name := range requiredOperations {
		op, ok := mapping.Operation(name)
		if !ok {
			results = append(results, OperationValidationResult{
				Operation: name,
				Success:   false,
				Error:     "operation is not mapped",
			})
			continue
		}

		args := map[string]any{}
		if name == OpListEvents {
			maps.Copy(args, listEventsArgs)
		}

		results = append(results, validateOperationResult(ctx, name, op, call, args))
	}
	return results
}

func validateOperationResult(ctx context.Context, name string, op workspace.OperationMapping, call ToolCaller, args map[string]any) OperationValidationResult {
	result := OperationValidationResult{Operation: name, Tool: op.Tool}

	raw, err := call(ctx, op.Tool, args)
	if err != nil {
		result.Error = fmt.Sprintf("connector call failed: %v", err)
		return result
	}

	items, err := Collection(raw, op)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.ItemsChecked = len(items)
	if len(items) == 0 {
		// An empty calendar/event list is a legitimate result (e.g. a fresh
		// account with no events) -- report success without field checks
		// rather than failing validation on account state we don't control.
		result.Success = true
		return result
	}

	requiredFields, _, _ := RequiredFieldsFor(name)
	missing := missingFieldsAcrossItems(items, op, requiredFields)
	if len(missing) > 0 {
		result.MissingFields = missing
		result.Error = fmt.Sprintf("connector response is missing required field(s): %v", missing)
		return result
	}

	result.Success = true
	return result
}

// missingFieldsAcrossItems returns the required fields that failed to
// resolve on every checked item. A field present on at least one item but
// not another is not reported as globally missing -- the mapping is
// structurally valid; that's a data-quality question for individual events,
// not a setup error.
func missingFieldsAcrossItems(items []any, op workspace.OperationMapping, requiredFields []string) []string {
	if len(requiredFields) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(requiredFields))
	// Bounded probe: checking a handful of items is enough to catch a mapping error.
	limit := min(len(items), 5)
	for i := 0; i < limit; i++ {
		values := FieldValues(items[i], op)
		for _, field := range requiredFields {
			if _, ok := values[field]; ok {
				seen[field] = true
			}
		}
	}
	var missing []string
	for _, field := range requiredFields {
		if !seen[field] {
			missing = append(missing, field)
		}
	}
	return missing
}

// DefaultValidationTimeRange returns a small, bounded [start, end) window
// (today through the next 24 hours) suitable for a list_events connection
// probe, formatted as RFC3339 strings.
func DefaultValidationTimeRange(now time.Time) (start, end string) {
	start = now.UTC().Format(time.RFC3339)
	end = now.UTC().Add(24 * time.Hour).Format(time.RFC3339)
	return start, end
}
