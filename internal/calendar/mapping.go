package calendar

import (
	"fmt"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ValidateMapping checks a workspace capability mapping against the calendar
// contract:
//   - the mapping's capability key must be "calendar"
//   - every required operation (list_calendars, list_events) must be present
//   - every mapped operation name must be a recognized calendar operation
//     ("duplicate semantic operations" is structurally impossible here:
//     workspace.CapabilityMapping.Operations is a map keyed by operation
//     name, so a duplicate name in authored JSON can only overwrite, never
//     coexist -- see workspace.NormalizeCapabilityMappings)
//   - every operation's tool name is non-empty and every JSON Pointer it
//     supplies is syntactically valid (RFC 6901)
//   - every operation supplies its contract's required canonical fields:
//     from Arguments for write operations, from Fields for read operations
//
// It does not invoke a connector or interpret live data -- that is
// ValidateConnection's job.
func ValidateMapping(mapping workspace.CapabilityMapping) error {
	if key := strings.ToLower(strings.TrimSpace(mapping.Capability)); key != CapabilityKey {
		return fmt.Errorf("mapping capability is %q, want %q", mapping.Capability, CapabilityKey)
	}

	for _, name := range requiredOperations {
		op, ok := mapping.Operation(name)
		if !ok {
			return fmt.Errorf("missing required operation %q", name)
		}
		if err := validateOperation(name, op); err != nil {
			return err
		}
	}

	for name, op := range mapping.Operations {
		if isRequiredOperation(name) {
			continue // already validated above
		}
		if _, known := operationContracts[name]; !known {
			return fmt.Errorf("unknown calendar operation %q", name)
		}
		if err := validateOperation(name, op); err != nil {
			return err
		}
	}

	return nil
}

func isRequiredOperation(name string) bool {
	return slices.Contains(requiredOperations, name)
}

func validateOperation(name string, op workspace.OperationMapping) error {
	contract, ok := operationContracts[name]
	if !ok {
		return fmt.Errorf("unknown calendar operation %q", name)
	}
	if strings.TrimSpace(op.Tool) == "" {
		return fmt.Errorf("operation %q: a tool name is required", name)
	}
	if err := ValidateJSONPointer(op.ResultCollection); err != nil {
		return fmt.Errorf("operation %q: result_collection: %w", name, err)
	}

	if contract.IsWrite {
		for _, field := range contract.RequiredFields {
			ptr, ok := op.Arguments[field]
			if !ok || strings.TrimSpace(ptr) == "" {
				return fmt.Errorf("operation %q: missing required argument mapping for canonical field %q", name, field)
			}
		}
		for field, ptr := range op.Arguments {
			if err := ValidateJSONPointer(ptr); err != nil {
				return fmt.Errorf("operation %q: arguments[%q]: %w", name, field, err)
			}
		}
		return nil
	}

	for _, field := range contract.RequiredFields {
		if _, ok := op.Fields[field]; !ok {
			return fmt.Errorf("operation %q: missing required result mapping for canonical field %q", name, field)
		}
	}
	for field, ptr := range op.Fields {
		if err := ValidateJSONPointer(ptr); err != nil {
			return fmt.Errorf("operation %q: fields[%q]: %w", name, field, err)
		}
	}
	return nil
}

// RequiredFieldsFor returns the canonical fields an operation's mapping must
// supply (from Arguments if it writes, from Fields if it reads), and ok is
// false for an unrecognized operation name.
func RequiredFieldsFor(operation string) (fields []string, isWrite bool, ok bool) {
	contract, known := operationContracts[operation]
	if !known {
		return nil, false, false
	}
	return append([]string{}, contract.RequiredFields...), contract.IsWrite, true
}

// IsCollectionOperation reports whether operation's result is an array of
// items (needing ResultCollection) rather than a single object.
func IsCollectionOperation(operation string) bool {
	return operationContracts[operation].IsCollection
}
