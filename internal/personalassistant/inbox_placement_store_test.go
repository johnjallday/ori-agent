package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func placementSourceFixture() *InboxPlacementSource {
	return &InboxPlacementSource{OfferID: "offer-a", ObservationID: "observed-a", Revision: "event-a", PlanDigest: strings.Repeat("a", 64),
		Target: foldercontext.Target{UserID: "local", WorkspaceID: "hq", AgentName: "atlas", ConversationID: "conversation-a"}}
}

func TestInboxPlacementStore_BuildRoundTripAndSettlePreserveOnlyProvenance(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	ctx := context.Background()
	build := openBuild("build-a", *now)
	build.Draft = BuildDraft{TemplateID: "file-janitor", Name: "Inbox"}
	build.PlacementSource, build.PlacementMonitoring = placementSourceFixture(), true
	_, err := store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error { doc.Add(build); return nil })
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(ctx, "local")
	if err != nil || *doc.Open().PlacementSource != *build.PlacementSource || !doc.Open().PlacementMonitoring {
		t.Fatalf("source lost: %+v, %v", doc, err)
	}
	_, err = store.Mutate(ctx, "local", func(doc *WorkspaceBuildDocument) error {
		b := doc.Open()
		b.Status = WorkspaceBuildAbandoned
		b.Settle()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err = store.Read(ctx, "local")
	if err != nil || doc.Session(build.ID).PlacementSource.OfferID != "offer-a" || doc.Session(build.ID).Folder != nil || doc.Session(build.ID).Preparation != nil {
		t.Fatalf("settle invented or lost authority: %+v, %v", doc, err)
	}
}

func TestInboxPlacementStore_V4BuildMigratesWithoutDiscardingEditFences(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	doc, err := store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	build := openBuild("build-a", *now)
	build.ContextVersion, build.InferenceID = 2, "inference-a"
	build.EditedFields = map[string]int64{"name": 1}
	doc.SchemaVersion, doc.Version, doc.Sessions = 4, 1, []WorkspaceBuildSession{build}
	data, _ := json.Marshal(doc)
	migrated, err := decodeWorkspaceBuild(data, doc.Owner)
	if err != nil || migrated.SchemaVersion != 5 || migrated.Open().PlacementSource != nil || migrated.Open().InferenceID != "inference-a" || len(migrated.Open().EditedFields) != 1 {
		t.Fatalf("v4 altered inference or invented a handoff: %+v, %v", migrated, err)
	}
	for _, version := range []int{1, 2, 3, 4, 6} {
		build := openBuild("build-a", *now)
		build.Draft.TemplateID = "file-janitor"
		build.PlacementSource = placementSourceFixture()
		doc.SchemaVersion, doc.Sessions = version, []WorkspaceBuildSession{build}
		data, _ = json.Marshal(doc)
		if _, err := decodeWorkspaceBuild(data, doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatalf("schema %d accepted misplaced authority: %v", version, err)
		}
	}
}

func TestInboxPlacementStore_FolderBindingRoundTripLegacyAndFailClosedValidation(t *testing.T) {
	f, service, source, _, _ := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Review(ctx, source, "conversation", observed.ID, folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	doc.SchemaVersion = 1
	data, _ := json.Marshal(doc)
	legacy, err := decodeFolderDigest(data, doc.Owner)
	if err != nil || legacy.SchemaVersion != 2 || legacy.Offer(view.ID).PlacementHandoff != nil || legacy.Offer(view.ID).Status != FolderOfferPending {
		t.Fatalf("legacy review acquired authority: %+v, %v", legacy, err)
	}
	binding := &InboxPlacementHandoff{BuildID: "build-a", Revision: "event-a", PlanDigest: strings.Repeat("a", 64)}
	doc, err = f.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error {
		offer := doc.Offer(view.ID)
		offer.Status, offer.PlacementHandoff = FolderOfferClosed, binding
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := f.store.Read(ctx, "local")
	if err != nil || *loaded.Offer(view.ID).PlacementHandoff != *binding || loaded.Offer(view.ID).DecidedAt != nil {
		t.Fatalf("handoff altered/accepted: %+v, %v", loaded, err)
	}
	for _, version := range []int{1, 3} {
		doc.SchemaVersion = version
		data, _ = json.Marshal(doc)
		if _, err := decodeFolderDigest(data, doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatalf("schema %d accepted handoff: %v", version, err)
		}
	}
	for _, status := range []FolderOfferStatus{FolderOfferPending, FolderOfferAwaitingOutcome, FolderOfferLater} {
		_, err := f.store.Mutate(ctx, "local", func(doc *FolderDigestDocument) error { doc.Offer(view.ID).Status = status; return nil })
		if err == nil {
			t.Fatalf("handed-off source became independently confirmable: %s", status)
		}
	}
	loaded, err = f.store.Read(ctx, "local")
	if err != nil || loaded.Offer(view.ID).Status != FolderOfferClosed {
		t.Fatal("failed mutation changed canonical source")
	}
}
