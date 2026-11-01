package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

func readTestFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parts...)) // #nosec G304 -- the test's own temp tree
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// createdHarness sets one up in the harness's Documents and returns its path.
func createdHarness(t *testing.T) (*secondBrainHarness, string) {
	t.Helper()
	h := newSecondBrainHarness(t)
	if _, err := h.create(t, "My Brain"); err != nil {
		t.Fatal(err)
	}
	return h, filepath.Join(h.documents(), "My Brain")
}

func TestSecondBrainCapture_WritesOneInboxNoteAndAReplayReturnsIt(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)

	first, err := h.service.Capture(ctx, "local", "  Ask Dana about the pressing run\nand the invoice ", "capture-1")
	if err != nil {
		t.Fatal(err)
	}
	wantRel := "Inbox/20261021-0900-ask-dana-about-the-pressing-run.md"
	if first.Note != wantRel || first.Name != "20261021-0900-ask-dana-about-the-pressing-run" ||
		!strings.HasPrefix(first.Link, "obsidian://open?path=") || !strings.Contains(first.Link, "My%20Brain") {
		t.Fatalf("capture = %+v", first)
	}
	content := readTestFile(t, brain, "Inbox", "20261021-0900-ask-dana-about-the-pressing-run.md")
	mark := secondbrain.ReadMark([]byte(content))
	if mark.Kind != secondbrain.NoteKindCapture || !mark.OwnedByOri || !strings.Contains(content, "and the invoice\n\nCaptured by Ori on 2026-10-21.") {
		t.Fatalf("note = %q (%+v)", content, mark)
	}

	before := snapshotTree(t, brain)
	replay, err := h.service.Capture(ctx, "local", "something else entirely", "capture-1")
	if err != nil || replay != first {
		t.Fatalf("replay = %+v, %v; want %+v", replay, err, first)
	}
	if after := snapshotTree(t, brain); after != before {
		t.Fatalf("a replay wrote again.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := h.service.Connect(ctx, "local", SecondBrainConnectInput{DetectedID: h.vaultID, RequestID: "capture-1"}); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("the id reused for another action: %v", err)
	}
}

func TestSecondBrainCapture_ANameTakenGetsANumberNeverAnOverwrite(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	one, err := h.service.Capture(ctx, "local", "Pick up the proofs", "c-1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := h.service.Capture(ctx, "local", "Pick up the proofs", "c-2")
	if err != nil {
		t.Fatal(err)
	}
	// The user wrote a note at the third name before the third capture.
	taken := filepath.Join(brain, "Inbox", "20261021-0900-pick-up-the-proofs-3.md")
	if err := os.WriteFile(taken, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	three, err := h.service.Capture(ctx, "local", "Pick up the proofs", "c-3")
	if err != nil {
		t.Fatal(err)
	}
	if one.Note == two.Note || two.Note == three.Note || three.Note != "Inbox/20261021-0900-pick-up-the-proofs-4.md" {
		t.Fatalf("notes = %s, %s, %s", one.Note, two.Note, three.Note)
	}
	if got := readTestFile(t, taken); got != "mine" {
		t.Fatalf("the user's note was overwritten: %q", got)
	}
}

func TestSecondBrainCapture_RefusesWhatItShouldNotWrite(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	before := snapshotTree(t, brain)
	secret := "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"
	for name, tc := range map[string]struct {
		text, id string
		want     error
	}{
		"empty":        {" ", "c-1", secondbrain.ErrCaptureInvalid},
		"too long":     {strings.Repeat("a", secondbrain.MaxCaptureRunes+1), "c-2", secondbrain.ErrCaptureInvalid},
		"a credential": {secret, "c-3", secondbrain.ErrCaptureSecretLike},
		"bad id":       {"fine", "has space", ErrValidation},
	} {
		if _, err := h.service.Capture(ctx, "local", tc.text, tc.id); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	if after := snapshotTree(t, brain); after != before {
		t.Fatalf("a refusal wrote.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSecondBrainCapture_NeverWritesToAFolderTheUserConnected(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	before := snapshotTree(t, h.music.Path)
	if _, err := h.service.Capture(ctx, "local", "Call Dana", "c-1"); !errors.Is(err, ErrSecondBrainNotOwned) {
		t.Fatalf("err = %v, want ErrSecondBrainNotOwned", err)
	}
	if after := snapshotTree(t, h.music.Path); after != before {
		t.Fatalf("a connected vault was written.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if mirrored, err := h.service.WriteDaily(ctx, "local", "2026-10-21", "# Journal"); mirrored || err != nil {
		t.Fatalf("WriteDaily on a connected vault = %v, %v; want nothing, no error", mirrored, err)
	}
	if after := snapshotTree(t, h.music.Path); after != before {
		t.Fatal("a connected vault was written by WriteDaily")
	}
}

func TestSecondBrainCapture_NothingConnectedIsRefused(t *testing.T) {
	h := newSecondBrainHarness(t)
	if _, err := h.service.Capture(context.Background(), "local", "Call Dana", "c-1"); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("err = %v, want ErrSecondBrainNotConnected", err)
	}
	if mirrored, err := h.service.WriteDaily(context.Background(), "local", "2026-10-21", "# Journal"); mirrored || err != nil {
		t.Fatalf("WriteDaily with nothing connected = %v, %v", mirrored, err)
	}
}

func TestSecondBrainWriteDaily_MirrorsLeavesAUserEditedNoteAloneAndReplacesAnIntactOne(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	daily := filepath.Join(brain, "Daily", "2026-10-21.md")

	mirrored, err := h.service.WriteDaily(ctx, "local", "2026-10-21", "# End of day — 2026-10-21\n\nShipped the mix.\n")
	if err != nil || !mirrored {
		t.Fatalf("first save = %v, %v", mirrored, err)
	}
	if got := readTestFile(t, daily); !strings.Contains(got, "ori: journal") || !strings.HasSuffix(got, "Shipped the mix.\n") {
		t.Fatalf("daily = %q", got)
	}
	if mirrored, err := h.service.WriteDaily(ctx, "local", "2026-10-21", "# End of day — 2026-10-21\n\nShipped the mix and the video.\n"); err != nil || !mirrored {
		t.Fatalf("re-save of an intact note = %v, %v", mirrored, err)
	}
	if got := readTestFile(t, daily); !strings.HasSuffix(got, "and the video.\n") {
		t.Fatalf("an intact note was not refreshed: %q", got)
	}

	edited := readTestFile(t, daily) + "\nMy own thought.\n"
	if err := os.WriteFile(daily, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if mirrored, err := h.service.WriteDaily(ctx, "local", "2026-10-21", "# newer"); mirrored || !errors.Is(err, secondbrain.ErrUserModified) {
		t.Fatalf("re-save over an edit = %v, %v; want ErrUserModified", mirrored, err)
	}
	if got := readTestFile(t, daily); got != edited {
		t.Fatalf("the user's edit was lost: %q", got)
	}

	// A day note the user wrote before Ori ever did is theirs too.
	own := filepath.Join(brain, "Daily", "2026-10-22.md")
	if err := os.WriteFile(own, []byte("# Mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.WriteDaily(ctx, "local", "2026-10-22", "# newer"); !errors.Is(err, secondbrain.ErrUserModified) {
		t.Fatalf("over the user's own note: %v", err)
	}
	for _, bad := range []string{"", "2026-10-2", "../2026-10-21", "2026-13-40", "2026-10-21.md"} {
		if _, err := h.service.WriteDaily(ctx, "local", bad, "# x"); !errors.Is(err, ErrValidation) {
			t.Errorf("date %q: err = %v, want ErrValidation", bad, err)
		}
	}
}

func TestSecondBrainStatus_ListsProjectsOnlyForAFolderOriSetUp(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	for _, folder := range []string{"Projects/Launch", "Projects/Album"} {
		if err := os.MkdirAll(filepath.Join(brain, folder), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil || len(status.Projects) != 2 || status.Projects[0].Name != "Album" || status.Projects[1].Rel != "Projects/Launch" ||
		status.Projects[1].Path != filepath.Join(brain, "Projects", "Launch") {
		t.Fatalf("projects = %+v, %v", status.Projects, err)
	}
	if got := h.service.Projects(ctx, "local"); len(got) != 2 {
		t.Fatalf("Projects() = %+v", got)
	}

	other := newSecondBrainHarness(t)
	other.connectVault(t)
	if err := os.MkdirAll(filepath.Join(other.music.Path, "Projects", "Mixes"), 0o750); err != nil {
		t.Fatal(err)
	}
	if status, _ := other.service.Status(ctx, "local"); len(status.Projects) != 0 {
		t.Fatalf("a connected vault listed projects: %+v", status.Projects)
	}
	if got := other.service.Projects(ctx, "local"); len(got) != 0 {
		t.Fatalf("Projects() on a connected vault = %+v", got)
	}
}

func TestSecondBrainStatus_CountsWhatOriWrote(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.service.SetSeeds(seedsFor(2, 1))
	if _, err := h.create(t, "My Brain"); err != nil {
		t.Fatal(err)
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil || status.WrittenByOri == nil || status.WrittenByOri.Count != 3 {
		t.Fatalf("status after setup = %+v, %v; want the 2 seeded Inbox notes and the seeded person", status.WrittenByOri, err)
	}

	if _, err := h.service.Capture(ctx, "local", "Call Dana", "c-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.WriteDaily(ctx, "local", "2026-10-21", "# Journal"); err != nil {
		t.Fatal(err)
	}
	status, err = h.service.Status(ctx, "local")
	if err != nil || status.WrittenByOri.Count != 5 || status.WrittenByOri.LastAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("status after a capture and a journal = %+v, %v", status.WrittenByOri, err)
	}

	// A connected vault reports no count at all.
	other := newSecondBrainHarness(t)
	other.connectVault(t)
	if status, _ := other.service.Status(ctx, "local"); status.WrittenByOri != nil {
		t.Fatalf("a connected vault reported %+v", status.WrittenByOri)
	}
}
