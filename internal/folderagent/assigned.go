package folderagent

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// StartAssigned starts only the look, never workspace creation or hiring. The
// durable record is the claim: a racing tab observes it before opening a ticket.
func (s *Service) StartAssigned(ctx context.Context, owner, id, agent, link, brief string) error {
	if s == nil || s.deps.AssignedRoot == nil || s.deps.Store == nil || s.deps.Tickets == nil {
		return ErrUnavailable
	}
	root, workspaceName, ok := s.deps.AssignedRoot(ctx, owner, id, agent, link)
	if !ok || root == "" {
		return ErrNotOwner
	}
	if _, err := s.Record(owner, id); !errors.Is(err, ErrNoRecord) {
		return err
	}
	brief, err := personalassistant.NormalizeFolderBrief(brief)
	if err != nil {
		return err
	}
	folder := filepath.Base(root)
	rec := &Record{WorkspaceID: id, OwnerUserID: owner, WorkspaceName: workspaceName, FolderName: folder, AgentName: agent, AssignedLink: link, Brief: brief, State: StateLooking, AsksPremise: true}
	if err := s.deps.Store.Create(rec); err != nil {
		if _, readErr := s.Record(owner, id); readErr == nil {
			return nil
		}
		return err
	}
	ticket, err := s.deps.Tickets.Open(id, "Look around "+folder, agent)
	if err != nil {
		_, saveErr := s.deps.Store.Update(id, func(r *Record) error {
			r.State = StateFailed
			r.Failure = "Could not open the first-look ticket. Nothing was scanned."
			return nil
		})
		return errors.Join(err, saveErr)
	}
	if _, err = s.deps.Store.Update(id, func(r *Record) error { r.TicketID = ticket; return nil }); err != nil {
		_ = s.deps.Tickets.Finish(id, ticket, "Could not record the ticket. Nothing was scanned.", true)
		return err
	}
	if pins, ok := s.deps.Pins.(interface{ PinAssigned(string, string) error }); ok {
		if err = pins.PinAssigned(id, agent); err != nil {
			const failure = "The folder binding or agent toolbox changed. Nothing was scanned."
			_, saveErr := s.deps.Store.Update(id, func(r *Record) error { r.State, r.Failure = StateFailed, failure; return nil })
			return errors.Join(err, saveErr, s.deps.Tickets.Finish(id, ticket, failure, true))
		}
	} else {
		s.pin(id, agent, false)
	}
	s.startLook(id, nil)
	return nil
}
