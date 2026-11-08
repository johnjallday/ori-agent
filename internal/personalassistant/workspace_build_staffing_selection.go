package personalassistant

import "fmt"

// BuildStaffingSelection is server-held selected intent, separate from advice.
// It carries no binding permission by itself; the existing creation review and
// operation must still pin and confirm the exact draft before effects.
type BuildStaffingSelection struct {
	Version          int                         `json:"version"`
	OwnerUserID      string                      `json:"owner_user_id"`
	ContextDigest    string                      `json:"context_digest"`
	TemplateRevision string                      `json:"template_revision"`
	Choices          []BuildStaffingSelectedRole `json:"choices"`
}

type BuildStaffingSelectedRole struct {
	RoleID        string `json:"role_id"`
	Scope         string `json:"scope"`
	Handle        string `json:"handle"`
	Name          string `json:"name"`
	SourceWitness string `json:"source_witness"`
}

func (s *BuildStaffingSelection) validate() error {
	if s == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("%w: selected staffing", ErrWorkspaceBuildInvalid) }
	if s.Version != 1 || !buildOpaqueID(s.OwnerUserID) || len(s.ContextDigest) != 64 || len(s.TemplateRevision) != 64 || len(s.Choices) == 0 || len(s.Choices) > 16 {
		return invalid()
	}
	roles, names := map[string]bool{}, map[string]bool{}
	for _, choice := range s.Choices {
		if !buildOpaqueID(choice.RoleID) || !buildOpaqueID(choice.Handle) || !buildOpaqueID(choice.SourceWitness) ||
			choice.Name == "" || !boundedText(choice.Name, 120) || roles[choice.RoleID] || names[choice.Name] ||
			(choice.Scope != "workspace" && choice.Scope != "project" && choice.Scope != "home") {
			return invalid()
		}
		roles[choice.RoleID], names[choice.Name] = true, true
	}
	return nil
}
