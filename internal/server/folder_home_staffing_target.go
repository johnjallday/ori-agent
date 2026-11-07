package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The destination owner has already proved the exact existing/own-created
// Home. A proposed Home additionally needs the real current provider template;
// a pre-install display guess never supplies a role declaration.
func (h *folderSetupHost) folderHomeStaffingDeclaration(ctx context.Context, req personalassistant.FolderSetupRequest, home *workspace.Workspace) (*workspace.AssistantProgramDeclaration, bool, error) {
	if home != nil {
		state := home.GetAssistantProgramState()
		if state == nil || state.Declaration == nil {
			return nil, false, personalassistant.ErrFolderPlanChanged
		}
		return state.Declaration, state.HomeProvider != nil, nil
	}
	expected := req.Plan.Destination
	if h == nil || h.builder == nil || h.builder.sessionHandler == nil || expected == nil {
		return nil, false, personalassistant.ErrFolderPlanChanged
	}
	entry, err := h.builder.sessionHandler.PortfolioHomeTemplate(ctx, expected.ProviderPlugin)
	if err != nil || entry.Representative == nil || entry.Representative.ProgramHomeOwner == nil || entry.Representative.AssistantProgram == nil {
		return nil, false, personalassistant.ErrFolderPlanChanged
	}
	owner, declaration := entry.Representative.ProgramHomeOwner, entry.Representative.AssistantProgram
	data, err := json.Marshal(struct {
		Owner       workspace.AssistantProgramHomeOwner
		Declaration *workspace.AssistantProgramDeclaration
	}{*owner, declaration})
	sum := sha256.Sum256(data)
	if err != nil || !owner.Valid() || owner.PluginID != expected.ProviderPlugin || owner.PluginVersion != expected.ProviderVersion || owner.ProgramID != expected.ProgramID || declaration.ID != expected.ProgramID || owner.DeclarationDigest != expected.DeclarationDigest || hex.EncodeToString(sum[:]) != expected.CompatibilityHash {
		return nil, false, personalassistant.ErrFolderPlanChanged
	}
	return declaration, true, nil
}

// folderHomeReviewTarget is only for pending draft actions. Unlike a running
// operation, a draft cannot adopt a Home another operation created meanwhile.
func (h *folderSetupHost) folderHomeReviewTarget(ctx context.Context, req personalassistant.FolderSetupRequest) (sessionhttp.HomeStaffingTarget, error) {
	var target sessionhttp.HomeStaffingTarget
	expected := req.Plan.Destination
	if h == nil || h.builder == nil || expected == nil || expected.OwnerUserID != req.UserID {
		return target, personalassistant.ErrFolderPlanChanged
	}
	current, err := h.ReadSetupDestination(ctx, req)
	if err != nil || current == nil || *current != *expected {
		return target, personalassistant.ErrFolderPlanChanged
	}
	var home *workspace.Workspace
	if expected.Status == "existing" {
		if h.builder.workspaceStore == nil {
			return target, personalassistant.ErrFolderPlanChanged
		}
		home, err = h.builder.workspaceStore.Get(expected.WorkspaceID)
		if err != nil || home == nil {
			return target, personalassistant.ErrFolderPlanChanged
		}
		live, err := folderSetupHomeDestination(home)
		if err != nil || live == nil || *live != *expected {
			return target, personalassistant.ErrFolderPlanChanged
		}
	}
	declaration, personal, err := h.folderHomeStaffingDeclaration(ctx, req, home)
	if err != nil {
		return target, err
	}
	return sessionhttp.HomeStaffingTarget{OwnerUserID: req.UserID, TemplateID: req.Plan.Intent.HomeTemplate, TemplateRevision: expected.CompatibilityHash,
		HomeID: expected.WorkspaceID, Declaration: declaration, Personal: personal}, nil
}
