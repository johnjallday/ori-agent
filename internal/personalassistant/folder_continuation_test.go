package personalassistant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// seedPickedAlbums makes a five-album collection the native chooser can return
// and drives it through a confirmed portfolio offer to a verified Home, exactly
// as the browser does before it lands on the Home.
func seedPickedAlbums(t *testing.T, f *folderDigestFixture) (offerID, root string) {
	t.Helper()
	return seedPickedCollection(t, f, "Albums")
}

// seedPickedCollection is seedPickedAlbums for a collection of any name, so one
// owner can hold several confirmed collections.
func seedPickedCollection(t *testing.T, f *folderDigestFixture, name string) (offerID, root string) {
	t.Helper()
	ctx := context.Background()
	root = filepath.Join(f.home, name)
	for i := range 5 {
		sub := filepath.Join(root, fmt.Sprintf("Album-%d", i+1))
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "Song.als"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.service.deps.HomeExists = func(context.Context, string, string) (bool, error) { return false, nil }
	f.service.deps.HomeJourney = &fakeHomeVerifier{}
	f.service.deps.Picker = fakeFolderPicker{path: root, chosen: true}
	offer, err := f.service.ScanPicked(ctx, "local")
	if err != nil || offer == nil || offer.Portfolio == nil {
		t.Fatalf("picked collection offer = %+v, %v", offer, err)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "confirmed-" + name}); err != nil {
		t.Fatal(err)
	}
	return offer.ID, root
}

func resolveAlbumsHome(t *testing.T, f *folderDigestFixture, offerID string) {
	t.Helper()
	resolveCollectionHome(t, f, offerID, "resolved")
}

func resolveCollectionHome(t *testing.T, f *folderDigestFixture, offerID, requestID string) {
	t.Helper()
	if _, err := f.service.ResolvePortfolio(context.Background(), "local", offerID, FolderResolveInput{HomeID: "new-home", RequestID: requestID}); err != nil {
		t.Fatal(err)
	}
}

func TestPortfolioContinuations_ReadyWhileTheOriginalSelectionIsHeld(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offerID, _ := seedPickedAlbums(t, f)

	// A confirmed-but-unresolved offer is not a continuation: no Home exists yet.
	if got, err := f.service.PortfolioContinuations(ctx, "local", "new-home"); err != nil || len(got) != 0 {
		t.Fatalf("before the Home exists: %+v, %v", got, err)
	}

	resolveAlbumsHome(t, f, offerID)
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.service.PortfolioContinuations(ctx, "local", "new-home")
	if err != nil || len(got) != 1 {
		t.Fatalf("continuations = %+v, %v", got, err)
	}
	if got[0].OfferID != offerID || got[0].Folder != "Albums" || got[0].State != FolderContinuationReady || got[0].Reason != "" {
		t.Fatalf("continuation = %+v", got[0])
	}
	if again, err := f.service.PortfolioContinuations(ctx, "local", "new-home"); err != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("a repeat read changed the answer: %+v, %v", again, err)
	}
	after, err := f.store.Read(ctx, "local")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("reading a continuation wrote to the offer store (err=%v)", err)
	}
}

func TestPortfolioContinuations_NeverCrossHomesOrOwners(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offerID, _ := seedPickedAlbums(t, f)
	resolveAlbumsHome(t, f, offerID)

	for _, tc := range []struct{ user, home string }{
		{"local", "another-home"},
		{"local", "  "},
		{"other", "new-home"},
	} {
		got, err := f.service.PortfolioContinuations(ctx, tc.user, tc.home)
		if tc.home == "  " {
			if err == nil {
				t.Fatalf("blank Home ID accepted: %+v", got)
			}
			continue
		}
		if err != nil || len(got) != 0 {
			t.Fatalf("%+v saw %+v, %v", tc, got, err)
		}
	}
}

func TestPortfolioContinuations_ExplainsWhyAPickIsNeeded(t *testing.T) {
	ctx := context.Background()

	t.Run("restart loses a dialog-picked folder", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		offerID, _ := seedPickedAlbums(t, f)
		resolveAlbumsHome(t, f, offerID)
		restarted := f.newService()
		got, err := restarted.PortfolioContinuations(ctx, "local", "new-home")
		if err != nil || len(got) != 1 || got[0].State != FolderContinuationNeedsPick || got[0].Reason != FolderContinuationLost {
			t.Fatalf("after restart: %+v, %v", got, err)
		}
	})

	t.Run("hand-off window elapsed", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		offerID, _ := seedPickedAlbums(t, f)
		resolveAlbumsHome(t, f, offerID)
		f.now = f.now.Add(portfolioRootHandoffTTL + time.Second)
		got, err := f.service.PortfolioContinuations(ctx, "local", "new-home")
		if err != nil || len(got) != 1 || got[0].State != FolderContinuationNeedsPick || got[0].Reason != FolderContinuationExpired {
			t.Fatalf("after expiry: %+v, %v", got, err)
		}
	})

	t.Run("expiry wins over restart", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		offerID, _ := seedPickedAlbums(t, f)
		resolveAlbumsHome(t, f, offerID)
		f.now = f.now.Add(portfolioRootHandoffTTL + time.Second)
		got, err := f.newService().PortfolioContinuations(ctx, "local", "new-home")
		if err != nil || len(got) != 1 || got[0].Reason != FolderContinuationExpired {
			t.Fatalf("expired and restarted: %+v, %v", got, err)
		}
	})

	t.Run("replaced directory", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		offerID, root := seedPickedAlbums(t, f)
		resolveAlbumsHome(t, f, offerID)
		if err := os.Rename(root, root+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o750); err != nil {
			t.Fatal(err)
		}
		got, err := f.service.PortfolioContinuations(ctx, "local", "new-home")
		if err != nil || len(got) != 1 || got[0].State != FolderContinuationNeedsPick || got[0].Reason != FolderContinuationChanged {
			t.Fatalf("replacement directory: %+v, %v", got, err)
		}
	})

	t.Run("agrees with PortfolioRoot", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		offerID, _ := seedPickedAlbums(t, f)
		resolveAlbumsHome(t, f, offerID)
		if _, _, err := f.service.PortfolioRoot(ctx, "local", offerID, "new-home"); err != nil {
			t.Fatalf("ready continuation but PortfolioRoot refused: %v", err)
		}
		f.now = f.now.Add(portfolioRootHandoffTTL + time.Second)
		if _, _, err := f.service.PortfolioRoot(ctx, "local", offerID, "new-home"); err == nil {
			t.Fatal("PortfolioRoot accepted a selection the continuation calls expired")
		}
	})
}

func TestPortfolioContinuations_SeveralCollectionsAreListedNewestFirstAndBounded(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	var ids []string
	names := []string{"Albums", "Sketches", "Demos", "Live", "Remixes", "Stems", "Archive"}
	for _, name := range names {
		offerID, _ := seedPickedCollection(t, f, name)
		f.now = f.now.Add(time.Minute) // a distinct resolve time per collection
		resolveCollectionHome(t, f, offerID, "resolve-"+name)
		ids = append(ids, offerID)
	}
	got, err := f.service.PortfolioContinuations(ctx, "local", "new-home")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != portfolioContinuationMax {
		t.Fatalf("listed %d continuations, want the newest %d only", len(got), portfolioContinuationMax)
	}
	for i, item := range got {
		wantID := ids[len(ids)-1-i]
		if item.OfferID != wantID || item.Folder != names[len(names)-1-i] {
			t.Fatalf("item %d = %+v, want offer %s (%s) newest first", i, item, wantID, names[len(names)-1-i])
		}
		if item.State != FolderContinuationReady {
			t.Fatalf("item %d not ready: %+v", i, item)
		}
	}
	// Reading several collections still changes nothing.
	before, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.PortfolioContinuations(ctx, "local", "new-home"); err != nil {
		t.Fatal(err)
	}
	if after, err := f.store.Read(ctx, "local"); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("listing wrote to the offer store (err=%v)", err)
	}
}

func TestPortfolioContinuations_OldOffersAreHistory(t *testing.T) {
	f := newFolderDigestFixture(t)
	ctx := context.Background()
	offerID, _ := seedPickedAlbums(t, f)
	resolveAlbumsHome(t, f, offerID)
	f.now = f.now.Add(portfolioContinuationLookback + time.Minute)
	got, err := f.service.PortfolioContinuations(ctx, "local", "new-home")
	if err != nil || len(got) != 0 {
		t.Fatalf("a day-old offer is still offered: %+v, %v", got, err)
	}
}
