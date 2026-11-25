// Package systemassistant recognizes the stored record of Ori's retired
// protected system assistant ("Ask Ori", earlier "Workspace Manager", "Ori").
//
// That persona no longer exists as a working route. Help answers product
// questions, the configured Personal Assistant handles cross-workspace work,
// and each workspace's own manager handles that workspace's work. What remains
// is the one fact needed to keep an existing profile's old record out of every
// list and route without deleting or rewriting it: the durable marker that
// record carries.
//
// Identification is by marker only. A name is never enough — a person is free
// to own an agent called "Ask Ori", and such an agent is an ordinary agent in
// every respect.
//
// This package deliberately imports nothing from the rest of the tree so every
// layer can depend on it without an import cycle.
package systemassistant

import "strings"

// ProtectedMarker is the durable tag stamped on the stored record that was the
// protected system assistant. It is namespaced so it cannot be confused with
// the ordinary descriptive tags ("system", "assistant", …) any agent may carry.
const ProtectedMarker = "ori:system-assistant"

// HasProtectedMarker reports whether the given tags identify the stored record
// as the retired protected system assistant.
func HasProtectedMarker(tags []string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), ProtectedMarker) {
			return true
		}
	}
	return false
}
