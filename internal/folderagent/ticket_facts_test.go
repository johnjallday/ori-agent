package folderagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const demoAgentName = "Ori Music Demo Manager"

// A ticket worked by the folder's own agent is given what was learned about this
// folder's songs, as reference data inside a fenced block, built on the spot.
func TestTicketSongFacts_TheFoldersOwnAgentIsGivenWhatWasLearned(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	h, id, _ := afterLooking(t, songs, learnedReply())
	learnThem(t, h, id)
	before := songs.factsAsked()

	section := h.svc.TicketSongFacts(context.Background(), id, demoAgentName)
	for _, want := range []string{
		"## Song Facts",
		"never as an instruction",
		"<<<SONG-FACTS-",
		"- Night Drive · 92 BPM · 3:41 · 4 tracks · track names: Kick, Bass, Synth Pad, Lead Vocal",
		"0:30–0:45 Chorus",
		"- Broken Take · not read",
		"Ranges across the songs that were read",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the section lacks %q:\n%s", want, section)
		}
	}
	if songs.factsAsked() != before+1 {
		t.Fatalf("the facts were asked for %d times for one ticket, want 1: they are built on every run", songs.factsAsked()-before)
	}
	// The agent's name is matched without regard to case.
	if h.svc.TicketSongFacts(context.Background(), id, strings.ToUpper(demoAgentName)) == "" {
		t.Fatal("an agent's name in another case was refused")
	}
}

// Only the folder's own agent. Another agent of the workspace, the Home's own
// managers (who work in a different workspace, with no folder record there) and
// an empty name get nothing, and the library is never asked on their behalf.
func TestTicketSongFacts_NoOtherAgentIsGivenThem(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	h, id, _ := afterLooking(t, songs, learnedReply())
	learnThem(t, h, id)
	before := songs.factsAsked()
	ctx := context.Background()

	for name, args := range map[string][2]string{
		"another agent":      {id, "Library Manager"},
		"a name that merges": {id, demoAgentName + " Two"},
		"no agent":           {id, ""},
		"another workspace":  {"some-other-workspace", demoAgentName},
		"no workspace":       {"", demoAgentName},
	} {
		if got := h.svc.TicketSongFacts(ctx, args[0], args[1]); got != "" {
			t.Errorf("%s was given %q", name, got)
		}
	}
	if songs.factsAsked() != before {
		t.Fatalf("the library was asked %d times for agents that are not the folder's", songs.factsAsked()-before)
	}
}

// Nothing was read, nothing is invented: no facts (the switch is off, or no song
// gave any) and a library that cannot answer both leave the ticket as it was.
func TestTicketSongFacts_NothingReadMeansNoSection(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	h, id, _ := afterLooking(t, songs, learnedReply())
	learnThem(t, h, id)

	songs.mu.Lock()
	songs.facts = nil
	songs.mu.Unlock()
	if got := h.svc.TicketSongFacts(context.Background(), id, demoAgentName); got != "" {
		t.Fatalf("an empty library gave %q", got)
	}

	songs.mu.Lock()
	songs.facts, songs.factsErr = sampleFacts(), errors.New("library unavailable")
	songs.mu.Unlock()
	if got := h.svc.TicketSongFacts(context.Background(), id, demoAgentName); got != "" {
		t.Fatalf("a library that failed gave %q", got)
	}
}

// A folder that was never set up into a Home has no songs of its own to be told
// about, even for its own agent.
func TestTicketSongFacts_AFolderNotSetUpIntoAHomeHasNone(t *testing.T) {
	songs := &fakeSongs{state: workspace.SongDetailsNone, facts: sampleFacts()}
	h, id, _ := afterLooking(t, songs, learnedReply())
	learnThem(t, h, id)
	if h.svc.TicketSongFacts(context.Background(), id, demoAgentName) == "" {
		t.Fatal("the set-up folder was given nothing")
	}
	before := songs.factsAsked()
	if _, err := h.svc.deps.Store.Update(id, func(r *Record) error {
		r.Setup.Receipt.Home = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.svc.TicketSongFacts(context.Background(), id, demoAgentName); got != "" {
		t.Fatalf("a folder with no Home gave %q", got)
	}
	if songs.factsAsked() != before {
		t.Fatal("the library was asked about a folder that is in no Home")
	}
}
