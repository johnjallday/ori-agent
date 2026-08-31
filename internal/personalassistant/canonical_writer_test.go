package personalassistant

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCanonicalWriter_CreateTicketUsesCanonicalIdempotentPath(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	if err := store.Save(hq); err != nil {
		t.Fatal(err)
	}
	writer := NewCanonicalWriter(workspace.NewTicketService(store))
	item := AssignmentPreviewItem{
		ID: "assignment-item-1", RecordType: AssignmentRecordTicket,
		Category: "priority", State: "ready", Title: "Prepare tax documents",
		Due: "2026-09-15",
	}

	first, created, err := writer.CreateTicket(hq.ID, "assistant-1", "preview-1", item)
	if err != nil || !created {
		t.Fatalf("first CreateTicket = %#v created=%t err=%v", first, created, err)
	}
	second, created, err := writer.CreateTicket(hq.ID, "assistant-1", "preview-1", item)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("retry CreateTicket = %#v created=%t err=%v", second, created, err)
	}
	ticket, err := workspace.NewTicketService(store).Get(hq.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.State != workspace.TicketStateReady || !ticket.AwaitingExecutionIntent ||
		ticket.Source != workspace.TicketSourceAssistant || ticket.SourceID != assignmentTicketSourcePrefix+"assistant-1:preview-1:"+item.ID ||
		ticket.DueDate == nil || ticket.DueDate.Format("2006-01-02") != "2026-09-15" {
		t.Fatalf("canonical ticket = %#v", ticket)
	}
	if len(ticket.StateHistory) != 1 || ticket.StateHistory[0].Actor != workspace.TicketActorAssistant ||
		ticket.StateHistory[0].ActorID != "assistant-1" {
		t.Fatalf("ticket history = %#v", ticket.StateHistory)
	}
}

func TestCanonicalWriter_CreateTicketPreservesBacklogSafety(t *testing.T) {
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	if err := store.Save(hq); err != nil {
		t.Fatal(err)
	}
	writer := NewCanonicalWriter(workspace.NewTicketService(store))
	ref, created, err := writer.CreateTicket(hq.ID, "assistant-1", "preview-1", AssignmentPreviewItem{
		ID: "backlog-item", RecordType: AssignmentRecordTicket,
		Category: "priority", State: "backlog", Title: "Someday item",
	})
	if err != nil || !created {
		t.Fatalf("CreateTicket = %#v created=%t err=%v", ref, created, err)
	}
	ticket, err := workspace.NewTicketService(store).Get(hq.ID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.State != workspace.TicketStateBacklog || ticket.Assignee != "" || ticket.ScheduleEnabled || ticket.CurrentRunID != "" {
		t.Fatalf("unsafe backlog ticket = %#v", ticket)
	}
}

func TestCanonicalWriter_CreateTicketRejectsInvalidProjectionBeforeWrite(t *testing.T) {
	fake := &countingTicketStore{}
	writer := NewCanonicalWriter(fake)
	_, _, err := writer.CreateTicket("hq", "assistant", "preview", AssignmentPreviewItem{
		ID: "bad", RecordType: AssignmentRecordTicket, State: "in_progress", Title: "Bad",
	})
	if err == nil || fake.calls != 0 {
		t.Fatalf("invalid state err=%v calls=%d", err, fake.calls)
	}
}

func TestCanonicalWriter_CreateFollowUpMapsAndDeduplicatesCanonicalCategories(t *testing.T) {
	ctx := context.Background()
	_, db := newTestStore(t)
	service := followup.NewService(followup.NewSQLiteStore(db))
	writer := NewCanonicalWriter(nil)
	writer.SetFollowUpService(service)
	tests := []struct {
		name      string
		category  string
		direction followup.Direction
	}{
		{"i owe", "i_owe", followup.DirectionOutbound},
		{"waiting on", "waiting_on", followup.DirectionInbound},
		{"decision", "needs_decision", followup.DirectionNone},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := AssignmentPreviewItem{
				ID: "item-" + test.category, RecordType: AssignmentRecordFollowUp,
				Category: test.category, State: "active", Title: "Explicit loop",
				Detail: "Keep exact detail", Counterparty: "Maya", Due: "2026-10-02",
			}
			ref, err := writer.CreateFollowUp(ctx, "user-a", "hq-a", "assistant-a", "preview-a", item)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := writer.CreateFollowUp(ctx, "user-a", "hq-a", "assistant-a", "preview-a", item)
			if err != nil || replayed.ID != ref.ID {
				t.Fatalf("retry = %#v, %v", replayed, err)
			}
			stored, err := service.Get(ctx, "user-a", ref.ID)
			if err != nil {
				t.Fatal(err)
			}
			if string(stored.Category) != test.category || stored.Direction != test.direction ||
				stored.Provenance != followup.ProvenanceExplicit || stored.Status != followup.StatusActive ||
				stored.Counterparty != "Maya" || stored.DueAt == nil || stored.DueAt.Format("2006-01-02") != "2026-10-02" {
				t.Fatalf("mapped follow-up = %#v", stored)
			}
			if index == 0 {
				closed, err := service.Complete(ctx, "user-a", ref.ID)
				if err != nil {
					t.Fatal(err)
				}
				item.Title = "Retry must not resurrect or rewrite"
				replayed, err = writer.CreateFollowUp(ctx, "user-a", "hq-a", "assistant-a", "preview-a", item)
				if err != nil || replayed.ID != ref.ID {
					t.Fatalf("closed retry = %#v, %v", replayed, err)
				}
				after, err := service.Get(ctx, "user-a", ref.ID)
				if err != nil || after.Status != followup.StatusCompleted || after.Title != closed.Title {
					t.Fatalf("closed item resurrected: %#v, %v", after, err)
				}
			}
		})
	}
}

func TestCanonicalWriter_CreateFollowUpScopesDedupByUserAndWorkspace(t *testing.T) {
	ctx := context.Background()
	_, db := newTestStore(t)
	service := followup.NewService(followup.NewSQLiteStore(db))
	writer := NewCanonicalWriter(nil)
	writer.SetFollowUpService(service)
	item := AssignmentPreviewItem{
		ID: "same-item", RecordType: AssignmentRecordFollowUp,
		Category: "waiting_on", State: "active", Title: "Scoped loop",
	}
	first, err := writer.CreateFollowUp(ctx, "user-a", "hq-a", "assistant-a", "preview", item)
	if err != nil {
		t.Fatal(err)
	}
	otherWorkspace, err := writer.CreateFollowUp(ctx, "user-a", "hq-b", "assistant-a", "preview", item)
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := writer.CreateFollowUp(ctx, "user-b", "hq-a", "assistant-a", "preview", item)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == otherWorkspace.ID || first.ID == otherUser.ID || otherWorkspace.ID == otherUser.ID {
		t.Fatalf("scoped refs collided: %#v %#v %#v", first, otherWorkspace, otherUser)
	}
}

type countingTicketStore struct{ calls int }

func (f *countingTicketStore) CreateIdempotent(workspace.TicketCreateInput) (*workspace.Ticket, bool, error) {
	f.calls++
	return &workspace.Ticket{ID: "unexpected"}, true, nil
}
