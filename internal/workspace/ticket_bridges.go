package workspace

import (
	"strings"
	"time"
)

// Bridges between a workspace's tickets and the work that leads to them (PRD 760
// FR-25 to FR-29): a follow-up made into a ticket, an open line in a project's
// notes, and Start this. Each one is a link or a legal move, never a merge.

const (
	// TicketSourceFollowUp records a ticket made from an HQ follow-up. The source
	// ID is the follow-up's ID, so making it again finds the same ticket.
	TicketSourceFollowUp = "followup"
	// TicketSourceNoteItem records a ticket made from an open line in a project's
	// notes. The source ID is the line's content hash.
	TicketSourceNoteItem = "note_item"

	// taskContextSourceNote is what a ticket says about the line it came from,
	// once that line is done or gone.
	taskContextSourceNote = "source_note"
)

// NoteLineClosed is what a ticket says when its line in the notes was ticked,
// marked DONE or removed. Its state does not change, because Done takes a person's
// acceptance (FR-29).
const NoteLineClosed = "The line this came from is done or gone in your notes."

// AcceptsTicketsFrom says whether this user may put a ticket in this workspace:
// it exists, is active, and is theirs. A workspace with no recorded owner belongs
// to the local user, so it is theirs.
func (s *TicketService) AcceptsTicketsFrom(workspaceID, userID string) bool {
	ws, err := s.store.Get(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil || ws.Status != StatusActive {
		return false
	}
	return ws.OwnerUserID == "" || ws.OwnerUserID == userID
}

// TicketStartInput is one Start this on a ticket (FR-27).
type TicketStartInput struct {
	// Assignee is who does the work. It may be empty only when the ticket already
	// has someone.
	Assignee string
	Actor    string
}

// PrepareStart makes a ticket startable: Backlog moves to Ready through the legal
// transition, recorded in history, and it gets an agent if it lacks one. The
// caller then starts the run, so a start that fails still leaves it Ready and
// assigned. Anything already past Ready is refused.
func (s *TicketService) PrepareStart(workspaceID, ticketID string, input TicketStartInput) (*Ticket, error) {
	actor := strings.TrimSpace(input.Actor)
	if actor == "" {
		actor = TicketActorUser
	}
	assignee := strings.TrimSpace(input.Assignee)
	var updated Task
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		return mutateTicket(ws, ticketID, func(task *Task) error {
			state := task.CanonicalState()
			if state != TicketStateBacklog && state != TicketStateReady {
				return &TicketValidationError{
					Field:   "state",
					Message: "Start this works on a Backlog or Ready ticket; this one is " + state.Label(),
				}
			}
			agent := assignee
			if agent == "" && !taskAssigneeIsDefaultable(task.To) {
				agent = strings.TrimSpace(task.To)
			}
			if agent == "" {
				return invalidTicketField("assignee", "Choose who should start this")
			}
			if !ws.hasAgentLocked(agent) {
				return invalidTicketField("assignee", "%s is not an agent in this workspace", agent)
			}
			if state == TicketStateBacklog {
				if err := task.TransitionTicket(TicketStateReady, TicketStateChange{
					Actor:     actor,
					Reason:    "Start this",
					Timestamp: s.clock().UTC(),
				}); err != nil {
					return err
				}
				task.StateRank = nextTicketRankLocked(ws, TicketStateReady)
				task.BacklogRank = 0
			}
			stampAssignment(task, agent, TaskAssignment{
				AgentName:  agent,
				Mode:       TaskAssignmentModeManual,
				AssignedBy: TaskAssignedByManual,
				Reason:     "Named when starting this ticket",
			})
			// The person's start is the run. Until it moves the ticket to In Progress
			// (StartTicketRun clears this), the executor's poll must not pick the
			// same ticket up as auto-runnable and start a second run. A refused start
			// leaves it waiting for the next Start this.
			task.AwaitingExecutionIntent = true
			touchTicket(task)
			updated = *task
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	ticket := NewTicket(&updated, updated.WorkspaceID, s.WorkspaceName(updated.WorkspaceID), s.WorkspaceSlug(updated.WorkspaceID))
	s.publishTicket(EventTicketUpdated, ticket, nil)
	return &ticket, nil
}

// MarkNoteLinesClosed records on a workspace's note-made tickets that their lines
// are done or gone. Only that note changes: not the state, and never the notes.
func (s *TicketService) MarkNoteLinesClosed(workspaceID string, itemIDs map[string]bool, at time.Time) (int, error) {
	if len(itemIDs) == 0 {
		return 0, nil
	}
	marked := 0
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		for i := range ws.Tasks {
			task := &ws.Tasks[i]
			if task.SourceType != TicketSourceNoteItem || !itemIDs[task.SourceID] {
				continue
			}
			if existing, _ := task.Context[taskContextSourceNote].(string); existing == NoteLineClosed {
				continue
			}
			if task.Context == nil {
				task.Context = map[string]any{}
			}
			task.Context[taskContextSourceNote] = NoteLineClosed
			task.UpdatedAt = at.UTC()
			marked++
		}
		return nil
	})
	return marked, err
}

// ticketSourceNote is the note shown on a ticket about where it came from.
func ticketSourceNote(task *Task) string {
	if task == nil || task.Context == nil {
		return ""
	}
	note, _ := task.Context[taskContextSourceNote].(string)
	return note
}

// hasAgentLocked reports whether agent is a member, for a caller already holding
// ws.mu.
func (w *Workspace) hasAgentLocked(agent string) bool {
	for _, name := range w.agentNamesLocked() {
		if name == agent {
			return true
		}
	}
	return false
}
