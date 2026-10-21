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

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FolderOfferWorkspaceRequest is the workspace the assistant sets up for a
// "show me a folder" project offer the user confirmed: the folder's name and
// the shape's blueprint (blank when empty). The offer id marks the workspace
// as this offer's, which the folder link afterwards verifies.
type FolderOfferWorkspaceRequest struct {
	Name       string
	TemplateID string
	OfferID    string
	ParentID   string
	// Mission, when set, is the goal the user gave the hire. It is created as
	// the workspace goal through the same goal-at-create path as the Create
	// Workspace modal, in place of the blueprint's own.
	Mission string
	// Roster, when set, holds the creation to the assistant roster a review
	// showed. The endpoint then creates the whole reviewed roster or nothing:
	// a roster that changed since the review is ErrFolderOfferRosterChanged.
	Roster *FolderOfferRosterReview
	// Group, when set, makes the workspace a group, created with the Group
	// Manager a review showed. A group has no blueprint, so TemplateID must be
	// empty; a roster that changed since the review is
	// ErrFolderOfferRosterChanged.
	Group *FolderOfferGroupRoster
}

// FolderOfferGroupRoster is the Group Manager a new group is created with, in
// the terms the creation endpoint checks: the revision of the group's roster
// plan, the manager's name, and whether it is a new saved agent ("create") or
// the user's own saved agent of that name ("reuse").
type FolderOfferGroupRoster struct {
	PlanRevision string
	Manager      string
	Action       string
}

// WorkspaceNameTaken reports whether creating a workspace or group of this name
// would be refused because its folder name is already in use. It applies the
// occupancy the create path's own conflict uses, and changes nothing.
func (h *Handler) WorkspaceNameTaken(ctx context.Context, name string) bool {
	slug := workspaceSlugFor(strings.TrimSpace(name))
	return h != nil && slug != "" && h.workspaceSlugOccupied(ctx, slug, "")
}

// FolderOfferGroup reads the roster a new group of this name would get, the
// same way the Create Workspace Team step reads it. Reading it creates no agent
// and no workspace.
func (h *Handler) FolderOfferGroup(name string) (FolderOfferGroupRoster, error) {
	name = strings.TrimSpace(name)
	if h == nil || h.agentStore == nil || name == "" {
		return FolderOfferGroupRoster{}, ErrFolderOfferRoster
	}
	plan := h.buildTemplateAgentPlan(groupRosterTemplate(name))
	if len(plan.Agents) != 1 || strings.TrimSpace(plan.Agents[0].Name) == "" || strings.TrimSpace(plan.Revision) == "" {
		return FolderOfferGroupRoster{}, ErrFolderOfferRoster
	}
	manager := plan.Agents[0]
	return FolderOfferGroupRoster{PlanRevision: plan.Revision, Manager: strings.TrimSpace(manager.Name), Action: manager.Action}, nil
}

// CreateFolderOfferWorkspace creates the workspace through the ordinary
// creation pipeline — validation, blueprint scaffolding and roster, provenance,
// the allowlist, the workspace.created event — by posting the same request
// the Create Workspace modal would, in process. It returns the new workspace's
// id. The folder itself is linked by the caller afterwards; no path is here.
func (h *Handler) CreateFolderOfferWorkspace(ctx context.Context, req FolderOfferWorkspaceRequest) (string, error) {
	if h == nil {
		return "", errors.New("workspace creation is unavailable")
	}
	name := strings.TrimSpace(req.Name)
	offerID := strings.TrimSpace(req.OfferID)
	if name == "" || offerID == "" {
		return "", errors.New("a folder offer workspace needs a name and an offer id")
	}
	create := createWorkspaceRequest{
		Name: name, TemplateID: strings.TrimSpace(req.TemplateID), ParentID: req.ParentID,
		EntryPoint: folderDigestEntryPoint, FolderOfferID: offerID,
	}
	if mission := strings.TrimSpace(req.Mission); mission != "" {
		create.Mission = &mission
	}
	if req.Roster != nil {
		overrides, review, err := h.folderOfferRosterRequest(create.TemplateID, *req.Roster)
		if err != nil {
			return "", err
		}
		create.TemplateAgentOverrides, create.TemplateAgentReview = overrides, review
	}
	if req.Group != nil {
		// The group's own reviewed roster contract: exactly its Group Manager,
		// as the review saw it, or nothing is created.
		if create.TemplateID != "" || req.Roster != nil || strings.TrimSpace(req.Group.Manager) == "" {
			return "", errors.New("a folder offer group has a Group Manager and no blueprint")
		}
		enabled := true
		create.Kind, create.GroupRoster, create.CreateTemplateAgents = string(session.WorkspaceKindGroup), true, &enabled
		create.TemplateAgentReview = &templateAgentReview{
			Version: templateAgentReviewVersion, PlanRevision: strings.TrimSpace(req.Group.PlanRevision),
			Expectations: []templateAgentReviewExpectation{{Index: 0, Name: strings.TrimSpace(req.Group.Manager), Action: strings.TrimSpace(req.Group.Action)}},
		}
	}
	body, err := json.Marshal(create)
	if err != nil {
		return "", err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/workspaces", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.HandleWorkspaces(recorder, httpRequest)
	if recorder.Code != http.StatusCreated {
		if (req.Roster != nil || req.Group != nil) && recorder.Code == http.StatusConflict && createConflictType(recorder.Body.Bytes()) == "template_agent_plan" {
			return "", ErrFolderOfferRosterChanged
		}
		return "", fmt.Errorf("folder offer workspace creation failed (%d): %s", recorder.Code, createFailureMessage(recorder.Body.Bytes()))
	}
	var response struct {
		Folder struct {
			ID string `json:"id"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || strings.TrimSpace(response.Folder.ID) == "" {
		return "", errors.New("folder offer workspace creation returned no workspace id")
	}
	return response.Folder.ID, nil
}

// FolderOfferWorkspaceReceipt reads the canonical workspace AFTER the shown
// folder has been linked and its first task seeded. Nothing in this receipt is
// inferred from the blueprint request or the browser's plan.
func (h *Handler) FolderOfferWorkspaceReceipt(workspaceID string, created bool) ([]personalassistant.FolderReceiptRow, error) {
	if h == nil || h.workspaceStore == nil {
		return nil, errors.New("workspace receipt is unavailable")
	}
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil || strings.TrimSpace(ws.FolderSlug) == "" {
		return nil, errors.New("created workspace is unavailable for its receipt")
	}
	detail := ""
	if !created {
		detail = "already set up"
	}
	rows := []personalassistant.FolderReceiptRow{{
		Kind: "workspace", Name: ws.Name, Detail: detail,
		Route: "/workspaces/" + url.PathEscape(ws.FolderSlug),
	}}
	primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
	if strings.TrimSpace(primary) != "" {
		if ref, err := ws.GetDirectoryReference(primary); err == nil && ref != nil {
			rows = append(rows, personalassistant.FolderReceiptRow{
				Kind: "folder", Name: ref.Name, Detail: "linked as primary",
			})
		}
	} else if name := linkedFolderName(ws); name != "" {
		// A project the setup journey connected is linked through its project
		// entry rather than a primary directory. The row names the folder, never
		// its path.
		rows = append(rows, personalassistant.FolderReceiptRow{
			Kind: "folder", Name: name, Detail: "linked where it is",
		})
	}
	if provenance := ws.GetTemplateProvenance(); provenance != nil && provenance.TemplateID != "" {
		rows = append(rows, personalassistant.FolderReceiptRow{
			Kind: "blueprint", Name: provenance.TemplateName,
		})
		for _, instance := range ws.AgentInstances {
			if strings.TrimSpace(instance.Name) != "" {
				rows = append(rows, personalassistant.FolderReceiptRow{
					Kind: "agent", Name: instance.Name, Detail: agentReceiptDetail(instance),
				})
			}
		}
	}
	if task := personalassistant.FindFolderFirstTask(ws); task != nil {
		// Say only what will really happen: the task starts on one click when it
		// has an agent and no setup dialog is still to open. Nothing starts it
		// without that click, so no model tokens are spent until the user asks.
		rows = append(rows, personalassistant.FolderReceiptRow{
			Kind: "task", Name: task.Description,
			Detail: personalassistant.FolderFirstTaskRowDetail(
				personalassistant.FolderFirstTaskStateOf(task), h.folderFirstTaskStartsOnClick(ws), task.Result),
		})
	}
	return rows, nil
}

// agentReceiptDetail says how an agent came to be in the workspace: "joined"
// for an agent the user already had (the Home's shared assistant), "added" for
// one this setup created. The role is named only when the name does not say it.
func agentReceiptDetail(instance workspace.AgentInstance) string {
	verb := "added"
	if instance.RoleSource == workspace.RoleSourceAssigned {
		verb = "joined"
	}
	role := strings.TrimSpace(instance.Role)
	if role == "" || strings.EqualFold(role, strings.TrimSpace(instance.Name)) {
		return verb
	}
	return role + ", " + verb
}

// createConflictType reads which kind of conflict refused a create.
func createConflictType(body []byte) string {
	var payload struct {
		Conflict struct {
			Type string `json:"type"`
		} `json:"conflict"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Conflict.Type)
}

// createFailureMessage pulls the user-facing error out of a refused create,
// for the log line; it is never shown as-is.
func createFailureMessage(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
		return strings.TrimSpace(payload.Error)
	}
	return strings.TrimSpace(string(body))
}
