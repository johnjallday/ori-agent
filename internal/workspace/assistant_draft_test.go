package workspace

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func newAssistantDraftFixture(t *testing.T) (*AssistantDraftService, *TicketService, *Workspace) {
	t.Helper()
	tickets, store := newTicketTestService(t)
	hq := newTicketTestWorkspace(t, store, "My HQ")
	return NewAssistantDraftService(NewBacklogService(store)), tickets, hq
}

func draftInput(workspaceID, operation, title, body string) AssistantDraftInput {
	return AssistantDraftInput{
		WorkspaceID: workspaceID, ConversationID: "conv-1", MessageID: "msg-6", OperationID: operation,
		Title: title, Body: body, ActorID: "local",
	}
}

func allTickets(t *testing.T, tickets *TicketService, workspaceID string) []Ticket {
	t.Helper()
	page, err := tickets.Search(TicketQuery{WorkspaceID: workspaceID, Archive: TicketArchiveAll, IncludeSubtickets: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return page.Tickets
}

const koreanDraft = "미나야, 생일 정말 축하해! 🎂\n\n오늘 하루가 기쁨과 웃음으로 가득하길 바라."

// A2: one confirmed save is one Backlog Ticket holding the exact reviewed text,
// unassigned, unscheduled, and not runnable.
func TestAssistantDraft_SaveCreatesOneExactBacklogTicket(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	receipt, err := drafts.Save(draftInput(hq.ID, "op-1", "Birthday greeting for Mina", koreanDraft))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	ticket := receipt.Ticket
	if !receipt.Created || receipt.ChangedSince {
		t.Fatalf("receipt=%+v", receipt)
	}
	if ticket.Title != "Birthday greeting for Mina" || ticket.Description != koreanDraft {
		t.Fatalf("saved content changed: title=%q body=%q", ticket.Title, ticket.Description)
	}
	if ticket.State != TicketStateBacklog || ticket.Assignee != "" || ticket.ScheduleEnabled || ticket.DueDate != nil || ticket.AwaitingExecutionIntent {
		t.Fatalf("a saved draft must be unassigned, unscheduled Backlog: %+v", ticket)
	}
	if ticket.Source != TicketSourceAssistant || ticket.OwningWorkspaceID != hq.ID || ticket.Number == 0 {
		t.Fatalf("provenance/owner/number: %+v", ticket)
	}
	key, ok := ParseAssistantDraftSourceID(ticket.SourceID)
	if !ok || key.ConversationID != "conv-1" || key.MessageID != "msg-6" || key.OperationID != "op-1" {
		t.Fatalf("source key=%q parsed=%+v ok=%v", ticket.SourceID, key, ok)
	}
	if strings.Contains(ticket.SourceID, "미나") || len(ticket.SourceID) > 200 {
		t.Fatalf("source key must hold references and a digest, never content: %q", ticket.SourceID)
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 1 {
		t.Fatalf("tickets=%d; want 1", len(got))
	}
}

// A3: the same save repeated — double-click, retry, restart — is one Ticket.
func TestAssistantDraft_ReplayReturnsTheSameTicket(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	input := draftInput(hq.ID, "op-1", "Birthday greeting", koreanDraft)
	first, err := drafts.Save(input)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	// A fresh service over the same store stands in for a restart.
	restarted := NewAssistantDraftService(NewBacklogService(drafts.backlog.store))
	second, err := restarted.Save(input)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Created || second.ChangedSince || second.Ticket.ID != first.Ticket.ID || second.Ticket.Number != first.Ticket.Number {
		t.Fatalf("replay must return the first Ticket: first=%s second=%+v", first.Ticket.ID, second)
	}
	// Surrounding blank space is the canonical normalization, so it is the
	// same reviewed content and the same save.
	padded := input
	padded.Title, padded.Body = "  Birthday greeting  ", "\n"+koreanDraft+"\n\n"
	third, err := drafts.Save(padded)
	if err != nil || third.Created || third.Ticket.ID != first.Ticket.ID {
		t.Fatalf("normalized-equal replay: %+v err=%v", third, err)
	}
	found, err := drafts.Find(input)
	if err != nil || found == nil || found.Ticket.ID != first.Ticket.ID || found.Created {
		t.Fatalf("Find: %+v err=%v", found, err)
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 1 {
		t.Fatalf("tickets=%d; want 1", len(got))
	}
}

// A3: a retry after the Ticket was edited or promoted is still the known prior
// success, not a conflict and not a second Ticket.
func TestAssistantDraft_ReplayAfterLaterEditsIsStillThePriorSuccess(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	input := draftInput(hq.ID, "op-1", "Birthday greeting", koreanDraft)
	first, err := drafts.Save(input)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	edited := "Edited in the Ticket view"
	if _, err := tickets.Update(hq.ID, first.Ticket.ID, TicketUpdateInput{Description: &edited, IfVersion: first.Ticket.Version}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := tickets.Promote(hq.ID, first.Ticket.ID, 0); err != nil {
		t.Fatalf("promote: %v", err)
	}

	replay, err := drafts.Save(input)
	if err != nil {
		t.Fatalf("replay after edits: %v", err)
	}
	if replay.Created || !replay.ChangedSince || replay.Ticket.ID != first.Ticket.ID {
		t.Fatalf("replay=%+v", replay)
	}
	if replay.Ticket.Description != edited || replay.Ticket.State != TicketStateReady {
		t.Fatalf("a replay must not overwrite later edits or the lifecycle: %+v", replay.Ticket)
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 1 {
		t.Fatalf("tickets=%d; want 1", len(got))
	}
}

// A3: an operation ID belongs to the content, target, and source it committed.
func TestAssistantDraft_SameOperationWithDifferentPayloadIsRefused(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	other := newTicketTestWorkspace(t, drafts.backlog.store, "Thesis")
	input := draftInput(hq.ID, "op-1", "Birthday greeting", koreanDraft)
	if _, err := drafts.Save(input); err != nil {
		t.Fatalf("first save: %v", err)
	}

	changes := map[string]func(in *AssistantDraftInput){
		"body":    func(in *AssistantDraftInput) { in.Body = koreanDraft + " 한 줄 추가" },
		"title":   func(in *AssistantDraftInput) { in.Title = "Another title" },
		"message": func(in *AssistantDraftInput) { in.MessageID = "msg-2" },
		"thread":  func(in *AssistantDraftInput) { in.ConversationID = "conv-9" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			changed := input
			change(&changed)
			if _, err := drafts.Save(changed); !errors.Is(err, ErrAssistantDraftConflict) {
				t.Fatalf("Save err=%v; want ErrAssistantDraftConflict", err)
			}
			// The conflict names the Ticket the earlier save created.
			saved, err := drafts.Find(changed)
			if !errors.Is(err, ErrAssistantDraftConflict) || saved == nil || saved.Ticket.Description != koreanDraft {
				t.Fatalf("Find saved=%+v err=%v; want the earlier Ticket with ErrAssistantDraftConflict", saved, err)
			}
		})
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 1 || got[0].Description != koreanDraft {
		t.Fatalf("a refused payload changed HQ: %+v", got)
	}
	// The same operation ID in another workspace is a different save there; it
	// does not touch HQ, and HQ's Ticket does not answer for it.
	elsewhere := input
	elsewhere.WorkspaceID = other.ID
	if found, err := drafts.Find(elsewhere); err != nil || found != nil {
		t.Fatalf("another workspace saw HQ's save: %+v err=%v", found, err)
	}
}

func TestAssistantDraft_NewOperationIsANewTicket(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	if _, err := drafts.Save(draftInput(hq.ID, "op-1", "Greeting", koreanDraft)); err != nil {
		t.Fatal(err)
	}
	second, err := drafts.Save(draftInput(hq.ID, "op-2", "Greeting", koreanDraft))
	if err != nil || !second.Created {
		t.Fatalf("second review: %+v err=%v", second, err)
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 2 {
		t.Fatalf("tickets=%d; want 2", len(got))
	}
}

// A3 under real concurrency: many identical saves, exactly one Ticket.
func TestAssistantDraft_ConcurrentIdenticalSavesCreateOneTicket(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	input := draftInput(hq.ID, "op-1", "Greeting", koreanDraft)

	const writers = 16
	var wg sync.WaitGroup
	wg.Add(writers)
	results := make(chan *AssistantDraftReceipt, writers)
	errs := make(chan error, writers)
	for range writers {
		go func() {
			defer wg.Done()
			receipt, err := drafts.Save(input)
			results <- receipt
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save failed: %v", err)
		}
	}
	created, ids := 0, map[string]bool{}
	for receipt := range results {
		if receipt.Created {
			created++
		}
		ids[receipt.Ticket.ID] = true
	}
	if created != 1 || len(ids) != 1 {
		t.Fatalf("created=%d distinct tickets=%d; want 1 and 1", created, len(ids))
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 1 {
		t.Fatalf("tickets=%d; want 1", len(got))
	}
}

// Concurrent saves that reuse one operation ID for different content settle on
// exactly one Ticket; every loser is refused.
func TestAssistantDraft_ConcurrentConflictingSavesKeepOneTicket(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	const writers = 12
	var wg sync.WaitGroup
	wg.Add(writers)
	errs := make(chan error, writers)
	for i := range writers {
		go func(n int) {
			defer wg.Done()
			_, err := drafts.Save(draftInput(hq.ID, "op-1", "Greeting", koreanDraft+strings.Repeat("!", n)))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	succeeded, refused := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrAssistantDraftConflict):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || refused != writers-1 {
		t.Fatalf("succeeded=%d refused=%d", succeeded, refused)
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 1 {
		t.Fatalf("tickets=%d; want 1", len(got))
	}
}

func TestAssistantDraft_ValidationRejectsBeforeAnyWrite(t *testing.T) {
	drafts, tickets, hq := newAssistantDraftFixture(t)
	tooLong := strings.Repeat("가", TicketDescriptionMaxLength+1)
	cases := map[string]struct {
		input AssistantDraftInput
		field string
	}{
		"empty title":     {draftInput(hq.ID, "op-1", "   ", koreanDraft), "title"},
		"multiline title": {draftInput(hq.ID, "op-1", "line one\nline two", koreanDraft), "title"},
		"long title":      {draftInput(hq.ID, "op-1", strings.Repeat("가", TicketTitleMaxLength+1), koreanDraft), "title"},
		"empty body":      {draftInput(hq.ID, "op-1", "Greeting", " \n\t "), "description"},
		"oversized body":  {draftInput(hq.ID, "op-1", "Greeting", tooLong), "description"},
		"no operation":    {draftInput(hq.ID, "", "Greeting", koreanDraft), "source_id"},
		"unsafe reference": {AssistantDraftInput{
			WorkspaceID: hq.ID, ConversationID: "conv:1", MessageID: "msg-6", OperationID: "op-1", Title: "Greeting", Body: koreanDraft,
		}, "source_id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := drafts.Save(tc.input)
			validation, ok := IsTicketValidationError(err)
			if !ok || validation.Field != tc.field {
				t.Fatalf("err=%v; want validation error on %q", err, tc.field)
			}
		})
	}
	if got := allTickets(t, tickets, hq.ID); len(got) != 0 {
		t.Fatalf("a rejected save wrote %d ticket(s)", len(got))
	}
	// The largest allowed body is saved whole, never truncated.
	exact := strings.Repeat("가", TicketDescriptionMaxLength)
	receipt, err := drafts.Save(draftInput(hq.ID, "op-max", "Greeting", exact))
	if err != nil || utf8.RuneCountInString(receipt.Ticket.Description) != TicketDescriptionMaxLength {
		t.Fatalf("max-size body: err=%v", err)
	}
}

// Markup in a draft is stored as written; the Ticket surfaces escape at render.
func TestAssistantDraft_HostileMarkupIsStoredVerbatim(t *testing.T) {
	drafts, _, hq := newAssistantDraftFixture(t)
	body := `<script>alert("x")</script> <img src=x onerror=alert(1)> [link](javascript:alert(1))`
	receipt, err := drafts.Save(draftInput(hq.ID, "op-1", `<b>Greeting</b>`, body))
	if err != nil || receipt.Ticket.Description != body || receipt.Ticket.Title != `<b>Greeting</b>` {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestNormalizeAssistantDraft_ReportsOnlyTrimming(t *testing.T) {
	title, body, changed, err := NormalizeAssistantDraft("Greeting", koreanDraft)
	if err != nil || changed || title != "Greeting" || body != koreanDraft {
		t.Fatalf("clean draft: %q %q %v %v", title, body, changed, err)
	}
	title, body, changed, err = NormalizeAssistantDraft(" Greeting ", "\n"+koreanDraft+"  \n")
	if err != nil || !changed || title != "Greeting" || body != koreanDraft {
		t.Fatalf("padded draft: %q %q %v %v", title, body, changed, err)
	}
}

func TestSuggestAssistantDraftTitle(t *testing.T) {
	for body, want := range map[string]string{
		koreanDraft:                           "미나야, 생일 정말 축하해!",
		"\n\n# Birthday note\nbody":           "Birthday note",
		"- first bullet\n- second":            "first bullet",
		"> quoted   opening   line":           "quoted opening line",
		"   \n\t":                             "Draft",
		strings.Repeat("가", 200):              strings.Repeat("가", assistantDraftTitleChars) + "…",
		"Happy birthday, Mina!\nSecond line.": "Happy birthday, Mina!",
		// The title is the first sentence, not the whole one-line draft.
		"Happy birthday, Mina! Wishing you a wonderful day filled with joy.": "Happy birthday, Mina!",
		"생일 축하해, 미나! 기쁨과 웃음으로 가득한 하루 보내길 바라. 💛":                              "생일 축하해, 미나!",
		// A short abbreviation is not a sentence; a decimal is not a sentence end.
		"Dr. Smith is here. Thanks for coming.": "Dr. Smith is here.",
		"Version 2.5 ships today":               "Version 2.5 ships today",
	} {
		got := SuggestAssistantDraftTitle(body)
		if got != want || !utf8.ValidString(got) || strings.ContainsAny(got, "\r\n") {
			t.Errorf("SuggestAssistantDraftTitle(%.20q) = %q; want %q", body, got, want)
		}
		if _, err := NormalizeTicketTitle(got); err != nil {
			t.Errorf("suggested title %q is not a valid Ticket title: %v", got, err)
		}
	}
}

func TestParseAssistantDraftSourceID(t *testing.T) {
	key := AssistantDraftKey{ConversationID: "c0ffee-1", MessageID: "m_2", OperationID: "op-3", Digest: strings.Repeat("a", assistantDraftDigestLength)}
	parsed, ok := ParseAssistantDraftSourceID(key.SourceID())
	if !ok || parsed != key {
		t.Fatalf("round trip: %+v ok=%v", parsed, ok)
	}
	for _, bad := range []string{
		"", "assistant-handoff-abc", "assistant-draft:c:m:op", "assistant-draft:c:m:op:short",
		"other:c:m:op:" + strings.Repeat("a", assistantDraftDigestLength),
		"assistant-draft:c/../x:m:op:" + strings.Repeat("a", assistantDraftDigestLength),
		"assistant-draft::m:op:" + strings.Repeat("a", assistantDraftDigestLength),
	} {
		if _, ok := ParseAssistantDraftSourceID(bad); ok {
			t.Errorf("ParseAssistantDraftSourceID(%q) accepted a malformed key", bad)
		}
	}
}
