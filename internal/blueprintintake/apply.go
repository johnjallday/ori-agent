package blueprintintake

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type TicketCreator interface {
	CreateIdempotent(workspace.TicketCreateInput) (*workspace.Ticket, bool, error)
}

type ApplyChoice struct {
	Key      string `json:"key"`
	Selected bool   `json:"selected"`
	Title    string `json:"title,omitempty"`
	DueAt    string `json:"due_at,omitempty"`
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

type ApplyService struct {
	proposals *ProposalStore
	tickets   TicketCreator
	now       func() time.Time
}

func NewApplyService(proposals *ProposalStore, tickets TicketCreator) *ApplyService {
	return &ApplyService{proposals: proposals, tickets: tickets, now: time.Now}
}

func (s *ApplyService) Apply(workspaceID, intakeKey string, request ApplyRequest) (Proposal, error) {
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
		if due := strings.TrimSpace(choice.DueAt); due != "" {
			parsed, _, parseErr := parseProposalDueAt(due, time.Local)
			if parseErr != nil {
				results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: "The edited date could not be read."})
				continue
			}
			item.DueAt = &parsed
		}
		proposal.Items[index] = item
		if item.Kind != "ticket" {
			results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: "This proposal kind is not available yet."})
			continue
		}
		ticket, _, createErr := s.tickets.CreateIdempotent(workspace.TicketCreateInput{
			WorkspaceID: workspaceID,
			State:       workspace.TicketStateBacklog,
			Title:       item.Title, Description: item.Description,
			DueDate: item.DueAt, Priority: 3,
			Source:   workspace.TicketSourceBlueprintIntake,
			SourceID: fmt.Sprintf("%s:%s", strings.TrimSpace(intakeKey), item.Key),
			Actor:    strings.TrimSpace(request.Actor),
		})
		if createErr != nil {
			results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "failed", Reason: createErr.Error()})
			continue
		}
		results = append(results, ApplyResult{Key: item.Key, Kind: item.Kind, Status: "created", RecordID: ticket.ID})
		ledger = append(ledger, LedgerEntry{Key: item.Key, Kind: item.Kind, RecordID: ticket.ID, Title: item.Title, DueAt: item.DueAt, AppliedAt: s.now().UTC()})
	}
	proposal.Status = "applied"
	proposal.Results = results
	proposal.UpdatedAt = s.now().UTC()
	if err := s.proposals.Finish(workspaceID, proposal, ledger); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}
