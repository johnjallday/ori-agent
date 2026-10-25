package personalassistant

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

type prerequisiteSetup struct {
	*fakeFolderSetup
	calls     int
	confirmed int
}

func (s *prerequisiteSetup) Prerequisite(_ context.Context, req FolderSetupRequest, input FolderPrerequisiteInput) (FolderPrerequisiteReview, error) {
	s.calls++
	if req.Path != "" || req.Brief != "" || req.Update != nil {
		return FolderPrerequisiteReview{}, errors.New("package repair acquired setup execution input")
	}
	if input.Confirm {
		s.confirmed++
	}
	return FolderPrerequisiteReview{Kind: input.Kind, Completed: input.Confirm}, nil
}

func TestFolderPrerequisiteRequiresCurrentCanonicalUnacceptedCreation(t *testing.T) {
	for _, scenario := range []string{"preview", "confirm", "accepted", "support", "other_owner", "lost_selection", "changed_destination", "missing_home", "unnamed_blocker", "unknown_kind", "build_owner"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			f, observations, source := observationFixture(t)
			setup := &prerequisiteSetup{fakeFolderSetup: &fakeFolderSetup{lines: []FolderPlanLine{{Kind: FolderPlanWorkspace, Name: "New workspace"}}, destination: &FolderSetupDestination{Status: "standalone"}}}
			f.service.deps.Setup = setup
			active := ""
			observations.ConfigureConversationReviews(func(_ context.Context, offer FolderOffer) error {
				if active != offer.ID {
					return ErrFolderSelection
				}
				return nil
			}, func(foldercontext.Target) (func(), bool) { return func() {}, true })
			song := filepath.Join(f.home, "Music", "Inline Song")
			writeFolderFiles(t, song, "Song.rpp")
			observation := observeSelected(t, f, observations, source, song)
			view, err := observations.Review(WithFolderReviewPlacement(ctx, FolderReviewPlacement{Operation: FolderOperationCreate}), source, "conversation", observation.ID, observation.Projects[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			observations.BindSaved(source, observation.ID, "conversation")
			active = view.ID
			input := FolderPrerequisiteInput{Kind: FolderPlanIntegration, RequestID: "explicit-package-action", Confirm: scenario == "confirm"}
			switch scenario {
			case "other_owner":
				active = "another-review"
			case "lost_selection":
				f.now = f.now.Add(31 * time.Minute)
			case "changed_destination":
				setup.destination = &FolderSetupDestination{Status: "new", ProgramID: "changed"}
			case "unknown_kind":
				input.Kind = "workspace"
			}
			_, err = f.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error {
				offer := doc.Offer(view.ID)
				switch scenario {
				case "accepted":
					offer.Status = FolderOfferAwaitingOutcome
				case "support":
					offer.ConversationReview.Operation = FolderOperationSupport
				case "missing_home", "unnamed_blocker":
					offer.ConversationReview.DestinationUnavailable = true
					offer.ConversationReview.Destination = nil
					if scenario == "missing_home" {
						offer.ConversationReview.DestinationBlocker = &FolderDestinationBlocker{IntegrationKey: "ori_reaper", HomeProviderKey: "music_project_management"}
					}
				case "build_owner":
					offer.ConversationReview.Target = BuildReviewTarget(source, "build")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.store.Read(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.service.Prerequisite(ctx, "local", view.ID, input)
			allowed := scenario == "preview" || scenario == "confirm" || scenario == "missing_home"
			if (err == nil) != allowed || (setup.calls == 1) != allowed {
				t.Fatalf("allowed=%t calls=%d error=%v", allowed, setup.calls, err)
			}
			after, readErr := f.store.Read(ctx, "local")
			if readErr != nil || !reflect.DeepEqual(before, after) || setup.runs != 0 {
				t.Fatal("package-only action changed folder lifecycle or ran setup", readErr)
			}
			if setup.confirmed != 0 && scenario != "confirm" {
				t.Fatal("preview confirmed a package")
			}
		})
	}
}

func TestFolderHireCandidateUnavailableIntegrationStillOpensRequirements(t *testing.T) {
	fx := newBuildSetupFixture(t)
	fx.setup.planErr = errors.New("Home plugin missing")
	candidate := fx.service.HireCandidate(t.Context(), fx.draft, fx.observation.ID)
	if candidate.CandidateID != fx.candidate {
		t.Fatalf("missing prerequisites hid the requirements review: %+v", candidate)
	}
	if fx.setup.runs != 0 {
		t.Fatal("requirements entry ran setup")
	}
	doc, err := fx.store.Read(t.Context(), "local")
	if err != nil || len(doc.Offers) != 0 {
		t.Fatal("eligibility persisted a review", err)
	}
}

func TestFolderPrerequisite_LibraryReviewOnlyRepairsPackagesBeforeAcceptance(t *testing.T) {
	f, observations, source, observation := albumsObservation(t)
	setup := &prerequisiteSetup{fakeFolderSetup: &fakeFolderSetup{lines: oneCardLines(), destination: &FolderSetupDestination{Status: "standalone"}}}
	f.service.deps.Setup = setup
	active := ""
	observations.ConfigureConversationReviews(func(_ context.Context, offer FolderOffer) error {
		if active != offer.ID {
			return ErrFolderSelection
		}
		return nil
	}, func(foldercontext.Target) (func(), bool) { return func() {}, true })
	ctx := WithFolderStructureAdjustment(WithFolderReviewPlacement(t.Context(), FolderReviewPlacement{Operation: FolderOperationCreate}), &FolderStructureAdjustment{GroupType: FolderGroupLibrary})
	view, err := observations.Review(ctx, source, "library-chat", observation.ID, folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	observations.BindSaved(source, observation.ID, "library-chat")
	active = view.ID
	before, _ := f.store.Read(t.Context(), "local")
	if _, err := f.service.Prerequisite(t.Context(), "local", view.ID, FolderPrerequisiteInput{Kind: FolderPlanProvider, RequestID: "preview-library-provider"}); err != nil || setup.calls != 1 {
		t.Fatalf("library package review: %d %v", setup.calls, err)
	}
	after, _ := f.store.Read(t.Context(), "local")
	if !reflect.DeepEqual(before, after) || setup.runs != 0 {
		t.Fatal("package preview changed library consent or effects")
	}
	// A provider-only library has no REAPER key but can still repair its Home.
	if _, err := f.store.Mutate(t.Context(), "local", func(d *FolderDigestDocument) error {
		o := d.Offer(view.ID)
		o.ConversationReview.Destination, o.ConversationReview.DestinationUnavailable = nil, true
		o.ConversationReview.DestinationBlocker = &FolderDestinationBlocker{HomeProviderKey: o.Portfolio.ProviderKey}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Prerequisite(t.Context(), "local", view.ID, FolderPrerequisiteInput{Kind: FolderPlanProvider, RequestID: "provider-only"}); err != nil || setup.calls != 2 {
		t.Fatal("provider-only library stranded", err)
	}
	if _, err := f.store.Mutate(t.Context(), "local", func(d *FolderDigestDocument) error { d.Offer(view.ID).Status = FolderOfferAwaitingOutcome; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Prerequisite(t.Context(), "local", view.ID, FolderPrerequisiteInput{Kind: FolderPlanProvider, RequestID: "accepted-library-provider"}); !errors.Is(err, ErrFolderOfferDecided) || setup.calls != 2 {
		t.Fatal("accepted library entered prerequisite repair", err)
	}
}
