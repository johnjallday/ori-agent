package setupjourney

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/johnjallday/ori-agent/internal/setupwizard"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const fileOnlyModeID = "file_only"

type WorkspaceSetupProjection struct {
	ModeID                string `json:"mode_id"`
	ModeLabel             string `json:"mode_label"`
	ModeDescription       string `json:"mode_description"`
	FilesConnected        bool   `json:"files_connected"`
	LiveControlConfigured bool   `json:"live_control_configured"`
	LiveControlTested     bool   `json:"live_control_tested"`
}

type ProjectFileReadiness interface {
	FilesConnected(string) bool
}

type WorkspaceSetupAdapter struct {
	wizard    *setupwizard.Service
	readiness ProjectFileReadiness
}

func NewWorkspaceSetupAdapter(wizard *setupwizard.Service, readiness ProjectFileReadiness) *WorkspaceSetupAdapter {
	return &WorkspaceSetupAdapter{wizard: wizard, readiness: readiness}
}

func (a *WorkspaceSetupAdapter) InputDigest(action ActionID, raw json.RawMessage) (string, error) {
	if action != ActionReviewFileOnlyMode && action != ActionSelectFileOnlyMode {
		return "", errors.New("workspace setup action is unavailable")
	}
	if err := decodeEmptyActionInput(raw); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte("workspace_setup:file_only:v1"))
	return hex.EncodeToString(digest[:]), nil
}

func (a *WorkspaceSetupAdapter) Review(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	if action != ActionReviewFileOnlyMode {
		return ActionReviewMaterial{}, errors.New("workspace setup action is unavailable")
	}
	return a.prepare(ctx, scope, ActionSelectFileOnlyMode, raw)
}

func (a *WorkspaceSetupAdapter) PrepareCommit(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	if action != ActionSelectFileOnlyMode {
		return ActionReviewMaterial{}, errors.New("workspace setup action is unavailable")
	}
	return a.prepare(ctx, scope, action, raw)
}

func (a *WorkspaceSetupAdapter) prepare(ctx context.Context, scope ReadScope, commitAction ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	if a == nil || a.wizard == nil || a.readiness == nil || scope.ProjectWorkspaceID == "" || !a.readiness.FilesConnected(scope.ProjectWorkspaceID) {
		return ActionReviewMaterial{}, errors.New("workspace setup owner is unavailable")
	}
	inputDigest, err := a.InputDigest(commitAction, raw)
	if err != nil {
		return ActionReviewMaterial{}, err
	}
	status, err := a.wizard.Status(ctx, scope.ProjectWorkspaceID)
	if err != nil || !status.Applicable || status.Diagnostic != "" {
		return ActionReviewMaterial{}, errors.New("workspace setup owner is unavailable")
	}
	projection, stepID, ok := fileOnlyProjection(status)
	if !ok || stepID == "" {
		return ActionReviewMaterial{}, errors.New("workspace setup owner is unavailable")
	}
	ownerDigest, disclosureDigest, err := workspaceSetupDigests(commitAction, status, projection)
	if err != nil {
		return ActionReviewMaterial{}, errors.New("workspace setup owner is unavailable")
	}
	return ActionReviewMaterial{
		CommitAction: commitAction, InputDigest: inputDigest,
		OwnerRevisionDigest: ownerDigest, DisclosureDigest: disclosureDigest,
		WorkspaceSetup: &projection,
	}, nil
}

func (a *WorkspaceSetupAdapter) Commit(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage, _ ActionReviewMaterial) (CanonicalResult, error) {
	if action != ActionSelectFileOnlyMode || a == nil || a.wizard == nil || a.readiness == nil ||
		scope.ProjectWorkspaceID == "" || !a.readiness.FilesConnected(scope.ProjectWorkspaceID) {
		return CanonicalResult{}, errors.New("workspace setup owner is unavailable")
	}
	if err := decodeEmptyActionInput(raw); err != nil {
		return CanonicalResult{}, err
	}
	status, err := a.wizard.Status(ctx, scope.ProjectWorkspaceID)
	if err != nil {
		return CanonicalResult{}, err
	}
	_, stepID, ok := fileOnlyProjection(status)
	if !ok {
		return CanonicalResult{}, errors.New("workspace setup owner is unavailable")
	}
	status, err = a.wizard.Confirm(ctx, scope.ProjectWorkspaceID, stepID, setupwizard.StepAction{Type: setupwizard.ActionConfirm, Option: fileOnlyModeID})
	if err != nil {
		return CanonicalResult{}, err
	}
	projection, _, ok := fileOnlyProjection(status)
	if !ok || projection.ModeID != fileOnlyModeID {
		return CanonicalResult{}, errors.New("workspace setup mode was not observed")
	}
	return CanonicalResult{SelectedModeID: fileOnlyModeID}, nil
}

func (a *WorkspaceSetupAdapter) Read(ctx context.Context, scope ReadScope) (CanonicalStepRead, error) {
	if a == nil || a.wizard == nil || a.readiness == nil || scope.ProjectWorkspaceID == "" || !a.readiness.FilesConnected(scope.ProjectWorkspaceID) {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
	}
	status, err := a.wizard.Status(ctx, scope.ProjectWorkspaceID)
	if err != nil || !status.Applicable || status.Diagnostic != "" {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
	}
	projection, _, ok := fileOnlyProjection(status)
	if !ok {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
	}
	actions := []ActionID{ActionReviewFileOnlyMode, ActionOpenWorkspaceSetup, ActionRefreshWorkspaceSetup, ActionOpenProject}
	read := CanonicalStepRead{AvailableActions: actions, WorkspaceSetup: &projection}
	if projection.ModeID == fileOnlyModeID {
		read.Complete = true
		read.Result = CanonicalResult{SelectedModeID: fileOnlyModeID}
	}
	return read, nil
}

func (a *WorkspaceSetupAdapter) ConsequenceObserved(action ActionID, state CanonicalStepRead) bool {
	return action == ActionSelectFileOnlyMode && state.Complete && state.Result.SelectedModeID == fileOnlyModeID &&
		state.WorkspaceSetup != nil && state.WorkspaceSetup.FilesConnected &&
		!state.WorkspaceSetup.LiveControlConfigured && !state.WorkspaceSetup.LiveControlTested
}

func fileOnlyProjection(status setupwizard.Status) (WorkspaceSetupProjection, string, bool) {
	for _, step := range status.Steps {
		if step.Kind != workspace.SetupStepKindRuntimeMode {
			continue
		}
		for _, option := range step.Options {
			if option.ID != fileOnlyModeID {
				continue
			}
			selected := step.SelectedOption == fileOnlyModeID || option.Selected
			projection := WorkspaceSetupProjection{
				ModeLabel: option.Label, ModeDescription: option.Description,
				FilesConnected: true, LiveControlConfigured: false, LiveControlTested: false,
			}
			if selected {
				projection.ModeID = fileOnlyModeID
			}
			return projection, step.ID, true
		}
	}
	return WorkspaceSetupProjection{}, "", false
}

func workspaceSetupDigests(action ActionID, status setupwizard.Status, projection WorkspaceSetupProjection) (string, string, error) {
	ownerBytes, err := json.Marshal(struct {
		WorkspaceID   string                   `json:"workspace_id"`
		WizardVersion int                      `json:"wizard_version"`
		State         string                   `json:"state"`
		Projection    WorkspaceSetupProjection `json:"projection"`
	}{status.WorkspaceID, status.WizardVersion, status.State, projection})
	if err != nil {
		return "", "", err
	}
	disclosureBytes, err := json.Marshal(struct {
		Action     ActionID                 `json:"action"`
		Projection WorkspaceSetupProjection `json:"projection"`
	}{action, projection})
	if err != nil {
		return "", "", err
	}
	owner := sha256.Sum256(ownerBytes)
	disclosure := sha256.Sum256(disclosureBytes)
	return hex.EncodeToString(owner[:]), hex.EncodeToString(disclosure[:]), nil
}

func decodeEmptyActionInput(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var input struct{}
	if err := decoder.Decode(&input); err != nil {
		return errors.New("workspace setup input is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("workspace setup input is invalid")
	}
	return nil
}

func validWorkspaceSetupProjection(projection *WorkspaceSetupProjection) bool {
	if projection == nil {
		return true
	}
	return (projection.ModeID == "" || projection.ModeID == fileOnlyModeID) &&
		len(strings.TrimSpace(projection.ModeLabel)) > 0 && len(projection.ModeLabel) <= 120 &&
		len(projection.ModeDescription) <= 500 && projection.FilesConnected &&
		!projection.LiveControlConfigured && !projection.LiveControlTested
}

func cloneWorkspaceSetupProjection(source *WorkspaceSetupProjection) *WorkspaceSetupProjection {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}
