package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"path"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// An older Ori saved a Personal HQ folder without a continuity checkpoint. Its
// workspace files still prove which assistant it belonged to: the presentation
// marker, the single entry instance and that instance's own workspace profile.
// Those alone — never a name match against this installation's roster — let
// the user's confirmed Import Folder make that assistant theirs here
// (PRD FR-28/FR-29). Everything else of the relationship is simply absent.

type continuityLegacyAssistant struct {
	DisplayName string `json:"display_name"`
}

// continuityLegacyAdoption is the Import Folder response's account of it.
type continuityLegacyAdoption struct {
	Adopted     bool   `json:"adopted"`
	DisplayName string `json:"display_name,omitempty"`
	// Reason is set when the assistant was not adopted: "existing_assistant"
	// or "identity_incomplete".
	Reason string `json:"reason,omitempty"`
}

// legacyHQEvidence reads an older folder's own identity evidence read-only:
// workspace.json and its entry instance's profile, each a bounded regular
// file inside the folder (links are refused).
func legacyHQEvidence(ctx context.Context, folder string) (*session.Workspace, *agent.Agent, bool) {
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return nil, nil, false
	}
	ws, err := agentworkspace.FromJSON(data)
	if err != nil || ws.SharedData[personalhq.PersonalAssistantPresentationKey] == nil {
		return nil, nil, false
	}
	entry := ws.EntryAgentName()
	if entry == "" {
		return nil, nil, false
	}
	profilePath := path.Join(agentworkspace.WorkspaceAgentsDir, agentworkspace.Slugify(entry), agentworkspace.WorkspaceAgentConfigFile)
	raw, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, profilePath, workspacecontinuity.MaxRecordBytes)
	if err != nil {
		return nil, nil, false
	}
	var profile agent.Agent
	if json.Unmarshal(raw, &profile) != nil {
		return nil, nil, false
	}
	return session.ConvertAgentWorkspace(ws), &profile, true
}

// reviewLegacyAssistant adds the assistant an older HQ folder proves, and
// whether this installation can take it, to a legacy review. Read-only.
func (h *Handler) reviewLegacyAssistant(ctx context.Context, folder string, review *continuityImportReview) error {
	ws, profile, ok := legacyHQEvidence(ctx, folder)
	if !ok {
		return nil
	}
	if _, err := personalassistant.NewContinuityBinding(ws, profile); err != nil {
		return nil // contradictory or incomplete identity: nothing to offer
	}
	review.LegacyAssistant = &continuityLegacyAssistant{DisplayName: entryInstanceName(ws)}
	blocked, err := h.legacyAdoptionBlocked(ctx, ws.ID)
	if err != nil {
		return err
	}
	review.LegacyAdoptionBlocked = blocked
	return nil
}

func (h *Handler) legacyAdoptionBlocked(ctx context.Context, workspaceID string) (string, error) {
	db := h.store.DB()
	var relationships int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE user_id=?`, userprofile.LocalUserID).Scan(&relationships); err != nil {
		return "", err
	}
	if relationships != 0 {
		return "existing_assistant", nil
	}
	var designated string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(personal_workspace_id,'') FROM users WHERE id=?`, userprofile.LocalUserID).Scan(&designated); err != nil {
		return "", err
	}
	if designated != "" && designated != workspaceID {
		return "existing_hq", nil
	}
	return "", nil
}

// adoptLegacyAssistant runs after an older HQ folder was imported with the
// user's explicit choice to continue with its assistant. The evidence is
// re-read from what was installed here, not trusted from the review.
func (h *Handler) adoptLegacyAssistant(ctx context.Context, workspaceID string) continuityLegacyAdoption {
	ws, err := h.store.GetWorkspace(ctx, workspaceID)
	if err != nil || ws == nil {
		return continuityLegacyAdoption{Reason: "identity_incomplete"}
	}
	entry := entryInstanceName(ws)
	profile, found, err := h.workspaceStore.GetWorkspaceAgent(workspaceID, entry)
	if err != nil || !found || entry == "" {
		return continuityLegacyAdoption{Reason: "identity_incomplete"}
	}
	binding, err := personalassistant.NewContinuityBinding(ws, profile)
	if err != nil {
		return continuityLegacyAdoption{Reason: "identity_incomplete"}
	}
	state, err := personalassistant.NewSQLiteStore(h.store.DB()).AdoptLegacyHQ(ctx, binding, ws.CreatedAt)
	switch {
	case errors.Is(err, workspacecontinuity.ErrConflict):
		return continuityLegacyAdoption{Reason: "existing_assistant"}
	case err != nil:
		logger.Warn("Legacy Personal HQ assistant not adopted", logger.Fields{"workspace_id": workspaceID, "error": err.Error()})
		return continuityLegacyAdoption{Reason: "identity_incomplete"}
	}
	h.rebindLegacyKnowledge(ctx, state, ws.FolderSlug)
	h.notifyContinuityAdmission(workspaceID)
	if h.continuity != nil && h.continuity.afterImport != nil {
		h.continuity.afterImport()
	}
	return continuityLegacyAdoption{Adopted: true, DisplayName: state.DisplayName}
}

// entryInstanceName is the single entry instance's name; NewContinuityBinding
// separately refuses zero or several.
func entryInstanceName(ws *session.Workspace) string {
	for _, instance := range ws.AgentInstances {
		if instance.EntryPoint {
			return instance.Name
		}
	}
	return ""
}

// rebindLegacyKnowledge points the folder's knowledge metadata at this
// installation's copy of the HQ, as a modern import does. Unrebindable
// metadata stays as copied: inert, its remembered items needing review.
func (h *Handler) rebindLegacyKnowledge(ctx context.Context, state *personalassistant.State, folderSlug string) {
	dir, err := h.workspaceStore.GetFolderPath(state.HQWorkspaceID)
	if err != nil {
		return
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, dir, personalassistant.KnowledgeSidecarPath, 8<<20)
	if err != nil {
		return
	}
	projected, _, err := personalassistant.ProjectContinuityKnowledge(data, personalassistant.KnowledgeBinding{
		UserID: state.UserID, AssistantID: state.AssistantID, HQWorkspaceID: state.HQWorkspaceID,
		HQFolderSlug: folderSlug, EntryAgentInstanceID: state.HQEntryAgentInstanceID})
	if err != nil {
		return
	}
	// A file that changed since it was read is left as it is (it stays inert).
	err = workspacecontinuity.ReplaceCanonicalFile(ctx, dir, personalassistant.KnowledgeSidecarPath, workspacecontinuity.Digest(data), projected)
	if err != nil && !errors.Is(err, workspacecontinuity.ErrChanged) {
		logger.Warn("Legacy Personal HQ knowledge not rebound", logger.Fields{"workspace_id": state.HQWorkspaceID, "error": err.Error()})
	}
}
