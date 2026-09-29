package projectlibrary

import (
	"sort"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ManagerHandoffReceipt is historical Home-owned evidence that an explicitly
// reviewed handoff created a child-owned Ticket. It is not the Ticket body,
// current Ticket state, an assignment, or permission to run/read the child.
type ManagerHandoffReceipt struct {
	TicketID     string    `json:"ticket_id"`
	TicketNumber int64     `json:"ticket_number,omitempty"`
	RecordedAt   time.Time `json:"recorded_at"`
}

type ManagerHandoffs struct {
	Total int                     `json:"total"`
	Rows  []ManagerHandoffReceipt `json:"rows"`
}

func validHandoffCitation(receipt *ManagerHandoffReceipt, link *ExactLink) bool {
	return link != nil && receipt != nil && receipt.TicketID != "" && validText(receipt.TicketID, 160) &&
		receipt.TicketNumber >= 0 && !receipt.RecordedAt.IsZero()
}

// A citation is chosen by Ticket ID, but all citation fields are copied from
// the Home's saved handoff receipt. Never consult the child Ticket or trust a
// browser-supplied number, link, status, or timestamp.
func homeHandoffChoices(state *workspace.AssistantProgramState, link *ExactLink) (ManagerHandoffs, bool) {
	if state == nil || link == nil {
		return ManagerHandoffs{}, false
	}
	result := ManagerHandoffs{Rows: []ManagerHandoffReceipt{}}
	seen := map[string]bool{}
	for _, receipt := range state.Portfolio.HandoffOperationReceipts {
		if receipt.LinkID != link.LinkID || receipt.ProjectWorkspaceID != link.WorkspaceID {
			continue
		}
		candidate := ManagerHandoffReceipt{TicketID: receipt.TicketID,
			TicketNumber: receipt.TicketNumber, RecordedAt: receipt.RecordedAt}
		if !validHandoffCitation(&candidate, link) || seen[candidate.TicketID] {
			return ManagerHandoffs{}, false // Conflicting or duplicated evidence must not be guessed.
		}
		seen[candidate.TicketID] = true
		result.Rows = append(result.Rows, candidate)
	}
	result.Total = len(result.Rows)
	sort.Slice(result.Rows, func(i, j int) bool {
		if result.Rows[i].RecordedAt.Equal(result.Rows[j].RecordedAt) {
			return result.Rows[i].TicketID < result.Rows[j].TicketID
		}
		return result.Rows[i].RecordedAt.After(result.Rows[j].RecordedAt)
	})
	if len(result.Rows) > 3 {
		result.Rows = result.Rows[:3]
	}
	return result, true
}

func homeHandoffCitation(state *workspace.AssistantProgramState, link *ExactLink, ticketID string) (*ManagerHandoffReceipt, bool) {
	if ticketID == "" || !validText(ticketID, 160) {
		return nil, false
	}
	choices, valid := homeHandoffChoices(state, link)
	if !valid {
		return nil, false
	}
	for _, row := range choices.Rows {
		if row.TicketID == ticketID {
			return &row, true
		}
	}
	return nil, false
}

// HandoffsForManager reads only the Home's own prior handoff receipts for one
// currently verified linked catalog entry. It never calls TicketService, rolls
// up child tasks, reads child Ticket bodies, or uses an agent-supplied child ID.
// Disconnected or divergent links cannot open even historical child receipts
// through this tool; the owner retains the Home record separately.
func (s *Store) HandoffsForManager(authority ManagerAuthority, entryID string) (ManagerHandoffs, error) {
	scope, err := s.authorizeManager(authority)
	if err != nil {
		return ManagerHandoffs{}, err
	}
	result, err := s.HandoffsForOwner(scope, entryID)
	if err != nil {
		return ManagerHandoffs{}, err
	}
	if _, err := s.authorizeManager(authority); err != nil {
		return ManagerHandoffs{}, err
	}
	return result, nil
}

// HandoffsForOwner lists only the Home's own confirmed receipt IDs while
// this entry still has an exact reciprocal child link. It never reads Tickets.
// The HTTP caller must authenticate the owner of scope before calling it.
func (s *Store) HandoffsForOwner(scope Scope, entryID string) (ManagerHandoffs, error) {
	refuse := func() (ManagerHandoffs, error) { return ManagerHandoffs{}, ErrConflict }
	if entryID == "" || !validText(entryID, 160) {
		return refuse()
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return ManagerHandoffs{}, err
	}
	entry := sessionEntry(doc, entryID)
	link, verified := s.verifiedRecapShareLink(scope, state, entry)
	if !verified {
		return refuse()
	}
	result, valid := homeHandoffChoices(state, link)
	if !valid {
		return ManagerHandoffs{}, ErrCorrupt
	}
	// Authorization includes this specific child, not merely the Home.
	// Recheck after the read before returning receipt IDs.
	latest, currentState, err := s.readSnapshot(scope)
	if err != nil {
		return ManagerHandoffs{}, err
	}
	currentLink, ok := s.verifiedRecapShareLink(scope, currentState, sessionEntry(latest, entryID))
	if !ok || *currentLink != *link {
		return refuse()
	}
	for _, row := range result.Rows {
		current, found := homeHandoffCitation(currentState, currentLink, row.TicketID)
		if !found || *current != row {
			return refuse()
		}
	}
	return result, nil
}
