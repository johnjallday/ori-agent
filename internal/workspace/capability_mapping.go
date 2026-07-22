package workspace

import "strings"

// OperationMapping wires one semantic capability operation (e.g. the calendar
// capability's "list_events") onto a connector's concrete MCP tool. It is
// deliberately domain-blind: the workspace layer stores and copies it, but does
// not interpret the JSON Pointers — the capability's own package (e.g.
// internal/calendar) validates and applies them.
//
//   - Tool is the connector's MCP tool name to invoke.
//   - Arguments maps a canonical input field name to an RFC 6901 JSON Pointer
//     describing where that value is placed in the tool's argument object.
//   - ResultCollection is an RFC 6901 JSON Pointer to the array of items inside
//     the tool result. The empty pointer means the whole result document is the
//     collection.
//   - Fields maps a canonical output field name to an RFC 6901 JSON Pointer
//     resolved against each collection item.
type OperationMapping struct {
	Tool             string            `json:"tool"`
	Arguments        map[string]string `json:"arguments,omitempty"`
	ResultCollection string            `json:"result_collection,omitempty"`
	Fields           map[string]string `json:"fields,omitempty"`
}

// CapabilityMapping binds a whole semantic capability (identified by Capability,
// e.g. "calendar") onto a connector's tools, keyed by semantic operation name.
// Using a map for Operations makes duplicate operation names structurally
// impossible once decoded; authoring flows that accept a list must reject
// duplicates before building this map.
type CapabilityMapping struct {
	Capability string                      `json:"capability"`
	Operations map[string]OperationMapping `json:"operations,omitempty"`
}

// Operation returns the mapping for a semantic operation name (case-insensitive)
// and whether it is present.
func (m CapabilityMapping) Operation(name string) (OperationMapping, bool) {
	if len(m.Operations) == 0 {
		return OperationMapping{}, false
	}
	op, ok := m.Operations[strings.ToLower(strings.TrimSpace(name))]
	return op, ok
}

// NormalizeCapabilityMappings trims and lower-cases capability keys and
// operation names, trims tool names and pointer values, drops operations with an
// empty tool name, drops mappings that end up with no capability key or no
// operations, and de-duplicates by capability key (first-seen wins, later
// operations merged in without overwriting). It never validates JSON Pointer
// syntax — that is the capability package's job — so a malformed pointer survives
// persistence and surfaces as a validation error at activation time rather than
// silently vanishing.
func NormalizeCapabilityMappings(mappings []CapabilityMapping) []CapabilityMapping {
	if len(mappings) == 0 {
		return nil
	}
	out := make([]CapabilityMapping, 0, len(mappings))
	index := make(map[string]int, len(mappings))
	for _, mapping := range mappings {
		key := strings.ToLower(strings.TrimSpace(mapping.Capability))
		if key == "" {
			continue
		}
		ops := normalizeOperationMap(mapping.Operations)
		if len(ops) == 0 {
			continue
		}
		if pos, exists := index[key]; exists {
			for name, op := range ops {
				if _, taken := out[pos].Operations[name]; !taken {
					out[pos].Operations[name] = op
				}
			}
			continue
		}
		index[key] = len(out)
		out = append(out, CapabilityMapping{Capability: key, Operations: ops})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeOperationMap(ops map[string]OperationMapping) map[string]OperationMapping {
	if len(ops) == 0 {
		return nil
	}
	out := make(map[string]OperationMapping, len(ops))
	for name, op := range ops {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		normalized := normalizeOperationMapping(op)
		if strings.TrimSpace(normalized.Tool) == "" {
			continue
		}
		out[name] = normalized
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeOperationMapping(op OperationMapping) OperationMapping {
	return OperationMapping{
		Tool:             strings.TrimSpace(op.Tool),
		Arguments:        normalizePointerMap(op.Arguments),
		ResultCollection: strings.TrimSpace(op.ResultCollection),
		Fields:           normalizePointerMap(op.Fields),
	}
}

// normalizePointerMap trims/lower-cases canonical field keys and trims pointer
// values, dropping entries whose key is blank. A blank pointer value is kept
// intentionally: the empty JSON Pointer is a valid RFC 6901 reference (the whole
// document), so blanking it must be a deliberate, representable choice.
func normalizePointerMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for key, ptr := range m {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(ptr)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CloneCapabilityMappings returns a deep copy so callers can never alias a
// workspace's internal maps/slices. Returns nil for an empty input to keep the
// omitted-vs-present distinction stable through persistence.
func CloneCapabilityMappings(mappings []CapabilityMapping) []CapabilityMapping {
	if len(mappings) == 0 {
		return nil
	}
	out := make([]CapabilityMapping, len(mappings))
	for i, mapping := range mappings {
		out[i] = CloneCapabilityMapping(mapping)
	}
	return out
}

// CloneCapabilityMapping deep-copies a single capability mapping.
func CloneCapabilityMapping(mapping CapabilityMapping) CapabilityMapping {
	out := CapabilityMapping{Capability: mapping.Capability}
	if len(mapping.Operations) > 0 {
		out.Operations = make(map[string]OperationMapping, len(mapping.Operations))
		for name, op := range mapping.Operations {
			out.Operations[name] = cloneOperationMapping(op)
		}
	}
	return out
}

func cloneOperationMapping(op OperationMapping) OperationMapping {
	return OperationMapping{
		Tool:             op.Tool,
		Arguments:        cloneStringStringMap(op.Arguments),
		ResultCollection: op.ResultCollection,
		Fields:           cloneStringStringMap(op.Fields),
	}
}

func cloneStringStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// cloneStringSlice returns a deep copy of a string slice, preserving the
// nil-vs-empty distinction (nil in -> nil out, non-nil empty in -> non-nil empty
// out). That distinction is load-bearing for MCPBinding.AllowedTools, where nil
// means "all tools allowed" and an explicit empty slice means "no tools allowed".
func cloneStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// AllowsAllTools reports whether the binding imposes no tool allowlist. A nil
// AllowedTools (the legacy shape, and the default for bindings authored before
// allowlists existed) allows every tool the server exposes; a non-nil list —
// including an explicit empty list — restricts the binding to exactly the named
// tools. Enforcement of this is added in a later group; this helper defines the
// contract now so the data model round-trips the distinction.
func (b MCPBinding) AllowsAllTools() bool {
	return b.AllowedTools == nil
}

// ToolAllowed reports whether toolName may be exposed for this binding under its
// allowlist. With no allowlist (nil) every tool is allowed; otherwise the tool
// must appear in AllowedTools (case-insensitive, trimmed).
func (b MCPBinding) ToolAllowed(toolName string) bool {
	if b.AllowedTools == nil {
		return true
	}
	want := strings.ToLower(strings.TrimSpace(toolName))
	for _, allowed := range b.AllowedTools {
		if strings.ToLower(strings.TrimSpace(allowed)) == want {
			return true
		}
	}
	return false
}

// FindCapabilityMapping returns the binding's mapping for a capability key
// (case-insensitive) and whether it is present.
func (b MCPBinding) FindCapabilityMapping(capability string) (CapabilityMapping, bool) {
	want := strings.ToLower(strings.TrimSpace(capability))
	if want == "" {
		return CapabilityMapping{}, false
	}
	for _, mapping := range b.CapabilityMappings {
		if strings.ToLower(strings.TrimSpace(mapping.Capability)) == want {
			return mapping, true
		}
	}
	return CapabilityMapping{}, false
}

// normalizeAllowedTools trims each entry, drops blanks, and de-duplicates
// case-insensitively while preserving first-seen order and the nil-vs-empty
// distinction: nil in -> nil out (no allowlist), a non-nil list that reduces to
// zero usable entries -> non-nil empty (explicit "no tools").
func normalizeAllowedTools(tools []string) []string {
	if tools == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(tools))
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		key := strings.ToLower(tool)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tool)
	}
	return out
}
