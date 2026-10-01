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
	description, details := personalassistant.FolderFirstTask(req.Shape)
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
