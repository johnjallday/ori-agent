package workspace

// TicketCounts is how many of a workspace's tickets sit in each canonical
// state. It is the one count every surface shows (PRD 760 FR-6): the workspace
// header, Personal HQ, Home, the workspace tree and the map all read it, so no
// surface derives its own number from legacy task status in the browser.
//
// The counts come from Task.CanonicalState, never from legacy Status directly.
// A record that predates TicketState still lands in the right bucket because
// CanonicalState falls back to the documented legacy mapping.
type TicketCounts struct {
	Backlog    int `json:"backlog"`
	Ready      int `json:"ready"`
	InProgress int `json:"in_progress"`
	Review     int `json:"review"`
	Done       int `json:"done"`
	Cancelled  int `json:"cancelled"`
}

// Add counts one ticket in the given canonical state. A state outside the six
// is ignored rather than guessed into a bucket.
func (c *TicketCounts) Add(state TicketState) {
	switch state {
	case TicketStateBacklog:
		c.Backlog++
	case TicketStateReady:
		c.Ready++
	case TicketStateInProgress:
		c.InProgress++
	case TicketStateReview:
		c.Review++
	case TicketStateDone:
		c.Done++
	case TicketStateCancelled:
		c.Cancelled++
	}
}

// Open is the work a person should be aware of: Review, In Progress and Ready.
// Backlog is not committed work, and Done and Cancelled are finished (PRD 760
// FR-8). The board still shows all six.
func (c TicketCounts) Open() int {
	return c.Review + c.InProgress + c.Ready
}

// Total is every ticket, whatever its state.
func (c TicketCounts) Total() int {
	return c.Backlog + c.Ready + c.InProgress + c.Review + c.Done + c.Cancelled
}

// TicketCounts counts the workspace's own tickets by canonical state. It holds
// the read lock for the pass, so it is safe beside concurrent mutations.
func (w *Workspace) TicketCounts() TicketCounts {
	if w == nil {
		return TicketCounts{}
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return CountTickets(w.Tasks)
}

// CountTickets counts tasks by canonical state.
func CountTickets(tasks []Task) TicketCounts {
	var counts TicketCounts
	for i := range tasks {
		counts.Add(tasks[i].CanonicalState())
	}
	return counts
}
