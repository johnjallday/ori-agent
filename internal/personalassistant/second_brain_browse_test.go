package personalassistant

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

func TestSecondBrainBrowseBindsToCurrentConnectionEvenAfterSameFolderReconnect(t *testing.T) {
	ctx := t.Context()
	h := newSecondBrainHarness(t)
	s := h.service
	h.connectVault(t)
	first, err := s.ConnectionStatus(ctx, "local")
	if err != nil || first.BrowseRef == "" || len(first.Detected) != 0 || first.InboxCount != nil {
		t.Fatalf("lightweight status = %+v, %v", first, err)
	}
	got, err := s.Browse(ctx, "local", first.BrowseRef, nil)
	if err != nil || len(got.Entries) == 0 {
		t.Fatalf("vault root = %+v, %v", got, err)
	}
	if _, err := s.Preview(ctx, "local", secondbrain.PreviewInbox, "private.md", first.BrowseRef); !errors.Is(err, ErrSecondBrainNoteIsYours) {
		t.Fatalf("private vault preview = %v", err)
	}
	if _, err := s.Browse(ctx, "local", first.BrowseRef, []string{".."}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unexpected invalid part result: %v", err)
	}
	if _, err := s.Disconnect(ctx, "local", "browse-disconnect", first.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Browse(ctx, "local", first.BrowseRef, nil); !errors.Is(err, ErrSecondBrainNotConnected) {
		t.Fatalf("disconnected browse = %v", err)
	}
	if _, err := s.Connect(ctx, "local", SecondBrainConnectInput{DetectedID: h.vaultID, RequestID: "browse-reconnect"}); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	if _, err := s.Browse(ctx, "local", first.BrowseRef, nil); !errors.Is(err, ErrSecondBrainBrowseStale) {
		t.Fatalf("stale browse = %v", err)
	}
	if _, err := s.Preview(ctx, "local", secondbrain.PreviewInbox, "private.md", first.BrowseRef); !errors.Is(err, ErrSecondBrainBrowseStale) {
		t.Fatalf("stale preview = %v", err)
	}
	current, err := s.ConnectionStatus(ctx, "local")
	if err != nil || current.BrowseRef == first.BrowseRef || current.Source.ID != first.Source.ID {
		t.Fatalf("reconnected status = %+v, %v", current, err)
	}
}
