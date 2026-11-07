package personalassistant

import (
	"encoding/json"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
)

// BuildStaffing is bounded advisory state, separate from TeamPatch and the
// draft's chosen actions. Merely saving/reading it never assigns a profile.
// SourceWitness is server-held review material; Public strips it for the UI.
type BuildStaffing struct {
	Version          int                     `json:"version"`
	ContextVersion   int64                   `json:"context_version"`
	TemplateID       string                  `json:"template_id"`
	TemplateRevision string                  `json:"template_revision"`
	State            string                  `json:"state"`
	Coverage         rosterstaffing.Coverage `json:"coverage"`
	Roles            []BuildStaffingRole     `json:"roles,omitempty"`
}

type BuildStaffingRole struct {
	RoleID  string                `json:"role_id"`
	Scope   string                `json:"scope"`
	State   string                `json:"state"`
	Choices []BuildStaffingChoice `json:"choices,omitempty"`
}

type BuildStaffingChoice struct {
	Handle        string                    `json:"handle"`
	Name          string                    `json:"name"`
	Source        string                    `json:"source"`
	Configured    bool                      `json:"configured"`
	Reasons       []rosterstaffing.Evidence `json:"reasons"`
	Gaps          []string                  `json:"gaps,omitempty"`
	SourceWitness string                    `json:"source_witness,omitempty"`
}

func (s *BuildStaffing) Public() *BuildStaffing {
	if s == nil {
		return nil
	}
	out := *s
	out.Roles = append([]BuildStaffingRole(nil), s.Roles...)
	for i := range out.Roles {
		out.Roles[i].Choices = append([]BuildStaffingChoice(nil), s.Roles[i].Choices...)
		for j := range out.Roles[i].Choices {
			out.Roles[i].Choices[j].SourceWitness = ""
		}
	}
	return &out
}

func (s *BuildStaffing) invalidate() {
	if s != nil {
		s.State = rosterstaffing.Stale
	}
}

func (s *BuildStaffing) validate(contextVersion int64) error {
	if s == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("%w: staffing recommendation", ErrWorkspaceBuildInvalid) }
	if s.Version != 1 || s.ContextVersion < 0 || s.ContextVersion > contextVersion ||
		!boundedText(s.TemplateID, 160) || !boundedText(s.TemplateRevision, 64) || len(s.Roles) > rosterstaffing.MaxRoles || !staffingState(s.State) ||
		s.Coverage.Inspected < 0 || s.Coverage.Included < 0 || s.Coverage.Included > rosterstaffing.MaxCandidates || s.Coverage.Included > s.Coverage.Inspected {
		return invalid()
	}
	roles := map[string]bool{}
	for _, role := range s.Roles {
		if !buildOpaqueID(role.RoleID) || roles[role.RoleID] || !staffingState(role.State) ||
			(role.Scope != "home" && role.Scope != "project" && role.Scope != "workspace") || len(role.Choices) > 1+rosterstaffing.MaxAlternatives {
			return invalid()
		}
		roles[role.RoleID] = true
		for _, choice := range role.Choices {
			if !buildOpaqueID(choice.Handle) || !buildOpaqueID(choice.SourceWitness) || !boundedText(choice.Name, 120) ||
				(choice.Source != "root" && choice.Source != "legacy") || len(choice.Reasons) == 0 || len(choice.Reasons) > 3 || len(choice.Gaps) > 8 {
				return invalid()
			}
			for _, reason := range choice.Reasons {
				if !buildOpaqueID(reason.ID) || !boundedText(reason.Value, rosterstaffing.MaxPurposeRunes) {
					return invalid()
				}
				switch reason.Kind {
				case "declared_purpose", "declared_role", "declared_capability", "current_assignment":
				default:
					return invalid()
				}
			}
			for _, gap := range choice.Gaps {
				if !buildOpaqueID(gap) {
					return invalid()
				}
			}
		}
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > 128*1024 {
		return invalid()
	}
	return nil
}

func staffingState(state string) bool {
	switch state {
	case rosterstaffing.Recommended, rosterstaffing.NeedsChoice, rosterstaffing.NoSuitableMatch, rosterstaffing.Unavailable, rosterstaffing.Stale:
		return true
	default:
		return false
	}
}
