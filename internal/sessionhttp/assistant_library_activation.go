package sessionhttp

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

// Creator review and commit use the same canonical workspace store and folder
// owner view as normal guided project setup. A browser supplies only an entry
// ID and a project-file name seen during an authorized metadata scan: the
// resolver derives the exact folder from the current Home grant each time.
func (h *Handler) assistantLibraryActivator() *projectlibrary.ActivationService {
	if h == nil || h.workspaceTaskStore == nil || h.workspaceStore == nil || h.assistantLibraryRoots == nil ||
		h.installedPluginLister == nil || h.groupRequirements == nil {
		return nil
	}
	owners := libraryFolderOwners{Store: h.workspaceTaskStore, folders: h.workspaceStore}
	inspector := projectlibrary.NewActivationInspector(h.assistantLibraryStore(), h.assistantLibraryRoots,
		h.installedPluginLister, owners)
	return projectlibrary.NewActivationService(inspector, func(resolver projectconnection.SelectionResolver) projectlibrary.ActivationCreator {
		creator := projectconnection.NewService(owners, resolver)
		creator.SetGroupRequirementService(h.groupRequirements)
		creator.SetCreatedWorkspaceRecorder(h.allowlistLocallyCreatedWorkspace)
		return creator
	})
}

type assistantActivationReviewRequest struct {
	IfRevision    int64  `json:"if_revision"`
	ProjectFile   string `json:"project_file"`
	WorkspaceName string `json:"workspace_name"`
}

func (h *Handler) ReviewAssistantLibraryActivation(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) {
		return
	}
	service := h.assistantLibraryActivator()
	if service == nil {
		_ = orihttp.RespondConflict(w, "The reviewed project creator is unavailable")
		return
	}
	var request assistantActivationReviewRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	review, err := service.Review(r.Context(), scope, r.PathValue("entryID"), request.IfRevision,
		request.ProjectFile, request.WorkspaceName)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, review)
}

func (h *Handler) CommitAssistantLibraryActivation(w http.ResponseWriter, r *http.Request) {
	scope, _, ok := h.assistantLibraryScope(w, r)
	if !ok || !h.libraryEntryExists(w, scope, r.PathValue("entryID")) {
		return
	}
	service := h.assistantLibraryActivator()
	if service == nil {
		_ = orihttp.RespondConflict(w, "The reviewed project creator is unavailable")
		return
	}
	var request libraryCommitRequest
	if !decodeLibraryAction(w, r, &request) {
		return
	}
	if !request.Confirm {
		_ = orihttp.RespondBadRequest(w, "Confirm the reviewed project setup")
		return
	}
	result, err := service.Commit(r.Context(), scope, r.PathValue("entryID"), request.ReviewToken,
		request.IdempotencyKey)
	if err != nil {
		respondLibraryActionError(w, err)
		return
	}
	_ = orihttp.RespondSuccess(w, result)
}
