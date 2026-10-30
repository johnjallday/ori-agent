package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func storedReviewPath(t *testing.T) (*folderDigestFixture, FolderOffer, folderPathEntry) {
	t.Helper()
	f, _, _, id, _, _ := reviewedObservation(t, "documents", "Scans")
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	offer := *doc.Offer(id)
	entry := folderPathEntry{
		RootPath: filepath.Join(f.home, "Documents"), RootIdentity: offer.RootIdentity,
		CandidatePath: filepath.Join(f.home, "Documents", "Scans"), CandidateIdentity: offer.ConversationReview.CandidateIdentity,
		SavedAt: f.now,
	}
	if err := f.store.saveFolderPath(context.Background(), "local", id, entry); err != nil {
		t.Fatal(err)
	}
	return f, offer, entry
}

func TestFolderPaths_ReviewSavesAndKeepChattingDeletes(t *testing.T) {
	f, service, target, id, _, _ := reviewedObservation(t, "documents", "Scans")
	ctx := context.Background()
	doc, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	offer := *doc.Offer(id)
	if _, found, err := f.store.readFolderPath(ctx, "local", offer); err != nil || !found {
		t.Fatalf("review did not save: %v", err)
	}
	if err := service.CloseReview(ctx, target, id); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.readFolderPath(ctx, "local", offer); err != nil || found {
		t.Fatalf("closed review retained: %v", err)
	}
}

func TestFolderPaths_PrivateRoundTripAndMode(t *testing.T) {
	f, offer, entry := storedReviewPath(t)
	ctx := context.Background()
	// A newly constructed store reads the same private sidecar after restart.
	restarted := NewFolderDigestStore(NewKnowledgeStore(f.resolver(), f.folder))
	got, found, err := restarted.readFolderPath(ctx, "local", offer)
	if err != nil || !found || got != entry {
		t.Fatalf("round trip: found=%v err=%v", found, err)
	}
	info, err := os.Stat(filepath.Join(f.folder.path, ".ori", folderPathsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v", info.Mode())
	}
	digest, err := os.ReadFile(filepath.Join(f.folder.path, ".ori", folderDigestFileName))
	if err != nil {
		t.Fatal(err)
	}
	view, err := json.Marshal(f.service.viewFor(ctx, "local", offer, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{digest, view} {
		if strings.Contains(string(data), f.home) || strings.Contains(string(data), "root_path") || strings.Contains(string(data), "candidate_path") {
			t.Fatal("private authority leaked into public document")
		}
	}
}

func TestFolderPaths_FinalStatesAndRemovalDeleteAuthority(t *testing.T) {
	for _, state := range []FolderOfferStatus{FolderOfferResolved, FolderOfferDeclined, FolderOfferLater, FolderOfferClosed, FolderOfferAbandoned, "removed"} {
		t.Run(string(state), func(t *testing.T) {
			f, offer, _ := storedReviewPath(t)
			ctx := context.Background()
			_, err := f.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error {
				if state == "removed" {
					doc.Offers = nil
				} else {
					doc.Offer(offer.ID).Status = state
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(f.folder.path, ".ori", folderPathsFileName))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), f.home) {
				t.Fatal("final path retained on disk")
			}
			if _, found, err := f.store.readFolderPath(ctx, "local", offer); err != nil || found {
				t.Fatalf("stale reader revived final review: %v", err)
			}
		})
	}
}

func TestFolderPaths_PruneWithOffer(t *testing.T) {
	f, offer, _ := storedReviewPath(t)
	_, err := f.store.Mutate(context.Background(), "local", func(doc *FolderDigestDocument) error {
		// Make the saved offer oldest, then overflow with live offers so the
		// normal pruning rule must drop it even though it has private authority.
		doc.Offers[0].Status = FolderOfferAwaitingOutcome
		for i := 0; i < folderDigestMaxOffers; i++ {
			next := offer
			next.ID, next.Status = fmt.Sprintf("other-%d", i), FolderOfferAwaitingOutcome
			next.CreatedAt = f.now.AddDate(0, 0, 1)
			doc.Offers = append(doc.Offers, next)
		}
		pruneFolderDigest(doc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.readFolderPath(context.Background(), "local", offer); err != nil || found {
		t.Fatalf("pruned path revived: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.folder.path, ".ori", folderPathsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), f.home) {
		t.Fatal("pruned path retained")
	}
}

func TestFolderPaths_BoundNeverEvictsAndCanRefresh(t *testing.T) {
	_, _, entry := storedReviewPath(t)
	doc := folderPathDocument{Version: folderPathsVersion, Entries: make(map[string]folderPathEntry)}
	for i := 0; i < folderPathsMaxEntries; i++ {
		if !doc.put(fmt.Sprint(i), entry) {
			t.Fatal("full early")
		}
	}
	if doc.put("overflow", entry) || len(doc.Entries) != folderPathsMaxEntries {
		t.Fatal("bound exceeded")
	}
	for i := 0; i < folderPathsMaxEntries; i++ {
		if doc.Entries[fmt.Sprint(i)] != entry {
			t.Fatal("entry evicted")
		}
	}
	entry.SavedAt = entry.SavedAt.AddDate(0, 0, 1)
	if !doc.put("0", entry) || doc.Entries["0"] != entry {
		t.Fatal("refresh refused at capacity")
	}
}

func TestFolderPaths_ConcurrentSaveCannotResurrectClosedReview(t *testing.T) {
	f, offer, entry := storedReviewPath(t)
	other := NewFolderDigestStore(NewKnowledgeStore(f.resolver(), f.folder))
	var writers sync.WaitGroup
	errorsSeen := make(chan error, 2)
	writers.Add(2)
	go func() {
		defer writers.Done()
		err := other.saveFolderPath(t.Context(), "local", offer.ID, entry)
		if err != nil && !errors.Is(err, ErrConflict) {
			errorsSeen <- err
		}
	}()
	go func() {
		defer writers.Done()
		_, err := f.store.Mutate(t.Context(), "local", func(doc *FolderDigestDocument) error {
			doc.Offer(offer.ID).Status = FolderOfferClosed
			return nil
		})
		if err != nil {
			errorsSeen <- err
		}
	}()
	writers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	if _, found, err := other.readFolderPath(t.Context(), "local", offer); err != nil || found {
		t.Fatalf("closed authority resurrected: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.folder.path, ".ori", folderPathsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), f.home) {
		t.Fatal("concurrent writer retained revoked path")
	}
}

func TestFolderPaths_RejectForeignOwnerCorruptionAndChangedWitness(t *testing.T) {
	f, offer, entry := storedReviewPath(t)
	ctx := context.Background()
	entry.RootIdentity = "different"
	if err := f.store.saveFolderPath(ctx, "local", offer.ID, entry); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign witness: %v", err)
	}
	if _, found, err := f.store.readFolderPath(ctx, "other-owner", offer); err == nil || found {
		t.Fatal("foreign owner read")
	}
	path := filepath.Join(f.folder.path, ".ori", folderPathsFileName)
	for _, data := range []string{"", `{"version":99}`, `{}`, `{"version":1,"entries":{},"unknown":true}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.store.readFolderPath(ctx, "local", offer); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatalf("corrupt store: %v", err)
		}
	}
}
