package blueprintintake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type proposalFolder struct{ root string }

func (f proposalFolder) GetFolderPath(string) (string, error) { return f.root, nil }

type recordingTicketCreator struct {
	calls  []workspace.TicketCreateInput
	failID string
}

func (c *recordingTicketCreator) CreateIdempotent(input workspace.TicketCreateInput) (*workspace.Ticket, bool, error) {
	c.calls = append(c.calls, input)
	if input.SourceID == c.failID {
		return nil, false, errors.New("ticket refused")
	}
	return &workspace.Ticket{ID: "ticket-" + input.SourceID}, true, nil
}

type recordingMemoryWriter struct{ entries []workspace.MemoryEntry }

func (w *recordingMemoryWriter) AppendUnique(_ string, entry workspace.MemoryEntry) (bool, error) {
	w.entries = append(w.entries, entry)
	return true, nil
}

type recordingNoteWriter struct{ notes []*session.WorkspaceNote }

func (w *recordingNoteWriter) CreateNote(_ context.Context, note *session.WorkspaceNote) error {
	w.notes = append(w.notes, note)
	return nil
}
func (w *recordingNoteWriter) GetNote(context.Context, string) (*session.WorkspaceNote, error) {
	return nil, session.ErrNoteNotFound
}

type recordingCalendarWriter struct {
	available bool
	events    []CalendarEventInput
	failTitle string
}

func (w *recordingCalendarWriter) Availability(context.Context, string) (bool, string, error) {
	return w.available, "Connect a calendar in this workspace to add these.", nil
}
func (w *recordingCalendarWriter) Create(_ context.Context, _ string, event CalendarEventInput) (string, error) {
	w.events = append(w.events, event)
	if event.Title == w.failTitle {
		return "", errors.New("calendar refused")
	}
	return "calendar-1", nil
}

func pendingProposal(t *testing.T, store *ProposalStore) Proposal {
	t.Helper()
	proposal, err := finalizeProposal("ws", "materials", []ProposalItem{
		{Kind: "ticket", Key: "one", Title: "One", Source: ProposalSource{SourceID: "s", Quote: "q"}},
		{Kind: "ticket", Key: "two", Title: "Two", Source: ProposalSource{SourceID: "s", Quote: "q"}},
	}, ProposalNotice{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(proposal); err != nil {
		t.Fatal(err)
	}
	return proposal
}

func TestApplyRequiresExactReviewedHashBeforeCreatingAnything(t *testing.T) {
	store := NewProposalStore(proposalFolder{root: t.TempDir()})
	proposal := pendingProposal(t, store)
	for name, hash := range map[string]string{"missing": "", "stale": "not-the-hash"} {
		t.Run(name, func(t *testing.T) {
			creator := &recordingTicketCreator{}
			service := NewApplyService(store, creator)
			_, err := service.Apply("ws", "materials", ApplyRequest{ProposalHash: hash, Items: []ApplyChoice{{Key: "one", Selected: true}}})
			if !errors.Is(err, ErrProposalStale) {
				t.Fatalf("error = %v", err)
			}
			if len(creator.calls) != 0 {
				t.Fatalf("direct ticket path called %d times before hash approval", len(creator.calls))
			}
		})
	}
	if proposal.Hash == "" {
		t.Fatal("fixture has no reviewed hash")
	}
}

func TestApplyRequiresReviewedHashForEveryRecordKind(t *testing.T) {
	store := NewProposalStore(proposalFolder{root: t.TempDir()})
	start := time.Now().UTC()
	end := start.Add(time.Hour)
	proposal, err := finalizeProposal("ws", "materials", []ProposalItem{
		{Kind: ProposalKindTicket, Key: "ticket", Title: "Ticket"},
		{Kind: ProposalKindMemory, Key: "memory", Text: "Remember this"},
		{Kind: ProposalKindNote, Key: "note", Title: "Note", Body: "Body"},
		{Kind: ProposalKindCalendarEvent, Key: "event", Title: "Event", Start: &start, End: &end},
	}, ProposalNotice{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(proposal); err != nil {
		t.Fatal(err)
	}
	tickets := &recordingTicketCreator{}
	memories := &recordingMemoryWriter{}
	notes := &recordingNoteWriter{}
	calendar := &recordingCalendarWriter{available: true}
	service := NewApplyService(store, tickets)
	service.SetMemoryWriter(memories)
	service.SetNoteWriter(notes)
	service.SetCalendarWriter(calendar)
	_, err = service.ApplyContext(context.Background(), "ws", "materials", ApplyRequest{ProposalHash: "stale", Items: []ApplyChoice{{Key: "ticket", Selected: true}, {Key: "memory", Selected: true}, {Key: "note", Selected: true}, {Key: "event", Selected: true}}})
	if !errors.Is(err, ErrProposalStale) {
		t.Fatalf("error = %v", err)
	}
	if len(tickets.calls)+len(memories.entries)+len(notes.notes)+len(calendar.events) != 0 {
		t.Fatalf("record writers were called before reviewed hash approval")
	}
}

func TestApplyCreatesReviewedMemoryNoteAndCalendarItems(t *testing.T) {
	store := NewProposalStore(proposalFolder{root: t.TempDir()})
	start := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	proposal, err := finalizeProposal("ws", "materials", []ProposalItem{
		{Kind: ProposalKindTicket, Key: "ticket", Title: "Read chapter 1", DueAt: &end, Source: ProposalSource{SourceID: "s", Quote: "q"}},
		{Kind: ProposalKindMemory, Key: "memory", Text: "Office hours are Tuesdays", MemoryType: "fact", Source: ProposalSource{SourceID: "s", Quote: "q"}},
		{Kind: ProposalKindNote, Key: "note", Title: "Reading list", Body: "Read chapter 1", Source: ProposalSource{SourceID: "s", Quote: "q"}},
		{Kind: ProposalKindCalendarEvent, Key: "event", Title: "Seminar", Start: &start, End: &end, Source: ProposalSource{SourceID: "s", Quote: "q"}},
	}, ProposalNotice{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(proposal); err != nil {
		t.Fatal(err)
	}
	memories := &recordingMemoryWriter{}
	notes := &recordingNoteWriter{}
	calendar := &recordingCalendarWriter{available: true}
	service := NewApplyService(store, &recordingTicketCreator{})
	service.SetMemoryWriter(memories)
	service.SetNoteWriter(notes)
	service.SetCalendarWriter(calendar)
	editedStart := start.Add(2 * time.Hour)
	editedEnd := end.Add(2 * time.Hour)
	result, err := service.ApplyContext(context.Background(), "ws", "materials", ApplyRequest{ProposalHash: proposal.Hash, Items: []ApplyChoice{{Key: "ticket", Selected: true}, {Key: "memory", Selected: true}, {Key: "note", Selected: true}, {Key: "event", Selected: true, Start: editedStart.Format(time.RFC3339), End: editedEnd.Format(time.RFC3339)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 4 || result.Results[0].Status != "created" || result.Results[1].Status != "created" || result.Results[2].Status != "created" || result.Results[3].Status != "created" {
		t.Fatalf("results = %+v", result.Results)
	}
	if len(memories.entries) != 1 || memories.entries[0].Provenance != "blueprint_intake:materials:memory" {
		t.Fatalf("memory entries = %+v", memories.entries)
	}
	if len(notes.notes) != 1 || notes.notes[0].Content != "Read chapter 1" || len(calendar.events) != 1 || !calendar.events[0].Start.Equal(editedStart) {
		t.Fatalf("notes/events = %+v / %+v", notes.notes, calendar.events)
	}
	ledger, err := store.Ledger("ws", "materials")
	if err != nil || len(ledger) != 4 {
		t.Fatalf("ledger = %+v, %v", ledger, err)
	}
	if ledger[0].DueAt == nil || ledger[1].Text == "" || ledger[2].Body == "" || ledger[3].Start == nil || ledger[3].End == nil {
		t.Fatalf("ledger did not retain applied values: %+v", ledger)
	}
}

func TestPrepareProposalDisablesCalendarWithoutAReadyConnector(t *testing.T) {
	service := NewApplyService(nil, nil)
	service.SetCalendarWriter(&recordingCalendarWriter{available: false})
	proposal := Proposal{WorkspaceID: "ws", Items: []ProposalItem{{Kind: ProposalKindCalendarEvent, Key: "event"}, {Kind: ProposalKindTicket, Key: "ticket"}}}
	if err := service.PrepareProposal(context.Background(), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Items[0].DisabledReason == "" || proposal.Items[1].DisabledReason != "" {
		t.Fatalf("proposal = %+v", proposal)
	}
}

func TestApplyReportsCalendarFailuresPerItemAndContinues(t *testing.T) {
	store := NewProposalStore(proposalFolder{root: t.TempDir()})
	start := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	proposal, err := finalizeProposal("ws", "materials", []ProposalItem{
		{Kind: ProposalKindCalendarEvent, Key: "bad", Title: "Bad", Start: &start, End: &end, Source: ProposalSource{SourceID: "s", Quote: "q"}},
		{Kind: ProposalKindCalendarEvent, Key: "good", Title: "Good", Start: &start, End: &end, Source: ProposalSource{SourceID: "s", Quote: "q"}},
	}, ProposalNotice{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(proposal); err != nil {
		t.Fatal(err)
	}
	calendar := &recordingCalendarWriter{available: true, failTitle: "Bad"}
	service := NewApplyService(store, &recordingTicketCreator{})
	service.SetCalendarWriter(calendar)
	result, err := service.ApplyContext(context.Background(), "ws", "materials", ApplyRequest{ProposalHash: proposal.Hash, Items: []ApplyChoice{{Key: "bad", Selected: true}, {Key: "good", Selected: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(calendar.events) != 2 || result.Results[0].Status != "failed" || result.Results[1].Status != "created" {
		t.Fatalf("events/results = %+v / %+v", calendar.events, result.Results)
	}
}

func TestApplyReportsEachItemAndWritesTicketProvenance(t *testing.T) {
	store := NewProposalStore(proposalFolder{root: t.TempDir()})
	proposal := pendingProposal(t, store)
	creator := &recordingTicketCreator{failID: "materials:two"}
	service := NewApplyService(store, creator)
	result, err := service.Apply("ws", "materials", ApplyRequest{ProposalHash: proposal.Hash, Items: []ApplyChoice{{Key: "one", Selected: true}, {Key: "two", Selected: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Status != "created" || result.Results[1].Status != "failed" {
		t.Fatalf("results = %+v", result.Results)
	}
	if len(creator.calls) != 2 || creator.calls[0].Source != workspace.TicketSourceBlueprintIntake || creator.calls[0].SourceID != "materials:one" {
		t.Fatalf("ticket provenance = %+v", creator.calls)
	}
	stored, err := store.Get("ws", "materials")
	if err != nil || stored.Status != "applied" {
		t.Fatalf("stored proposal = %+v, %v", stored, err)
	}
}
