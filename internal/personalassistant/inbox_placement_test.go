package personalassistant

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func inboxPlacementFixture(t *testing.T) (*FolderObservationService, *WorkspaceBuildStore, InboxPlacementInput, InboxPlacementSeed) {
	t.Helper()
	f, service, source, setup, _ := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Review(ctx, source, "conversation", observed.ID, folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.service.StoredOffer(ctx, "local", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	setup.intent = FolderSetupIntent{Placement: "standalone", Recipe: folderdigest.BuiltinInboxFolder, RecipeVersion: 1, Inbox: &FolderInboxIntent{
		WorkspaceName: "Desktop", SubjectKey: stored.Subject.Key, SubjectIdentity: stored.ConversationReview.CandidateIdentity,
		BlueprintID: "file-janitor", BlueprintVersion: 5, BlueprintDigest: "blueprint-1", PlanRevision: "plan-1",
		Agent: FolderInboxAgent{RoleID: "file-curator", Name: "File Curator", Action: FolderSoftwareCreate, ConfigDigest: "config-1"},
	}}
	service.BindSaved(source, observed.ID, "conversation")
	source.ConversationID, source.DraftID = "conversation", ""
	service.ConfigureInboxPlacementRevision(func(context.Context, FolderOffer) (string, error) { return "event-a", nil })
	read, err := service.ReadReview(ctx, source, view.ID)
	if err != nil || read.Plan == nil {
		t.Fatalf("source plan: %+v, %v", read, err)
	}
	builds := NewWorkspaceBuildStore(f.store.knowledge)
	builds.SetClock(func() time.Time { return f.now })
	input := InboxPlacementInput{OfferID: view.ID, PlanDigest: read.Plan.Digest, ConversationID: "conversation", ObservationID: observed.ID, SourceRevision: "event-a"}
	seed := func(context.Context, FolderSetupPlan, InboxPlacementInput) (BuildDraft, error) {
		return BuildDraft{TemplateID: "file-janitor", Name: "Desktop", Mission: "Exact goal"}, nil
	}
	return service, builds, input, seed
}

func TestInboxPlacementHandoff_RefusesBeforeSeedForInvalidSources(t *testing.T) {
	for _, kind := range []string{"accepted", "ineligible", "expired", "foreign", "replaced"} {
		t.Run(kind, func(t *testing.T) {
			service, builds, input, _ := inboxPlacementFixture(t)
			ctx := context.Background()
			owner := "local"
			switch kind {
			case "accepted", "ineligible":
				_, err := service.digest.store.Mutate(ctx, owner, func(doc *FolderDigestDocument) error {
					offer := doc.Offer(input.OfferID)
					if kind == "accepted" {
						now := service.digest.now()
						offer.Status, offer.DecidedAt, offer.Choice = FolderOfferAwaitingOutcome, &now, FolderChoiceProject
					} else {
						offer.CapabilitySuppressed = true
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			case "expired":
				expired := service.digest.now().Add(foldercontext.SelectionTTL)
				service.digest.deps.Now = func() time.Time { return expired }
			case "foreign":
				owner = "foreign"
			case "replaced":
				offer, err := service.digest.StoredOffer(ctx, owner, input.OfferID)
				if err != nil {
					t.Fatal(err)
				}
				root, err := service.ResolveRoot(ctx, offer.ConversationReview.Target, input.ObservationID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(root, root+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			seed := func(context.Context, FolderSetupPlan, InboxPlacementInput) (BuildDraft, error) {
				t.Fatal("invalid source reached seed")
				return BuildDraft{}, nil
			}
			if _, err := service.HandoffInbox(ctx, owner, input, builds, seed); err == nil {
				t.Fatal("invalid source transferred")
			}
			doc, err := builds.Read(ctx, "local")
			if err != nil || doc.Open() != nil {
				t.Fatal("refusal created a draft", err)
			}
			offer, err := service.digest.StoredOffer(ctx, "local", input.OfferID)
			if err != nil || offer.PlacementHandoff != nil {
				t.Fatal("refusal changed source ownership", err)
			}
		})
	}
}

type failedPlacementWrite struct{ InboxPlacementBuildStore }

func (f failedPlacementWrite) Mutate(ctx context.Context, owner string, mutate func(*WorkspaceBuildDocument) error) (WorkspaceBuildDocument, error) {
	doc, err := f.Read(ctx, owner)
	if err != nil {
		return doc, err
	}
	if err := mutate(&doc); err != nil {
		return WorkspaceBuildDocument{}, err
	}
	return WorkspaceBuildDocument{}, errors.New("injected destination write failure")
}

func TestInboxPlacementHandoff_FailedDestinationWriteRetiresSourceAndRetriesOnlyReservedIdentity(t *testing.T) {
	service, builds, input, seed := inboxPlacementFixture(t)
	ctx := context.Background()
	if _, err := service.HandoffInbox(ctx, "local", input, failedPlacementWrite{builds}, seed); err == nil {
		t.Fatal("lost destination write reported success")
	}
	offer, err := service.digest.StoredOffer(ctx, "local", input.OfferID)
	if err != nil || offer.PlacementHandoff == nil || offer.PlacementHandoff.DraftSaved || offer.Status != FolderOfferClosed || offer.DecidedAt != nil {
		t.Fatalf("unsafe source after failure: %+v, %v", offer, err)
	}
	reserved := offer.PlacementHandoff.BuildID
	if _, err := service.digest.StartSetup(ctx, "local", input.OfferID, FolderSetupInput{RequestID: "old-card", PlanDigest: input.PlanDigest}); !errors.Is(err, ErrFolderOfferDecided) {
		t.Fatalf("old source could confirm: %v", err)
	}
	result, err := service.HandoffInbox(ctx, "local", input, builds, seed)
	if err != nil || result.Session.ID != reserved {
		t.Fatalf("retry replaced identity: %+v, %v", result, err)
	}
	release, err := service.AcquireInboxPlacement(ctx, "local", result.Session)
	if err != nil {
		t.Fatal(err)
	}
	release()
	// A lost/pruned acknowledged record is fundamentally different from an
	// interrupted seed. Never reconstruct it, even using the old reserved ID.
	if _, err := builds.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error { doc.Sessions = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := service.HandoffInbox(ctx, "local", input, builds, seed); err == nil {
		t.Fatal("pruned operation was reconstructed")
	}
}

func TestInboxPlacementHandoff_ReopenAndRestartCannotRestoreOriginalConfirmation(t *testing.T) {
	service, builds, input, seed := inboxPlacementFixture(t)
	ctx := context.Background()
	result, err := service.HandoffInbox(ctx, "local", input, builds, seed)
	if err != nil {
		t.Fatal(err)
	}
	source := result.Session.PlacementSource
	view, err := service.Review(ctx, source.Target, input.ConversationID, input.ObservationID, folderRootCandidateID)
	if err != nil || view.ID != input.OfferID || view.PlacementHandoff == nil || view.Plan != nil {
		t.Fatalf("re-review minted source authority: %+v, %v", view, err)
	}
	// Losing process-local holds cannot be repaired by reading or resuming the
	// durable draft. Its identity and user edits still remain recoverable.
	service.mu.Lock()
	service.selections = map[string]heldFolderObservation{}
	service.mu.Unlock()
	replayed, err := service.HandoffInbox(ctx, "local", input, builds, seed)
	if err != nil || !replayed.Resumed || replayed.Session.ID != result.Session.ID {
		t.Fatalf("restart lost draft identity: %+v, %v", replayed, err)
	}
	target := source.Target
	target.ConversationID, target.DraftID = "", "build:"+result.Session.ID
	if _, err := service.ResolveRoot(ctx, target, replayed.Session.Folder.Observation.ID); !errors.Is(err, ErrFolderSelection) {
		t.Fatal("stored snapshot revived a hold")
	}
}
