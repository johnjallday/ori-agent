// Package specialist retains the domain metadata needed to read historical
// accepted relationships and their downstream setup journeys. New capability
// offers are triggered only by user-fed folder evidence in folderdigest.
package specialist

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Assignment item types. These mirror the durable first-assignment input types
// and must never change: a domain re-labels them, it does not add to them.
const (
	ItemPriority        = "priority"
	ItemIOwe            = "i_owe"
	ItemWaitingOn       = "waiting_on"
	ItemFixedCommitment = "fixed_commitment"
)

// OfferCopy retains the domain-specific copy in the host capability table.
type OfferCopy = folderdigest.OfferCopy

// FocusOption is one focus checkbox offered in place of the generic six. Value
// must be a valid personalassistant.FocusArea; the server rejects anything else.
type FocusOption = folderdigest.FocusOption

// AssignmentLabel re-words one first-assignment item type. Type is the durable
// payload value and is never rewritten.
type AssignmentLabel = folderdigest.AssignmentLabel

// AssignmentStep re-words one of the three first-assignment wizard steps.
type AssignmentStep = folderdigest.AssignmentStep

// Suggestion is the post-hire workspace recommendation. It is a suggestion the
// user acts on deliberately: hiring never creates a workspace or runs a setup
// wizard on its behalf.
type Suggestion = folderdigest.Suggestion

// Entry is the legacy domain projection used by accepted relationships.
type Entry struct {
	// Slug is the stable machine identity persisted on the relationship. It is
	// never shown to users and never changes once shipped.
	Slug string `json:"slug"`
	// AppPatterns are retained for compatibility with historical projections;
	// app matching no longer triggers capability offers.
	AppPatterns [][]string `json:"-"`
	// DisplayName is the domain in the user's words, e.g. "music projects".
	DisplayName string `json:"display_name"`
	// SpecialistName is the named expert who owns this domain's work. It is the
	// agent the domain's workspace template already seeds — this package never
	// creates it.
	SpecialistName string `json:"specialist_name"`

	OfferCopy        OfferCopy         `json:"offer_copy"`
	FocusAreas       []FocusOption     `json:"focus_areas"`
	AssignmentLabels []AssignmentLabel `json:"assignment_labels"`
	AssignmentSteps  []AssignmentStep  `json:"assignment_steps"`

	// SuggestedTemplateID is the workspace blueprint recommended after hire.
	SuggestedTemplateID string     `json:"suggested_template_id"`
	Suggestion          Suggestion `json:"suggestion"`

	// CapabilityOrder lists post-hire capability card keys in the order this
	// domain wants them. Keys the projection does not know are ignored; keys it
	// knows but this list omits keep their default relative order at the end.
	CapabilityOrder []string `json:"capability_order"`

	// IntegrationKey optionally names the reviewed integration this domain is
	// set up with. Its guided setup is the integration's generated install quest
	// until the plugin is installed, then the quest the plugin declares. Entries
	// without one retain their existing suggestion behavior. The key must exist
	// in the reviewed-integration registry; that package's tests prove it, since
	// it imports this one.
	IntegrationKey string `json:"integration_key,omitempty"`
}

// Historical domain metadata is derived from host-owned capability rows.
// No independent domain definitions or plugin-authored rows are accepted.
var registry = mustNormalizeRegistry(entriesFromCapabilities())

func entriesFromCapabilities() []Entry {
	var entries []Entry
	for _, row := range folderdigest.AllCapabilities() {
		if row.Offer == nil {
			continue
		}
		offer := row.Offer
		entries = append(entries, Entry{
			Slug: offer.Slug, AppPatterns: offer.AppPatterns,
			DisplayName: offer.DisplayName, SpecialistName: offer.SpecialistName,
			OfferCopy: offer.OfferCopy, FocusAreas: offer.FocusAreas,
			AssignmentLabels: offer.AssignmentLabels, AssignmentSteps: offer.AssignmentSteps,
			SuggestedTemplateID: offer.SuggestedTemplateID, Suggestion: offer.Suggestion,
			CapabilityOrder: offer.CapabilityOrder, IntegrationKey: offer.IntegrationKey,
		})
	}
	return entries
}

// All returns a deep copy of the built-in mapping.
func All() []Entry {
	out := make([]Entry, len(registry))
	for index := range registry {
		out[index] = cloneEntry(registry[index])
	}
	return out
}

// Get returns the entry for a slug. An empty or unknown slug returns false, so
// callers can reject a slug that did not come from this table.
func Get(slug string) (Entry, bool) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return Entry{}, false
	}
	for _, entry := range registry {
		if entry.Slug == slug {
			return cloneEntry(entry), true
		}
	}
	return Entry{}, false
}

// FocusValues returns the entry's focus values in declared order.
func (e Entry) FocusValues() []string {
	out := make([]string, 0, len(e.FocusAreas))
	for _, focus := range e.FocusAreas {
		out = append(out, focus.Value)
	}
	return out
}

// MatchesTemplate reports whether a workspace's template ID is this entry's
// suggested blueprint. A blueprint published by a plugin carries a namespaced
// ID ("plugin:<plugin>:<blueprint>"), so the bare blueprint ID is compared
// against the last segment as well.
func (e Entry) MatchesTemplate(templateID string) bool {
	suggested := strings.TrimSpace(e.SuggestedTemplateID)
	templateID = strings.TrimSpace(templateID)
	if suggested == "" || templateID == "" {
		return false
	}
	if templateID == suggested {
		return true
	}
	return strings.HasSuffix(templateID, ":"+suggested)
}

func cloneEntry(source Entry) Entry {
	clone := source
	clone.AppPatterns = make([][]string, len(source.AppPatterns))
	for index := range source.AppPatterns {
		clone.AppPatterns[index] = append([]string(nil), source.AppPatterns[index]...)
	}
	clone.FocusAreas = append([]FocusOption(nil), source.FocusAreas...)
	clone.AssignmentLabels = append([]AssignmentLabel(nil), source.AssignmentLabels...)
	clone.AssignmentSteps = append([]AssignmentStep(nil), source.AssignmentSteps...)
	clone.CapabilityOrder = append([]string(nil), source.CapabilityOrder...)
	return clone
}
