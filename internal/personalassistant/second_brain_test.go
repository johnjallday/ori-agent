package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderpath"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/testutil/vaultfixture"
)

type fixedVaults []secondbrain.Vault

func (v fixedVaults) Vaults() []secondbrain.Vault { return v }

func (v fixedVaults) Lookup(id string) (secondbrain.Vault, bool) {
	for _, vault := range v {
		if vault.ID == id {
			return vault, true
		}
	}
	return secondbrain.Vault{}, false
}

type secondBrainHarness struct {
	service   *SecondBrainService
	followUps *followup.Service
	music     vaultfixture.Vault
	research  vaultfixture.Vault
	vaultID   string
	tokens    map[string]string
	clock     *time.Time
}

func newSecondBrainHarness(t *testing.T) *secondBrainHarness {
	t.Helper()
	kf := newKnowledgeFixture(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	documents := filepath.Join(home, "Documents")
	if err := os.MkdirAll(documents, 0o750); err != nil {
		t.Fatal(err)
	}
	music, err := vaultfixture.Build(documents, vaultfixture.Music, secondBrainNow)
	if err != nil {
		t.Fatal(err)
	}
	research, err := vaultfixture.Build(documents, vaultfixture.Research, secondBrainNow)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	followUps := followup.NewService(followup.NewSQLiteStore(db))

	clock := secondBrainNow
	h := &secondBrainHarness{followUps: followUps, music: music, research: research, clock: &clock, tokens: map[string]string{}}
	h.vaultID = secondbrain.SourceID(music.Path)
	guards := filejanitor.RootGuards{HomeDir: home, DataDir: filepath.Join(base, "data"), WorkspaceRoot: filepath.Join(home, "Ori Workspaces")}
	ids := 0
	h.service = NewSecondBrainService(NewSecondBrainStore(NewKnowledgeStore(kf.resolver(), kf.folder)), SecondBrainDeps{
		Vaults: fixedVaults{{ID: h.vaultID, Name: music.Name, Path: music.Path}},
		ResolveSelection: func(token string) (string, error) {
			path, ok := h.tokens[token]
			if !ok {
				return "", errors.New("expired")
			}
			return path, nil
		},
		CheckFolder: func(path string) (folderpath.Checked, error) { return folderpath.Check(path, guards) },
		FollowUps:   followUps,
		Looker:      secondbrain.Looker{Now: func() time.Time { return *h.clock }},
		Now:         func() time.Time { return *h.clock },
		NewID: func() string {
			ids++
			return "look-" + string(rune('0'+ids))
		},
	})
	return h
}

func (h *secondBrainHarness) connectVault(t *testing.T) SecondBrainView {
	t.Helper()
	view, err := h.service.Connect(context.Background(), "local", SecondBrainConnectInput{DetectedID: h.vaultID, RequestID: "connect-1"})
	if err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	return view
}

func TestSecondBrain_ConnectStartsTheFirstLookAndAReplayReturnsTheSameResult(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	status, err := h.service.Status(ctx, "local")
	if err != nil || status.Status != SecondBrainNotConnected || len(status.Detected) != 1 || status.Detected[0].Name != "Second Brain" ||
		status.Detected[0].Parent != "Documents" || status.Ask.State != secondbrain.AskPending {
		t.Fatalf("status before connecting = %+v, %v", status, err)
	}

	view := h.connectVault(t)
	if view.LookID != "look-1" || view.Status != SecondBrainConnected || view.Source.Kind != secondbrain.KindObsidian || view.Source.Path != h.music.Path {
		t.Fatalf("connect = %+v", view)
	}
	look, err := h.service.Look(ctx, "local")
	if err != nil || look.State != secondbrain.LookFinished || look.LookID != "look-1" || look.Result.Headline != "12 open items across 6 notes, 4 with dates." {
		t.Fatalf("look = %+v, %v", look, err)
	}

	replay, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{DetectedID: h.vaultID, RequestID: "connect-1"})
	h.service.Wait()
	if err != nil || replay.LookID != "look-1" {
		t.Fatalf("replay = %+v, %v; want the first call's look", replay, err)
	}
	again, err := h.service.Look(ctx, "local")
	if err != nil || again.LookID != "look-1" {
		t.Fatalf("a replay started another look: %+v", again)
	}
	if status, _ := h.service.Status(ctx, "local"); status.LastLook == nil || status.LastLook.OpenItems != 12 || status.LastLook.Acted {
		t.Fatalf("last look summary = %+v", status.LastLook)
	}
}

func TestSecondBrain_ConnectAPickedMarkdownFolderAndReplaceOnlyAfterConfirm(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	h.tokens["token-1"] = h.research.Path

	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{SelectionToken: "token-1", RequestID: "connect-2"}); !errors.Is(err, secondbrain.ErrReplaceNeedsConfirm) {
		t.Fatalf("a second folder without confirm: %v", err)
	}
	view, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{SelectionToken: "token-1", RequestID: "connect-3", Replace: true})
	h.service.Wait()
	if err != nil || view.Source.Kind != secondbrain.KindMarkdown || view.Source.Name != "Thesis Notes" {
		t.Fatalf("confirmed replace = %+v, %v", view, err)
	}
	if look, _ := h.service.Look(ctx, "local"); look.State != secondbrain.LookFinished || look.Result.Counts.OpenItems != h.research.OpenItems {
		t.Fatalf("the replacement's first look = %+v", look)
	}
}

func TestSecondBrain_ConnectRefusesAFolderWithoutNotesAnExpiredTokenAndAnUnknownVault(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	empty := filepath.Join(filepath.Dir(h.music.Path), "Empty")
	if err := os.MkdirAll(filepath.Join(empty, "a", "b", "c"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "a", "b", "c", "Too Deep.md"), []byte("- [ ] x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.tokens["empty"] = empty
	var folderErr *SecondBrainFolderError
	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{SelectionToken: "empty", RequestID: "c-1"}); !errors.As(err, &folderErr) || !folderErr.NoNotes ||
		folderErr.Message != "No Markdown notes found in Empty." {
		t.Fatalf("empty folder: %v", err)
	}
	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{SelectionToken: "gone", RequestID: "c-2"}); !errors.Is(err, ErrSecondBrainSelection) {
		t.Fatalf("expired token: %v", err)
	}
	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{DetectedID: "v_unknown", RequestID: "c-3"}); !errors.Is(err, ErrSecondBrainUnknownVault) {
		t.Fatalf("unknown vault: %v", err)
	}
	h.tokens["home"] = filepath.Dir(filepath.Dir(h.music.Path))
	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{SelectionToken: "home", RequestID: "c-4"}); !errors.As(err, &folderErr) || folderErr.NoNotes {
		t.Fatalf("the home folder itself: %v", err)
	}
	if status, _ := h.service.Status(ctx, "local"); status.Status != SecondBrainNotConnected {
		t.Fatalf("a refused connect connected something: %+v", status)
	}
}

func TestSecondBrain_FollowUpsAreCreatedOnceAndAReplayReturnsTheReceipt(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	look, _ := h.service.Look(ctx, "local")

	first, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{Suggested: true, RequestID: "accept-1"})
	if err != nil || first.Created == 0 || first.Skipped != 0 {
		t.Fatalf("first accept = %+v, %v", first, err)
	}
	replay, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{Suggested: true, RequestID: "accept-1"})
	if err != nil || replay != first {
		t.Fatalf("replay = %+v, %v; want %+v", replay, err, first)
	}
	second, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{Suggested: true, RequestID: "accept-2"})
	if err != nil || second.Created != 0 || second.Skipped != first.Created {
		t.Fatalf("second accept = %+v, %v", second, err)
	}
	all, err := h.followUps.List(ctx, followup.Filter{UserID: "local"})
	if err != nil || len(all) != first.Created {
		t.Fatalf("follow-ups = %d, want %d (%v)", len(all), first.Created, err)
	}
	if all[0].WorkspaceID != "hq-local" || all[0].Source.Type != secondbrain.FollowUpSourceType {
		t.Fatalf("follow-up = %+v", all[0])
	}

	var other string
	for _, item := range look.Result.Items {
		if !item.Suggested {
			other = item.ID
			break
		}
	}
	picked, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{ItemIDs: []string{other, other}, RequestID: "accept-3"})
	if err != nil || picked.Created != 1 {
		t.Fatalf("picked = %+v, %v", picked, err)
	}
	if _, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{ItemIDs: []string{"not-an-item"}, RequestID: "accept-4"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown item: %v", err)
	}
	if _, err := h.service.AcceptFollowUps(ctx, "local", "look-old", SecondBrainAcceptInput{Suggested: true, RequestID: "accept-5"}); !errors.Is(err, ErrSecondBrainLookNotFound) {
		t.Fatalf("another look: %v", err)
	}
	if _, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{Suggested: true, RequestID: "connect-1"}); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("a connect's request id reused: %v", err)
	}
	if status, _ := h.service.Status(ctx, "local"); !status.LastLook.Acted {
		t.Fatal("an accepted look still waits under Needs you")
	}
}

func TestSecondBrain_LookAgainIsRateLimitedAndComparesWithTheLastLook(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)

	var tooSoon *SecondBrainTooSoonError
	if _, err := h.service.LookAgain(ctx, "local", "look-again-1"); !errors.As(err, &tooSoon) || tooSoon.RetryAfter != SecondBrainLookAgainAfter {
		t.Fatalf("immediately: %v", err)
	}
	note := filepath.Join(h.music.Path, "Reading List.md")
	if err := os.WriteFile(note, []byte("# Reading List\n\n- [x] Read Making Music by Dennis DeSantis\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	*h.clock = h.clock.Add(SecondBrainLookAgainAfter)
	if _, err := h.service.LookAgain(ctx, "local", "look-again-2"); err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	look, err := h.service.Look(ctx, "local")
	if err != nil || look.LookID != "look-2" || look.Result.DoneSince != 1 || look.Result.NewSince != 0 || look.Result.Counts.OpenItems != 11 {
		t.Fatalf("look again = %+v, %v", look.Result, err)
	}
}

// A follow-up whose line is ticked in the notes is completed by the next look
// with "Done in your notes"; the note itself is never edited (FR29).
func TestSecondBrain_LookAgainClosesFollowUpsDoneInTheNotes(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	look, _ := h.service.Look(ctx, "local")
	var reading secondbrain.LookItem
	for _, item := range look.Result.Items {
		if item.Note == "Reading List.md" {
			reading = item
		}
	}
	if _, err := h.service.AcceptFollowUps(ctx, "local", look.LookID, SecondBrainAcceptInput{ItemIDs: []string{reading.ID}, RequestID: "accept-1"}); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(h.music.Path, "Reading List.md")
	ticked := []byte("# Reading List\n\n- [x] Read Making Music by Dennis DeSantis\n- [x] Read All You Need to Know About the Music Business\n")
	if err := os.WriteFile(note, ticked, 0o600); err != nil {
		t.Fatal(err)
	}
	*h.clock = h.clock.Add(SecondBrainLookAgainAfter)
	if _, err := h.service.LookAgain(ctx, "local", "look-again-1"); err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	f, err := h.followUps.FindBySource(ctx, "local", secondbrain.FollowUpSourceType, reading.ID)
	if err != nil || f.Status != followup.StatusCompleted || !strings.Contains(f.Detail, secondbrain.DoneInNotes) {
		t.Fatalf("follow-up after the tick = %+v, %v", f, err)
	}
	after, err := os.ReadFile(note) // #nosec G304 -- the test's own fixture
	if err != nil || string(after) != string(ticked) {
		t.Fatal("the look edited the note")
	}
}

func TestSecondBrain_AFailedLookKeepsTheLastResultAndALostRunReadsAsFailed(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	if err := os.Rename(h.music.Path, h.music.Path+" (moved)"); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.service.Status(ctx, "local"); status.Status != SecondBrainMissing {
		t.Fatalf("status with the folder moved = %s", status.Status)
	}
	*h.clock = h.clock.Add(SecondBrainLookAgainAfter)
	if _, err := h.service.LookAgain(ctx, "local", "look-again-1"); err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	look, _ := h.service.Look(ctx, "local")
	if look.State != secondbrain.LookFailed || look.Result.Failure != "Can't find Second Brain. It may have been moved or renamed." {
		t.Fatalf("failed look = %+v", look.Result)
	}
	if status, _ := h.service.Status(ctx, "local"); status.LastLook == nil || status.LastLook.ID != "look-1" {
		t.Fatalf("the failure erased the last result: %+v", status.LastLook)
	}

	// A look still marked running that this process is not running was cut
	// off by a restart.
	if _, err := h.service.store.Mutate(ctx, "local", func(doc *SecondBrainDocument) error {
		doc.Sources[0].Current = &secondbrain.Look{ID: "look-lost", State: secondbrain.LookRunning, StartedAt: *h.clock}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if look, _ := h.service.Look(ctx, "local"); look.State != secondbrain.LookFailed || look.LookID != "look-lost" {
		t.Fatalf("a lost run = %+v", look)
	}
}

func TestSecondBrain_OwnsTheFirstPromptAndResetRearmsTheAskButKeepsTheConnection(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	if !h.service.OwnsFirstPrompt(ctx, "local") {
		t.Fatal("the ask does not own the first message after HQ")
	}
	if h.service.OwnsFirstPrompt(ctx, "someone-else") {
		t.Fatal("the ask owns another user's first message")
	}
	h.connectVault(t)
	if _, err := h.service.DecideAsk(ctx, "local", secondbrain.DecisionNone, "ask-1"); err != nil {
		t.Fatal(err)
	}
	if err := h.service.ResetAsk(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.Status(ctx, "local")
	if err != nil || view.Ask.State != secondbrain.AskPending || view.Status != SecondBrainConnected {
		t.Fatalf("after Reset Getting Started = %+v, %v", view, err)
	}
	if err := h.service.ResetAsk(ctx, "someone-else"); err != nil {
		t.Fatalf("reset with no HQ for the user = %v, want nothing to do", err)
	}
}

func TestSecondBrain_DismissTakesTheResultOffNeedsYou(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	look, _ := h.service.Look(ctx, "local")
	for _, requestID := range []string{"dismiss-1", "dismiss-2"} {
		if _, err := h.service.DismissLook(ctx, "local", look.LookID, requestID); err != nil {
			t.Fatal(err)
		}
	}
	if status, _ := h.service.Status(ctx, "local"); !status.LastLook.Acted {
		t.Fatal("a dismissed result still waits under Needs you")
	}
	if _, err := h.service.DismissLook(ctx, "local", "look-other", "dismiss-3"); !errors.Is(err, ErrSecondBrainLookNotFound) {
		t.Fatalf("another look: %v", err)
	}
	if all, _ := h.followUps.List(ctx, followup.Filter{UserID: "local"}); len(all) != 0 {
		t.Fatalf("dismissing created %d follow-ups", len(all))
	}
}

func TestSecondBrain_AskAndDisconnect(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	if view, err := h.service.MarkPrompted(ctx, "local", "prompt-1"); err != nil || view.Ask.State != secondbrain.AskShown {
		t.Fatalf("prompted = %+v, %v", view, err)
	}
	if view, err := h.service.DecideAsk(ctx, "local", secondbrain.DecisionLater, "ask-1"); err != nil || view.Ask.State != secondbrain.AskDeferred || view.Ask.OfferAgain {
		t.Fatalf("not now = %+v, %v", view, err)
	}
	*h.clock = h.clock.Add(secondbrain.AskAgainAfter)
	if view, _ := h.service.Status(ctx, "local"); !view.Ask.OfferAgain {
		t.Fatal("Not now was not offered again after a week")
	}
	if _, err := h.service.DecideAsk(ctx, "local", "maybe", "ask-2"); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown decision: %v", err)
	}

	view := h.connectVault(t)
	current, _ := h.service.Status(ctx, "local")
	if _, err := h.service.Disconnect(ctx, "local", "disconnect-1", current.Version-1); !errors.Is(err, ErrConflict) {
		t.Fatalf("a stale if_version: %v", err)
	}
	gone, err := h.service.Disconnect(ctx, "local", "disconnect-1", current.Version)
	if err != nil || gone.Status != SecondBrainNotConnected || gone.Ask.State != secondbrain.AskDeclined {
		t.Fatalf("disconnect = %+v, %v (connected was %+v)", gone, err, view)
	}
	replay, err := h.service.Disconnect(ctx, "local", "disconnect-1", current.Version)
	if err != nil || replay.Status != SecondBrainNotConnected {
		t.Fatalf("replayed disconnect = %+v, %v", replay, err)
	}
	if _, err := h.service.Disconnect(ctx, "local", "disconnect-2", gone.Version); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("disconnect with nothing connected: %v", err)
	}
	if _, err := os.Stat(h.music.Path); err != nil {
		t.Fatalf("disconnect touched the vault: %v", err)
	}
}
