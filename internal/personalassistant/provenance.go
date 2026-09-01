package personalassistant

import (
	"fmt"
	"strings"
)

// Profile provenance markers.
//
// A hired assistant's global agent profile is looked up by its current name,
// but a name can never answer "is this profile mine?" — a user may create an
// unrelated agent with the same name, and the hired profile may be renamed.
// These durable namespaced tags, stamped on the profile when it is created,
// bind it to the stable relationship identity instead.
//
// The shape deliberately mirrors systemassistant.ProtectedMarker: namespaced so
// it cannot collide with the ordinary descriptive tags a user may add, and
// carrying no user-authored text.
const (
	// ProfileAssistantMarkerPrefix binds a profile to the stable assistant ID.
	ProfileAssistantMarkerPrefix = "ori:personal-assistant:"
	// ProfileHireMarkerPrefix binds a profile to the hire request that created
	// it, so a retry can tell its own profile from a name collision.
	ProfileHireMarkerPrefix = "ori:personal-assistant-hire:"
)

// ProfileAssistantMarker returns the durable ownership tag for assistantID.
func ProfileAssistantMarker(assistantID string) string {
	return ProfileAssistantMarkerPrefix + strings.TrimSpace(assistantID)
}

// ProfileHireMarker returns the durable hire-request tag for requestID.
func ProfileHireMarker(requestID string) string {
	return ProfileHireMarkerPrefix + strings.TrimSpace(requestID)
}

// ProfileProvenance is the bounded ownership view of one global agent profile.
// It deliberately carries no prompt, model, tool, credential, or appearance
// data: it answers ownership questions and nothing else.
type ProfileProvenance struct {
	// Name is the profile's current lookup name in the global agent store.
	Name string
	// AssistantID is the stable relationship identity stamped on the profile,
	// empty when the profile carries no PAF ownership marker.
	AssistantID string
	// HireRequestID is the hire request that created the profile, empty when the
	// profile predates the marker or was not created by a hire.
	HireRequestID string
}

// OwnedBy reports whether this profile is provably owned by assistantID.
func (p ProfileProvenance) OwnedBy(assistantID string) bool {
	assistantID = strings.TrimSpace(assistantID)
	return assistantID != "" && strings.TrimSpace(p.AssistantID) == assistantID
}

// ProfileProvenanceFromTags extracts bounded ownership from a profile's durable
// tags. Unrelated tags are ignored; nothing is inferred from the profile name.
func ProfileProvenanceFromTags(name string, tags []string) ProfileProvenance {
	provenance := ProfileProvenance{Name: strings.TrimSpace(name)}
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		switch {
		case strings.HasPrefix(trimmed, ProfileAssistantMarkerPrefix):
			provenance.AssistantID = strings.TrimSpace(strings.TrimPrefix(trimmed, ProfileAssistantMarkerPrefix))
		case strings.HasPrefix(trimmed, ProfileHireMarkerPrefix):
			provenance.HireRequestID = strings.TrimSpace(strings.TrimPrefix(trimmed, ProfileHireMarkerPrefix))
		}
	}
	return provenance
}

// EnsureProfileMarkers returns tags carrying both ownership markers exactly
// once, preserving order and any tags the user added. A pre-existing marker for
// a different assistant is a conflict, not something to overwrite.
func EnsureProfileMarkers(tags []string, assistantID, hireRequestID string) ([]string, error) {
	existing := ProfileProvenanceFromTags("", tags)
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" {
		return nil, fmt.Errorf("personal assistant: assistant id is required to mark a profile")
	}
	if existing.AssistantID != "" && existing.AssistantID != assistantID {
		return nil, fmt.Errorf("%w: profile is owned by another relationship", ErrConflict)
	}
	out := append([]string(nil), tags...)
	if existing.AssistantID == "" {
		out = append(out, ProfileAssistantMarker(assistantID))
	}
	if hireRequestID = strings.TrimSpace(hireRequestID); hireRequestID != "" && existing.HireRequestID == "" {
		out = append(out, ProfileHireMarker(hireRequestID))
	}
	return out, nil
}

// ProfileReader resolves bounded ownership for one global agent profile.
//
// It is intentionally narrower than the agent store: the read projection needs
// to know whether the hired profile still exists and is still ours, and must
// never be able to read a prompt, a credential, or someone else's agent record
// through this seam.
type ProfileReader interface {
	// PersonalAssistantProfileProvenance returns the provenance of the profile
	// currently stored under name. ok is false when no such profile exists.
	PersonalAssistantProfileProvenance(name string) (provenance ProfileProvenance, ok bool)
}
