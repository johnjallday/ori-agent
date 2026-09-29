package projectlibrary

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func chatAuthority(f *runFixture) ManagerAuthority {
	return ManagerAuthority{HomeID: f.scope.HomeID, AgentInstanceID: "local-manager", AgentName: "Manager"}
}

func chatNextAction(t *testing.T, f *runFixture, entryID, key string) ManagerProposal {
	t.Helper()
	entry := sessionEntry(f.doc(t), entryID)
	proposal, _, err := f.library.ProposeNextAction(chatAuthority(f), entryID, entry.Fields.Revision,
		"Listen through "+entryID, "", key)
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func TestDismiss_MarksOnlyTheSuggestionAndReplaysExactly(t *testing.T) {
	f := newRunFixture(t)
	proposal := chatNextAction(t, f, "single", "chat-single")
	before := f.doc(t)
	dismissed, replay, err := f.library.DismissManagerProposal(f.scope, proposal.ID, "dismiss-1")
	if err != nil || replay || dismissed.DismissedAt == nil || dismissed.ID != proposal.ID {
		t.Fatalf("dismiss: %+v %v %v", dismissed, replay, err)
	}
	after := f.doc(t)
	if len(after.Dismissals) != 1 || after.Dismissals[0].ProposalID != proposal.ID || after.Dismissals[0].EntryID != "single" ||
		after.Dismissals[0].Kind != "" {
		t.Fatalf("dismissal memory: %+v", after.Dismissals)
	}
	if !reflect.DeepEqual(sessionEntry(after, "single").Fields, sessionEntry(before, "single").Fields) ||
		len(after.Entries) != len(before.Entries) || len(after.Sessions) != len(before.Sessions) ||
		len(after.Roots) != len(before.Roots) || len(after.Proposals) != len(before.Proposals) {
		t.Fatal("dismissal changed more than the suggestion")
	}
	page, err := f.library.ListManagerProposals(f.scope)
	if err != nil || page.Rows[0].Status != "dismissed" {
		t.Fatalf("dismissed suggestion still offered: %+v %v", page, err)
	}
	if summary, err := f.library.Summary(f.scope); err != nil || summary.ReadyProposals != 0 {
		t.Fatalf("dismissed suggestion still counted: %+v %v", summary, err)
	}
	if again, replay, err := f.library.DismissManagerProposal(f.scope, proposal.ID, "dismiss-1"); err != nil || !replay ||
		again.DismissedAt == nil || !again.DismissedAt.Equal(*dismissed.DismissedAt) {
		t.Fatalf("exact retry: %+v %v %v", again, replay, err)
	}
	if _, _, err := f.library.DismissManagerProposal(f.scope, proposal.ID, "dismiss-2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a second dismissal under a new key: %v", err)
	}
	if _, _, err := f.library.DismissManagerProposal(f.scope, "no-such-suggestion", "dismiss-3"); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("unknown suggestion: %v", err)
	}
	if _, _, err := f.library.DismissManagerProposal(f.scope, proposal.ID, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing idempotency key: %v", err)
	}
}

func TestDismiss_AllowedAfterProviderLossButNotAfterConfirmation(t *testing.T) {
	f := newRunFixture(t)
	orphaned := chatNextAction(t, f, "single", "chat-orphaned")
	confirmed := chatNextAction(t, f, "alternates", "chat-confirmed")
	review, err := f.library.ReviewProposedNextAction(f.scope, confirmed.ID, f.scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.library.CommitProposedNextAction(f.scope, confirmed.ID, review.Token, "confirm-alternates",
		f.scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.library.DismissManagerProposal(f.scope, confirmed.ID, "dismiss-confirmed"); !errors.Is(err, ErrConflict) {
		t.Fatalf("an already confirmed suggestion was dismissed: %v", err)
	}
	f.available.Store(false)
	page, err := f.library.ListManagerProposals(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Rows {
		if row.Proposal.ID == orphaned.ID && row.Status != "unavailable" {
			t.Fatalf("provider loss did not leave the suggestion unavailable: %+v", row)
		}
	}
	if _, _, err := f.library.DismissManagerProposal(f.scope, orphaned.ID, "dismiss-orphaned"); err != nil {
		t.Fatalf("dismissal is reductive and must survive provider loss: %v", err)
	}
}

func TestDismiss_ASecondScanDoesNotReproposeWithinSevenDays(t *testing.T) {
	f := newRunFixture(t)
	proposal := chatNextAction(t, f, "single", "chat-first")
	if _, _, err := f.library.DismissManagerProposal(f.scope, proposal.ID, "dismiss-first"); err != nil {
		t.Fatal(err)
	}
	doc := f.doc(t)
	second := scanRoot(t, f.roots, f.scope, doc.Roots[0].ID, "second-scan")
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
			return callTools(toolCall("1", "home_library_propose_next_action",
				nextActionArgs(t, f, "single", "scan-review-again")))(ctx, request)
		},
	}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, second.ID)
	if err != nil || run.Status != runFinished || run.Proposals != 0 {
		t.Fatalf("second scan receipt: %+v %v", run, err)
	}
	if reply := f.chat.requests[1].Messages[2].Content; !strings.Contains(reply, "dismissed a matching suggestion recently") {
		t.Fatalf("the model was not told why: %s", reply)
	}
	if got := runProposals(f.doc(t), second.ID); len(got) != 0 {
		t.Fatalf("a recently dismissed pair was proposed again: %+v", got)
	}
	// The memory lasts seven days, for this exact entry and kind only.
	current := f.doc(t)
	dismissedAt := current.Dismissals[0].At
	if !recentlyDismissed(current, "single", "", dismissedAt.Add(6*24*time.Hour)) {
		t.Fatal("dismissal memory ended before seven days")
	}
	if recentlyDismissed(current, "single", "", dismissedAt.Add(8*24*time.Hour)) {
		t.Fatal("dismissal memory outlived seven days")
	}
	if recentlyDismissed(current, "single", "project_review", dismissedAt) ||
		recentlyDismissed(current, "alternates", "", dismissedAt) {
		t.Fatal("dismissal memory spread to another entry or kind")
	}
}

func TestDismiss_DocumentRejectsForgedDismissals(t *testing.T) {
	f := newRunFixture(t)
	proposal := chatNextAction(t, f, "single", "chat-forge")
	if _, _, err := f.library.DismissManagerProposal(f.scope, proposal.ID, "dismiss-forge"); err != nil {
		t.Fatal(err)
	}
	doc := f.doc(t)
	for name, forge := range map[string]func(*Document){
		"unknown kind":     func(d *Document) { d.Dismissals[0].Kind = "activate_now" },
		"missing proposal": func(d *Document) { d.Dismissals[0].ProposalID = "" },
		"missing time":     func(d *Document) { d.Dismissals[0].At = time.Time{} },
		"dismissed before created": func(d *Document) {
			early := d.Proposals[0].CreatedAt.Add(-time.Hour)
			d.Proposals[0].DismissedAt = &early
		},
	} {
		forged := doc
		forged.Dismissals = append([]ProposalDismissal(nil), doc.Dismissals...)
		forged.Proposals = append([]ManagerProposal(nil), doc.Proposals...)
		forge(&forged)
		if forged.valid(f.scope) {
			t.Fatalf("%s: forged dismissal accepted", name)
		}
	}
}
