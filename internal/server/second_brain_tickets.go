package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var errBridgeTicketsUnavailable = errors.New("tickets are unavailable")

// ticketServiceForBridges is the ticket service the follow-up and second-brain
// bridges use. It is built on demand because workspace storage is wired after
// the handlers that hold it.
func (b *ServerBuilder) ticketServiceForBridges() *workspace.TicketService {
	if b == nil || b.workspaceStore == nil {
		return nil
	}
	service := workspace.NewTicketService(b.workspaceStore)
	if b.eventBus != nil {
		service.SetEventBus(b.eventBus)
	}
	return service
}

// secondBrainProjectTickets routes a project's open lines to its workspace (PRD
// 760 FR-28, FR-29).
type secondBrainProjectTickets struct {
	b *ServerBuilder
	// source overrides the workspace cache in tests.
	source starterWorkspaceSource
}

// activeWorkspaces is every active workspace, read from the cache that carries
// shared data and directory references.
func (t secondBrainProjectTickets) activeWorkspaces() map[string]*workspace.Workspace {
	active := map[string]*workspace.Workspace{}
	source := t.source
	if source == nil && t.b != nil {
		source = t.b.starterWorkspaces()
	}
	if source == nil {
		return active
	}
	for id, ws := range source.CachedWorkspaces() {
		if ws != nil && ws.GetStatus() == workspace.StatusActive {
			active[id] = ws
		}
	}
	return active
}

// WorkspaceForFolder finds the active workspace whose primary directory is
// exactly this folder. A workspace on a parent or a child of it does not count.
func (t secondBrainProjectTickets) WorkspaceForFolder(path string) (string, string, bool) {
	want := resolvedPath(path)
	for id, ws := range t.activeWorkspaces() {
		primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
		if strings.TrimSpace(primary) == "" {
			continue
		}
		ref, err := ws.GetDirectoryReference(strings.TrimSpace(primary))
		if err != nil || ref == nil || strings.TrimSpace(ref.Path) == "" {
			continue
		}
		if resolvedPath(ref.Path) == want {
			return id, ws.Name, true
		}
	}
	return "", "", false
}

// CreateNoteTicket makes the Backlog ticket for a line, once.
func (t secondBrainProjectTickets) CreateNoteTicket(_ context.Context, workspaceID string, ticket secondbrain.NoteTicket) (bool, error) {
	service := t.b.ticketServiceForBridges()
	if service == nil {
		return false, errBridgeTicketsUnavailable
	}
	_, created, err := service.CreateWithKey(workspace.TicketCreateInput{
		WorkspaceID: workspaceID,
		State:       workspace.TicketStateBacklog,
		Title:       ticket.Title,
		Description: ticket.Detail,
		DueDate:     ticket.DueAt,
		Source:      workspace.TicketSourceNoteItem,
		SourceID:    ticket.ItemID,
		Actor:       workspace.TicketActorUser,
	})
	return created, err
}

// MarkNoteLinesClosed notes the finished lines on whichever workspace's tickets
// came from them. One workspace failing does not stop the others.
func (t secondBrainProjectTickets) MarkNoteLinesClosed(_ context.Context, itemIDs []string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	service := t.b.ticketServiceForBridges()
	if service == nil {
		return nil
	}
	gone := make(map[string]bool, len(itemIDs))
	for _, id := range itemIDs {
		gone[id] = true
	}
	for id := range t.activeWorkspaces() {
		// The store, not the cache: the cache may predate the ticket.
		ws, err := t.b.workspaceStore.Get(id)
		if err != nil || ws == nil {
			continue
		}
		holds := false
		for i := range ws.Tasks {
			if ws.Tasks[i].SourceType == workspace.TicketSourceNoteItem && gone[ws.Tasks[i].SourceID] {
				holds = true
				break
			}
		}
		if !holds {
			continue
		}
		if _, err := service.MarkNoteLinesClosed(id, gone, time.Now()); err != nil {
			logger.Warn("A workspace could not note its finished lines", logger.Fields{"workspace_id": id, "error": err.Error()})
		}
	}
	return nil
}
