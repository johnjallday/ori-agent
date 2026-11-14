package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// currentHQFirstVisitForPage supplies the pre-paint hint from the same current
// user's canonical designation. A failed read never manufactures an offer.
func (s *Server) currentHQFirstVisitForPage(workspaceID string) string {
	if s == nil || s.Storage == nil || s.Storage.PersonalHQ == nil {
		return ""
	}
	status, err := s.Storage.PersonalHQ.Status(context.Background(), userprofile.LocalUserID)
	if err != nil || status == nil || !status.Valid || status.WorkspaceID != workspaceID {
		return ""
	}
	return string(status.FirstVisitState)
}

// currentPersonalHQForPage is a navigation hint, never an authorization gate.
// Resolve the live hired relationship instead of trusting a portable workspace
// marker or a matching agent name. The dossier API revalidates independently.
func (s *Server) currentPersonalHQForPage(workspaceID, entryName string) bool {
	if s == nil || s.Storage == nil || s.Storage.PersonalAssistant == nil ||
		(strings.TrimSpace(workspaceID) == "" && strings.TrimSpace(entryName) == "") {
		return false
	}
	current, err := s.Storage.PersonalAssistant.Get(context.Background(), userprofile.LocalUserID)
	if err != nil || current == nil ||
		(current.State != personalassistant.APIStateActive && current.State != personalassistant.APIStatePaused) ||
		!current.Availability.PersonalHQ.Available || !current.Availability.AgentInstance.Available ||
		(workspaceID != "" && current.HQWorkspaceID != workspaceID) {
		return false
	}
	return entryName == "" || strings.EqualFold(strings.TrimSpace(entryName), strings.TrimSpace(current.GlobalAgentProfile))
}
