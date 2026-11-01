package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFirstBatchLearnedSentence_SaysHowManyAndWhereTheyGo(t *testing.T) {
	for learned, want := range map[int]string{
		0:  "",
		-1: "",
		1:  "I learned 1 thing from this folder; approve it and it goes into your second brain.",
		3:  "I learned 3 things from this folder; approve them and they go into your second brain.",
	} {
		if got := firstBatchLearnedSentence(learned); got != want {
			t.Errorf("firstBatchLearnedSentence(%d) = %q, want %q", learned, got, want)
		}
	}
}

func TestWithLearnedSentence_OnlyAReadyBatchGainsTheLineAndTheAction(t *testing.T) {
	ready := TodayItem{Kind: TodayKindFirstBatchReport, State: "finished", Title: "A first batch.", Actions: []string{FirstBatchActionReview, FirstLookActionNotNow}}
	protected := TodayItem{Kind: TodayKindFirstBatchReport, State: "finished", Actions: []string{FirstBatchActionProtection, FirstBatchActionReview, FirstLookActionNotNow}}
	empty := TodayItem{Kind: TodayKindFirstBatchReport, State: "finished", Actions: []string{FirstBatchActionOpen, FirstLookActionNotNow}}
	failed := TodayItem{Kind: TodayKindFirstBatchReport, State: "failed", Actions: []string{FirstBatchActionRetry}}
	other := TodayItem{Kind: "folder_first_look", State: "finished", Actions: []string{FirstBatchActionReview, FirstLookActionNotNow}}
	in := []TodayItem{ready, protected, empty, failed, other}

	out := withLearnedSentence(in, 2)
	sentence := "I learned 2 things from this folder; approve them and they go into your second brain."
	if out[0].Detail != sentence || strings.Join(out[0].Actions, ",") != "review_batch,review_learned,not_now" {
		t.Errorf("ready = %+v", out[0])
	}
	if out[1].Detail != sentence || strings.Join(out[1].Actions, ",") != "check_protection,review_batch,review_learned,not_now" {
		t.Errorf("protected = %+v", out[1])
	}
	for index, name := range []string{"empty", "failed", "another kind"} {
		if got := out[index+2]; got.Detail != "" || strings.Contains(strings.Join(got.Actions, ","), FirstBatchActionLearned) {
			t.Errorf("%s changed: %+v", name, got)
		}
	}
	// The input is not edited, and nothing learned leaves the report as it was.
	if in[0].Detail != "" || len(in[0].Actions) != 2 {
		t.Errorf("the input was edited: %+v", in[0])
	}
	if same := withLearnedSentence(in, 0); same[0].Detail != "" || len(same[0].Actions) != 2 {
		t.Errorf("nothing learned changed the report: %+v", same[0])
	}
}

func TestSetupLearning_ListsOnlyWhatTheBeatRaisedAndCountsIt(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	learning := NewSetupLearningService(svc)
	if n := learning.Count(ctx, "local"); n != 0 {
		t.Fatalf("an empty queue counts %d", n)
	}
	// Two from the files, one filing preference from the Janitor, and others that
	// are not this beat's: a saved-app suggestion and a workspace fact.
	for _, name := range []string{"Chase", "Dana Lee"} {
		if _, _, err := svc.Propose(ctx, "local", setupBatchProposal(name)); err != nil {
			t.Fatal(err)
		}
	}
	janitor := testProposal("documents", "You may prefer filing documents in your approved File Janitor folder.")
	janitor.SourceKind = "file_janitor"
	for index := range janitor.Evidence {
		janitor.Evidence[index].SourceKind = "file_janitor"
	}
	if _, _, err := svc.Propose(ctx, "local", janitor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Propose(ctx, "local", testProposal("ableton", "Maybe you use Ableton.")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "A workspace fact.")); err != nil {
		t.Fatal(err)
	}

	view, err := learning.List(ctx, "local")
	if err != nil || view.Total != 3 || len(view.Items) != 3 {
		t.Fatalf("view = %+v, %v", view, err)
	}
	bySource := map[string]int{}
	for _, item := range view.Items {
		bySource[item.Source]++
		if item.ID == "" || item.Version < 1 || item.Text == "" || strings.ContainsAny(item.Text, "\r\n") {
			t.Errorf("item = %+v", item)
		}
	}
	if bySource["files"] != 2 || bySource["filing"] != 1 {
		t.Fatalf("by source = %v", bySource)
	}
	for _, item := range view.Items {
		if item.Source == "files" && (len(item.Evidence) != 2 || !strings.HasPrefix(item.Evidence[0], "User-approved filing of a file whose name includes ")) {
			t.Errorf("evidence = %v", item.Evidence)
		}
	}
	if n := learning.Count(ctx, "local"); n != 3 {
		t.Fatalf("count = %d", n)
	}
	var nilService *SetupLearningService
	if _, err := nilService.List(ctx, "local"); !errors.Is(err, ErrRepairNeeded) {
		t.Fatalf("unwired list = %v", err)
	}
	if nilService.Count(ctx, "local") != 0 {
		t.Fatal("unwired count was not zero")
	}
}

func TestSetupLearning_DismissTombstonesWhatIsLeftAndKeepsWhatWasAnswered(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	learning := NewSetupLearningService(svc)
	var items []KnowledgeItem
	for _, name := range []string{"Chase", "Dana Lee", "Acme"} {
		item, _, err := svc.Propose(ctx, "local", setupBatchProposal(name))
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	// One was rejected from the Profile page before the card was dismissed.
	if _, err := svc.SuppressCandidate(ctx, "local", items[0].ID, items[0].Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}

	if _, err := learning.Dismiss(ctx, "local", "has a space"); !errors.Is(err, ErrValidation) {
		t.Fatalf("a bad request id = %v", err)
	}
	count, err := learning.Dismiss(ctx, "local", "dismiss-1")
	if err != nil || count != 2 {
		t.Fatalf("dismissed %d, %v", count, err)
	}
	if n := learning.Count(ctx, "local"); n != 0 {
		t.Fatalf("%d learnings stayed in the queue", n)
	}
	// Tombstoned: the producer proposing the same meaning again is refused.
	for _, name := range []string{"Chase", "Dana Lee", "Acme"} {
		if _, created, err := svc.Propose(ctx, "local", setupBatchProposal(name)); !errors.Is(err, ErrKnowledgeSuppressed) || created {
			t.Errorf("%s came back: created=%v err=%v", name, created, err)
		}
	}
	// A second dismissal finds nothing to do.
	if again, err := learning.Dismiss(ctx, "local", "dismiss-2"); err != nil || again != 0 {
		t.Fatalf("second dismissal = %d, %v", again, err)
	}
}
