package agenthttp

import (
	"strings"
	"testing"
)

// The first-run greeting behavior must appear only when firstRun is true, and
// must vanish for established users so Ori returns to normal behavior.
func TestHomeSystemPrompt_FirstRunGreetingGate(t *testing.T) {
	const marker = "what are they working on"

	firstRun := buildHomeSystemPrompt(true)
	if !strings.Contains(strings.ToLower(firstRun), marker) {
		t.Fatal("first-run prompt should include the greeting behavior")
	}
	if !strings.Contains(strings.ToLower(firstRun), "brand-new user") {
		t.Fatal("first-run prompt should frame the user as brand-new")
	}

	established := buildHomeSystemPrompt(false)
	if strings.Contains(strings.ToLower(established), marker) {
		t.Fatal("established-user prompt must NOT include the greeting behavior")
	}

	// The base behavior is present in both.
	if !strings.Contains(established, "Ori's home assistant") {
		t.Fatal("base prompt should be present regardless of firstRun")
	}
}
