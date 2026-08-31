package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	assignmentTicketSourcePrefix = "personal-assistant:first-assignment:"
	assignmentFollowUpSourceType = "personal_assistant_first_assignment"
)

// AssignmentTicketStore is the canonical Ticket mutation boundary used by
// first assignment. It deliberately exposes no direct workspace persistence.
type AssignmentTicketStore interface {
	CreateIdempotent(input workspace.TicketCreateInput) (*workspace.Ticket, bool, error)
}

// CanonicalWriter maps reviewed preview rows into existing product stores.
type AssignmentFollowUpStore interface {
	Capture(ctx context.Context, input followup.CaptureInput) (*followup.FollowUp, error)
}

type CanonicalWriter struct {
	tickets   AssignmentTicketStore
	followUps AssignmentFollowUpStore
}

func NewCanonicalWriter(tickets AssignmentTicketStore) *CanonicalWriter {
	return &CanonicalWriter{tickets: tickets}
}

func (w *CanonicalWriter) SetFollowUpService(followUps AssignmentFollowUpStore) {
	if w != nil {
		w.followUps = followUps
	}
}

// CreateTicket materializes exactly one ticket preview item. Source and
// source_id make retries converge on the original canonical Ticket.
func (w *CanonicalWriter) CreateTicket(workspaceID, assistantID, previewID string, item AssignmentPreviewItem) (CanonicalRef, bool, error) {
	if w == nil || w.tickets == nil {
		return CanonicalRef{}, false, errors.New("personal assistant: ticket writer is not configured")
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(assistantID) == "" || strings.TrimSpace(previewID) == "" {
		return CanonicalRef{}, false, errors.New("personal assistant: ticket ownership and preview identity are required")
	}
	if item.RecordType != AssignmentRecordTicket || strings.TrimSpace(item.ID) == "" {
		return CanonicalRef{}, false, fmt.Errorf("%w: preview item is not a canonical ticket", ErrValidation)
	}
	state := workspace.TicketStateBacklog
	switch item.State {
	case string(workspace.TicketStateBacklog):
		state = workspace.TicketStateBacklog
	case string(workspace.TicketStateReady):
		state = workspace.TicketStateReady
	default:
		return CanonicalRef{}, false, fmt.Errorf("%w: unsupported ticket state", ErrValidation)
	}
	var dueDate *time.Time
	if item.Due != "" {
		parsed, err := time.Parse("2006-01-02", item.Due)
		if err != nil {
			return CanonicalRef{}, false, fmt.Errorf("%w: invalid ticket due date", ErrValidation)
		}
		dueDate = &parsed
	}
	ticket, created, err := w.tickets.CreateIdempotent(workspace.TicketCreateInput{
		WorkspaceID: workspaceID,
		State:       state,
		Title:       item.Title,
		DueDate:     dueDate,
		Source:      workspace.TicketSourceAssistant,
		SourceID:    assignmentTicketSourcePrefix + assistantID + ":" + previewID + ":" + item.ID,
		Actor:       workspace.TicketActorAssistant,
		ActorID:     assistantID,
	})
	if err != nil {
		return CanonicalRef{}, false, err
	}
	return CanonicalRef{
		Kind: string(AssignmentRecordTicket), WorkspaceID: workspaceID, ID: ticket.ID,
	}, created, nil
}

// CreateFollowUp maps explicit first-assignment categories and directions into
// the existing follow-up service. Its non-manual source key makes replay
// idempotent while the service's lifecycle rules prevent closed resurrection.
func (w *CanonicalWriter) CreateFollowUp(ctx context.Context, userID, workspaceID, assistantID, previewID string, item AssignmentPreviewItem) (CanonicalRef, error) {
	if w == nil || w.followUps == nil {
		return CanonicalRef{}, errors.New("personal assistant: follow-up writer is not configured")
	}
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(workspaceID) == "" ||
		strings.TrimSpace(assistantID) == "" || strings.TrimSpace(previewID) == "" || strings.TrimSpace(item.ID) == "" {
		return CanonicalRef{}, errors.New("personal assistant: follow-up ownership and preview identity are required")
	}
	if item.RecordType != AssignmentRecordFollowUp {
		return CanonicalRef{}, fmt.Errorf("%w: preview item is not a canonical follow-up", ErrValidation)
	}
	var category followup.Category
	var direction followup.Direction
	switch item.Category {
	case string(followup.CategoryIOwe):
		category, direction = followup.CategoryIOwe, followup.DirectionOutbound
	case string(followup.CategoryWaitingOn):
		category, direction = followup.CategoryWaitingOn, followup.DirectionInbound
	case string(followup.CategoryNeedsDecision):
		category, direction = followup.CategoryNeedsDecision, followup.DirectionNone
	default:
		return CanonicalRef{}, fmt.Errorf("%w: unsupported follow-up category", ErrValidation)
	}
	var dueAt *time.Time
	if item.Due != "" {
		parsed, err := time.Parse("2006-01-02", item.Due)
		if err != nil {
			return CanonicalRef{}, fmt.Errorf("%w: invalid follow-up due date", ErrValidation)
		}
		dueAt = &parsed
	}
	followUp, err := w.followUps.Capture(ctx, followup.CaptureInput{
		UserID: userID, WorkspaceID: workspaceID, Category: category, Direction: direction,
		Title: item.Title, Detail: item.Detail, Counterparty: item.Counterparty, DueAt: dueAt,
		Source: followup.SourceRef{
			Type: assignmentFollowUpSourceType,
			ID:   assistantID + ":" + workspaceID + ":" + previewID + ":" + item.ID,
		},
		Provenance: followup.ProvenanceExplicit,
	})
	if err != nil {
		return CanonicalRef{}, err
	}
	return CanonicalRef{Kind: string(AssignmentRecordFollowUp), WorkspaceID: workspaceID, ID: followUp.ID}, nil
}
