package specialist

import (
	"regexp"
	"strings"
	"testing"
)

// userVisibleStrings collects every string in the mapping a user can read.
// The guards below run over all of them, for every domain, so the boundary and
// the vocabulary hold for the next entry as well as this one.
func userVisibleStrings(entry Entry) map[string]string {
	out := map[string]string{
		"display_name":              entry.DisplayName,
		"offer_copy.headline":       entry.OfferCopy.Headline,
		"offer_copy.question":       entry.OfferCopy.Question,
		"offer_copy.accept_label":   entry.OfferCopy.AcceptLabel,
		"offer_copy.decline_label":  entry.OfferCopy.DeclineLabel,
		"offer_copy.accepted_note":  entry.OfferCopy.AcceptedNote,
		"offer_copy.manual_label":   entry.OfferCopy.ManualLabel,
		"suggestion.title":          entry.Suggestion.Title,
		"suggestion.body":           entry.Suggestion.Body,
		"suggestion.action_label":   entry.Suggestion.ActionLabel,
		"specialist_name":           entry.SpecialistName,
		"suggested_template_id.doc": entry.SuggestedTemplateID,
	}
	for _, focus := range entry.FocusAreas {
		out["focus_areas["+focus.Value+"].label"] = focus.Label
	}
	for _, label := range entry.AssignmentLabels {
		out["assignment_labels["+label.Type+"].label"] = label.Label
		out["assignment_labels["+label.Type+"].placeholder"] = label.Placeholder
		out["assignment_labels["+label.Type+"].add_label"] = label.AddLabel
	}
	for _, step := range entry.AssignmentSteps {
		out["assignment_steps.title"] += " " + step.Title
		out["assignment_steps.legend"] += " " + step.Legend
	}
	return out
}

// The relationship this feature ships is read-only. The assistant can see
// across workspaces; it cannot act across them, because DelegateTask requires
// both agents in one workspace and the specialist is in its own. No copy may
// imply otherwise — the honest claim is that the assistant keeps an eye on the
// studio, not that it runs it.
func TestMappingCopyNeverImpliesTheAssistantDirectsTheSpecialist(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bdelegate\b`),
		regexp.MustCompile(`(?i)\bassign(s|ed|ing)? (it|them|work|tasks)\b`),
		regexp.MustCompile(`(?i)\bhand (off|over)\b`),
		regexp.MustCompile(`(?i)\bhands work\b`),
		regexp.MustCompile(`(?i)\binstruct(s|ed)?\b`),
		regexp.MustCompile(`(?i)\bon your behalf\b`),
		regexp.MustCompile(`(?i)\bruns your\b`),
		regexp.MustCompile(`(?i)\bmanages (it|them|the studio)\b`),
		regexp.MustCompile(`(?i)\btells? it to\b`),
		regexp.MustCompile(`(?i)\bworks for your assistant\b`),
		regexp.MustCompile(`(?i)\breports to\b`),
	}
	for _, entry := range All() {
		for field, value := range userVisibleStrings(entry) {
			for _, pattern := range forbidden {
				if pattern.MatchString(value) {
					t.Errorf("%s: %s implies the assistant can direct the specialist: %q",
						entry.Slug, field, value)
				}
			}
		}
	}
}

// "Hire" names one durable, singular relationship — the personal assistant —
// in both the code and the UI. A specialist is recruited or brought in.
func TestMappingCopyReservesHireForThePersonalAssistant(t *testing.T) {
	hire := regexp.MustCompile(`(?i)\bhir(e|es|ed|ing)\b`)
	for _, entry := range All() {
		for field, value := range userVisibleStrings(entry) {
			if hire.MatchString(value) {
				t.Errorf("%s: %s uses hire vocabulary for a specialist: %q", entry.Slug, field, value)
			}
		}
	}
}

// The hierarchy is never mandatory. Addressing the specialist directly is
// first-class, so no copy may frame it as a workaround, a fallback, or an
// advanced path — and none may require going through the assistant.
func TestMappingCopyNeverMakesTheHierarchyMandatory(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bmust (go|ask|route) through\b`),
		regexp.MustCompile(`(?i)\bonly (through|via) your assistant\b`),
		regexp.MustCompile(`(?i)\bworkaround\b`),
		regexp.MustCompile(`(?i)\badvanced (users|path|option)\b`),
		regexp.MustCompile(`(?i)\bfall ?back\b`),
		regexp.MustCompile(`(?i)\bif you really need\b`),
	}
	for _, entry := range All() {
		for field, value := range userVisibleStrings(entry) {
			for _, pattern := range forbidden {
				if pattern.MatchString(value) {
					t.Errorf("%s: %s frames the direct route as a lesser one: %q",
						entry.Slug, field, value)
				}
			}
		}
	}
}

// The offer states what was found and asks about intent. It must never put the
// install itself to the user as a question — that is already known, and asking
// reads as not paying attention.
func TestOfferCopyDoesNotAskWhatIsAlreadyKnown(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*(do|are|have) you\b`),
		regexp.MustCompile(`(?i)\bdo you use\b`),
		regexp.MustCompile(`(?i)\bare you a\b`),
		regexp.MustCompile(`(?i)\bhave you (got|installed)\b`),
	}
	for _, entry := range All() {
		for _, pattern := range forbidden {
			if pattern.MatchString(entry.OfferCopy.Question) {
				t.Errorf("%s: offer asks a question of fact: %q", entry.Slug, entry.OfferCopy.Question)
			}
			if pattern.MatchString(entry.OfferCopy.Headline) {
				t.Errorf("%s: offer headline asks a question of fact: %q", entry.Slug, entry.OfferCopy.Headline)
			}
		}
		// The headline states a finding; the question asks about intent.
		if !strings.HasSuffix(strings.TrimSpace(entry.OfferCopy.Question), "?") {
			t.Errorf("%s: offer question is not a question: %q", entry.Slug, entry.OfferCopy.Question)
		}
		if strings.Contains(entry.OfferCopy.Headline, "?") {
			t.Errorf("%s: offer headline should state, not ask: %q", entry.Slug, entry.OfferCopy.Headline)
		}
	}
}

// Every entry's focus values must be plausible enum members: the server
// validates them against a closed enum and rejects anything else, so a typo
// here would fail the hire rather than degrade.
func TestFocusValuesLookLikeEnumMembers(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for _, entry := range All() {
		seen := map[string]struct{}{}
		for _, focus := range entry.FocusAreas {
			if !valid.MatchString(focus.Value) {
				t.Errorf("%s: focus value %q is not a canonical enum form", entry.Slug, focus.Value)
			}
			if _, duplicate := seen[focus.Value]; duplicate {
				t.Errorf("%s: focus value %q is offered twice", entry.Slug, focus.Value)
			}
			seen[focus.Value] = struct{}{}
		}
	}
}

// A domain re-words the four durable item types. It may not add a fifth, drop
// one, or rename a type.
func TestAssignmentLabelsOnlyCoverTheDurableItemTypes(t *testing.T) {
	allowed := map[string]struct{}{
		ItemPriority: {}, ItemIOwe: {}, ItemWaitingOn: {}, ItemFixedCommitment: {},
	}
	for _, entry := range All() {
		seen := map[string]struct{}{}
		for _, label := range entry.AssignmentLabels {
			if _, ok := allowed[label.Type]; !ok {
				t.Errorf("%s: assignment label for unknown item type %q", entry.Slug, label.Type)
			}
			if _, duplicate := seen[label.Type]; duplicate {
				t.Errorf("%s: item type %q is labelled twice", entry.Slug, label.Type)
			}
			seen[label.Type] = struct{}{}
		}
		if len(seen) != len(allowed) {
			t.Errorf("%s: labelled %d item types, want %d", entry.Slug, len(seen), len(allowed))
		}
		for index, step := range entry.AssignmentSteps {
			if step.Index != index {
				t.Errorf("%s: assignment step %d declares index %d", entry.Slug, index, step.Index)
			}
		}
	}
}

// The suggestion is a route inside this app, and hiring never follows it. An
// absolute or protocol-relative URL would be a way out of the app.
func TestSuggestionRoutesStayInsideTheApp(t *testing.T) {
	for _, entry := range All() {
		route := entry.Suggestion.ActionRoute
		if !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") ||
			strings.Contains(route, "://") || strings.ContainsAny(route, "\r\n") {
			t.Errorf("%s: suggestion route is not app-relative: %q", entry.Slug, route)
		}
	}
}
