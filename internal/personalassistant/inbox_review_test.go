package personalassistant

import (
	"context"
	"errors"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"os"
	"path/filepath"
	"testing"
)

func TestInboxReview_SourceBoundAndReadOnly(t *testing.T) {
	ctx := context.Background()
	h, brain := createdHarness(t)
	s := h.service
	ledger := &secondbrain.ReviewLedger{DB: openTestDB(t).DB}
	s.deps.ReviewLedger = ledger
	view, err := s.ConnectionStatus(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	ref := view.BrowseRef
	const text = "Maya said disposal costs extra. Compare both roofing quotes before Friday."
	capture, err := s.CaptureForSource(ctx, "local", text, "new-capture", ref)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, brain)
	name := filepath.Base(capture.Note)
	preview, err := s.Preview(ctx, "local", "inbox", name, ref)
	if err != nil || len(preview.Suggestions) != 1 || preview.Suggestions[0].Title != "Compare both roofing quotes before Friday" || preview.Suggestions[0].State != "pending" {
		t.Fatalf("expected one pending comparison, err=%v", err)
	}
	if _, err = s.Preview(ctx, "local", "inbox", name, "other"); !errors.Is(err, ErrSecondBrainBrowseStale) {
		t.Fatalf("stale ref: %v", err)
	}
	if snapshotTree(t, brain) != before || len(h.tickets.made) != 0 {
		t.Fatal("review changed notes or created tickets")
	}
	if err = os.Rename(filepath.Join(brain, capture.Note), filepath.Join(brain, "Inbox", "moved.md")); err != nil {
		t.Fatal(err)
	}
	moved, err := s.Preview(ctx, "local", "inbox", "moved.md", ref)
	if err != nil || len(moved.Suggestions) != 0 {
		t.Fatalf("moved note acquired actions: %v", err)
	}
}
