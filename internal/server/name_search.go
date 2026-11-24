package server

import (
	"net/http"

	"github.com/johnjallday/ori-agent/internal/namesearch"
	"github.com/johnjallday/ori-agent/internal/namesearchhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// nameSearchHandler is POST /api/name-search: Home's Files view finding Ori's own
// items by name. It reads the session store (workspace tree, note names, chats)
// and the workspace store (tickets, agents, stored files) and writes nothing.
// A source that is not wired makes only its own kind unavailable.
//
// It is deliberately not limited to the Mac that runs Ori, unlike the filename
// index: it reads only Ori's own records.
func (s *Server) nameSearchHandler() http.Handler {
	if s.Storage == nil {
		return namesearchhttp.Handler{}
	}
	sources := namesearch.Sources{Users: s.Storage.UserProvider}
	if store := s.Storage.SessionStore; store != nil {
		sources.Tree = store
		sources.Notes = store
		sources.Chats = store
	}
	if store := s.Storage.WorkspaceStore; store != nil {
		backed := namesearch.WorkspaceBacked{Store: store}
		sources.Tickets = workspace.NewTicketService(store)
		sources.Agents = backed
		sources.Files = backed
	}
	return namesearchhttp.Handler{Service: namesearch.New(sources)}
}
