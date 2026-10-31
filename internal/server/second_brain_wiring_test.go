package server

import "testing"

// A second brain Ori set up is written to by three things: the setup, the
// Journal's Daily copy and the assistant's capture tool. Each reaches the one
// notes service the builder made; none builds its own.
func TestSecondBrain_TheWritersShareTheNotesServiceTheBuilderMade(t *testing.T) {
	f := newDraftServerFixture(t)
	service := f.builder.personalAssistantSecondBrain
	if service == nil {
		t.Fatal("the builder made no second-brain service")
	}
	if f.builder.server.Storage.PersonalAssistantSecondBrain != service {
		t.Fatal("the storage facade does not hold the builder's second-brain service")
	}
	if got := f.builder.server.newHomeAssistantAskHandler().SecondBrainCapture; got == nil {
		t.Fatal("the assistant's capture tool is not wired to the notes service")
	}
	// The Journal copy reads the service when a journal is saved, so it cannot
	// be built before it; with nothing connected it copies nothing.
	mirror := journalDailyMirror{builder: f.builder}
	if result := mirror.MirrorDaily(t.Context(), "local", "2026-10-31", "# Journal"); result != "" {
		t.Fatalf("mirror with nothing connected = %q", result)
	}
	if result := (journalDailyMirror{}).MirrorDaily(t.Context(), "local", "2026-10-31", "# Journal"); result != "" {
		t.Fatalf("mirror with no builder = %q", result)
	}
}
