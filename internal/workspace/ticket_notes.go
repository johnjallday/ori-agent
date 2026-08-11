package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Ticket ↔ Note relationships
// (tasks/prd-workspace-ticket-management.md FR-17, FR-18, FR-71 through
// FR-79, FR-123, FR-124).
//
// Notes stay independent knowledge records. A Ticket may reference zero or
// more of them, and that reference is the ONLY thing linking creates:
//
//   - Linking never changes a Note's ownership, tags, body, filename, or
//     lifecycle (FR-18).
//   - Relationships are explicit structured IDs on the Ticket, never parsed
//     out of body Markdown, and never a new per-Ticket Note file (FR-17,
//     FR-124).
//   - Bodies are never synchronized. Prefill from a Note is a one-time copy;
//     afterwards each side is edited on its own (FR-76).
//   - Deleting either side never deletes the other (FR-18, FR-72).

// ErrNoteNotFound is returned when a Note ID does not resolve inside the
// Ticket's owning workspace. As with Tickets, an unknown ID and a foreign ID
// are reported identically so this route cannot be used to probe another
// workspace's records.
var ErrNoteNotFound = errors.New("note not found in this workspace")

// TicketNoteRef is the minimum a Ticket needs to know about a linked Note in
// order to display and navigate to it. Deliberately not the Note's body: the
// Ticket references a Note, it does not contain one.
type TicketNoteRef struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	WorkspaceID string `json:"workspace_id"`
}

// TicketNoteLookup resolves Notes for validation and display.
//
// The workspace package defines this interface rather than importing the Note
// store directly, because internal/session already imports internal/workspace
// — taking the dependency the other way would be an import cycle. The server
// wires the concrete store in at construction.
type TicketNoteLookup interface {
	// LookupNote returns the Note's identity, or an error wrapping
	// ErrNoteNotFound when it does not exist.
	LookupNote(ctx context.Context, noteID string) (TicketNoteRef, error)
}

// SetNoteLookup wires Note validation and display (FR-17, FR-71). Optional:
// with no lookup configured, link operations are refused rather than silently
// accepting unvalidated IDs — accepting them would let a Ticket accumulate
// references to Notes that do not exist or belong to someone else.
func (s *TicketService) SetNoteLookup(lookup TicketNoteLookup) {
	if s == nil {
		return
	}
	s.notes = lookup
}

// resolveNoteForWorkspace validates that noteID exists AND belongs to
// workspaceID. Cross-workspace links are refused: a Ticket may only reference
// Notes from its own workspace (FR-17).
func (s *TicketService) resolveNoteForWorkspace(ctx context.Context, workspaceID, noteID string) (TicketNoteRef, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return TicketNoteRef{}, invalidTicketField("note_id", "note_id is required")
	}
	if s.notes == nil {
		return TicketNoteRef{}, fmt.Errorf("note linking is not available: no note store configured")
	}

	ref, err := s.notes.LookupNote(ctx, noteID)
	if err != nil {
		return TicketNoteRef{}, fmt.Errorf("%w: %s", ErrNoteNotFound, noteID)
	}
	if ref.WorkspaceID != workspaceID {
		// Reported exactly like a missing note, on purpose.
		return TicketNoteRef{}, fmt.Errorf("%w: %s", ErrNoteNotFound, noteID)
	}
	return ref, nil
}

// IsNoteNotFound reports whether err is (or wraps) ErrNoteNotFound.
func IsNoteNotFound(err error) bool { return errors.Is(err, ErrNoteNotFound) }

// LinkNote attaches a Note to a Ticket (FR-77).
//
// Linking is idempotent: re-linking an already-linked Note returns the current
// Ticket unchanged rather than duplicating the reference or erroring, so a
// double-click or a retried request is safe.
func (s *TicketService) LinkNote(ctx context.Context, workspaceID, ticketID, noteID string, ifVersion int64) (*Ticket, error) {
	ref, err := s.resolveNoteForWorkspace(ctx, workspaceID, noteID)
	if err != nil {
		return nil, err
	}

	var (
		updated Task
		linked  bool
	)
	err = s.store.Update(workspaceID, func(ws *Workspace) error {
		return mutateTicket(ws, ticketID, func(task *Task) error {
			if err := checkTicketVersion(task, ifVersion); err != nil {
				return err
			}
			if slices.Contains(task.LinkedNoteIDs, ref.ID) {
				// Already linked: return the current Ticket unchanged rather
				// than duplicating the reference or erroring.
				updated = *task
				return nil
			}
			task.LinkedNoteIDs = NormalizeLinkedNoteIDs(append(task.LinkedNoteIDs, ref.ID))
			linked = true
			touchTicket(task)
			updated = *task
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	ticket := NewTicket(&updated, updated.WorkspaceID, s.WorkspaceName(updated.WorkspaceID))
	if linked {
		s.publishTicket(EventTicketNoteLinked, ticket, map[string]any{
			"note_id":    ref.ID,
			"note_title": ref.Title,
		})
	}
	return &ticket, nil
}

// UnlinkNote detaches a Note from a Ticket (FR-77).
//
// It removes the reference and NOTHING else: the Note itself is untouched
// (FR-18). Unlinking a Note that is not linked is a no-op, so an unlink racing
// another unlink does not fail the second caller.
func (s *TicketService) UnlinkNote(ctx context.Context, workspaceID, ticketID, noteID string, ifVersion int64) (*Ticket, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return nil, invalidTicketField("note_id", "note_id is required")
	}

	var (
		updated  Task
		unlinked bool
	)
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		return mutateTicket(ws, ticketID, func(task *Task) error {
			if err := checkTicketVersion(task, ifVersion); err != nil {
				return err
			}
			remaining := make([]string, 0, len(task.LinkedNoteIDs))
			for _, existing := range task.LinkedNoteIDs {
				if existing == noteID {
					unlinked = true
					continue
				}
				remaining = append(remaining, existing)
			}
			if !unlinked {
				updated = *task
				return nil
			}
			task.LinkedNoteIDs = NormalizeLinkedNoteIDs(remaining)
			touchTicket(task)
			updated = *task
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	ticket := NewTicket(&updated, updated.WorkspaceID, s.WorkspaceName(updated.WorkspaceID))
	if unlinked {
		s.publishTicket(EventTicketNoteUnlinked, ticket, map[string]any{"note_id": noteID})
	}
	return &ticket, nil
}

// LinkedNotes resolves a Ticket's linked Notes for display (FR-69).
//
// A Note that has since been deleted is skipped rather than failing the read:
// a stale reference is a display problem, not a reason a Ticket cannot be
// opened (FR-78). The Ticket keeps the ID until someone unlinks it, so the
// relationship is never silently rewritten behind the user's back.
func (s *TicketService) LinkedNotes(ctx context.Context, workspaceID, ticketID string) ([]TicketNoteRef, error) {
	ticket, err := s.Get(workspaceID, ticketID)
	if err != nil {
		return nil, err
	}
	if len(ticket.LinkedNoteIDs) == 0 || s.notes == nil {
		return nil, nil
	}

	refs := make([]TicketNoteRef, 0, len(ticket.LinkedNoteIDs))
	for _, noteID := range ticket.LinkedNoteIDs {
		ref, err := s.notes.LookupNote(ctx, noteID)
		if err != nil || ref.WorkspaceID != workspaceID {
			continue
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// TicketsLinkedToNote returns the Tickets in workspaceID that reference
// noteID, as compact summaries (FR-75, FR-78).
//
// This is the reverse direction of the same structured relationship — derived
// by scanning the owning workspace's Tickets rather than stored twice, so the
// two directions cannot disagree. It returns summaries, not full Tickets: the
// Note surface displays and navigates to Tickets, it is never a second place
// to mutate them (FR-78).
func (s *TicketService) TicketsLinkedToNote(workspaceID, noteID string) ([]TicketSummary, error) {
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		return nil, invalidTicketField("note_id", "note_id is required")
	}

	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}

	ws.mu.RLock()
	defer ws.mu.RUnlock()

	out := make([]TicketSummary, 0, 2)
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		for _, linked := range task.LinkedNoteIDs {
			if linked != noteID {
				continue
			}
			out = append(out, NewTicketSummary(task, ws.ID, ws.Name))
			break
		}
	}
	return out, nil
}

// linkedNotesContextItem describes a Ticket's linked Notes for a Run's
// prepared context (FR-79).
//
// Identity ONLY — IDs and a count, never bodies. Access is on_demand because
// the agent reads Note content through the existing workspace-note tools,
// which already enforce this workspace's permissions. Injecting bodies here
// would route around that gate and silently widen what a Ticket's run can
// see, which FR-104 forbids.
func linkedNotesContextItem(task Task) (TaskPreparedContextItem, bool) {
	if len(task.LinkedNoteIDs) == 0 {
		return TaskPreparedContextItem{}, false
	}
	return TaskPreparedContextItem{
		Kind:   "linked_notes",
		Ref:    strings.Join(task.LinkedNoteIDs, ","),
		Name:   "Linked notes",
		Access: "on_demand",
		Detail: fmt.Sprintf(
			"%d note(s) are linked to this ticket. Their identifiers are listed; read their contents with the workspace note tools when needed.",
			len(task.LinkedNoteIDs),
		),
	}, true
}

// CreateTicketFromNote captures a Ticket seeded from a Note and links the two
// (FR-73 through FR-76).
//
// The Note is preserved exactly as it was. Prefilled title and description are
// a ONE-TIME copy: nothing here establishes ongoing synchronization, so
// editing either side later never rewrites the other (FR-76).
//
// The caller supplies the reviewed values rather than this deriving them, so
// the user gets to edit the prefill before anything is created — creating from
// a Note is a decision, not an automatic conversion.
func (s *TicketService) CreateTicketFromNote(ctx context.Context, workspaceID, noteID string, input TicketCreateInput) (*Ticket, error) {
	ref, err := s.resolveNoteForWorkspace(ctx, workspaceID, noteID)
	if err != nil {
		return nil, err
	}

	input.WorkspaceID = workspaceID
	input.Source = TicketSourceNote
	input.SourceID = ref.ID
	// The originating Note is linked from the start; any extra links the
	// caller asked for are preserved alongside it.
	input.LinkedNoteIDs = NormalizeLinkedNoteIDs(append([]string{ref.ID}, input.LinkedNoteIDs...))
	if strings.TrimSpace(input.Title) == "" {
		input.Title = ref.Title
	}

	return s.Create(input)
}
