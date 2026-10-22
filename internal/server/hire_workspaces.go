package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
)

// hireWorkspaces gives a Researcher or Writer hired from the Agents page its
// blueprint workspace through sessionhttp's ordinary creation pipeline
// (agents-hire-with-a-brief FR8b). The session handler is read on each call,
// since route wiring may run before it is built.
type hireWorkspaces struct{ server *Server }

var _ agenthttp.HireWorkspaces = hireWorkspaces{}

func (h hireWorkspaces) session() (*sessionhttp.Handler, error) {
	if h.server == nil || h.server.Handlers == nil || h.server.Handlers.Session == nil {
		return nil, errors.New("workspace creation is not available on this server")
	}
	return h.server.Handlers.Session, nil
}

func (h hireWorkspaces) CreateHireWorkspace(ctx context.Context, name, blueprintID, mission, agentName string) (string, error) {
	session, err := h.session()
	if err != nil {
		return "", err
	}
	id, err := session.CreateHireWorkspace(ctx, sessionhttp.HireWorkspaceRequest{
		Name: name, TemplateID: blueprintID, Mission: mission, AgentName: agentName,
	})
	if errors.Is(err, sessionhttp.ErrHireWorkspaceNameTaken) {
		return "", agenthttp.ErrHireWorkspaceNameTaken
	}
	return id, err
}

func (h hireWorkspaces) DeleteHireWorkspace(ctx context.Context, workspaceID string) error {
	session, err := h.session()
	if err != nil {
		return err
	}
	return session.DeleteHireWorkspace(ctx, workspaceID)
}
