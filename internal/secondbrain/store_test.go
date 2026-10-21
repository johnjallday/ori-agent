package secondbrain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var storeNow = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func testSource(path string) Source {
	return Source{ID: SourceID(path), Kind: KindObsidian, Name: filepath.Base(path), Path: path, ConnectedAt: storeNow, RequestID: "connect-1"}
}

func testItem(source Source, note, text string, line int) LookItem {
	return LookItem{ID: ItemID(source.ID, note, text), Text: text, Note: note, Line: line}
}

func TestRecord_ConnectReplacesOnlyAfterAConfirm(t *testing.T) {
	var record Record
	first := testSource("/Users/sam/Second Brain")
	if _, err := record.Connect(first, false); err != nil {
		t.Fatal(err)
	}
	record.Sources[0].LastLook = &Look{ID: "look-1", State: LookFinished, StartedAt: storeNow, FinishedAt: &storeNow}

	again := testSource("/Users/sam/Second Brain")
	again.RequestID = "connect-2"
	got, err := record.Connect(again, false)
	if err != nil || got.RequestID != "connect-1" || got.LastLook == nil {
		t.Fatalf("connecting the same folder again = %+v, %v; want the existing source kept", got, err)
	}

	second := testSource("/Users/sam/Work Notes")
	if _, err := record.Connect(second, false); !errors.Is(err, ErrReplaceNeedsConfirm) {
		t.Fatalf("a second folder without confirm: err = %v", err)
	}
	if record.Connected().ID != first.ID {
		t.Fatal("the refused replace changed the connection")
	}
	if _, err := record.Connect(second, true); err != nil {
		t.Fatal(err)
	}
	if len(record.Sources) != 1 || record.Connected().ID != second.ID || record.Connected().LastLook != nil {
		t.Fatalf("after a confirmed replace = %+v", record.Sources)
	}
}

func TestRecord_ConnectRefusesAMalformedSource(t *testing.T) {
	good := testSource("/Users/sam/Brain")
	for name, change := range map[string]func(*Source){
		"relative path":   func(s *Source) { s.Path = "Brain"; s.ID = SourceID(s.Path) },
		"unclean path":    func(s *Source) { s.Path = "/Users/sam/../sam/Brain"; s.ID = SourceID(s.Path) },
		"id not the path": func(s *Source) { s.ID = SourceID("/elsewhere") },
		"kind":            func(s *Source) { s.Kind = "notion" },
		"no name":         func(s *Source) { s.Name = "" },
		"request id":      func(s *Source) { s.RequestID = "has space" },
		"no time":         func(s *Source) { s.ConnectedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			source := good
			change(&source)
			var record Record
			if _, err := record.Connect(source, false); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("err = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func TestRecord_AskStates(t *testing.T) {
	var record Record
	if got := record.Ask.State(); got != AskPending {
		t.Fatalf("new = %s", got)
	}
	if !record.MarkPrompted("prompt-1", storeNow) {
		t.Fatal("the first prompt was not recorded")
	}
	if record.MarkPrompted("prompt-2", storeNow.Add(time.Hour)) || record.Ask.PromptRequestID != "prompt-1" {
		t.Fatal("a second prompt replaced the receipt")
	}
	if got := record.Ask.State(); got != AskShown {
		t.Fatalf("after the prompt = %s", got)
	}

	if err := record.Decide(DecisionLater, "ask-1", storeNow); err != nil {
		t.Fatal(err)
	}
	if got := record.Ask.State(); got != AskDeferred {
		t.Fatalf("after Not now = %s", got)
	}
	if record.Ask.OfferAgain(storeNow.Add(AskAgainAfter - time.Minute)) {
		t.Fatal("Not now offered again inside the week")
	}
	if !record.Ask.OfferAgain(storeNow.Add(AskAgainAfter)) {
		t.Fatal("Not now not offered again after the week")
	}

	if err := record.Decide(DecisionNone, "ask-2", storeNow); err != nil {
		t.Fatal(err)
	}
	if got := record.Ask.State(); got != AskDeclined || record.Ask.OfferAgain(storeNow.Add(30*24*time.Hour)) {
		t.Fatalf("after I don't keep notes = %s", got)
	}
	if err := record.Decide("maybe", "ask-3", storeNow); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unknown decision err = %v", err)
	}

	record.AskAgain()
	if got := record.Ask.State(); got != AskShown {
		t.Fatalf("after Ask again = %s, want shown (the prompt receipt stays)", got)
	}
	record.ResetAsk()
	if got := record.Ask.State(); got != AskPending {
		t.Fatalf("after Reset Getting Started = %s", got)
	}
}

func TestRecord_DecidingWithoutAPromptStillCountsAsShown(t *testing.T) {
	var record Record
	if err := record.Decide(DecisionLater, "ask-1", storeNow); err != nil {
		t.Fatal(err)
	}
	if record.Ask.PromptedAt == nil {
		t.Fatal("an answered ask has no prompt receipt, so it could be shown again")
	}
}

func TestRecord_DisconnectTombstonesTheAskAndKeepsNothingOfTheFolder(t *testing.T) {
	var record Record
	if err := record.Disconnect(storeNow); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("disconnect with nothing connected: err = %v", err)
	}
	if _, err := record.Connect(testSource("/Users/sam/Brain"), false); err != nil {
		t.Fatal(err)
	}
	record.MarkPrompted("prompt-1", storeNow)
	if err := record.Disconnect(storeNow); err != nil {
		t.Fatal(err)
	}
	if record.Connected() != nil || record.Ask.State() != AskDeclined || record.Ask.TombstonedAt == nil {
		t.Fatalf("after disconnect = %+v", record)
	}
	record.AskAgain()
	if record.Ask.TombstonedAt != nil || record.Ask.State() != AskShown {
		t.Fatalf("Ask again left the tombstone: %+v", record.Ask)
	}
}

func TestRecord_ReceiptsReplayTheSameActionAndRefuseAnother(t *testing.T) {
	var record Record
	record.Remember(Receipt{RequestID: "accept-1", Action: "follow_ups", At: storeNow, LookID: "look-1", Created: 4, Skipped: 1})
	got, ok, err := record.Replay("accept-1", "follow_ups")
	if err != nil || !ok || got.Created != 4 || got.Skipped != 1 {
		t.Fatalf("replay = %+v, %v, %v", got, ok, err)
	}
	if _, _, err := record.Replay("accept-1", "disconnect"); !errors.Is(err, ErrRequestReused) {
		t.Fatalf("another action under the same id: err = %v", err)
	}
	if _, ok, err := record.Replay("unknown", "follow_ups"); ok || err != nil {
		t.Fatalf("unknown id = %v, %v", ok, err)
	}
	for i := range MaxReceipts + 3 {
		record.Remember(Receipt{RequestID: fmt.Sprintf("r-%d", i), Action: "look", At: storeNow})
	}
	if len(record.Receipts) != MaxReceipts || record.Receipts[0].RequestID != "r-3" {
		t.Fatalf("receipts = %d starting %s, want the newest %d", len(record.Receipts), record.Receipts[0].RequestID, MaxReceipts)
	}
}

func TestItemID_IsAStableContentHash(t *testing.T) {
	id := ItemID("v_1", "Projects/Album.md", "Master track 7")
	if len(id) != 64 {
		t.Fatalf("id %q is not a sha256 hex digest", id)
	}
	if ItemID("v_1", "Projects/Album.md", "  master   TRACK 7 ") != id {
		t.Fatal("case and spacing changed the id")
	}
	for _, other := range []string{
		ItemID("v_2", "Projects/Album.md", "Master track 7"),
		ItemID("v_1", "Projects/EP.md", "Master track 7"),
		ItemID("v_1", "Projects/Album.md", "Master track 8"),
	} {
		if other == id {
			t.Fatal("a different source, note or text kept the id")
		}
	}
}

func TestRecord_ValidateBoundsWhatALookKeeps(t *testing.T) {
	source := testSource("/Users/sam/Brain")
	finished := storeNow.Add(time.Minute)
	valid := func() Record {
		look := &Look{
			ID: "look-1", State: LookFinished, RequestID: "connect-1", StartedAt: storeNow, FinishedAt: &finished,
			Headline: "3 open items across 2 notes, 1 with dates.", HeadlineBy: HeadlineByRules,
			Counts: LookCounts{Notes: 2, OpenItems: 3, NotesWithOpen: 2, Dated: 1},
			Items: []LookItem{
				testItem(source, "Inbox.md", "call the distributor", 2),
				testItem(source, "Projects/Album.md", "master track 7", 4),
			},
		}
		look.Items[0].Due = "2026-10-25"
		withLook := source
		withLook.LastLook = look
		return Record{Sources: []Source{withLook}}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid record: %v", err)
	}
	for name, change := range map[string]func(*Record){
		"two sources":       func(r *Record) { r.Sources = append(r.Sources, testSource("/Users/sam/Other")) },
		"long headline":     func(r *Record) { r.Sources[0].LastLook.Headline = strings.Repeat("x", MaxHeadlineRunes+1) },
		"running last look": func(r *Record) { r.Sources[0].LastLook.State = LookRunning; r.Sources[0].LastLook.FinishedAt = nil },
		"finished current":  func(r *Record) { r.Sources[0].Current = r.Sources[0].LastLook },
		"unknown state":     func(r *Record) { r.Sources[0].LastLook.State = "waiting" },
		"headline author":   func(r *Record) { r.Sources[0].LastLook.HeadlineBy = "user" },
		"item id":           func(r *Record) { r.Sources[0].LastLook.Items[0].ID = "abc" },
		"item text":         func(r *Record) { r.Sources[0].LastLook.Items[0].Text = strings.Repeat("y", MaxTaskTextRunes+1) },
		"item newline":      func(r *Record) { r.Sources[0].LastLook.Items[0].Text = "two\nlines" },
		"absolute note":     func(r *Record) { r.Sources[0].LastLook.Items[0].Note = "/etc/passwd" },
		"escaping note":     func(r *Record) { r.Sources[0].LastLook.Items[0].Note = "../outside.md" },
		"unclean note":      func(r *Record) { r.Sources[0].LastLook.Items[0].Note = "a/./b.md" },
		"line zero":         func(r *Record) { r.Sources[0].LastLook.Items[0].Line = 0 },
		"bad due":           func(r *Record) { r.Sources[0].LastLook.Items[0].Due = "2026-02-30" },
		"too many items": func(r *Record) {
			look := r.Sources[0].LastLook
			for i := range MaxLookItems {
				look.Items = append(look.Items, testItem(source, "Inbox.md", fmt.Sprintf("item %d", i), i+1))
			}
		},
		"decision without time": func(r *Record) { r.Ask.Decision = DecisionLater },
		"bad receipt":           func(r *Record) { r.Receipts = []Receipt{{RequestID: "", Action: "look"}} },
	} {
		t.Run(name, func(t *testing.T) {
			record := valid()
			change(&record)
			if err := record.Validate(); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("err = %v, want ErrInvalidRecord", err)
			}
		})
	}
}
