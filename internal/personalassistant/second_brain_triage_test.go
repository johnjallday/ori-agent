package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// notesSnapshot records every entry's bytes, mode and modification time.
func notesSnapshot(t *testing.T, root string) string {
	t.Helper()
	var out strings.Builder
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&out, "%s|%s|%s\n", path, info.Mode(), info.ModTime().Format(time.RFC3339Nano))
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out.Write(data)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func triageItems(t *testing.T, h *secondBrainHarness) (string, []secondbrain.LookItem) {
	t.Helper()
	look, err := h.service.Look(context.Background(), "local")
	if err != nil || look.Result == nil {
		t.Fatalf("look = %+v, %v", look, err)
	}
	return look.LookID, look.Result.Items
}

func actedAs(t *testing.T, view SecondBrainLookView, id string) string {
	t.Helper()
	item, ok := view.Result.Item(id)
	if !ok {
		t.Fatalf("item %s is not on the look", id)
	}
	return item.ActedAs
}

func TestActOnItem_FollowUpDoneAndDismissLeaveTheNotesUntouched(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	lookID, items := triageItems(t, h)
	before := notesSnapshot(t, h.music.Path)

	view, err := h.service.ActOnItem(ctx, "local", lookID, items[0].ID, SecondBrainItemFollowUp, "t-1")
	if err != nil || actedAs(t, view, items[0].ID) != secondbrain.ActedFollowUp {
		t.Fatalf("follow up = %+v, %v", view, err)
	}
	made, err := h.followUps.FindBySource(ctx, "local", secondbrain.FollowUpSourceType, items[0].ID)
	if err != nil || made.Title != items[0].Text || !made.IsOpen() {
		t.Fatalf("the follow-up = %+v, %v", made, err)
	}

	// Done on the item that has a follow-up completes it.
	view, err = h.service.ActOnItem(ctx, "local", lookID, items[0].ID, SecondBrainItemDone, "t-2")
	if err != nil || actedAs(t, view, items[0].ID) != secondbrain.ActedDone {
		t.Fatalf("done = %+v, %v", view, err)
	}
	closed, err := h.followUps.FindBySource(ctx, "local", secondbrain.FollowUpSourceType, items[0].ID)
	if err != nil || closed.IsOpen() || !strings.Contains(closed.Detail, secondbrain.DoneInView) {
		t.Fatalf("the done item's follow-up = %+v, %v", closed, err)
	}

	// Done with no follow-up creates nothing.
	view, err = h.service.ActOnItem(ctx, "local", lookID, items[1].ID, SecondBrainItemDone, "t-3")
	if err != nil || actedAs(t, view, items[1].ID) != secondbrain.ActedDone {
		t.Fatalf("done without a follow-up = %+v, %v", view, err)
	}
	if _, err := h.followUps.FindBySource(ctx, "local", secondbrain.FollowUpSourceType, items[1].ID); !errors.Is(err, followup.ErrNotFound) {
		t.Fatalf("done made a follow-up: %v", err)
	}

	// Dismiss only hides.
	view, err = h.service.ActOnItem(ctx, "local", lookID, items[2].ID, SecondBrainItemDismiss, "t-4")
	if err != nil || actedAs(t, view, items[2].ID) != secondbrain.ActedDismissed {
		t.Fatalf("dismiss = %+v, %v", view, err)
	}
	if _, err := h.followUps.FindBySource(ctx, "local", secondbrain.FollowUpSourceType, items[2].ID); !errors.Is(err, followup.ErrNotFound) {
		t.Fatalf("dismiss made a follow-up: %v", err)
	}

	if after := notesSnapshot(t, h.music.Path); after != before {
		t.Fatal("triage changed the notes folder")
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil || status.LastLook.Handled != 3 || !status.LastLook.Acted {
		t.Fatalf("summary = %+v, %v", status.LastLook, err)
	}
}

func TestActOnItem_IsIdempotentByRequestIDAndRefusesAReusedOne(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	lookID, items := triageItems(t, h)

	if _, err := h.service.ActOnItem(ctx, "local", lookID, items[0].ID, SecondBrainItemDismiss, "t-1"); err != nil {
		t.Fatal(err)
	}
	// A later choice, then a replay of the first request: the replay changes nothing.
	if _, err := h.service.ActOnItem(ctx, "local", lookID, items[0].ID, SecondBrainItemDone, "t-2"); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.ActOnItem(ctx, "local", lookID, items[0].ID, SecondBrainItemDismiss, "t-1")
	if err != nil || actedAs(t, view, items[0].ID) != secondbrain.ActedDone {
		t.Fatalf("replay = %+v, %v", view, err)
	}
	if _, err := h.service.ActOnItem(ctx, "local", lookID, items[1].ID, SecondBrainItemDismiss, "t-1"); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("the same id on another item: %v", err)
	}
	if _, err := h.service.ActOnItem(ctx, "local", lookID, items[0].ID, SecondBrainItemFollowUp, "t-1"); !errors.Is(err, secondbrain.ErrRequestReused) {
		t.Fatalf("the same id for another action: %v", err)
	}
}

func TestActOnItem_Refusals(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	if _, err := h.service.ActOnItem(ctx, "local", "look-1", strings.Repeat("a", 64), SecondBrainItemDone, "t-0"); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("nothing connected: %v", err)
	}
	h.connectVault(t)
	lookID, items := triageItems(t, h)
	id := items[0].ID
	for name, tc := range map[string]struct {
		look, item, action, request string
		want                        error
	}{
		"unknown action": {lookID, id, "archive", "t-1", ErrValidation},
		"bad request id": {lookID, id, SecondBrainItemDone, "", ErrValidation},
		"bad item id":    {lookID, "abc", SecondBrainItemDone, "t-2", ErrValidation},
		"another look":   {"look-9", id, SecondBrainItemDone, "t-3", ErrSecondBrainLookNotFound},
		"unknown item":   {lookID, strings.Repeat("a", 64), SecondBrainItemDone, "t-4", ErrSecondBrainItemNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.service.ActOnItem(ctx, "local", tc.look, tc.item, tc.action, tc.request); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestActOnItem_ADoneItemStaysHiddenAndADismissedOneReturnsOnTheNextLook(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	lookID, items := triageItems(t, h)
	var suggested []secondbrain.LookItem
	for _, item := range items {
		if item.Suggested {
			suggested = append(suggested, item)
		}
	}
	done, dismissed := suggested[0], suggested[1]
	if _, err := h.service.ActOnItem(ctx, "local", lookID, done.ID, SecondBrainItemDone, "t-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ActOnItem(ctx, "local", lookID, dismissed.ID, SecondBrainItemDismiss, "t-2"); err != nil {
		t.Fatal(err)
	}
	*h.clock = h.clock.Add(SecondBrainLookAgainAfter + time.Minute)
	if _, err := h.service.LookAgain(ctx, "local", "again-1"); err != nil {
		t.Fatal(err)
	}
	h.service.Wait()
	look, err := h.service.Look(ctx, "local")
	if err != nil || look.LookID == lookID {
		t.Fatalf("look = %+v, %v", look, err)
	}
	if got, _ := look.Result.Item(done.ID); got == nil || got.ActedAs != secondbrain.ActedDone || got.Suggested {
		t.Fatalf("the done item = %+v", got)
	}
	if got, _ := look.Result.Item(dismissed.ID); got == nil || got.ActedAs != "" || !got.Suggested {
		t.Fatalf("the dismissed item = %+v", got)
	}
}

func TestAcceptFollowUps_TheSuggestedSetSkipsWhatIsHandledAndFilesWhatItAdds(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	h.connectVault(t)
	lookID, items := triageItems(t, h)
	var suggested []secondbrain.LookItem
	for _, item := range items {
		if item.Suggested {
			suggested = append(suggested, item)
		}
	}
	if _, err := h.service.ActOnItem(ctx, "local", lookID, suggested[0].ID, SecondBrainItemDismiss, "t-1"); err != nil {
		t.Fatal(err)
	}
	accept, err := h.service.AcceptFollowUps(ctx, "local", lookID, SecondBrainAcceptInput{Suggested: true, RequestID: "bulk-1"})
	if err != nil || accept.Created != len(suggested)-1 || accept.Skipped != 1 {
		t.Fatalf("accept = %+v, %v", accept, err)
	}
	if _, err := h.followUps.FindBySource(ctx, "local", secondbrain.FollowUpSourceType, suggested[0].ID); !errors.Is(err, followup.ErrNotFound) {
		t.Fatalf("the dismissed item got a follow-up: %v", err)
	}
	look, err := h.service.Look(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if got := actedAs(t, look, suggested[1].ID); got != secondbrain.ActedFollowUp {
		t.Fatalf("an added item = %q", got)
	}
	again, err := h.service.AcceptFollowUps(ctx, "local", lookID, SecondBrainAcceptInput{Suggested: true, RequestID: "bulk-2"})
	if err != nil || again.Created != 0 || again.Skipped != len(suggested) {
		t.Fatalf("second accept = %+v, %v", again, err)
	}
}

func TestSecondBrainInbox_IsOnlyForAFolderOriSetUpAndACaptureDropsTheCache(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	if _, err := h.service.Inbox(ctx, "local"); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("nothing connected: %v", err)
	}
	h.connectVault(t)
	if _, err := h.service.Inbox(ctx, "local"); !errors.Is(err, ErrSecondBrainNoInbox) {
		t.Fatalf("a connected vault: %v", err)
	}
	status, err := h.service.Status(ctx, "local")
	if err != nil || status.InboxCount != nil || status.TodayNote != nil {
		t.Fatalf("a connected vault's status carried Inbox fields: %+v, %v", status, err)
	}
}

func TestSecondBrainInbox_ListsCachesAndTodayNoteFollowsTheJournal(t *testing.T) {
	ctx := context.Background()
	h := newSecondBrainHarness(t)
	created, err := h.create(t, "My Brain")
	if err != nil {
		t.Fatal(err)
	}
	source := created.Source.Path
	inbox, err := h.service.Inbox(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	start := inbox.Total
	if _, err := h.service.Capture(ctx, "local", "Call Dana about the mix", "note-1"); err != nil {
		t.Fatal(err)
	}
	inbox, err = h.service.Inbox(ctx, "local")
	if err != nil || inbox.Total != start+1 || inbox.Notes[0].Kind != secondbrain.NoteKindCapture || !inbox.Notes[0].OwnedByOri ||
		inbox.Notes[0].Title != "Call Dana about the mix" || !strings.HasPrefix(inbox.Notes[0].Link, "obsidian://open?path=") {
		t.Fatalf("after a capture = %+v, %v", inbox, err)
	}

	// A note dropped in by hand waits for the 30-second cache.
	if err := os.WriteFile(filepath.Join(source, secondbrain.FolderInbox, "by-hand.md"), []byte("# By hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cached, _ := h.service.Inbox(ctx, "local"); cached.Total != start+1 {
		t.Fatalf("a listing inside the cache window = %d", cached.Total)
	}
	*h.clock = h.clock.Add(secondBrainInboxCacheFor)
	if fresh, _ := h.service.Inbox(ctx, "local"); fresh.Total != start+2 {
		t.Fatalf("a listing past the cache window = %d", fresh.Total)
	}

	status, err := h.service.Status(ctx, "local")
	if err != nil || status.InboxCount == nil || *status.InboxCount != start+2 {
		t.Fatalf("inbox count = %v, %v", status.InboxCount, err)
	}
	date := h.clock.Format("2006-01-02")
	if status.TodayNote == nil || status.TodayNote.Date != date || status.TodayNote.State != secondbrain.TodayNoteNone || status.TodayNote.Link != "" {
		t.Fatalf("today before the Journal = %+v", status.TodayNote)
	}
	if mirrored, err := h.service.WriteDaily(ctx, "local", date, "Shipped the mix."); err != nil || !mirrored {
		t.Fatalf("mirror = %v, %v", mirrored, err)
	}
	status, err = h.service.Status(ctx, "local")
	if err != nil || status.TodayNote == nil || status.TodayNote.State != secondbrain.TodayNoteOri || !strings.HasPrefix(status.TodayNote.Link, "obsidian://open?path=") {
		t.Fatalf("today after the Journal = %+v, %v", status.TodayNote, err)
	}
	if err := os.WriteFile(filepath.Join(source, secondbrain.FolderDaily, date+".md"), []byte("Mine now\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.service.Status(ctx, "local"); status.TodayNote == nil || status.TodayNote.State != secondbrain.TodayNoteUser {
		t.Fatalf("today after an edit = %+v", status.TodayNote)
	}
}
