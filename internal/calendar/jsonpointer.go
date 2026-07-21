// Package calendar defines Ori's provider-neutral calendar capability
// contract: the canonical types agents and the UI consume, the deterministic
// RFC 6901 JSON Pointer mapping between a connector's MCP tools and those
// canonical types, and guided-mapping/connection-validation helpers. Nothing
// here calls an LLM or accepts free text as event data -- every field either
// resolves deterministically from structured JSON or is absent.
package calendar

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateJSONPointer reports whether ptr is syntactically a valid RFC 6901
// JSON Pointer: the empty string (the whole document) or a sequence of
// "/"-prefixed tokens using "~1" for "/" and "~0" for "~".
func ValidateJSONPointer(ptr string) error {
	if ptr == "" {
		return nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return fmt.Errorf("json pointer %q must be empty or start with '/'", ptr)
	}
	for _, tok := range strings.Split(ptr[1:], "/") {
		if err := validateEscaping(tok); err != nil {
			return fmt.Errorf("json pointer %q: %w", ptr, err)
		}
	}
	return nil
}

// validateEscaping rejects a bare "~" not followed by "0" or "1", which is
// the only way an RFC 6901 token can be malformed once split on "/".
func validateEscaping(tok string) error {
	for i := 0; i < len(tok); i++ {
		if tok[i] != '~' {
			continue
		}
		if i+1 >= len(tok) || (tok[i+1] != '0' && tok[i+1] != '1') {
			return fmt.Errorf("invalid '~' escape in token %q", tok)
		}
	}
	return nil
}

func unescapeToken(tok string) string {
	// Order matters: RFC 6901 requires replacing ~1 before ~0 during
	// decoding is unambiguous either way here since we do two independent
	// literal passes, but the spec's own worked examples decode ~1 first.
	tok = strings.ReplaceAll(tok, "~1", "/")
	tok = strings.ReplaceAll(tok, "~0", "~")
	return tok
}

func escapeToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}

// ResolvePointer resolves ptr against doc (a decoded JSON tree of
// map[string]any / []any / scalars, as produced by encoding/json into an
// `any`) per RFC 6901. The empty pointer resolves to doc itself. ok is false
// if any segment is missing, out of range, or traverses into a scalar --
// callers decide whether a missing value is acceptable (an optional
// canonical field) or an error (a required one); this function never treats
// "missing" as a Go error.
func ResolvePointer(doc any, ptr string) (value any, ok bool) {
	if ptr == "" {
		return doc, true
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, false
	}

	current := doc
	for _, rawTok := range strings.Split(ptr[1:], "/") {
		tok := unescapeToken(rawTok)
		switch node := current.(type) {
		case map[string]any:
			val, exists := node[tok]
			if !exists {
				return nil, false
			}
			current = val
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			current = node[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

// SetPointer places value at ptr within doc, creating intermediate object
// (map[string]any) levels as needed. It only traverses/creates objects, not
// arrays: Ori's calendar mappings place canonical input values into a tool's
// argument object, which is always a JSON object at every level a mapping
// targets. Returns an error for the empty pointer (nothing addressable to
// set) or a pointer whose path runs through an existing non-object value.
func SetPointer(doc map[string]any, ptr string, value any) error {
	if doc == nil {
		return fmt.Errorf("cannot set pointer %q on a nil document", ptr)
	}
	if ptr == "" {
		return fmt.Errorf("cannot set the empty json pointer")
	}
	if !strings.HasPrefix(ptr, "/") {
		return fmt.Errorf("json pointer %q must start with '/'", ptr)
	}

	tokens := strings.Split(ptr[1:], "/")
	current := doc
	for i, rawTok := range tokens {
		tok := unescapeToken(rawTok)
		if i == len(tokens)-1 {
			current[tok] = value
			return nil
		}
		next, exists := current[tok]
		if !exists {
			child := make(map[string]any)
			current[tok] = child
			current = child
			continue
		}
		child, isObject := next.(map[string]any)
		if !isObject {
			return fmt.Errorf("json pointer %q: segment %q is not an object", ptr, tok)
		}
		current = child
	}
	return nil
}
