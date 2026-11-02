package secondbrain

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/followup"
)

type fakeFollowUps struct {
	held     map[string]bool
	captured []followup.CaptureInput
}

func (f *fakeFollowUps) Capture(_ context.Context, in followup.CaptureInput) (*followup.FollowUp, error) {
	f.captured = append(f.captured, in)
	return &followup.FollowUp{ID: "f"}, nil
}

func (f *fakeFollowUps) FindBySource(_ context.Context, _, _, id string) (*followup.FollowUp, error) {
	if f.held[id] {
		return &followup.FollowUp{ID: "held"}, nil
	}
	return nil, followup.ErrNotFound
}

type fakeProjectTickets struct {
	folders map[string]string
	made    map[string]bool
	calls   []NoteTicket
}

func (f *fakeProjectTickets) WorkspaceForFolder(path string) (string, string, bool) {
	name, ok := f.folders[path]
	return "ws-" + name, name, ok
}

func (f *fakeProjectTickets) HasNoteTicket(_, itemID string) bool { return f.made[itemID] }

func (f *fakeProjectTickets) CreateNoteTicket(_ context.Context, _ string, ticket NoteTicket) (bool, error) {
	f.calls = append(f.calls, ticket)
	if f.made == nil {
		f.made = map[string]bool{}
	}
	if f.made[ticket.ItemID] {
		return false, nil
	}
	f.made[ticket.ItemID] = true
	return true, nil
}

func TestProjectFolderOf(t *testing.T) {
	cases := []struct {
		note, want string
		ok         bool
	}{
		{"Projects/Album/plan.md", "Projects/Album", true},
		{"Projects/Album/notes/deep/plan.md", "Projects/Album", true},
		{"Projects/plan.md", "", false},
		{"Areas/Album/plan.md", "", false},
		{"Projects/../secrets/plan.md", "", false},
		{"Projects/Album/../../outside.md", "", false},
		{"Projects/.hidden/plan.md", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := ProjectFolderOf(tc.note)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ProjectFolderOf(%q) = %q, %v, want %q, %v", tc.note, got, ok, tc.want, tc.ok)
		}
	}
}

func routingAcceptance(tickets ProjectTickets, items ...LookItem) Acceptance {
	return Acceptance{UserID: "u", WorkspaceID: "hq", Source: Source{Path: "/vault"}, Items: items, Tickets: tickets}
}

func TestAcceptRouted_AProjectLineGoesToItsWorkspace(t *testing.T) {
	follow := &fakeFollowUps{}
	tickets := &fakeProjectTickets{folders: map[string]string{"/vault/Projects/Album": "Album"}}
	result, err := AcceptRouted(context.Background(), follow, routingAcceptance(tickets,
		LookItem{ID: "a", Text: "Mix the single", Note: "Projects/Album/plan.md", Due: "2026-12-01"},
		LookItem{ID: "b", Text: "Call the dentist", Note: "Daily/today.md"},
		LookItem{ID: "c", Text: "No workspace yet", Note: "Projects/Other/plan.md"},
	))
	if err != nil {
		t.Fatalf("AcceptRouted: %v", err)
	}
	if result.Tickets["Album"] != 1 || result.TicketCount() != 1 || result.FollowUps != 2 || result.Skipped != 0 {
		t.Errorf("result = %+v, want 1 ticket and 2 follow-ups", result)
	}
	if tickets.calls[0].ItemID != "a" || tickets.calls[0].Title != "Mix the single" || tickets.calls[0].DueAt == nil {
		t.Errorf("ticket = %+v, want the line, its id as key, and its date", tickets.calls[0])
	}
	if tickets.calls[0].Detail != "Projects/Album/plan.md" {
		t.Errorf("detail = %q, want the note it came from", tickets.calls[0].Detail)
	}
}

func TestAcceptRouted_ALaterLookMakesNoDuplicate(t *testing.T) {
	follow := &fakeFollowUps{}
	tickets := &fakeProjectTickets{folders: map[string]string{"/vault/Projects/Album": "Album"}}
	item := LookItem{ID: "a", Text: "Mix", Note: "Projects/Album/plan.md"}
	if _, err := AcceptRouted(context.Background(), follow, routingAcceptance(tickets, item)); err != nil {
		t.Fatalf("first: %v", err)
	}
	result, err := AcceptRouted(context.Background(), follow, routingAcceptance(tickets, item))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if result.TicketCount() != 0 || result.Skipped != 1 {
		t.Errorf("second result = %+v, want it skipped", result)
	}
}

func TestAcceptRouted_AnItemHeldAsAFollowUpIsSkippedNotMoved(t *testing.T) {
	follow := &fakeFollowUps{held: map[string]bool{"a": true}}
	tickets := &fakeProjectTickets{folders: map[string]string{"/vault/Projects/Album": "Album"}}
	result, err := AcceptRouted(context.Background(), follow, routingAcceptance(tickets,
		LookItem{ID: "a", Text: "Mix", Note: "Projects/Album/plan.md"}))
	if err != nil {
		t.Fatalf("AcceptRouted: %v", err)
	}
	if result.Skipped != 1 || len(tickets.calls) != 0 || len(follow.captured) != 0 {
		t.Errorf("result %+v, tickets %d, captured %d, want skipped and nothing made", result, len(tickets.calls), len(follow.captured))
	}
}

func TestAcceptRouted_SeededItemsStaySkipped(t *testing.T) {
	follow := &fakeFollowUps{}
	tickets := &fakeProjectTickets{folders: map[string]string{"/vault/Projects/Album": "Album"}}
	result, err := AcceptRouted(context.Background(), follow, routingAcceptance(tickets,
		LookItem{ID: "a", Text: "Mix", Note: "Projects/Album/plan.md", Seeded: true}))
	if err != nil {
		t.Fatalf("AcceptRouted: %v", err)
	}
	if result.Skipped != 1 || len(tickets.calls) != 0 {
		t.Errorf("result %+v, tickets %d", result, len(tickets.calls))
	}
}

func TestAcceptRouted_WithoutRoutingEverythingStaysAFollowUp(t *testing.T) {
	follow := &fakeFollowUps{}
	created, skipped, err := Accept(context.Background(), follow, Acceptance{
		UserID: "u", WorkspaceID: "hq", Items: []LookItem{{ID: "a", Text: "Mix", Note: "Projects/Album/plan.md"}},
	})
	if err != nil || created != 1 || skipped != 0 {
		t.Errorf("created %d skipped %d err %v", created, skipped, err)
	}
}

type failingTickets struct{ fakeProjectTickets }

func (f *failingTickets) CreateNoteTicket(context.Context, string, NoteTicket) (bool, error) {
	return false, errors.New("store down")
}

func TestAcceptRouted_AFailedTicketIsReportedNotTurnedIntoAFollowUp(t *testing.T) {
	follow := &fakeFollowUps{}
	tickets := &failingTickets{fakeProjectTickets{folders: map[string]string{"/vault/Projects/Album": "Album"}}}
	_, err := AcceptRouted(context.Background(), follow, routingAcceptance(tickets,
		LookItem{ID: "a", Text: "Mix", Note: "Projects/Album/plan.md"}))
	if err == nil {
		t.Fatal("want the failure reported")
	}
	if len(follow.captured) != 0 {
		t.Errorf("a line was quietly made a follow-up instead: %+v", follow.captured)
	}
}
