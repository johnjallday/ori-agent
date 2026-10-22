package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// hireEntryPoint labels a create made by a hire from the Agents page.
const hireEntryPoint = "agents_hire"

// HireWorkspaceRequest is the workspace an agent hired from the Agents page
// gets when its role has a project blueprint (agents-hire-with-a-brief): the
// blueprint, the hire's brief as its goal, and the hired agent — which already
// exists — in the blueprint's primary role. Every other declared role stays
// open; nothing else is created.
type HireWorkspaceRequest struct {
	Name       string
	TemplateID string
	Mission    string
	AgentName  string
}

// ErrHireWorkspaceNameTaken is a workspace name whose folder is already in use.
var ErrHireWorkspaceNameTaken = errors.New("a workspace folder with that name already exists")

// CreateHireWorkspace creates the hire's workspace through the ordinary
// creation pipeline — validation, blueprint scaffolding, provenance, the goal
// at create, the workspace.created event — by posting, in process, the strict
// team request the Create Workspace wizard would: the blueprint's current plan
// revision and one role_staffing entry that assigns the hired agent to the
// primary role. It returns the new workspace's id.
func (h *Handler) CreateHireWorkspace(ctx context.Context, req HireWorkspaceRequest) (string, error) {
	if h == nil {
		return "", errors.New("workspace creation is unavailable")
	}
	name, templateID, agent := strings.TrimSpace(req.Name), strings.TrimSpace(req.TemplateID), strings.TrimSpace(req.AgentName)
	if name == "" || templateID == "" || agent == "" {
		return "", errors.New("a hire's workspace needs a name, a blueprint and the hired agent")
	}
	tpl, err := h.resolveProjectTemplate(templateID, "")
	if err != nil {
		return "", fmt.Errorf("blueprint %q: %w", templateID, err)
	}
	roles := projecttemplates.AgentRoleIDs(tpl.Agents)
	if len(roles) == 0 {
		return "", fmt.Errorf("blueprint %q declares no role for the hire", templateID)
	}
	intent, err := json.Marshal(workspaceTeamIntent{
		Version: 1, Mode: workspaceTeamModeStaffed, PlanRevision: h.buildTemplateAgentPlan(tpl).Revision,
	})
	if err != nil {
		return "", err
	}
	mission := strings.TrimSpace(req.Mission)
	create := createWorkspaceRequest{
		Name: name, TemplateID: templateID, EntryPoint: hireEntryPoint, Mission: &mission,
		TeamIntent:   intent,
		RoleStaffing: []roleStaffingInput{{RoleID: roles[0], Mode: roleStaffingModeAssign, Name: agent}},
	}
	body, err := json.Marshal(create)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/workspaces", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, request)
	if recorder.Code != http.StatusCreated {
		if recorder.Code == http.StatusConflict && createConflictType(recorder.Body.Bytes()) == "folder_slug" {
			return "", ErrHireWorkspaceNameTaken
		}
		return "", fmt.Errorf("the %s workspace could not be created (%d): %s", tpl.Name, recorder.Code, createFailureMessage(recorder.Body.Bytes()))
	}
	var response struct {
		Folder struct {
			ID string `json:"id"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || strings.TrimSpace(response.Folder.ID) == "" {
		return "", errors.New("the hire's workspace creation returned no workspace id")
	}
	return response.Folder.ID, nil
}

// DeleteHireWorkspace removes a workspace a hire created but could not finish,
// with its sessions, through the same guarded delete the UI uses.
func (h *Handler) DeleteHireWorkspace(ctx context.Context, workspaceID string) error {
	if h == nil || strings.TrimSpace(workspaceID) == "" {
		return errors.New("workspace deletion is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		"/api/workspaces/"+url.PathEscape(workspaceID)+"?confirm=true&delete_sessions=true", nil)
	if err != nil {
		return err
	}
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, request)
	if recorder.Code < 200 || recorder.Code > 299 {
		return fmt.Errorf("the hire's workspace could not be removed (%d): %s", recorder.Code, createFailureMessage(recorder.Body.Bytes()))
	}
	return nil
}
