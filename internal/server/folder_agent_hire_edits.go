package server

import (
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
)

type folderAgentAssistantEdit struct {
	roleID string
	value  folderagent.HireEdit
}

// Confirmed settings are addressed by the original Home step and role, never
// by a mutable agent name. No settings are applied to a bind or saved profile.
func folderAgentHireEdits(rec folderagent.Record, home folderlook.Candidate) (map[string]folderagent.HireEdit, *folderAgentAssistantEdit) {
	roles := map[string]folderagent.HireEdit{}
	if len(rec.HireEdits) == 0 || rec.Setup == nil {
		return roles, nil
	}
	plan := folderagent.BuildPlan(rec, rec.Setup.Selected)
	step := ""
	for _, candidate := range plan.Steps {
		if candidate.CandidateID == home.ID {
			step = candidate.Handle
			break
		}
	}
	var assistant *folderAgentAssistantEdit
	for _, hire := range plan.Hires {
		if hire.Step != step || hire.RoleID == "" {
			continue
		}
		edit, ok := rec.HireEdits[hire.Identity()]
		if !ok {
			continue
		}
		if hire.Assistant {
			assistant = &folderAgentAssistantEdit{roleID: hire.RoleID, value: edit}
		} else {
			roles[hire.RoleID] = edit
		}
	}
	return roles, assistant
}
