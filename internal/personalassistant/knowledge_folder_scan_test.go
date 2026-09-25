package personalassistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// acceptFolderScanAuthority stands in for the host authority: every
// folder_scan item revalidates, mirroring a folder that still exists.
type acceptFolderScanAuthority struct{}

func (acceptFolderScanAuthority) Revalidate(_ context.Context, _ KnowledgeBinding, item KnowledgeItem) error {
	if item.SourceKind != FolderScanSourceKind {
		return ErrRepairNeeded
	}
	return nil
}

func newFolderScanLearning(t *testing.T) (*knowledgeFixture, *KnowledgeLearningService, *KnowledgeStore) {
	t.Helper()
	f := newKnowledgeFixture(t)
	store := NewKnowledgeStore(f.resolver(), f.folder)
	learning := NewKnowledgeLifecycleService(store, f.memory, acceptFolderScanAuthority{})
	return f, learning, store
}

func resolvedProjectOffer(name, marker, markerName, ext string) FolderOffer {
	subjectKey := FolderKey("/Users/me/Documents/" + name)
	return FolderOffer{
		ID: "offer-" + strings.ToLower(name), Status: FolderOfferResolved, Verdict: "project",
		FolderKey: FolderKey("/Users/me/Documents"), FolderName: "Documents",
		ScannedAt: time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC),
		Subject: FolderCandidateRecord{
			Key: subjectKey, Name: name, Kind: FolderChoiceProject, Shape: "manuscript",
			Marker: marker, MarkerName: markerName, DominantExtension: ext, RelPath: name,
		},
		Outcome: &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "ws-" + strings.ToLower(name)},
	}
}

func TestFolderScanProducer_ApprovesTheProjectAndQueuesTheTool(t *testing.T) {
	f, learning, store := newFolderScanLearning(t)
	ctx := context.Background()
	producer := NewFolderScanProducer(learning)
	offer := resolvedProjectOffer("Thesis", "LaTeX manuscript", "main.tex", ".tex")

	learned := producer.LearnFromOffer(ctx, "local", offer)
	if !learned.Remembered || learned.Note != "" {
		t.Fatalf("learning=%+v", learned)
	}
	doc, err := store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	var fact, tool *KnowledgeItem
	for i := range doc.Items {
		switch doc.Items[i].Category {
		case "projects":
			fact = &doc.Items[i]
		case "how_you_work":
			tool = &doc.Items[i]
		}
	}
	if fact == nil || fact.State != KnowledgeApproved || fact.SourceKind != FolderScanSourceKind {
		t.Fatalf("project fact=%+v", fact)
	}
	revision := fact.Revisions[len(fact.Revisions)-1]
	if revision.Text != "You are working on a project in the folder Thesis." {
		t.Errorf("fact text=%q", revision.Text)
	}
	if len(revision.Evidence) != 1 || revision.Evidence[0].SourceID != offer.Subject.Key || revision.Evidence[0].Summary != "Folder shown to the assistant; marker: LaTeX manuscript" || !revision.Evidence[0].ObservedAt.Equal(offer.ScannedAt) {
		t.Errorf("fact evidence=%+v", revision.Evidence)
	}
	if tool == nil || tool.State != KnowledgeCandidate || tool.Revisions[0].Text != "LaTeX may be one of the tools you use." {
		t.Fatalf("tool candidate=%+v", tool)
	}
	// The approved fact reached MEMORY.md; the candidate did not.
	snapshot, err := f.memory.SnapshotExact("hq-local")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, entry := range snapshot.Entries {
		texts = append(texts, entry.Entry.Text)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "project in the folder Thesis") || strings.Contains(joined, "LaTeX may be") {
		t.Errorf("memory=%q", joined)
	}

	// A replayed resolve learns nothing twice.
	again := producer.LearnFromOffer(ctx, "local", offer)
	doc, _ = store.Read(ctx, "local")
	if !again.Remembered || len(doc.Items) != 2 {
		t.Fatalf("replay learning=%+v items=%d", again, len(doc.Items))
	}
}

func TestFolderScanProducer_QuotaPauseAndRefusalSkipTheFact(t *testing.T) {
	ctx := context.Background()

	t.Run("quota", func(t *testing.T) {
		_, learning, _ := newFolderScanLearning(t)
		producer := NewFolderScanProducer(learning)
		// Three admissions fill the rolling window; the fourth is refused.
		for _, name := range []string{"Alpha", "Beta", "Gamma"} {
			if learned := producer.LearnFromOffer(ctx, "local", resolvedProjectOffer(name, "git repository", ".git", ".go")); !learned.Remembered {
				t.Fatalf("%s: %+v", name, learned)
			}
		}
		learned := producer.LearnFromOffer(ctx, "local", resolvedProjectOffer("Delta", "git repository", ".git", ".go"))
		if learned.Remembered || !strings.Contains(learned.Note, "review queue is full") {
			t.Fatalf("over quota learning=%+v", learned)
		}
	})

	t.Run("paused", func(t *testing.T) {
		f, learning, _ := newFolderScanLearning(t)
		state, err := f.relationships.GetState(ctx, "local")
		if err != nil {
			t.Fatal(err)
		}
		state.Status = StatusPaused
		if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
			t.Fatal(err)
		}
		learned := NewFolderScanProducer(learning).LearnFromOffer(ctx, "local", resolvedProjectOffer("Thesis", "", "", ".tex"))
		if learned.Remembered || !strings.Contains(learned.Note, "paused") {
			t.Fatalf("paused learning=%+v", learned)
		}
	})

	t.Run("secret-looking name", func(t *testing.T) {
		_, learning, store := newFolderScanLearning(t)
		secret := "sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnop"
		if _, err := workspace.ValidateMemoryText(FolderProjectFactText(secret)); err == nil {
			t.Skip("memory validator does not flag this name; pick another fixture")
		}
		learned := NewFolderScanProducer(learning).LearnFromOffer(ctx, "local", resolvedProjectOffer(secret, "", "", ".tex"))
		if learned.Remembered || !strings.Contains(learned.Note, "could not save") {
			t.Fatalf("refused learning=%+v", learned)
		}
		doc, _ := store.Read(ctx, "local")
		for _, item := range doc.Items {
			if item.Category == "projects" {
				t.Fatalf("a secret-looking name became a fact: %+v", item)
			}
		}
	})
}

func TestFolderScanProducer_SkipsToolsTheSavedAppProducerAlreadyHolds(t *testing.T) {
	_, learning, store := newFolderScanLearning(t)
	ctx := context.Background()
	// The saved-app producer proposed Obsidian first.
	if _, _, err := learning.Propose(ctx, "local", KnowledgeProposal{
		SourceKind: "saved_app", ScopeID: "saved-onboarding", SubjectID: "obsidian",
		Predicate: "uses tool", Value: "obsidian", Category: "how_you_work",
		Text:     "Obsidian may be one of the tools you use to keep notes.",
		Evidence: []KnowledgeEvidence{{SourceKind: "saved_app", SourceID: "obsidian", Summary: "Saved onboarding app observation"}},
	}); err != nil {
		t.Fatal(err)
	}
	offer := resolvedProjectOffer("Vault", "Obsidian vault", ".obsidian", ".md")
	NewFolderScanProducer(learning).LearnFromOffer(ctx, "local", offer)
	doc, _ := store.Read(ctx, "local")
	obsidian := 0
	for _, item := range doc.Items {
		for _, revision := range item.Revisions {
			if strings.Contains(revision.Text, "Obsidian") {
				obsidian++
			}
		}
	}
	if obsidian != 1 {
		t.Fatalf("Obsidian proposed %d times", obsidian)
	}
}

func TestValidateKnowledgeProposal_FolderScanCategories(t *testing.T) {
	base := KnowledgeProposal{
		SourceKind: FolderScanSourceKind, ScopeID: "scope", SubjectID: "subject", Predicate: "works on project", Value: "subject",
		Text:     "You are working on a project in the folder Thesis.",
		Evidence: []KnowledgeEvidence{{SourceKind: FolderScanSourceKind, SourceID: "subject", Summary: "Folder shown to the assistant"}},
	}
	for _, category := range []string{"projects", "how_you_work"} {
		proposal := base
		proposal.Category = category
		if _, err := validateKnowledgeProposal(proposal); err != nil {
			t.Errorf("%s: %v", category, err)
		}
	}
	for _, category := range []string{"people", "routines", "sources"} {
		proposal := base
		proposal.Category = category
		if _, err := validateKnowledgeProposal(proposal); err == nil {
			t.Errorf("%s accepted for a folder scan", category)
		}
	}
}
