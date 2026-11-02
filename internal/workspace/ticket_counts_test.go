package workspace

import "testing"

func TestCountTickets_CountsEachCanonicalState(t *testing.T) {
	cases := []struct {
		name  string
		state TicketState
		pick  func(TicketCounts) int
	}{
		{"backlog", TicketStateBacklog, func(c TicketCounts) int { return c.Backlog }},
		{"ready", TicketStateReady, func(c TicketCounts) int { return c.Ready }},
		{"in progress", TicketStateInProgress, func(c TicketCounts) int { return c.InProgress }},
		{"review", TicketStateReview, func(c TicketCounts) int { return c.Review }},
		{"done", TicketStateDone, func(c TicketCounts) int { return c.Done }},
		{"cancelled", TicketStateCancelled, func(c TicketCounts) int { return c.Cancelled }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CountTickets([]Task{
				{ID: "a", TicketState: tc.state},
				{ID: "b", TicketState: tc.state},
			})
			if n := tc.pick(got); n != 2 {
				t.Errorf("%s bucket = %d, want 2 (%+v)", tc.name, n, got)
			}
			if got.Total() != 2 {
				t.Errorf("Total() = %d, want 2 (a ticket must land in exactly one bucket): %+v", got.Total(), got)
			}
		})
	}
}

// A stored TicketState is the answer; legacy Status is only a projection of it.
// A Review ticket projects to legacy "completed", which on its own would read
// as Done.
func TestCountTickets_PrefersStoredStateOverLegacyStatus(t *testing.T) {
	got := CountTickets([]Task{
		{ID: "review", TicketState: TicketStateReview, Status: TaskStatusCompleted},
		{ID: "failed run", TicketState: TicketStateInProgress, Status: TaskStatusFailed},
	})
	if got.Review != 1 || got.Done != 0 {
		t.Errorf("a Review ticket counted as Done: %+v", got)
	}
	if got.InProgress != 1 {
		t.Errorf("a ticket whose latest run failed must stay In Progress: %+v", got)
	}
}

// Records written before TicketState existed carry only legacy status, and
// CanonicalState maps them (FR-8).
func TestCountTickets_MapsRecordsWithOnlyLegacyStatus(t *testing.T) {
	got := CountTickets([]Task{
		{ID: "1", Status: TaskStatusBacklog},
		{ID: "2", Status: TaskStatusPending},
		{ID: "3", Status: TaskStatusAssigned},
		{ID: "4", Status: TaskStatusInProgress},
		{ID: "5", Status: TaskStatusWaitingForChoice},
		{ID: "6", Status: TaskStatusFailed},
		{ID: "7", Status: TaskStatusTimeout},
		{ID: "8", Status: TaskStatusCompleted},
		{ID: "9", Status: TaskStatusCancelled},
		{ID: "10", Status: ""},
	})
	want := TicketCounts{Backlog: 1, Ready: 3, InProgress: 4, Review: 0, Done: 1, Cancelled: 1}
	if got != want {
		t.Errorf("legacy-only counts = %+v, want %+v", got, want)
	}
}

func TestTicketCounts_OpenIsReviewInProgressAndReady(t *testing.T) {
	counts := TicketCounts{Backlog: 4, Ready: 3, InProgress: 2, Review: 1, Done: 7, Cancelled: 5}
	if got := counts.Open(); got != 6 {
		t.Errorf("Open() = %d, want 6: Backlog, Done and Cancelled are not open work", got)
	}
	if got := counts.Total(); got != 22 {
		t.Errorf("Total() = %d, want 22", got)
	}
	if got := (TicketCounts{}).Open(); got != 0 {
		t.Errorf("empty Open() = %d, want 0", got)
	}
}

func TestTicketCounts_IgnoresAnUnknownState(t *testing.T) {
	var counts TicketCounts
	counts.Add(TicketState("blocked"))
	counts.Add(TicketState(""))
	if counts.Total() != 0 {
		t.Errorf("an unknown state was counted into a bucket: %+v", counts)
	}
}

func TestComputeMapSummaryFields_IncludesTicketCounts(t *testing.T) {
	ws := &Workspace{Tasks: []Task{
		{ID: "r", TicketState: TicketStateReady, Status: TaskStatusPending, To: "Writer"},
		{ID: "i", TicketState: TicketStateInProgress, Status: TaskStatusInProgress, To: "Writer"},
		{ID: "v", TicketState: TicketStateReview, Status: TaskStatusCompleted, To: "Writer"},
		{ID: "b", TicketState: TicketStateBacklog, Status: TaskStatusBacklog},
		{ID: "d", TicketState: TicketStateDone, Status: TaskStatusCompleted},
	}}
	got := ComputeMapSummaryFields(ws).TicketCounts
	want := TicketCounts{Backlog: 1, Ready: 1, InProgress: 1, Review: 1, Done: 1}
	if got != want {
		t.Errorf("TicketCounts = %+v, want %+v", got, want)
	}
}
