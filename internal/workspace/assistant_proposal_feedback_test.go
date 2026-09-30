package workspace

import (
	"testing"
)

func TestRecordProposalFeedback_CountsEachSuggestionOnceAndNeverReachesPrompts(t *testing.T) {
	store := NewAssistantLearningStore(testFolderResolver{root: t.TempDir()})
	before, err := store.Read("home")
	if err != nil {
		t.Fatal(err)
	}
	promptBefore := RenderManagedLearningPromptSection(before)
	for _, step := range []struct{ id, kind, outcome string }{
		{"p1", "next_action", "dismissed"},
		{"p1", "next_action", "dismissed"}, // a retried answer is not counted twice
		{"p2", "next_action", "accepted"},
		{"p3", "project_review", "dismissed"},
	} {
		if _, err := store.RecordProposalFeedback("home", step.id, step.kind, step.outcome); err != nil {
			t.Fatalf("%+v: %v", step, err)
		}
	}
	after, err := store.Read("home")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]int{"next_action": {1, 1}, "project_review": {0, 1}}
	if len(after.ProposalFeedback) != len(want) {
		t.Fatalf("feedback kinds: %+v", after.ProposalFeedback)
	}
	for _, row := range after.ProposalFeedback {
		if counts := want[row.Kind]; row.Accepted != counts[0] || row.Dismissed != counts[1] || row.UpdatedAt.IsZero() {
			t.Fatalf("%s counts: %+v", row.Kind, row)
		}
	}
	if len(after.Candidates) != 0 || len(after.Learnings) != 0 || len(after.Suggestions) != 0 {
		t.Fatalf("feedback created a learning or candidate: %+v", after)
	}
	if got := RenderManagedLearningPromptSection(after); got != promptBefore {
		t.Fatalf("feedback changed the prompt section:\n%s", got)
	}
	for _, bad := range []struct{ id, kind, outcome string }{
		{"", "next_action", "accepted"},
		{"p4", "", "accepted"},
		{"p4", "next_action", "applied"},
	} {
		if _, err := store.RecordProposalFeedback("home", bad.id, bad.kind, bad.outcome); err == nil {
			t.Fatalf("invalid feedback accepted: %+v", bad)
		}
	}
}

func TestRecordProposalFeedback_DedupeMemoryIsBounded(t *testing.T) {
	store := NewAssistantLearningStore(testFolderResolver{root: t.TempDir()})
	for i := 0; i < maxFeedbackProposalIDs+10; i++ {
		if _, err := store.RecordProposalFeedback("home", "p"+string(rune('a'+i%26))+string(rune('0'+i/26)), "next_action", "dismissed"); err != nil {
			t.Fatal(err)
		}
	}
	document, err := store.Read("home")
	if err != nil || len(document.FeedbackProposalIDs) != maxFeedbackProposalIDs {
		t.Fatalf("dedupe memory is unbounded: %d %v", len(document.FeedbackProposalIDs), err)
	}
}
