package samplelibrary

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedTagReaderIsConsentBoundAllowlistedAndSanitized(t *testing.T) {
	payload := append([]byte{3}, []byte("Kick\u202e\x00Title")...)
	frame := make([]byte, 10+len(payload))
	copy(frame[:4], "TIT2")
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[10:], payload)
	header := []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, byte(len(frame))}
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "tagged.mp3"), append(header, frame...), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service, _, _, selections, homeID := newTestService(t)
	token, _ := selections.Issue(rootPath)
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect")
	if err != nil {
		t.Fatal(err)
	}
	consent, err := service.ReviewAnalysis(ctx, homeID, root.ID, false, true)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err = service.CommitAnalysis(ctx, homeID, root.ID, consent.Token, "tags", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Index(ctx, homeID, root.ID, "index", state.CatalogRevision, root.Revision); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%#v %v", entries, err)
	}
	if entries[0].Content.Title != "KickTitle" || entries[0].SHA256 != "" {
		t.Fatalf("bounded facts=%+v hash=%q", entries[0].Content, entries[0].SHA256)
	}
}
