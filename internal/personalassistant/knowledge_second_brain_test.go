package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

type acceptSecondBrainAuthority struct{}

func (acceptSecondBrainAuthority) Revalidate(_ context.Context, _ KnowledgeBinding, item KnowledgeItem) error {
	if item.SourceKind != SecondBrainSourceKind {
		return ErrRepairNeeded
	}
	return nil
}

func TestSecondBrainKnowledge_LearnsWhereNotesLiveAndUnlearnsOnDisconnect(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	store := NewKnowledgeStore(f.resolver(), f.folder)
	learning := NewKnowledgeLifecycleService(store, f.memory, acceptSecondBrainAuthority{})
	notes := NewSecondBrainKnowledge(learning)
	source := secondbrain.Source{
		ID: secondbrain.SourceID("/Users/sam/Second Brain"), Kind: secondbrain.KindObsidian, Name: "Second Brain",
		Path: "/Users/sam/Second Brain", ConnectedAt: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC), RequestID: "connect-1",
	}

	notes.Learn(ctx, "local", source)
	notes.Learn(ctx, "local", source) // connecting the same folder again learns nothing twice
	doc, err := store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Items) != 1 {
		t.Fatalf("items = %d, want one fact", len(doc.Items))
	}
	fact := doc.Items[0]
	revision := fact.Revisions[len(fact.Revisions)-1]
	if fact.State != KnowledgeApproved || fact.SourceKind != SecondBrainSourceKind || fact.Category != "how_you_work" ||
		revision.Text != `You keep your notes in Obsidian, in the vault "Second Brain".` {
		t.Fatalf("fact = %+v text %q", fact, revision.Text)
	}
	snapshot, err := f.memory.SnapshotExact("hq-local")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range snapshot.Entries {
		found = found || strings.Contains(entry.Entry.Text, "You keep your notes in Obsidian")
	}
	if !found {
		t.Fatal("the approved fact did not reach MEMORY.md")
	}

	notes.Unlearn(ctx, "local", source)
	doc, _ = store.Read(ctx, "local")
	if doc.Items[0].State != KnowledgeNeedsReview {
		t.Fatalf("after disconnect the fact is %s, want needs review", doc.Items[0].State)
	}
}

func TestSecondBrainFactText_NamesAMarkdownFolder(t *testing.T) {
	if got := SecondBrainFactText(secondbrain.KindMarkdown, "Thesis Notes"); got != "You keep your notes in a Markdown folder called Thesis Notes." {
		t.Fatalf("text = %q", got)
	}
}

func TestValidateKnowledgeProposal_SecondBrainIsHowYouWorkOnly(t *testing.T) {
	proposal := SecondBrainKnowledgeProposal(secondbrain.Source{ID: "v_1", Kind: secondbrain.KindObsidian, Name: "Brain", ConnectedAt: time.Now()})
	if _, err := validateKnowledgeProposal(proposal); err != nil {
		t.Fatalf("valid proposal: %v", err)
	}
	proposal.Category = "people"
	if _, err := validateKnowledgeProposal(proposal); !errors.Is(err, ErrValidation) {
		t.Fatalf("people from a notes folder: %v", err)
	}
}
