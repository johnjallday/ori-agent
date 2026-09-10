package sessionhttp

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const assistantRemovalReviewRequired = "assistant_program_review_required"

// allowWorkspaceRemoval is shared by all generic delete modes, before they
// unlink sessions, un-nest children, or touch disk. Both stores are evidence:
// neither a DB-only Home nor a folder-only link may bypass the review gate.
func (h *Handler) allowWorkspaceRemoval(w http.ResponseWriter, r *http.Request, root *session.Workspace) bool {
	blocked, state, err := h.workspaceRemovalBlocker(r.Context(), root)
	if err != nil {
		logger.Error("Failed to check workspace removal", logger.Fields{"id": root.ID, "error": err})
		_ = orihttp.RespondInternalError(w, "Workspace removal could not be checked. No changes were made.")
		return false
	}
	if blocked == nil {
		return true
	}

	homeID := blocked.ID
	action := "Review Home removal"
	message := fmt.Sprintf("%q is an Assistant Home. Use Review Home removal to preserve its linked projects.", blocked.Name)
	if state.State == nil && state.Link != nil {
		homeID = state.Link.StationWorkspaceID
		action = "Review disconnect"
		message = fmt.Sprintf("%q is linked to an Assistant Home. Use Review disconnect in that Home before deleting the workspace.", blocked.Name)
	}
	if blocked.ID != root.ID {
		message = fmt.Sprintf("Cannot delete %q while it contains protected assistant workspaces. %s", root.Name, message)
	}
	details := map[string]string{"workspace_id": blocked.ID, "station_workspace_id": homeID, "review_action": action}
	home, err := h.store.GetWorkspace(r.Context(), homeID)
	if err == nil {
		switch home.Status {
		case session.WorkspaceStatusTrashed:
			message += fmt.Sprintf(" Restore %q from Trash first; its assistant relationship is still recorded.", home.Name)
		case session.WorkspaceStatusMissing:
			message += fmt.Sprintf(" Locate or restore the missing Home %q first.", home.Name)
		default:
			message += fmt.Sprintf(" Open %q to review the impact; nothing has been deleted.", home.Name)
			if workspace.IsCanonicalWorkspaceSlug(home.FolderSlug) {
				details["review_home_slug"] = home.FolderSlug
			}
		}
	} else {
		message += " The Assistant Home is unavailable; restore its registration before reviewing removal. Nothing has been deleted."
	}
	_ = orihttp.RespondAPIError(w, http.StatusConflict, orihttp.NewAPIError(assistantRemovalReviewRequired, message).WithDetails(details))
	return false
}

func (h *Handler) workspaceRemovalBlocker(ctx context.Context, root *session.Workspace) (*session.Workspace, workspaceAssistantState, error) {
	var empty workspaceAssistantState
	rows, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, empty, err
	}
	registered := map[string]bool{root.ID: true}
	children := make(map[string][]string)
	for _, row := range rows {
		registered[row.ID] = true
		children[row.ParentID] = append(children[row.ParentID], row.ID)
	}

	// A stale DB parent must not hide something the recursive folder operation
	// will remove (or vice versa). Traverse the union, guarding against cycles.
	disk := make(map[string]*workspace.Workspace)
	if h.workspaceStore != nil {
		ids, listErr := h.workspaceStore.List()
		if listErr != nil {
			return nil, empty, listErr
		}
		for _, id := range ids {
			candidate, getErr := h.workspaceStore.Get(id)
			if getErr != nil {
				return nil, empty, getErr
			}
			disk[id] = candidate
			children[candidate.ParentID] = append(children[candidate.ParentID], id)
		}
	}
	queue := []string{root.ID}
	seen := make(map[string]bool)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		sort.Strings(children[id])
		queue = append(queue, children[id]...)
		var row *session.Workspace
		if id == root.ID {
			row = root
		} else if registered[id] {
			// Listing rows deliberately omit assistant_program_json. Read the
			// full record before deciding that a descendant is unprotected.
			row, err = h.store.GetWorkspace(ctx, id)
			if err != nil {
				return nil, empty, err
			}
		}
		if row != nil {
			state, decodeErr := decodeWorkspaceAssistantState(row.AssistantProgramJSON)
			if decodeErr != nil {
				return nil, empty, decodeErr
			}
			if state.State != nil || state.Link != nil {
				return row, state, nil
			}
		}
		if candidate := disk[id]; candidate != nil {
			state := workspaceAssistantState{State: candidate.GetAssistantProgramState(), Link: candidate.GetAssistantProjectLink()}
			if state.State != nil || state.Link != nil {
				return session.ConvertAgentWorkspace(candidate), state, nil
			}
		}
	}
	return nil, empty, nil
}
