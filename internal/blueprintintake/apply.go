package blueprintintake

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type TicketCreator interface {
	CreateIdempotent(workspace.TicketCreateInput) (*workspace.Ticket, bool, error)
}

type TicketUpdater interface {
	Get(workspaceID, ticketID string) (*workspace.Ticket, error)
	Update(workspaceID, ticketID string, input workspace.TicketUpdateInput) (*workspace.Ticket, error)
}

type ApplyChoice struct {
	Key                string `json:"key"`
	Selected           bool   `json:"selected"`
	Title              string `json:"title,omitempty"`
	DueAt              string `json:"due_at,omitempty"`
	Start              string `json:"start,omitempty"`
	End                string `json:"end,omitempty"`
	ConflictResolution string `json:"conflict_resolution,omitempty"`
}

type ApplyRequest struct {
	ProposalHash string        `json:"proposal_hash"`
	Items        []ApplyChoice `json:"items"`
	Actor        string        `json:"actor,omitempty"`
}

type ApplyResult struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	RecordID string `json:"record_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type MemoryWriter interface {
	AppendUnique(workspaceID string, entry workspace.MemoryEntry) (bool, error)
}

type MemoryUpdater interface {
	Read(workspaceID string) (workspace.MemoryDocument, error)
	EditAt(workspaceID string, index int, entry workspace.MemoryEntry) error
}

type NoteWriter interface {
	CreateNote(context.Context, *session.WorkspaceNote) error
	GetNote(context.Context, string) (*session.WorkspaceNote, error)
}

type NoteUpdater interface {
	UpdateNote(context.Context, *session.WorkspaceNote) error
}

type CalendarEventInput struct {
	Title, Description, Location string
	Start, End                   time.Time
	AllDay                       bool
}

type CalendarWriter interface {
	Availability(context.Context, string) (bool, string, error)
	Create(context.Context, string, CalendarEventInput) (string, error)
}

type CalendarUpdater interface {
	Update(context.Context, string, string, CalendarEventInput) (string, error)
}

type ApplyService struct {
	proposals *ProposalStore
	tickets   TicketCreator
	memories  MemoryWriter
	notes     NoteWriter
	calendar  CalendarWriter
	now       func() time.Time
}

func NewApplyService(proposals *ProposalStore, tickets TicketCreator) *ApplyService {
	return &ApplyService{proposals: proposals, tickets: tickets, now: time.Now}
}

func (s *ApplyService) SetMemoryWriter(writer MemoryWriter)     { s.memories = writer }
func (s *ApplyService) SetNoteWriter(writer NoteWriter)         { s.notes = writer }
func (s *ApplyService) SetCalendarWriter(writer CalendarWriter) { s.calendar = writer }

func (s *ApplyService) PrepareProposal(ctx context.Context, proposal *Proposal) error {
	if proposal == nil {
		return nil
	}
	latest := make(map[string]LedgerEntry)
	hasChanged := false
	for _, item := range proposal.Items {
		hasChanged = hasChanged || item.Classification == ProposalClassificationChanged
	}
	if hasChanged {
		if s.proposals == nil {
			return errors.New("intake ledger is unavailable")
		}
		ledger, err := s.proposals.Ledger(proposal.WorkspaceID, proposal.IntakeKey)
		if err != nil {
			return err
		}
		for _, entry := range ledger {
			latest[ledgerKey(entry.Kind, entry.Key)] = entry
		}
	}
	for index := range proposal.Items {
		item := &proposal.Items[index]
		if item.Classification != ProposalClassificationChanged {
			continue
		}
		previous := latest[ledgerKey(item.Kind, item.Key)]
		switch item.Kind {
		case ProposalKindTicket:
			updater, ok := s.tickets.(TicketUpdater)
			if !ok {
				item.DisabledReason = "Ticket updates are unavailable."
				continue
			}
			current, getErr := updater.Get(proposal.WorkspaceID, previous.RecordID)
			if getErr != nil || current == nil {
				item.DisabledReason = "The existing ticket could not be loaded, so Ori will not overwrite it."
				continue
			}
			currentDue := formatProposalTime(current.DueDate)
			if current.Title != previous.Title || current.Description != previous.Description || currentDue != formatProposalTime(previous.DueAt) {
				item.Conflict = &ProposalConflict{Current: fmt.Sprintf("%s — %s", current.Title, currentDue), Proposed: fmt.Sprintf("%s — %s", item.Title, formatProposalTime(item.DueAt))}
			}
		case ProposalKindNote:
			if s.notes == nil {
				item.DisabledReason = "Note updates are unavailable."
				continue
			}
			current, getErr := s.notes.GetNote(ctx, previous.RecordID)
			if getErr != nil || current == nil {
				item.DisabledReason = "The existing note could not be loaded, so Ori will not overwrite it."
				continue
			}
			if current.Name != previous.Title || current.Content != previous.Body {
				item.Conflict = &ProposalConflict{Current: current.Name + ": " + current.Content, Proposed: item.Title + ": " + item.Body}
			}
		case ProposalKindMemory:
			updater, ok := s.memories.(MemoryUpdater)
			if !ok {
				item.DisabledReason = "Memory updates are unavailable."
				continue
			}
			doc, readErr := updater.Read(proposal.WorkspaceID)
			if readErr != nil {
				item.DisabledReason = "The existing memory entry could not be loaded, so Ori will not overwrite it."
				continue
			}
			current, memoryIndex := findMemoryEntry(doc.Entries(), proposal.IntakeKey, item.Key)
			if memoryIndex < 0 {
				item.DisabledReason = "The existing memory entry could not be found, so Ori will not overwrite it."
				continue
			}
			if current.Text != previous.Text || string(current.Type) != previous.MemoryType {
				item.Conflict = &ProposalConflict{Current: string(current.Type) + ": " + current.Text, Proposed: item.MemoryType + ": " + item.Text}
			}
		case ProposalKindCalendarEvent:
			if _, ok := s.calendar.(CalendarUpdater); !ok {
				item.DisabledReason = "This calendar connection cannot update existing events."
				continue
			}
			item.Conflict = &ProposalConflict{Current: "Keep the existing calendar event (it may have been edited in the calendar).", Proposed: fmt.Sprintf("%s — %s", item.Title, formatProposalTime(item.Start))}
		}
	}
	if s.calendar == nil {
		return nil
	}
	available, reason, err := s.calendar.Availability(ctx, proposal.WorkspaceID)
	if err != nil {
		return err
	}
	if !available {
		for index := range proposal.Items {
			if proposal.Items[index].Kind == ProposalKindCalendarEvent {
				proposal.Items[index].DisabledReason = reason
			}
		}
	}
	return nil
}

func (s *ApplyService) Apply(workspaceID, intakeKey string, request ApplyRequest) (Proposal, error) {
	return s.ApplyContext(context.Background(), workspaceID, intakeKey, request)
}

func (s *ApplyService) ApplyContext(ctx context.Context, workspaceID, intakeKey string, request ApplyRequest) (Proposal, error) {
	if s == nil || s.proposals == nil || s.tickets == nil {
		return Proposal{}, errors.New("intake apply service is unavailable")
	}
	proposal, err := s.proposals.BeginApply(workspaceID, intakeKey, request.ProposalHash)
	if err != nil {
		return Proposal{}, err
	}
	choices := make(map[string]ApplyChoice, len(request.Items))
	for _, choice := range request.Items {
		choice.Key = strings.TrimSpace(choice.Key)
		if choice.Key != "" {
			choices[choice.Key] = choice
		}
	}
	results := make([]ApplyResult, 0, len(proposal.Items))
	ledger := make([]LedgerEntry, 0, len(proposal.Items))
	for index := range proposal.Items {
		item := proposal.Items[index]
		choice, selected := choices[item.Key]
		if item.Classification == ProposalClassificationUnchanged || item.Classification == ProposalClassificationNoLongerFound {
			results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "skipped"})
			continue
		}
		if !selected || !choice.Selected {
			results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "skipped"})
			continue
		}
		if title := strings.TrimSpace(choice.Title); title != "" {
			if !bounded(title, 240) {
				results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: "The edited title is too long."})
				continue
			}
			item.Title = title
		}
		if item.Kind == ProposalKindTicket {
			if due := strings.TrimSpace(choice.DueAt); due != "" {
				parsed, _, parseErr := parseProposalDueAt(due, time.Local)
				if parseErr != nil {
					results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: "The edited date could not be read."})
					continue
				}
				item.DueAt = &parsed
			}
		}
		if item.Kind == ProposalKindCalendarEvent && strings.TrimSpace(choice.Start) != "" {
			var start, end time.Time
			var parseErr error
			if item.AllDay {
				location := time.Local
				if item.Start != nil {
					location = item.Start.Location()
				}
				start, parseErr = time.ParseInLocation("2006-01-02", strings.TrimSpace(choice.Start), location)
				end = start.AddDate(0, 0, 1)
			} else {
				start, parseErr = time.Parse(time.RFC3339, strings.TrimSpace(choice.Start))
				if parseErr == nil {
					end, parseErr = time.Parse(time.RFC3339, strings.TrimSpace(choice.End))
				}
			}
			if parseErr != nil || !end.After(start) {
				results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: "The edited event time could not be read."})
				continue
			}
			item.Start, item.End = &start, &end
		}
		proposal.Items[index] = item
		if item.Conflict != nil && choice.ConflictResolution == "keep_current" {
			results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "skipped", Reason: "Kept the current record."})
			continue
		}
		if item.Conflict != nil && choice.ConflictResolution != "use_proposed" {
			results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: "Choose whether to keep the current value or use the proposal."})
			continue
		}
		if item.UnusableReason != "" || item.DisabledReason != "" {
			reason := item.UnusableReason
			if reason == "" {
				reason = item.DisabledReason
			}
			results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: reason})
			continue
		}
		result, entry := s.applyItem(ctx, workspaceID, intakeKey, request.Actor, item)
		results = append(results, result)
		if result.Status == "created" || result.Status == "updated" {
			ledger = append(ledger, entry)
		}
	}
	proposal.Status = "applied"
	proposal.Results = results
	proposal.UpdatedAt = s.now().UTC()
	if err := s.proposals.Finish(workspaceID, proposal, ledger); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func (s *ApplyService) applyItem(ctx context.Context, workspaceID, intakeKey, actor string, item ProposalItem) (ApplyResult, LedgerEntry) {
	result := ApplyResult{Key: item.Key, Kind: item.Kind}
	entry := LedgerEntry{Key: item.Key, Kind: item.Kind, SourceID: item.Source.SourceID, Title: item.Title, Description: item.Description, Text: item.Text, MemoryType: item.MemoryType, Body: item.Body, DueAt: item.DueAt, Start: item.Start, End: item.End, AllDay: item.AllDay, Location: item.Location, AppliedAt: s.now().UTC()}
	var recordID string
	var err error
	switch item.Kind {
	case ProposalKindTicket:
		var ticket *workspace.Ticket
		if item.Classification == ProposalClassificationChanged {
			updater, ok := s.tickets.(TicketUpdater)
			if !ok {
				err = errors.New("ticket updates are unavailable")
				break
			}
			previous, previousErr := s.latestLedgerEntry(workspaceID, intakeKey, item.Kind, item.Key)
			if previousErr != nil {
				err = previousErr
				break
			}
			title, description, due := item.Title, item.Description, item.DueAt
			_, err = updater.Update(workspaceID, previous.RecordID, workspace.TicketUpdateInput{Title: &title, Description: &description, DueDate: &due})
			recordID = previous.RecordID
		} else {
			ticket, _, err = s.tickets.CreateIdempotent(workspace.TicketCreateInput{WorkspaceID: workspaceID, State: workspace.TicketStateBacklog, Title: item.Title, Description: item.Description, DueDate: item.DueAt, Priority: 3, Source: workspace.TicketSourceBlueprintIntake, SourceID: fmt.Sprintf("%s:%s", strings.TrimSpace(intakeKey), item.Key), Actor: strings.TrimSpace(actor)})
			if ticket != nil {
				recordID = ticket.ID
			}
		}
	case ProposalKindMemory:
		if s.memories == nil {
			err = errors.New("workspace memory is unavailable")
			break
		}
		var memoryText string
		memoryText, err = workspace.ValidateMemoryText(item.Text)
		if err != nil {
			break
		}
		memoryEntry := workspace.MemoryEntry{Type: workspace.NormalizeMemoryEntryType(item.MemoryType), Date: s.now().Format("2006-01-02"), Provenance: "blueprint_intake:" + intakeKey + ":" + item.Key, Text: memoryText}
		if item.Classification == ProposalClassificationChanged {
			updater, ok := s.memories.(MemoryUpdater)
			if !ok {
				err = errors.New("memory updates are unavailable")
				break
			}
			var doc workspace.MemoryDocument
			doc, err = updater.Read(workspaceID)
			if err != nil {
				break
			}
			currentMemory, memoryIndex := findMemoryEntry(doc.Entries(), intakeKey, item.Key)
			if memoryIndex < 0 {
				err = errors.New("the existing memory entry could not be found")
				break
			}
			memoryEntry.Date = currentMemory.Date
			err = updater.EditAt(workspaceID, memoryIndex, memoryEntry)
		} else {
			_, err = s.memories.AppendUnique(workspaceID, memoryEntry)
		}
		recordID = "MEMORY.md#" + intakeKey + ":" + item.Key
	case ProposalKindNote:
		if s.notes == nil {
			err = errors.New("workspace notes are unavailable")
			break
		}
		recordID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("ori:blueprint-intake:"+workspaceID+":"+intakeKey+":"+item.Key)).String()
		if item.Classification != ProposalClassificationChanged {
			if existing, getErr := s.notes.GetNote(ctx, recordID); getErr == nil && existing != nil {
				break
			} else if getErr != nil && !errors.Is(getErr, session.ErrNoteNotFound) {
				err = getErr
				break
			}
		}
		now := s.now().UTC()
		if item.Classification == ProposalClassificationChanged {
			updater, ok := s.notes.(NoteUpdater)
			if !ok {
				err = errors.New("note updates are unavailable")
				break
			}
			var current *session.WorkspaceNote
			current, err = s.notes.GetNote(ctx, recordID)
			if err != nil || current == nil {
				break
			}
			current.Name, current.Content, current.UpdatedAt = item.Title, item.Body, now
			err = updater.UpdateNote(ctx, current)
		} else {
			err = s.notes.CreateNote(ctx, &session.WorkspaceNote{ID: recordID, WorkspaceID: workspaceID, Name: item.Title, Content: item.Body, CreatedAt: now, UpdatedAt: now})
		}
	case ProposalKindCalendarEvent:
		if s.calendar == nil || item.Start == nil || item.End == nil {
			err = errors.New("workspace calendar is unavailable")
			break
		}
		calendarInput := CalendarEventInput{Title: item.Title, Description: item.Description, Location: item.Location, Start: *item.Start, End: *item.End, AllDay: item.AllDay}
		if item.Classification == ProposalClassificationChanged {
			updater, ok := s.calendar.(CalendarUpdater)
			if !ok {
				err = errors.New("calendar updates are unavailable")
				break
			}
			previous, previousErr := s.latestLedgerEntry(workspaceID, intakeKey, item.Kind, item.Key)
			if previousErr != nil {
				err = previousErr
				break
			}
			recordID, err = updater.Update(ctx, workspaceID, previous.RecordID, calendarInput)
		} else {
			recordID, err = s.calendar.Create(ctx, workspaceID, calendarInput)
		}
	default:
		err = errors.New("this proposal kind is not available")
	}
	if err != nil {
		result.Status, result.Reason = "failed", err.Error()
		return result, entry
	}
	entry.RecordID = recordID
	result.Status, result.RecordID = "created", recordID
	if item.Classification == ProposalClassificationChanged {
		result.Status = "updated"
	}
	return result, entry
}

func findMemoryEntry(entries []workspace.MemoryEntry, intakeKey, itemKey string) (workspace.MemoryEntry, int) {
	provenance := "blueprint_intake:" + intakeKey + ":" + itemKey
	for index, entry := range entries {
		if entry.Provenance == provenance {
			return entry, index
		}
	}
	return workspace.MemoryEntry{}, -1
}

func (s *ApplyService) latestLedgerEntry(workspaceID, intakeKey, kind, key string) (LedgerEntry, error) {
	ledger, err := s.proposals.Ledger(workspaceID, intakeKey)
	if err != nil {
		return LedgerEntry{}, err
	}
	for index := len(ledger) - 1; index >= 0; index-- {
		if ledger[index].Kind == kind && ledger[index].Key == key {
			return ledger[index], nil
		}
	}
	return LedgerEntry{}, errors.New("the existing intake record could not be found")
}
