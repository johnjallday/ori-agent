package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// existingHomeFixture scans a five-album collection while a reviewed Home
// already exists, using a dialog-picked folder (the case a restart loses).
func existingHomeFixture(t *testing.T, existing func(context.Context, string, string) (FolderCreateResult, error)) (*folderDigestFixture, *FolderOfferView, string) {
	t.Helper()
	f := newFolderDigestFixture(t)
	root := filepath.Join(f.home, "Albums")
	for i := range 5 {
		sub := filepath.Join(root, "Album-"+string(rune('1'+i)))
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "Song.als"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.service.deps.HomeExists = func(context.Context, string, string) (bool, error) { return true, nil }
	f.service.deps.ExistingHome = existing
	f.service.deps.Picker = fakeFolderPicker{path: root, chosen: true}
	offer, err := f.service.ScanPicked(context.Background(), "local")
	if err != nil || offer == nil {
		t.Fatalf("scan = %+v, %v", offer, err)
	}
	return f, offer, root
}

func existingHome() (FolderCreateResult, error) {
	return FolderCreateResult{WorkspaceID: "music-home", Route: "/workspaces/music-home"}, nil
}

func TestExistingHome_OffersToAddTheCollectionInsteadOfASecondHome(t *testing.T) {
	_, offer, _ := existingHomeFixture(t, func(context.Context, string, string) (FolderCreateResult, error) { return existingHome() })
	if offer.Portfolio == nil || !offer.Portfolio.ExistingHome {
		t.Fatalf("existing Home was suppressed to a plain suggestion: %+v", offer)
	}
	if offer.Capability == nil || offer.Capability.Question != "Add this collection to your Music Production Home?" ||
		offer.Capability.AcceptLabel != "Yes, add to my Home" {
		t.Fatalf("capability = %+v", offer.Capability)
	}
	if offer.CreateAvailable || offer.Blueprint != "" || offer.Remember {
		t.Fatalf("an add-to-Home card must not create a workspace or promise to remember: %+v", offer)
	}
}

func TestExistingHome_FallsBackToTheSamePlainSuggestionWhenTheHomeCannotBeRead(t *testing.T) {
	for name, existing := range map[string]func(context.Context, string, string) (FolderCreateResult, error){
		"not wired": nil,
		"read failed": func(context.Context, string, string) (FolderCreateResult, error) {
			return FolderCreateResult{}, ErrFolderWorkspaceRefused
		},
		"no route": func(context.Context, string, string) (FolderCreateResult, error) {
			return FolderCreateResult{WorkspaceID: "h"}, nil
		},
		"blank Home ID": func(context.Context, string, string) (FolderCreateResult, error) {
			return FolderCreateResult{Route: "/workspaces/x"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, offer, _ := existingHomeFixture(t, existing)
			if offer.Portfolio != nil || offer.Capability != nil {
				t.Fatalf("an unreadable Home still produced a Home offer: %+v", offer)
			}
		})
	}
}

func TestExistingHome_ResolveRecordsTheServerReadHomeAndFeedsTheLibraryHandOff(t *testing.T) {
	f, offer, root := existingHomeFixture(t, func(context.Context, string, string) (FolderCreateResult, error) { return existingHome() })
	ctx := context.Background()

	// Not yet confirmed: nothing to resolve.
	if _, err := f.service.ResolveExistingHome(ctx, "local", offer.ID, "early"); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("early resolve: %v", err)
	}
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.ResolveExistingHome(ctx, "local", offer.ID, "resolve-1")
	if err != nil || got.Status != FolderOfferResolved || got.Outcome == nil ||
		got.Outcome.Kind != FolderChoiceHome || got.Outcome.WorkspaceID != "music-home" ||
		got.Outcome.Route != "/workspaces/music-home" || !got.Outcome.Existing {
		t.Fatalf("resolved = %+v, %v", got, err)
	}
	// A retried click returns the same answer; a different request is refused.
	if again, err := f.service.ResolveExistingHome(ctx, "local", offer.ID, "resolve-1"); err != nil || again.Status != FolderOfferResolved {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	if _, err := f.service.ResolveExistingHome(ctx, "local", offer.ID, "resolve-2"); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("second resolve of a settled offer: %v", err)
	}

	// The unchanged library hand-off accepts it for exactly that Home...
	if selected, identity, err := f.service.PortfolioRoot(ctx, "local", offer.ID, "music-home"); err != nil || selected != root || identity == "" {
		t.Fatalf("hand-off = %q %v", selected, err)
	}
	if _, _, err := f.service.PortfolioRoot(ctx, "local", offer.ID, "another-home"); !errors.Is(err, ErrFolderWorkspaceRefused) {
		t.Fatalf("hand-off to another Home: %v", err)
	}
	// ...and a Home reopened without its address finds it.
	list, err := f.service.PortfolioContinuations(ctx, "local", "music-home")
	if err != nil || len(list) != 1 || list[0].OfferID != offer.ID || list[0].State != FolderContinuationReady {
		t.Fatalf("continuations = %+v, %v", list, err)
	}
}

func TestExistingHome_NeverResolvesThroughTheBrowserNamedHomePath(t *testing.T) {
	f, offer, _ := existingHomeFixture(t, func(context.Context, string, string) (FolderCreateResult, error) { return existingHome() })
	ctx := context.Background()
	f.service.deps.HomeJourney = &fakeHomeVerifier{} // would accept "new-home" for a new Home
	if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ResolvePortfolio(ctx, "local", offer.ID, FolderResolveInput{HomeID: "new-home", RequestID: "browser"}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("browser-named Home resolved an add-to-Home offer: %v", err)
	}
}

func TestExistingHome_RefusesForeignOwnersNewHomeOffersAndAHomeThatDisappeared(t *testing.T) {
	ctx := context.Background()

	t.Run("foreign owner", func(t *testing.T) {
		f, offer, _ := existingHomeFixture(t, func(context.Context, string, string) (FolderCreateResult, error) { return existingHome() })
		if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "confirmed"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.ResolveExistingHome(ctx, "other", offer.ID, "foreign"); err == nil {
			t.Fatal("another owner resolved this offer")
		}
	})

	t.Run("a new-Home offer cannot use this route", func(t *testing.T) {
		f := newFolderDigestFixture(t)
		offerID, _ := seedPickedAlbums(t, f) // HomeExists=false => a normal new-Home offer
		f.service.deps.ExistingHome = func(context.Context, string, string) (FolderCreateResult, error) { return existingHome() }
		if _, err := f.service.ResolveExistingHome(ctx, "local", offerID, "wrong-route"); !errors.Is(err, ErrFolderOfferDecided) {
			t.Fatalf("new-Home offer resolved as existing: %v", err)
		}
	})

	t.Run("the Home disappeared before confirming", func(t *testing.T) {
		gone := false
		f, offer, _ := existingHomeFixture(t, func(context.Context, string, string) (FolderCreateResult, error) {
			if gone {
				return FolderCreateResult{}, ErrFolderWorkspaceRefused
			}
			return existingHome()
		})
		if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "confirmed"}); err != nil {
			t.Fatal(err)
		}
		gone = true
		if _, err := f.service.ResolveExistingHome(ctx, "local", offer.ID, "late"); !errors.Is(err, ErrFolderWorkspaceRefused) {
			t.Fatalf("resolved against a Home that is gone: %v", err)
		}
		stored, err := f.store.Read(ctx, "local")
		if err != nil || stored.Offer(offer.ID).Status != FolderOfferAwaitingOutcome {
			t.Fatalf("a refused resolve changed the offer: %v", err)
		}
	})

	t.Run("blank and oversized request IDs", func(t *testing.T) {
		f, offer, _ := existingHomeFixture(t, func(context.Context, string, string) (FolderCreateResult, error) { return existingHome() })
		if _, err := f.service.Decide(ctx, "local", offer.ID, FolderDecisionInput{Decision: FolderDecisionYes, Choice: FolderChoiceProject, RequestID: "confirmed"}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"", "   ", string(make([]byte, folderRequestIDMax+1))} {
			if _, err := f.service.ResolveExistingHome(ctx, "local", offer.ID, id); !errors.Is(err, ErrFolderOutcomeUnavailable) {
				t.Fatalf("request id %q: %v", id, err)
			}
		}
	})
}
