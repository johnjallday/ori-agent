package server

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/folderagent"
)

// The seam translates the folder agent's hold for the chat handler, and holds
// nothing until the folder agent is wired (it is wired in another phase).
func TestFolderWelcomeHold_MapsTheFolderAgentsHoldAndHoldsNothingUnwired(t *testing.T) {
	for hold, want := range map[folderagent.WelcomeHold]chathttp.FolderWelcome{
		folderagent.WelcomeNotFed:     chathttp.FolderWelcomeNone,
		folderagent.WelcomeHeld:       chathttp.FolderWelcomeHold,
		folderagent.WelcomeAfterSetUp: chathttp.FolderWelcomeAfterSetUp,
		folderagent.WelcomeAfterKept:  chathttp.FolderWelcomeAfterKept,
		folderagent.WelcomeHold(99):   chathttp.FolderWelcomeNone,
	} {
		if got := folderWelcomeOf(hold); got != want {
			t.Errorf("folderWelcomeOf(%d) = %q, want %q", hold, got, want)
		}
	}
	if got := (folderWelcomeHold{}).FolderWelcome("ws-1"); got != chathttp.FolderWelcomeNone {
		t.Errorf("no builder: %q", got)
	}
	if got := (folderWelcomeHold{builder: &ServerBuilder{}}).FolderWelcome("ws-1"); got != chathttp.FolderWelcomeNone {
		t.Errorf("folder agent not wired: %q", got)
	}
}

// Through the real service and store: a workspace with a looking record is held,
// one set up or kept is released with the right words, and one with no record is
// not held.
func TestFolderWelcomeHold_ReadsTheRealFolderAgent(t *testing.T) {
	dir := t.TempDir()
	store := folderagent.NewStore(func(id string) (string, error) { return filepath.Join(dir, id), nil })
	b := &ServerBuilder{folderAgentService: folderagent.NewService(folderagent.Deps{Store: store})}
	seam := folderWelcomeHold{builder: b}

	if got := seam.FolderWelcome("never-fed"); got != chathttp.FolderWelcomeNone {
		t.Fatalf("no record: %q", got)
	}
	if err := store.Create(&folderagent.Record{WorkspaceID: "ws-1", State: folderagent.StateLooking}); err != nil {
		t.Fatal(err)
	}
	if got := seam.FolderWelcome("ws-1"); got != chathttp.FolderWelcomeHold {
		t.Fatalf("looking: %q", got)
	}
	for state, want := range map[folderagent.State]chathttp.FolderWelcome{
		folderagent.StateReported:  chathttp.FolderWelcomeHold,
		folderagent.StateFailed:    chathttp.FolderWelcomeHold,
		folderagent.StateSettingUp: chathttp.FolderWelcomeHold,
		folderagent.StateSetUp:     chathttp.FolderWelcomeAfterSetUp,
		folderagent.StateKept:      chathttp.FolderWelcomeAfterKept,
	} {
		if _, err := store.Update("ws-1", func(r *folderagent.Record) error { r.State = state; return nil }); err != nil {
			t.Fatal(err)
		}
		if got := seam.FolderWelcome("ws-1"); got != want {
			t.Errorf("state %s: %q, want %q", state, got, want)
		}
	}
}
