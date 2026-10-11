package sessionhttp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrFolderOfferRosterChanged means the roster a folder offer's review showed
// is no longer the roster that would be created: the blueprint, a saved agent
// of the reviewed name, or the system model changed. Nothing was created.
var ErrFolderOfferRosterChanged = errors.New("the reviewed assistant roster changed")

// FolderOfferRosterReview is the entry assistant a folder offer's review
// promised, in the terms the creation endpoint checks: the revision of the
// blueprint's roster plan, the assistant's name, and whether it is a new saved
// agent ("create") or the user's own saved agent of that name ("reuse").
type FolderOfferRosterReview struct {
	PlanRevision string
	Assistant    string
	Action       string
}

// folderOfferRosterRequest turns a review into the creation endpoint's own
// strict contract: the rename as a Team-step override, and one expectation for
// every agent the blueprint declares. The entry assistant's expectation is the
// reviewed one, so a saved agent that appeared or vanished since the review is
// refused by the endpoint rather than quietly reused or replaced.
func (h *Handler) folderOfferRosterRequest(templateID string, review FolderOfferRosterReview) ([]templateAgentOverride, *templateAgentReview, error) {
	raw, effective, err := h.folderOfferTemplate(templateID, review.Assistant)
	if err != nil {
		return nil, nil, err
	}
	var overrides []templateAgentOverride
	if name := strings.TrimSpace(effective.Agents[0].Name); name != strings.TrimSpace(raw.Agents[0].Name) {
		index := 0
		overrides = []templateAgentOverride{{Index: &index, Name: &name}}
	}
	expectations := make([]templateAgentReviewExpectation, 0, len(effective.Agents))
	for index, spec := range effective.Agents {
		action := "create"
		if _, exists := h.agentStore.GetAgent(spec.Name); exists {
			action = "reuse"
		}
		if index == 0 {
			action = strings.TrimSpace(review.Action)
		}
		expectations = append(expectations, templateAgentReviewExpectation{Index: index, Name: strings.TrimSpace(spec.Name), Action: action})
	}
	return overrides, &templateAgentReview{
		Version: templateAgentReviewVersion, PlanRevision: strings.TrimSpace(review.PlanRevision), Expectations: expectations,
	}, nil
}

// folderOfferTemplate resolves a blueprint and applies the one rename a folder
// offer's review may ask for.
func (h *Handler) folderOfferTemplate(templateID, assistant string) (raw, effective projecttemplates.Template, err error) {
	if h == nil || h.agentStore == nil || strings.TrimSpace(templateID) == "" {
		return raw, effective, ErrFolderOfferRoster
	}
	raw, err = h.resolveProjectTemplate(templateID, "")
	if err != nil || len(raw.Agents) == 0 {
		return raw, effective, ErrFolderOfferRoster
	}
	effective = raw
	if assistant = strings.TrimSpace(assistant); assistant != "" && assistant != strings.TrimSpace(raw.Agents[0].Name) {
		index := 0
		effective, err = applyTemplateAgentOverrides(raw, []templateAgentOverride{{Index: &index, Name: &assistant}})
		if err != nil {
			return raw, effective, ErrFolderOfferRoster
		}
	}
	return raw, effective, nil
}

// FolderOfferAssistant is what attaching a reviewed assistant did.
type FolderOfferAssistant struct {
	Name string
	// Created is true when this call saved a new agent. A reused agent is
	// attached exactly as it was saved.
	Created bool
}

// AttachFolderOfferAssistant gives a workspace that has no entry agent the
// assistant a folder offer's review promised, and makes it the entry agent. It
// is for a workspace the user already had; a new workspace gets its assistant
// from the creation endpoint's own reviewed roster.
//
// It changes nothing it was not asked to: a workspace that already has an entry
// agent is left alone (the same assistant is a replay; another one is
// ErrFolderOfferRosterChanged), and a saved agent of the reviewed name is never
// rewritten. An agent this call created is deleted again if the workspace could
// not be saved, so a failure leaves no orphan; a reused agent is never deleted.
func (h *Handler) AttachFolderOfferAssistant(ctx context.Context, workspaceID, templateID string, review FolderOfferRosterReview) (FolderOfferAssistant, error) {
	_, effective, err := h.folderOfferTemplate(templateID, review.Assistant)
	if err != nil {
		return FolderOfferAssistant{}, err
	}
	if h.store == nil {
		return FolderOfferAssistant{}, errors.New("workspace storage is unavailable")
	}
	spec := effective.Agents[0]
	name := strings.TrimSpace(spec.Name)
	ws, err := h.store.GetWorkspace(ctx, strings.TrimSpace(workspaceID))
	if err != nil || ws == nil {
		return FolderOfferAssistant{}, errors.New("the workspace is unavailable")
	}
	if entry := strings.TrimSpace(currentWorkspaceEntryAgentName(ws)); entry != "" {
		if !strings.EqualFold(entry, name) {
			return FolderOfferAssistant{}, ErrFolderOfferRosterChanged
		}
		// A replay: make sure workspace.json has what the session row has.
		if err := h.syncWorkspacePortableStateToFileStore(ws); err != nil {
			return FolderOfferAssistant{}, err
		}
		return FolderOfferAssistant{Name: entry}, nil
	}

	_, exists := h.agentStore.GetAgent(name)
	if exists != (strings.TrimSpace(review.Action) == "reuse") {
		return FolderOfferAssistant{}, ErrFolderOfferRosterChanged
	}
	created := false
	if !exists {
		config, _ := h.templateAgentCreateConfig(spec)
		if err := h.agentStore.CreateAgent(name, config); err != nil {
			return FolderOfferAssistant{}, err
		}
		created = true
	}
	undo := func() {
		if created {
			if err := h.agentStore.DeleteAgent(name); err != nil {
				logger.Warn("An assistant created for a folder setup could not be removed after the setup failed",
					logger.Fields{"workspace_id": workspaceID, "agent": name, "error": err.Error()})
			}
		}
	}
	attachWorkspaceSpecialist(ws, name)
	setWorkspaceEntryAgent(ws, name)
	ws.UpdatedAt = time.Now()
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		undo()
		return FolderOfferAssistant{}, err
	}
	if err := h.syncWorkspacePortableStateToFileStore(ws); err != nil {
		// The session row has the assistant and workspace.json does not. The
		// agent is kept: a retry finds it as this workspace's entry agent and
		// only has the copy left to make.
		return FolderOfferAssistant{}, err
	}
	h.claimUnassignedTasksForEntryAgentLogged(ws.ID)
	return FolderOfferAssistant{Name: name, Created: created}, nil
}

// FolderOfferAssessment is the first task a recipe-run folder setup seeds.
type FolderOfferAssessment struct {
	Recipe        string
	RecipeVersion int
	Description   string
	Details       string
}

// SeedFolderOfferAssessment gives a workspace its one first task under a host
// recipe, through the same first-task lifecycle every shown folder uses. A
// workspace that already has a first task keeps it exactly as it is, whatever
// it was seeded with and whatever state it is in: seeded is then false and
// taskID names the task that was found. Nothing is started.
func (h *Handler) SeedFolderOfferAssessment(workspaceID string, assessment FolderOfferAssessment) (taskID string, seeded bool, err error) {
	if h == nil {
		return "", false, errors.New("workspace tasks are unavailable")
	}
	store := h.taskMutationStore()
	id := strings.TrimSpace(workspaceID)
	if store == nil || id == "" || strings.TrimSpace(assessment.Recipe) == "" || strings.TrimSpace(assessment.Description) == "" {
		return "", false, errors.New("workspace tasks are unavailable")
	}
	err = store.Update(id, func(ws *agentworkspace.Workspace) error {
		if existing := personalassistant.FindFolderFirstTask(ws); existing != nil {
			taskID = existing.ID
			return nil
		}
		task := agentworkspace.Task{
			WorkspaceID: id, Description: assessment.Description, Details: assessment.Details,
			Priority: 1, Status: agentworkspace.TaskStatusPending,
			Context: map[string]any{
				taskContextTemplateID:          folderFirstTaskTemplateID,
				taskContextTemplateStarterTask: true,
			},
		}
		ws.ApplyEntryAgentDefault(&task)
		added, addErr := ws.AddRecipeTask(task, assessment.Recipe, assessment.RecipeVersion)
		if addErr != nil {
			return addErr
		}
		taskID, seeded = added, true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return taskID, seeded, nil
}
