// Package systemassistant holds the one identity contract for Ori's protected
// system assistant.
//
// It exists because that identity used to be a bare string copied into every
// package that needed it, each with a "keep these in lockstep" comment. They did
// not stay in lockstep: internal/chathttp's preflight carve-out still matched
// only the pre-2026 names and silently stopped recognizing the assistant when it
// was renamed to "Workspace Manager". Issue #350 (FR49) replaces that pattern
// with a single collision-safe contract.
//
// This package deliberately imports nothing from the rest of the tree so every
// layer — HTTP handlers, stores, workspace runtime, resolvers — can depend on it
// without an import cycle.
package systemassistant

import "strings"

// CanonicalName is the protected system assistant's one user-facing identity.
//
// "Ask Ori" won the consolidation decided in Issue #323 (Q2=A): the app-wide
// guide brand absorbs the working assistant rather than the reverse, so a user
// never has to pick between two similarly-purposed surfaces.
const CanonicalName = "Ask Ori"

// ProtectedMarker is a durable tag stamped on the stored agent record that IS
// the protected system assistant.
//
// A name alone cannot answer "is this the system assistant?" once a user is
// allowed to own an agent called "Ask Ori" (FR55). The marker is namespaced so
// it cannot be confused with the ordinary descriptive tags ("system",
// "assistant", …) that any agent may carry.
const ProtectedMarker = "ori:system-assistant"

// LegacyNames are names that were canonical in earlier releases, newest first.
//
// Order matters: migration walks this list and takes the first match, so an
// install carrying both a recent "Workspace Manager" and a stale "__assistant__"
// migrates the record the user has actually been using.
//
// Entries are never removed without a compatibility decision — a user who
// skipped releases still has one of these on disk (FR50).
var LegacyNames = []string{
	"Workspace Manager",
	"Ori",
	"__assistant__",
}

// IsCanonicalName reports whether name is the current protected identity.
//
// This is the check protection guards should use: a retired name is a migration
// concern, not a live protected identity.
func IsCanonicalName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), CanonicalName)
}

// IsLegacyName reports whether name was canonical in an earlier release.
func IsLegacyName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	for _, legacy := range LegacyNames {
		if strings.EqualFold(trimmed, legacy) {
			return true
		}
	}
	return false
}

// IsKnownName reports whether name refers to the system assistant under the
// canonical identity or any supported legacy one.
//
// Use it to resolve a persisted reference; use IsCanonicalName to guard a
// protected operation.
func IsKnownName(name string) bool {
	return IsCanonicalName(name) || IsLegacyName(name)
}

// Canonicalize maps a known system-assistant name to the canonical identity and
// leaves every other name alone (trimmed).
//
// This is the compatibility seam of FR57: a stored "Workspace Manager" reference
// keeps resolving, while an unrelated agent name passes through untouched so no
// user-authored record is ever swept up by identity matching (FR56).
func Canonicalize(name string) string {
	trimmed := strings.TrimSpace(name)
	if IsKnownName(trimmed) {
		return CanonicalName
	}
	return trimmed
}

// HasProtectedMarker reports whether the given tags identify the stored record
// as the protected system assistant.
func HasProtectedMarker(tags []string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), ProtectedMarker) {
			return true
		}
	}
	return false
}

// EnsureProtectedMarker returns tags with the protected marker present exactly
// once, preserving order and any tags the user added.
func EnsureProtectedMarker(tags []string) []string {
	if HasProtectedMarker(tags) {
		return tags
	}
	return append(tags, ProtectedMarker)
}
