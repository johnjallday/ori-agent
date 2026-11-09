package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// runFolderAgentGroup is the Group stage (PRD FR34–FR36): the folder
// workspace becomes a group in place (same id, name, tile and agent, now its
// Group Manager), then each project folder below it becomes a member
// workspace anchored on that folder, or, when a workspace already anchors
// it, that workspace is moved into the group. Nothing on disk moves. A second
// run of the same plan finds what the first made.
func (b *ServerBuilder) runFolderAgentGroup(ctx context.Context, rec folderagent.Record,
	lines []personalassistant.FolderPlanLine, setLine func(name, state, progress string)) (string, error) {
	line := func(kind string) string {
		for _, l := range lines {
			if l.Kind == kind {
				return l.Name
			}
		}
		return ""
	}
	promoteLine := line(folderagent.FolderPlanPromote)
	setLine(promoteLine, personalassistant.FolderLineWorking, "")
	store, ok := b.workspaceStore.(workspace.PromoteStore)
	mover, movable := b.workspaceStore.(interface {
		MoveWorkspaceFolder(string, string) ([]workspace.MovedWorkspace, error)
	})
	if !ok || !movable || b.sessionHandler == nil {
		setLine(promoteLine, personalassistant.FolderLineFailed, "")
		return personalassistant.FolderStopFailed, errors.New("the workspace store cannot promote")
	}
	if _, err := workspace.PromoteWorkspaceToGroup(store, rec.WorkspaceID); err != nil {
		setLine(promoteLine, personalassistant.FolderLineFailed, "")
		return personalassistant.FolderStopFailed, err
	}
	setLine(promoteLine, personalassistant.FolderLineDone, "")

	root, err := folderAgentWorkspaces{builder: b}.AnchorRoot(rec.WorkspaceID)
	if err != nil {
		return personalassistant.FolderStopNeedsPick, err
	}
	members, _ := folderagent.GroupMembers(rec)
	failed := 0
	for _, member := range members {
		if member.Existing != nil {
			setLine(member.Existing.Name, personalassistant.FolderLineWorking, "")
			if err := b.moveIntoGroup(mover, member.Existing.ID, rec.WorkspaceID); err != nil {
				failed++
				setLine(member.Existing.Name, personalassistant.FolderLineFailed, "")
				logger.Warn("Folder agent could not include a member", logger.Fields{"workspace_id": rec.WorkspaceID, "member": member.Existing.ID, "error": err.Error()})
				continue
			}
			setLine(member.Existing.Name, personalassistant.FolderLineDone, "")
			continue
		}
		name := "Creates " + member.Name + ", linked to its own folder"
		setLine(name, personalassistant.FolderLineWorking, "")
		if err := b.createGroupMember(ctx, mover, rec.WorkspaceID, filepath.Join(root, filepath.FromSlash(member.Path)), member.Name); err != nil {
			failed++
			setLine(name, personalassistant.FolderLineFailed, "")
			logger.Warn("Folder agent could not create a member", logger.Fields{"workspace_id": rec.WorkspaceID, "member": member.Path, "error": err.Error()})
			continue
		}
		setLine(name, personalassistant.FolderLineDone, "")
	}
	setLine(line(folderagent.FolderPlanSoftware), personalassistant.FolderLineDone, "")
	setLine(line(folderagent.FolderPlanMore), personalassistant.FolderLineDone, "")
	if failed > 0 {
		return personalassistant.FolderStopMemberFailed, fmt.Errorf("%d members did not finish", failed)
	}
	return "", nil
}

// createGroupMember creates one member anchored on its project folder under
// the group, with no agent of its own. A folder already anchored by a
// workspace is included instead: left where it is when it already is this
// group's member (a second run), otherwise moved in.
func (b *ServerBuilder) createGroupMember(ctx context.Context, mover interface {
	MoveWorkspaceFolder(string, string) ([]workspace.MovedWorkspace, error)
}, groupID, folder, folderName string) error {
	name := folderName
	for i := 2; i < 100 && b.sessionHandler.WorkspaceNameTaken(ctx, name); i++ {
		name = fmt.Sprintf("%s %d", folderName, i)
	}
	_, err := b.sessionHandler.CreateFolderAgentWorkspace(ctx, sessionhttp.FolderAgentWorkspaceRequest{
		Name: name, AnchorPath: folder, ParentID: groupID,
	})
	var refusal *sessionhttp.FolderAgentRefusal
	if errors.As(err, &refusal) && refusal.Duplicate != nil && strings.TrimSpace(refusal.Duplicate.WorkspaceID) != "" {
		return b.moveIntoGroup(mover, refusal.Duplicate.WorkspaceID, groupID)
	}
	return err
}

// moveIntoGroup moves an existing workspace under the group unless it already
// is there; running work in it refuses the move.
func (b *ServerBuilder) moveIntoGroup(mover interface {
	MoveWorkspaceFolder(string, string) ([]workspace.MovedWorkspace, error)
}, id, groupID string) error {
	ws, err := b.workspaceStore.Get(id)
	if err != nil || ws == nil {
		return fmt.Errorf("workspace %s is unavailable", id)
	}
	if ws.ParentID == groupID {
		return nil
	}
	for _, task := range ws.Tasks {
		if task.Status == workspace.TaskStatusInProgress || task.Status == workspace.TaskStatusWaitingForChoice {
			return workspace.ErrPromoteActiveWork
		}
	}
	_, err = mover.MoveWorkspaceFolder(id, groupID)
	return err
}
