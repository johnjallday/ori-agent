package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectinspect"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/softwareassessment"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Where the user goes to repair what an assessment needs.
const (
	assessmentShowFolderRoute = "/?panel=today&folder=show"
	// An assistant's own model is chosen where assistants are edited.
	assessmentAgentsRoute = "/agents"
)

// softwareAssessmentRunner is the host recipe behind a software project's first
// assessment. At the moment of the run it re-checks that the workspace is
// still bound to the folder the user confirmed, reads that folder through the
// bounded inspector, and lays what it read out as the one message a model is
// given. It hands the model nothing else: no path, no tool, no agent prompt.
type softwareAssessmentRunner struct{ builder *ServerBuilder }

var _ workspace.RecipeRunner = softwareAssessmentRunner{}

// project resolves the task's workspace and the folder it is bound to. The
// folder is the one the user confirmed, or there is no folder: a missing or
// replaced directory is never used in its place. It opens no file.
func (r softwareAssessmentRunner) project(task workspace.Task) (*workspace.Workspace, string, *workspace.TaskBlockedError) {
	b := r.builder
	if b == nil || b.workspaceFileStore == nil {
		return nil, "", &workspace.TaskBlockedError{ReasonCode: "workspace_unavailable", Reason: "The workspace store is unavailable. Nothing was read or sent to a model."}
	}
	ws, err := b.workspaceFileStore.Get(strings.TrimSpace(task.WorkspaceID))
	if err != nil || ws == nil || ws.GetStatus() != workspace.StatusActive {
		return nil, "", &workspace.TaskBlockedError{
			ReasonCode: "workspace_unavailable",
			Reason:     "This workspace is not available, so its project was not read. Nothing was sent to a model.",
			Question:   "Open the workspace and try again.",
		}
	}
	folder, bound, err := workspace.SoftwareProjectFolder(ws, projectlibrary.DirectoryIdentity)
	if !bound || err != nil {
		return nil, "", assessmentFolderUnavailable()
	}
	return ws, folder, nil
}

// Ready implements workspace.RecipeRunner.
func (r softwareAssessmentRunner) Ready(task workspace.Task) *workspace.TaskBlockedError {
	_, _, blocked := r.project(task)
	return blocked
}

// Brief implements workspace.RecipeRunner.
func (r softwareAssessmentRunner) Brief(ctx context.Context, task workspace.Task, contextWindow int) (workspace.RecipeBrief, error) {
	ws, folder, blocked := r.project(task)
	if blocked != nil {
		return workspace.RecipeBrief{}, blocked
	}
	evidence, err := projectinspect.Inspect(ctx, folder, projectinspect.Options{})
	if err != nil {
		return workspace.RecipeBrief{}, assessmentFolderUnavailable()
	}
	// Checked again after the read: a directory swapped in while it was being
	// read is not the project this assessment is about.
	if again, stillBound, err := workspace.SoftwareProjectFolder(ws, projectlibrary.DirectoryIdentity); !stillBound || err != nil || again != folder {
		return workspace.RecipeBrief{}, assessmentFolderUnavailable()
	}
	if len(evidence.Documents) == 0 {
		return workspace.RecipeBrief{}, &workspace.TaskBlockedError{
			ReasonCode: "nothing_to_assess",
			Reason:     "Ori found no README, instructions or build files it could read in this project, so there is nothing to base an assessment on. Nothing was sent to a model.",
			Question:   "Add a README or build file to the project, then try again.",
		}
	}

	// What the owner asked the hired assistant to do is the workspace's goal:
	// the brief from its hire card, or whatever they changed it to since.
	intent := strings.TrimSpace(ws.Mission)
	brief := softwareassessment.BuildWithIntent(assessmentProjectName(ws), intent, evidence, softwareassessment.TierFull)
	if !brief.Fits(contextWindow) {
		brief = softwareassessment.BuildWithIntent(assessmentProjectName(ws), intent, evidence, softwareassessment.TierCompact)
	}
	if !brief.Fits(contextWindow) {
		return workspace.RecipeBrief{}, &workspace.TaskBlockedError{
			ReasonCode: "context_too_small",
			Reason:     "This assistant's model has too small a context window for even a short reading of the project. Nothing was sent to it.",
			Question:   "Choose a model with a larger context window, then try again.",
			Repair:     &workspace.TaskRepairAction{Code: "choose_agent_model", Label: "Choose a model", URL: assessmentAgentsRoute},
		}
	}
	return workspace.RecipeBrief{System: brief.System, User: brief.User, State: brief}, nil
}

// Finish implements workspace.RecipeRunner.
func (softwareAssessmentRunner) Finish(brief workspace.RecipeBrief, reply string) (string, error) {
	state, ok := brief.State.(softwareassessment.Brief)
	if !ok {
		return "", errors.New("the assessment's brief was lost")
	}
	return softwareassessment.Finish(reply, state)
}

func assessmentFolderUnavailable() *workspace.TaskBlockedError {
	return &workspace.TaskBlockedError{
		ReasonCode: "project_folder_unavailable",
		Reason:     "The project folder this workspace was set up for is missing, or a different folder is now in its place. Nothing was read and nothing was sent to a model.",
		Question:   "Put the folder back where it was, or show Ori the project again.",
		Repair:     &workspace.TaskRepairAction{Code: "show_folder", Label: "Show the folder", URL: assessmentShowFolderRoute},
	}
}

// softwareAssessmentReadiness is what a software project's first assessment
// needs and can use, read now: the project folder, the assistant's model, and
// GitHub, which is optional. It opens no file and calls no model or network
// service, and it says only what Ori has a reader for: a model that is set is
// "set, not checked", never "working".
func (b *ServerBuilder) softwareAssessmentReadiness(ctx context.Context, view personalassistant.FolderFirstTaskView) []personalassistant.FolderPrerequisite {
	blockedBy := ""
	if !view.CanStart && view.State == personalassistant.FolderFirstTaskSeeded || view.State == personalassistant.FolderFirstTaskFailed && !view.CanRetry {
		blockedBy = view.Reason
	}

	folder := personalassistant.FolderPrerequisite{
		ID: "folder", Label: "Project folder", Required: true, Source: "this workspace's linked folder",
		State:  personalassistant.FolderPrerequisiteVerified,
		Detail: "The folder this workspace was set up for is where it was. It is read only when the assessment runs.",
	}
	if b.workspaceFileStore != nil {
		if ws, err := b.workspaceFileStore.Get(view.WorkspaceID); err != nil || ws == nil {
			folder.State, folder.Detail = personalassistant.FolderPrerequisiteUnknown, "Ori could not read this workspace's linked folder just now."
		} else if _, bound, err := workspace.SoftwareProjectFolder(ws, projectlibrary.DirectoryIdentity); !bound || err != nil {
			folder.State, folder.Route = personalassistant.FolderPrerequisiteAbsent, assessmentShowFolderRoute
			folder.Detail = "The folder is missing, or a different folder is now in its place. Nothing is read until the same folder is back."
		}
	}

	who := strings.TrimSpace(view.Agent)
	model := personalassistant.FolderPrerequisite{ID: "model", Label: "Assistant's model", Required: true, Source: "the assistant's saved settings"}
	switch {
	case who == "":
		// The repair is assigning an assistant in the workspace, which the view's
		// own repair action offers. A model link on this row would name the
		// wrong fix.
		model.State, model.Detail = personalassistant.FolderPrerequisiteAbsent, "The assessment has no assistant, so there is no model to run it."
	case blockedBy == personalassistant.FolderFirstTaskReasonNoModel:
		model.State, model.Route = personalassistant.FolderPrerequisiteAbsent, assessmentAgentsRoute
		model.Detail = who + " has no model chosen."
	case blockedBy == personalassistant.FolderFirstTaskReasonModelUnavailable:
		model.State, model.Route = personalassistant.FolderPrerequisiteAbsent, assessmentAgentsRoute
		model.Detail = "The model " + who + " is set to use is not set up on this computer."
	case blockedBy == personalassistant.FolderFirstTaskReasonModelUnsupported:
		model.State, model.Route = personalassistant.FolderPrerequisiteAbsent, assessmentAgentsRoute
		model.Detail = "The model " + who + " is set to use cannot be held to a read-only assessment. Another model is needed for it."
	default:
		model.State = personalassistant.FolderPrerequisiteConfigured
		model.Detail = who + " has a model that can be held to a read-only assessment. It is not called until you start one, so this does not show that it answers."
		if provider, name, found := b.sessionHandler.SavedAgentModel(who); found && strings.TrimSpace(name) != "" {
			model.Detail = who + " is set to use " + name
			if strings.TrimSpace(provider) != "" {
				model.Detail += " through " + provider
			}
			model.Detail += ", which can be held to a read-only assessment. It is not called until you start one, so this does not show that it answers."
		}
	}

	github := personalassistant.FolderPrerequisite{ID: "github", Label: "GitHub", Source: "Ori's saved connections", Route: softwareGitHubRoute}
	host := &folderSetupHost{builder: b}
	switch connected, err := host.githubConnected(ctx); {
	case err != nil:
		github.State = personalassistant.FolderPrerequisiteUnknown
		github.Detail = "Ori could not read whether GitHub is connected. The assessment does not use it."
	case connected:
		github.State, github.Route = personalassistant.FolderPrerequisiteConfigured, ""
		github.Detail = "A GitHub connection is saved. The assessment does not use it, and a saved connection does not show that this repository can be reached."
	default:
		github.State = personalassistant.FolderPrerequisiteAbsent
		github.Detail = "GitHub is not connected. The assessment works without it; a command-line login on this computer is not Ori's connection."
	}
	return []personalassistant.FolderPrerequisite{folder, model, github}
}

// assessmentProjectName is the linked folder's own name, else the workspace's.
func assessmentProjectName(ws *workspace.Workspace) string {
	if primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); strings.TrimSpace(primary) != "" {
		if ref, err := ws.GetDirectoryReference(primary); err == nil && ref != nil && strings.TrimSpace(ref.Name) != "" {
			return strings.TrimSpace(ref.Name)
		}
	}
	return strings.TrimSpace(ws.Name)
}
