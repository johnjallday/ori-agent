package agenthttp

import (
	"context"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type folderPlacementOption struct {
	Operation     string `json:"operation"`
	DestinationID string `json:"destination_id,omitempty"`
	Label         string `json:"label"`
}

func (h *HomeAssistantAskHandler) resolveFolderReviewPlacement(w http.ResponseWriter, r *http.Request, target foldercontext.Target, refs *HomeAssistantRouteContext, operation, destinationID, selectionID, candidateID, currentOfferID string) (context.Context, bool) {
	ctx := r.Context()
	if refs == nil && operation == "" && destinationID == "" {
		// Legacy callers can refresh an exact canonical candidate, but lack the
		// navigation authority to select a different operation/destination.
		if currentOfferID != "" {
			prior, err := h.FolderSetups.ReadReview(ctx, target, currentOfferID)
			if err != nil {
				writeFolderReviewError(w, err)
				return ctx, false
			}
			if prior != nil && prior.CandidateID == candidateID {
				return personalassistant.WithFolderReviewPlacement(ctx, personalassistant.FolderReviewPlacement{Operation: prior.Operation, DestinationID: prior.RequestedDestinationID}), true
			}
		}
		return ctx, true
	} // legacy explicit review
	if operation != "" && operation != personalassistant.FolderOperationCreate && operation != personalassistant.FolderOperationSupport {
		writeFolderError(w, foldercontext.ErrInvalid)
		return ctx, false
	}
	if refs == nil || refs.ContextVersion != 1 || refs.Origin != "personal_assistant_panel" {
		writeFolderError(w, foldercontext.ErrInvalid)
		return ctx, false
	}
	work, err := h.resolvePersonalAssistantContext(ctx)
	turn := h.bindWorkspaceTurn(ctx, "", refs, work)
	if err != nil || turn == nil || h.revalidateWorkspaceTurn(ctx, turn) != nil {
		writeFolderError(w, foldercontext.ErrInvalid)
		return ctx, false
	}
	if currentOfferID != "" && operation == "" {
		// Refreshing this exact candidate follows its saved review, not the new
		// page. Selecting a different candidate still requires contextual choice.
		prior, err := h.FolderSetups.ReadReview(ctx, target, currentOfferID)
		if err == nil && prior != nil && prior.CandidateID == candidateID {
			return personalassistant.WithFolderReviewPlacement(ctx, personalassistant.FolderReviewPlacement{Operation: prior.Operation, DestinationID: prior.RequestedDestinationID}), true
		}
	}
	if reader, ok := h.FolderSetups.(interface {
		PendingElsewhere(context.Context, foldercontext.Target) (*personalassistant.FolderOfferView, error)
	}); ok {
		pending, err := reader.PendingElsewhere(ctx, target)
		if err != nil {
			writeFolderReviewError(w, err)
			return ctx, false
		}
		if pending != nil {
			orihttp.WriteJSON(w, map[string]any{"folder_pending_elsewhere": map[string]any{"conversation_id": pending.ConversationID, "subject": workspaceContextText(pending.Subject.Name, 120)}})
			return ctx, false
		}
	}
	previewer, ok := h.FolderSetups.(interface {
		PreviewReview(context.Context, foldercontext.Target, string, string) (personalassistant.FolderOfferView, error)
	})
	if !ok {
		writeFolderError(w, foldercontext.ErrInvalid)
		return ctx, false
	}
	preview, err := previewer.PreviewReview(ctx, target, selectionID, candidateID)
	if err != nil {
		writeFolderReviewError(w, err)
		return ctx, false
	}
	subject := turn.projection.Subject
	choice := func(message string, options []folderPlacementOption) (context.Context, bool) {
		orihttp.WriteJSON(w, map[string]any{"folder_placement_choice": map[string]any{
			"candidate_id": candidateID, "subject": preview.Subject.Name, "message": message, "options": options,
		}})
		return ctx, false
	}
	createOption := folderPlacementOption{Operation: personalassistant.FolderOperationCreate, Label: "Review a separate project"}
	if destination := preview.Destination; destination != nil && destination.Status == "existing" {
		createOption.DestinationID = destination.WorkspaceID
		createOption.Label = "Review a separate project in " + destination.Name
	}
	if operation == "" && subject != nil && subject.Kind == "project" {
		if preview.Portfolio != nil {
			return choice("A collection cannot replace this project's primary entry. Review its Home/library setup separately.", []folderPlacementOption{createOption})
		}
		if createOption.DestinationID == "" && turn.projection.Overview != nil && turn.projection.Overview.Parent != nil {
			parent := turn.projection.Overview.Parent
			createOption.DestinationID, createOption.Label = parent.ID, "Review a separate project in "+parent.Name
		}
		return choice("Link this folder as a supporting source, or create a separate project? Neither choice runs setup.", []folderPlacementOption{
			{Operation: personalassistant.FolderOperationSupport, DestinationID: subject.ID, Label: "Review supporting folder in " + subject.Name}, createOption,
		})
	}
	if operation == "" {
		operation = personalassistant.FolderOperationCreate
	}
	if operation == personalassistant.FolderOperationSupport && (subject == nil || subject.Kind != "project" || destinationID != subject.ID || preview.Portfolio != nil) {
		writeFolderError(w, foldercontext.ErrInvalid)
		return ctx, false
	}
	if operation == personalassistant.FolderOperationCreate && destinationID == "" && subject != nil && subject.Kind != "project" {
		if preview.Destination != nil && preview.Destination.Status == "existing" && preview.Destination.WorkspaceID != subject.ID {
			return choice("This group is not the reviewed blueprint's Home. Choose the named destination explicitly; nothing will be reparented.", []folderPlacementOption{createOption})
		}
		destinationID = subject.ID
	}
	placement := personalassistant.FolderReviewPlacement{Operation: operation, DestinationID: destinationID}
	bound := personalassistant.WithFolderReviewPlacement(ctx, placement)
	// Resolve the exact selected destination before allocating any offer.
	selected, err := previewer.PreviewReview(bound, target, selectionID, candidateID)
	if err != nil || selected.DestinationStatus == "unavailable" {
		return choice("That destination is not compatible or is unavailable. No setup has been prepared there.", []folderPlacementOption{createOption})
	}
	// This exact folder may already be a project. Point at it rather than
	// preparing a second one; a supporting link is a different operation.
	if reader, ok := h.FolderSetups.(interface {
		CompletedProject(context.Context, foldercontext.Target, string, string) (*personalassistant.FolderOfferView, error)
	}); ok && operation == personalassistant.FolderOperationCreate {
		done, err := reader.CompletedProject(bound, target, selectionID, candidateID)
		if err != nil {
			writeFolderReviewError(w, err)
			return ctx, false
		}
		if existing := h.existingProjectHandoff(turn.userID, target, done); existing != nil {
			orihttp.WriteJSON(w, map[string]any{"folder_existing_project": existing})
			return ctx, false
		}
	}
	return bound, true
}

// existingProjectHandoff names the completed project from its current canonical
// record. It carries a page link, never a control that runs or repeats setup.
func (h *HomeAssistantAskHandler) existingProjectHandoff(userID string, target foldercontext.Target, done *personalassistant.FolderOfferView) map[string]any {
	if done == nil || done.Outcome == nil || h.WorkspaceContext == nil || h.WorkspaceContext.Source == nil {
		return nil
	}
	ws, err := h.WorkspaceContext.Source.Get(done.Outcome.WorkspaceID)
	if err != nil || !workspaceReadable(ws, userID) || ws.Kind == "group" {
		return nil
	}
	ref := contextWorkspaceRef(ws)
	existing := map[string]any{"subject": workspaceContextText(done.Subject.Name, 120), "workspace": ref.Name}
	if ref.Slug != "" {
		existing["route"] = workspaceHref(ref.Slug)
	}
	if done.ConversationID != "" && done.ConversationID != target.ConversationID {
		existing["conversation_id"] = done.ConversationID
	}
	return existing
}
