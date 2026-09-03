// Package specialist maps a detected desktop application to a domain
// specialist, so the personal-assistant hire can be shaped around the work the
// user actually does.
//
// The mapping is a built-in, server-side table. Adding a domain is one new
// Entry plus its copy — no change to the onboarding wizard, the hire payload
// builder, or the capability projection. Templates and plugins deliberately
// cannot register their own app signatures; that is a separate design.
//
// Nothing in this package acts. It answers two questions: "does a detected app
// suggest a domain?" and "what copy and ordering does that domain use?".
package specialist

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// Assignment item types. These mirror the durable first-assignment input types
// and must never change: a domain re-labels them, it does not add to them.
const (
	ItemPriority        = "priority"
	ItemIOwe            = "i_owe"
	ItemWaitingOn       = "waiting_on"
	ItemFixedCommitment = "fixed_commitment"
)

// OfferCopy is the exact wording of the in-wizard offer. The headline states
// what was found; the question asks the only thing actually unknown, which is
// whether the user wants help with it. It never asks whether they use the app —
// the install is already known, and asking reads as not paying attention.
type OfferCopy struct {
	Headline     string `json:"headline"`
	Question     string `json:"question"`
	AcceptLabel  string `json:"accept_label"`
	DeclineLabel string `json:"decline_label"`
	// AcceptedNote confirms what accepting did. It describes watching and
	// reporting, never directing: the assistant cannot hand work to a
	// specialist in another workspace, and no copy may imply otherwise.
	AcceptedNote string `json:"accepted_note"`
	// ManualLabel reaches this domain when nothing was detected — a second
	// machine, or the app installed elsewhere.
	ManualLabel string `json:"manual_label"`
}

// FocusOption is one focus checkbox offered in place of the generic six. Value
// must be a valid personalassistant.FocusArea; the server rejects anything else.
type FocusOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Selected bool   `json:"selected"`
}

// AssignmentLabel re-words one first-assignment item type. Type is the durable
// payload value and is never rewritten.
type AssignmentLabel struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	AddLabel    string `json:"add_label"`
}

// AssignmentStep re-words one of the three first-assignment wizard steps.
type AssignmentStep struct {
	Index  int    `json:"index"`
	Title  string `json:"title"`
	Legend string `json:"legend"`
}

// Suggestion is the post-hire workspace recommendation. It is a suggestion the
// user acts on deliberately: hiring never creates a workspace or runs a setup
// wizard on its behalf.
type Suggestion struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	ActionLabel string `json:"action_label"`
	ActionRoute string `json:"action_route"`
}

// Entry is one app-to-domain mapping.
type Entry struct {
	// Slug is the stable machine identity persisted on the relationship. It is
	// never shown to users and never changes once shipped.
	Slug string `json:"slug"`
	// AppPatterns are the detected application names this entry answers to,
	// normalized to lowercase tokens. See Match for the comparison rule.
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
}

// The mapping's entries live in domains.go, which is data only. Nothing in
// this file names an application.

// All returns a copy of the built-in mapping.
func All() []Entry {
	out := make([]Entry, len(registry))
	copy(out, registry)
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
			return entry, true
		}
	}
	return Entry{}, false
}

// App is the shape Match needs from a detected application. It mirrors the
// fields of detector.DetectedApp that matter here, so this package stays
// independent of how detection is performed on any one platform.
type App struct {
	Name     string
	LastUsed time.Time
}

// Match returns at most one specialist for a set of detected apps.
//
// A user with three creative apps installed is not helped by three offers at
// the moment they are trying to finish setting up, so when several apps match,
// the one used most recently wins. Ties resolve deterministically by slug and
// then by app name, so the same input always produces the same offer.
func Match(apps []App) (Entry, bool) {
	type candidate struct {
		entry Entry
		app   App
	}
	matches := make([]candidate, 0, 1)
	for _, app := range apps {
		tokens := nameTokens(app.Name)
		if len(tokens) == 0 {
			continue
		}
		for _, entry := range registry {
			if entryMatches(entry, tokens) {
				matches = append(matches, candidate{entry: entry, app: app})
				break
			}
		}
	}
	if len(matches) == 0 {
		return Entry{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left, right := matches[i], matches[j]
		if !left.app.LastUsed.Equal(right.app.LastUsed) {
			return left.app.LastUsed.After(right.app.LastUsed)
		}
		if left.entry.Slug != right.entry.Slug {
			return left.entry.Slug < right.entry.Slug
		}
		return left.app.Name < right.app.Name
	})
	return matches[0].entry, true
}

func entryMatches(entry Entry, tokens []string) bool {
	for _, pattern := range entry.AppPatterns {
		if hasPrefixTokens(tokens, pattern) {
			return true
		}
	}
	return false
}

// hasPrefixTokens reports whether the app's tokens begin with the pattern's
// tokens. Anchoring at the start is what lets a pattern match an app plus its
// version text while rejecting an unrelated product that merely happens to
// contain the pattern word later in its name.
func hasPrefixTokens(tokens, pattern []string) bool {
	if len(pattern) == 0 || len(tokens) < len(pattern) {
		return false
	}
	for i, want := range pattern {
		if tokens[i] != want {
			return false
		}
	}
	return true
}

// nameTokens lowercases an application name, drops a trailing ".app", splits on
// anything that is not a letter or digit, and strips a trailing digit run from
// each token. That is what makes matching tolerant of the version text vendors
// bake into a bundle name: "Foo", "FOO64", and "Foo 7" all reduce to a leading
// "foo" token.
func nameTokens(name string) []string {
	lowered := strings.ToLower(strings.TrimSpace(name))
	lowered = strings.TrimSuffix(lowered, ".app")
	fields := strings.FieldsFunc(lowered, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimRightFunc(field, unicode.IsDigit)
		if trimmed == "" {
			// A purely numeric token ("7", "64") carries version text only, but
			// it still has to occupy its position so a pattern cannot skip it.
			trimmed = field
		}
		tokens = append(tokens, trimmed)
	}
	return tokens
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
