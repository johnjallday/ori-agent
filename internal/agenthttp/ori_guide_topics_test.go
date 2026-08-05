package agenthttp

import (
	"strings"
	"testing"
)

// A topic's destination has to be a real registered route. This is the link
// between the guide's vocabulary and home_nav_catalog_test.go, which already
// proves every catalog Href resolves to a registered page — so together they
// establish that Ori can never offer a link to nowhere (FR-31/FR-49).
func TestEveryTopicNavKeyExistsInTheNavigationCatalog(t *testing.T) {
	for _, topic := range GuideTopics() {
		if topic.NavKey == "" {
			continue
		}
		if _, ok := FindHomeNavEntry(topic.NavKey); !ok {
			t.Errorf("topic %q references unknown nav key %q", topic.Key, topic.NavKey)
		}
	}
}

func TestEverySetupStepResolvesToARegisteredRoute(t *testing.T) {
	for step, navKey := range setupStepRoutes {
		if _, ok := FindHomeNavEntry(navKey); !ok {
			t.Errorf("setup step %q references unknown nav key %q", step, navKey)
		}
	}
	for _, topic := range GuideTopics() {
		if topic.Setup == "" {
			continue
		}
		if _, ok := setupStepRoutes[topic.Setup]; !ok {
			t.Errorf("topic %q uses setup step %q with no route binding", topic.Key, topic.Setup)
		}
	}
}

// FR-33 names the concepts V1 must cover. A missing one is a gap the user will
// hit as an "I don't know".
func TestApprovedConceptCatalogCoversTheRequiredConcepts(t *testing.T) {
	required := []string{
		"home", "workspace", "agent", "workspace-manager", "toolbox",
		"skill", "tool", "vault", "connection", "action-center", "personal-hq",
	}
	have := map[string]bool{}
	for _, topic := range GuideTopics() {
		have[topic.Key] = true
	}
	for _, key := range required {
		if !have[key] {
			t.Errorf("required concept %q is missing from the topic catalog", key)
		}
	}
}

// A topic pointing at a coachmark the browser cannot resolve would offer a
// "Show me where" that does nothing. The mirrored list in
// ori-guide-coachmarks.test.js checks the other direction.
func TestEveryTopicCoachmarkIsRegistered(t *testing.T) {
	known := map[CoachmarkKey]bool{}
	for _, k := range registeredCoachmarkKeys {
		known[k] = true
	}
	for _, topic := range GuideTopics() {
		if topic.Coachmark == "" {
			continue
		}
		if !known[topic.Coachmark] {
			t.Errorf("topic %q uses unregistered coachmark %q", topic.Key, topic.Coachmark)
		}
	}
}

func TestCoachmarkKeysArePlainTokens(t *testing.T) {
	for _, key := range registeredCoachmarkKeys {
		for _, bad := range []string{"#", ".", "[", " ", ">", "/"} {
			if strings.Contains(string(key), bad) {
				t.Errorf("coachmark key %q looks like a selector", key)
			}
		}
	}
}

func TestTopicsHaveStableKeysAndUsableCopy(t *testing.T) {
	seen := map[string]bool{}
	for _, topic := range GuideTopics() {
		if topic.Key == "" {
			t.Errorf("topic %q has no key", topic.Label)
		}
		if seen[topic.Key] {
			t.Errorf("duplicate topic key %q", topic.Key)
		}
		seen[topic.Key] = true

		if topic.Label == "" {
			t.Errorf("topic %q has no label", topic.Key)
		}
		// The explanation is what a user reads when no model is configured, so a
		// stub would silently degrade the model-free experience (FR-47).
		if len(strings.TrimSpace(topic.Explanation)) < 40 {
			t.Errorf("topic %q has a too-short explanation: %q", topic.Key, topic.Explanation)
		}
		if len(topic.Aliases) == 0 {
			t.Errorf("topic %q has no aliases, so nothing will ever match it", topic.Key)
		}
		for _, alias := range topic.Aliases {
			if alias != strings.ToLower(strings.TrimSpace(alias)) {
				t.Errorf("topic %q alias %q must be lowercase and trimmed to match normalization",
					topic.Key, alias)
			}
		}
	}
}

// An alias shared by two topics makes resolution order-dependent, which would
// make the guide's answers unstable for the same question.
func TestAliasesAreUnambiguousAcrossTopics(t *testing.T) {
	owner := map[string]string{}
	for _, topic := range GuideTopics() {
		for _, alias := range topic.Aliases {
			if prev, taken := owner[alias]; taken {
				t.Errorf("alias %q is claimed by both %q and %q", alias, prev, topic.Key)
			}
			owner[alias] = topic.Key
		}
	}
}

func TestFindGuideTopicResolvesKeysLabelsAndAliases(t *testing.T) {
	cases := map[string]string{
		"workspace":         "workspace",
		"Workspace":         "workspace",
		"  workspaces  ":    "workspace",
		"vault":             "vault",
		"secrets":           "vault",
		"api keys":          "model-setup",
		"action centre":     "action-center",
		"Workspace Manager": "workspace-manager",
		"toolbox":           "toolbox",
	}
	for query, want := range cases {
		topic, ok := FindGuideTopic(query)
		if !ok {
			t.Errorf("query %q did not resolve", query)
			continue
		}
		if topic.Key != want {
			t.Errorf("query %q resolved to %q, want %q", query, topic.Key, want)
		}
	}
}

// The longest matching alias wins, so a question mentioning several concepts
// lands on the most specific one rather than the first checked.
func TestLongestAliasWinsOnContainment(t *testing.T) {
	topic, ok := FindGuideTopic("where do i put my openai key")
	if !ok {
		t.Fatal("expected a match")
	}
	if topic.Key != "model-setup" {
		t.Fatalf("got %q, want model-setup", topic.Key)
	}
}

func TestFindGuideTopicRefusesToGuess(t *testing.T) {
	for _, query := range []string{"", "   ", "?", "asdfgh", "the meaning of life"} {
		if topic, ok := FindGuideTopic(query); ok {
			t.Errorf("query %q should not have matched, got %q", query, topic.Key)
		}
	}
}

// Route context re-orders approved topics; it never introduces a new one.
func TestSuggestedTopicsAreDrawnOnlyFromTheApprovedCatalog(t *testing.T) {
	approved := map[string]bool{}
	for _, topic := range GuideTopics() {
		approved[topic.Key] = true
	}
	routes := []string{"/", "/agents", "/vaults", "/mcp", "/settings", "/action-center",
		"/skills", "/plugins", "/models", "/usage", "/profile",
		"/workspace/abc", "/workspaces", "/anything-else", ""}
	for _, route := range routes {
		suggestions := suggestedTopicsFor(route)
		if len(suggestions) == 0 {
			t.Errorf("route %q produced no suggestions", route)
		}
		for _, topic := range suggestions {
			if !approved[topic.Key] {
				t.Errorf("route %q suggested unapproved topic %q", route, topic.Key)
			}
		}
	}
}

// GuideTopics hands out a copy; a caller mutating it must not corrupt the
// server-owned catalog for every later request.
func TestGuideTopicsReturnsACopy(t *testing.T) {
	first := GuideTopics()
	if len(first) == 0 {
		t.Fatal("no topics")
	}
	original := first[0].Explanation
	first[0].Explanation = "tampered"

	if GuideTopics()[0].Explanation != original {
		t.Fatal("mutating the returned slice changed the server-owned catalog")
	}
}

// Ori explains; it never claims to act. A topic whose copy promises to do the
// work would undermine the boundary the whole feature exists to draw.
func TestTopicCopyNeverPromisesToPerformWork(t *testing.T) {
	forbidden := []string{
		"i will take care", "i'll take care", "i can do that for you",
		"i will run", "i'll run", "i will create", "i'll create",
		"i will send", "i'll send", "leave it to me",
	}
	for _, topic := range GuideTopics() {
		lower := strings.ToLower(topic.Explanation)
		for _, phrase := range forbidden {
			if strings.Contains(lower, phrase) {
				t.Errorf("topic %q promises to perform work (%q)", topic.Key, phrase)
			}
		}
	}
}
