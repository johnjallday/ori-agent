package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func buildPreparationFixture(now time.Time) *BuildPreparationClaim {
	return &BuildPreparationClaim{ID: uuid.NewString(), WorkspaceID: uuid.NewString(), BuildVersion: 1,
		ReviewRevision: strings.Repeat("a", 64), RootRevision: strings.Repeat("b", 64), TermsRevision: strings.Repeat("c", 64), AcceptedAt: now}
}

func TestWorkspaceBuildPreparation_SurvivesExpiryAndRefusesAnotherOpenBuild(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	build := openBuild("accepted-build", *now)
	build.Status, build.Preparation = WorkspaceBuildPreparing, buildPreparationFixture(*now)
	build.Draft = BuildDraft{Name: "Reviewed name", TemplateID: "file-janitor"}
	_, err := store.Mutate(context.Background(), "local", func(doc *WorkspaceBuildDocument) error { doc.Add(build); return nil })
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * WorkspaceBuildExpiry)
	doc, err := store.Read(context.Background(), "local")
	if err != nil || doc.Open() == nil || doc.Open().Preparation.ID != build.Preparation.ID || doc.Open().Draft.Name != "Reviewed name" {
		t.Fatalf("accepted build expired or lost its exact draft: %+v, %v", doc, err)
	}
	_, err = store.Mutate(context.Background(), "local", func(doc *WorkspaceBuildDocument) error { doc.Add(openBuild("another", *now)); return nil })
	if !errors.Is(err, ErrWorkspaceBuildOpen) {
		t.Fatalf("second open build: %v", err)
	}
}

func TestWorkspaceBuildPreparation_LegacyDecodeNeverFabricatesConsent(t *testing.T) {
	store, _, now := newWorkspaceBuildStoreFixture(t)
	doc, err := store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	doc.SchemaVersion, doc.Version = 1, 1
	doc.Sessions = []WorkspaceBuildSession{openBuild("old-build", *now)}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := decodeWorkspaceBuild(data, doc.Owner)
	if err != nil || loaded.SchemaVersion != 2 || loaded.Open().Preparation != nil {
		t.Fatalf("legacy = %+v, %v", loaded, err)
	}
	for _, version := range []int{1, WorkspaceBuildSchemaVersion + 1} {
		doc.SchemaVersion = version
		doc.Sessions[0].Preparation = buildPreparationFixture(*now)
		doc.Sessions[0].Status = WorkspaceBuildPreparing
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeWorkspaceBuild(data, doc.Owner); !errors.Is(err, ErrKnowledgeCorrupt) {
			t.Fatalf("schema %d accepted unknown authority: %v", version, err)
		}
	}
}
