package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// folderFirstTaskSeeder gives a workspace the setup journey created the same
// first task a generic project gets when its folder is linked. It goes through
// the ordinary starter-task seeding, which skips a workspace that already has the
// task, so a replayed resolve adds nothing.
type folderFirstTaskSeeder struct{ builder *ServerBuilder }

var _ personalassistant.FolderFirstTaskSeeder = folderFirstTaskSeeder{}

func (s folderFirstTaskSeeder) SeedFirstTask(_ context.Context, req personalassistant.FolderFirstTaskRequest) (personalassistant.FolderReceiptRow, error) {
	if s.builder == nil || s.builder.sessionHandler == nil {
		return personalassistant.FolderReceiptRow{}, errors.New("workspace tasks are unavailable")
	}
	description, details := personalassistant.FolderFirstTaskFor(req.Shape, req.Brief)
	handler := s.builder.sessionHandler
	if _, err := handler.SeedStarterTasks(req.WorkspaceID, projecttemplates.Template{
		ID:           "folder-digest",
		StarterTasks: []projecttemplates.StarterTask{{Description: description, Details: details}},
	}); err != nil {
		return personalassistant.FolderReceiptRow{}, err
	}
	// The row is read back from the workspace, so it says what the task really is
	// and whether it will start on its own.
	rows, err := handler.FolderOfferWorkspaceReceipt(req.WorkspaceID, true)
	if err != nil {
		return personalassistant.FolderReceiptRow{}, err
	}
	for _, row := range rows {
		if row.Kind == "task" {
			return row, nil
		}
	}
	return personalassistant.FolderReceiptRow{}, errors.New("the first task was not found after seeding")
}

// folderFirstLookStarter starts a hired agent's first look under the hire's
// consent. It starts only a look that reads as one Start first look would start
// now, through the same one-start path; a look whose agent has no usable model,
// or that waits on anything else, is left as it is with its reason, which the
// card shows with its repair.
type folderFirstLookStarter struct{ builder *ServerBuilder }

var _ personalassistant.FolderFirstLookStarter = folderFirstLookStarter{}

func (s folderFirstLookStarter) StartFirstLook(ctx context.Context, _ string, workspaceID string) (personalassistant.FolderFirstLookStart, error) {
	b := s.builder
	if b == nil || b.sessionHandler == nil {
		return personalassistant.FolderFirstLookStart{}, errors.New("workspace tasks are unavailable")
	}
	view, ok := b.folderFirstTaskView(ctx, workspaceID, b.assistantModelConfigured(ctx))
	if !ok {
		return personalassistant.FolderFirstLookStart{Reason: "no_first_task"}, nil
	}
	if view.State != personalassistant.FolderFirstTaskSeeded {
		return personalassistant.FolderFirstLookStart{Reason: "not_pending", TaskID: view.TaskID}, nil
	}
	if !view.CanStart {
		reason := view.Reason
		if reason == "" {
			reason = "not_ready"
		}
		return personalassistant.FolderFirstLookStart{Reason: reason, TaskID: view.TaskID}, nil
	}
	start := b.sessionHandler.StartFolderFirstTask(ctx, workspaceID)
	return personalassistant.FolderFirstLookStart{Started: start.Started, Reason: start.Reason, TaskID: start.TaskID}, nil
}
