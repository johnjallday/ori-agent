package filejanitor

import "github.com/johnjallday/ori-agent/internal/filecuratorguidance"

// CuratorGuidanceStatus is read-only product context, not a selectable user
// skill or permission grant. Candidate names are picker labels only.
type CuratorGuidanceStatus struct {
	Bundled         bool               `json:"bundled"`
	Available       bool               `json:"available"`
	AgentInstanceID string             `json:"agent_instance_id,omitempty"`
	Candidates      []CuratorCandidate `json:"candidates,omitempty"`
}

type CuratorCandidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) CuratorGuidance(workspaceID string) CuratorGuidanceStatus {
	out := CuratorGuidanceStatus{Bundled: filecuratorguidance.Prompt() != ""}
	if s == nil || s.workspaces == nil {
		return out
	}
	ws, err := s.workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return out
	}
	if curator, ok := ws.FileCurator(); ok && out.Bundled {
		out.Available = true
		out.AgentInstanceID = curator.ID
		return out
	}
	for _, instance := range ws.AgentInstances {
		if instance.ID != "" && instance.Name != "" {
			out.Candidates = append(out.Candidates, CuratorCandidate{ID: instance.ID, Name: instance.Name})
		}
	}
	return out
}
