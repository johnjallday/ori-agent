package workspace

import (
	"errors"
	"sync"
	"testing"
)

func saveDraftForUpdate(t *testing.T) (*AssistantDraftService, *TicketService, *Workspace, Ticket) {
	t.Helper()
	drafts, tickets, hq := newAssistantDraftFixture(t)
	receipt, err := drafts.Save(draftInput(hq.ID, "op-1", "Birthday greeting", koreanDraft))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return drafts, tickets, hq, receipt.Ticket
}

const revisedDraft = "미나야, 생일 축하해! 🎂 올해도 건강하고 행복하길."

// A4: an approved update changes the same Ticket's title and body and nothing
// else.
func TestAssistantDraftUpdate_ChangesOnlyTitleAndBody(t *testing.T) {
	drafts, tickets, hq, saved := saveDraftForUpdate(t)
	receipt, err := drafts.Update(AssistantDraftUpdateInput{
		WorkspaceID: hq.ID, TicketID: saved.ID, IfVersion: saved.Version, Title: " Birthday greeting (shorter) ", Body: "\n" + revisedDraft + "\n",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated := receipt.Ticket
	if !receipt.Applied || updated.ID != saved.ID || updated.Number != saved.Number {
		t.Fatalf("update must land on the same Ticket: %+v", receipt)
	}
	if updated.Title != "Birthday greeting (shorter)" || updated.Description != revisedDraft || updated.Version != saved.Version+1 {
		t.Fatalf("updated content/version: %+v", updated)
	}
	if updated.State != saved.State || updated.Assignee != "" || updated.ScheduleEnabled || updated.DueDate != nil ||
		updated.Source != saved.Source || updated.SourceID != saved.SourceID || updated.OwningWorkspaceID != saved.OwningWorkspaceID ||
		updated.Priority != saved.Priority || len(updated.StateHistory) != len(saved.StateHistory) {
		t.Fatalf("update changed something other than title and body:\nbefore=%+v\nafter=%+v", saved, updated)
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 1 {
		t.Fatalf("tickets=%d; want 1", len(got))
	}
}

// A4: an outside edit makes the review stale. The update is refused with the
// current Ticket attached, and nothing is overwritten.
func TestAssistantDraftUpdate_StaleReviewIsRefusedWithTheCurrentTicket(t *testing.T) {
	drafts, tickets, hq, saved := saveDraftForUpdate(t)
	outside := "Edited in the Ticket view"
	if _, err := tickets.Update(hq.ID, saved.ID, TicketUpdateInput{Description: &outside, IfVersion: saved.Version}); err != nil {
		t.Fatalf("outside edit: %v", err)
	}
	_, err := drafts.Update(AssistantDraftUpdateInput{
		WorkspaceID: hq.ID, TicketID: saved.ID, IfVersion: saved.Version, Title: saved.Title, Body: revisedDraft,
	})
	var changed *AssistantDraftChangedError
	if !errors.As(err, &changed) || !errors.Is(err, ErrTicketVersionConflict) {
		t.Fatalf("err=%v; want AssistantDraftChangedError", err)
	}
	if changed.Current.Description != outside || changed.Current.Version != saved.Version+1 {
		t.Fatalf("conflict must carry the current Ticket: %+v", changed.Current)
	}
	link, err := drafts.Get(hq.ID, saved.ID)
	if err != nil || link.Ticket.Description != outside {
		t.Fatalf("a stale update overwrote the outside edit: %+v err=%v", link, err)
	}
}

// A4/3.5: a retry of an update that already landed is recognized from the
// Ticket itself and applies nothing twice. A later outside change is not
// mistaken for it.
func TestAssistantDraftUpdate_RetryAfterLostResponseIsNotASecondWrite(t *testing.T) {
	drafts, tickets, hq, saved := saveDraftForUpdate(t)
	input := AssistantDraftUpdateInput{WorkspaceID: hq.ID, TicketID: saved.ID, IfVersion: saved.Version, Title: saved.Title, Body: revisedDraft}
	first, err := drafts.Update(input)
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	retry, err := drafts.Update(input)
	if err != nil || retry.Applied || retry.Ticket.Version != first.Ticket.Version || retry.Ticket.Description != revisedDraft {
		t.Fatalf("retry: %+v err=%v", retry, err)
	}

	// The same text again, but after someone else changed and restored it: the
	// version no longer matches "the next one", so it is a conflict to review.
	other := "Changed elsewhere"
	if _, err := tickets.Update(hq.ID, saved.ID, TicketUpdateInput{Description: &other}); err != nil {
		t.Fatal(err)
	}
	restored := revisedDraft
	if _, err := tickets.Update(hq.ID, saved.ID, TicketUpdateInput{Description: &restored}); err != nil {
		t.Fatal(err)
	}
	var changed *AssistantDraftChangedError
	if _, err := drafts.Update(input); !errors.As(err, &changed) {
		t.Fatalf("a much later retry must be reviewed again, got %v", err)
	}
}

func TestAssistantDraftUpdate_DeletedTicketIsNotRecreated(t *testing.T) {
	drafts, tickets, hq, saved := saveDraftForUpdate(t)
	if err := tickets.Delete(hq.ID, saved.ID, 0); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := drafts.Update(AssistantDraftUpdateInput{WorkspaceID: hq.ID, TicketID: saved.ID, IfVersion: saved.Version, Title: saved.Title, Body: revisedDraft})
	if !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("err=%v; want ErrTicketNotFound", err)
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 0 {
		t.Fatalf("an update restored a deleted Ticket: %+v", got)
	}
}

// 3.4: a lifecycle change is a version change, so an old review is stale; and a
// draft whose work has started or closed is not updated from a conversation at
// all, even with a fresh version.
func TestAssistantDraftUpdate_RespectsTheTicketLifecycle(t *testing.T) {
	drafts, tickets, hq, saved := saveDraftForUpdate(t)
	update := func(version int64) error {
		_, err := drafts.Update(AssistantDraftUpdateInput{WorkspaceID: hq.ID, TicketID: saved.ID, IfVersion: version, Title: saved.Title, Body: revisedDraft})
		return err
	}

	ready, err := tickets.Promote(hq.ID, saved.ID, 0)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	var changed *AssistantDraftChangedError
	if err := update(saved.Version); !errors.As(err, &changed) || changed.Current.State != TicketStateReady {
		t.Fatalf("a review from before the promotion must be stale: %v", err)
	}
	// Ready work that has not started may still be revised after a fresh review.
	if err := update(ready.Version); err != nil {
		t.Fatalf("update of a Ready draft with its current version: %v", err)
	}
	link, _ := drafts.Get(hq.ID, saved.ID)
	if link.Ticket.State != TicketStateReady {
		t.Fatalf("update changed the lifecycle: %s", link.Ticket.State)
	}

	for _, to := range []TicketState{TicketStateInProgress, TicketStateReview, TicketStateDone} {
		moved, err := tickets.Transition(hq.ID, saved.ID, TicketTransitionInput{To: to, Actor: TicketActorUser})
		if err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
		if err := update(moved.Version); !errors.Is(err, ErrAssistantDraftNotEditable) {
			t.Fatalf("%s: err=%v; want ErrAssistantDraftNotEditable", to, err)
		}
		after, _ := drafts.Get(hq.ID, saved.ID)
		if after.Ticket.State != to || after.Ticket.Version != moved.Version {
			t.Fatalf("%s: a refused update changed the Ticket: %+v", to, after.Ticket)
		}
	}
}

func TestAssistantDraftUpdate_RequiresAVersionAndASavedDraft(t *testing.T) {
	drafts, tickets, hq, saved := saveDraftForUpdate(t)
	_, err := drafts.Update(AssistantDraftUpdateInput{WorkspaceID: hq.ID, TicketID: saved.ID, IfVersion: 0, Title: saved.Title, Body: revisedDraft})
	if validation, ok := IsTicketValidationError(err); !ok || validation.Field != "version" {
		t.Fatalf("an update with no version must be refused, got %v", err)
	}

	// An ordinary Ticket is not a saved draft: this path can neither read nor
	// edit it, even with its real ID and version.
	ordinary := mustCreateTicket(t, tickets, TicketCreateInput{WorkspaceID: hq.ID, State: TicketStateBacklog, Title: "Pay rent"})
	_, err = drafts.Update(AssistantDraftUpdateInput{WorkspaceID: hq.ID, TicketID: ordinary.ID, IfVersion: ordinary.Version, Title: "x", Body: "y"})
	if !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("ordinary Ticket: err=%v; want ErrTicketNotFound", err)
	}
	if _, err := drafts.Get(hq.ID, ordinary.ID); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("Get of an ordinary Ticket: %v", err)
	}
	// A saved draft in another workspace is not found from this one.
	other := newTicketTestWorkspace(t, drafts.backlog.store, "Thesis")
	if _, err := drafts.Get(other.ID, saved.ID); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("cross-workspace Get: %v", err)
	}
	after, _ := tickets.Get(hq.ID, ordinary.ID)
	if after.Title != "Pay rent" || after.Version != ordinary.Version {
		t.Fatalf("ordinary Ticket was changed: %+v", after)
	}
}

// Many updates reviewed against the same version: exactly one applies.
func TestAssistantDraftUpdate_ConcurrentUpdatesExactlyOneWins(t *testing.T) {
	drafts, _, hq, saved := saveDraftForUpdate(t)
	const writers = 12
	var wg sync.WaitGroup
	wg.Add(writers)
	results := make(chan error, writers)
	bodies := make([]string, writers)
	for i := range writers {
		bodies[i] = revisedDraft + string(rune('A'+i))
		go func(body string) {
			defer wg.Done()
			_, err := drafts.Update(AssistantDraftUpdateInput{WorkspaceID: hq.ID, TicketID: saved.ID, IfVersion: saved.Version, Title: saved.Title, Body: body})
			results <- err
		}(bodies[i])
	}
	wg.Wait()
	close(results)
	applied := 0
	for err := range results {
		var changed *AssistantDraftChangedError
		switch {
		case err == nil:
			applied++
		case errors.As(err, &changed):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	link, err := drafts.Get(hq.ID, saved.ID)
	if err != nil || applied != 1 || link.Ticket.Version != saved.Version+1 {
		t.Fatalf("applied=%d version=%d (saved %d) err=%v", applied, link.Ticket.Version, saved.Version, err)
	}
}

func TestAssistantDraft_ListByConversation(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	mustSave := func(conversation, message, operation string) {
		t.Helper()
		input := draftInput(hq.ID, operation, "Greeting "+operation, koreanDraft)
		input.ConversationID, input.MessageID = conversation, message
		if _, err := drafts.Save(input); err != nil {
			t.Fatalf("save %s: %v", operation, err)
		}
	}
	mustSave("conv-1", "msg-2", "op-1")
	mustSave("conv-1", "msg-4", "op-2")
	mustSave("conv-2", "msg-2", "op-3")
	mustCreateTicket(t, tickets, TicketCreateInput{WorkspaceID: hq.ID, State: TicketStateBacklog, Title: "Not a draft", Source: TicketSourceAssistant, SourceID: "assistant-handoff-abc"})

	links, err := drafts.ListByConversation(hq.ID, "conv-1")
	if err != nil || len(links) != 2 || links[0].Key.MessageID != "msg-2" || links[1].Key.MessageID != "msg-4" {
		t.Fatalf("links=%+v err=%v", links, err)
	}
	if none, err := drafts.ListByConversation(hq.ID, "conv-9"); err != nil || len(none) != 0 {
		t.Fatalf("unknown conversation: %+v err=%v", none, err)
	}
}
