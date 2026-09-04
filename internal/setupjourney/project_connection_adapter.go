package setupjourney

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

type ProjectTemplateResolver interface {
	ResolveProjectTemplate(context.Context, ReadScope) (projecttemplates.Template, error)
}

type ProjectConnectionAdapter struct {
	owner     *projectconnection.Service
	templates ProjectTemplateResolver
}

func NewProjectConnectionAdapter(owner *projectconnection.Service, templates ProjectTemplateResolver) *ProjectConnectionAdapter {
	return &ProjectConnectionAdapter{owner: owner, templates: templates}
}

func (a *ProjectConnectionAdapter) InputDigest(action ActionID, raw json.RawMessage) (string, error) {
	request, err := decodeProjectConnectionRequest(action, raw)
	if err != nil {
		return "", err
	}
	return projectconnection.InputDigest(request)
}

func (a *ProjectConnectionAdapter) Review(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	commitAction, ok := projectCommitForReview(action)
	if !ok {
		return ActionReviewMaterial{}, errors.New("project connection action is unavailable")
	}
	return a.prepare(ctx, scope, commitAction, raw)
}

func (a *ProjectConnectionAdapter) PrepareCommit(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	if action != ActionConnectExistingProject && action != ActionCreateNewProject {
		return ActionReviewMaterial{}, errors.New("project connection action is unavailable")
	}
	return a.prepare(ctx, scope, action, raw)
}

func (a *ProjectConnectionAdapter) prepare(ctx context.Context, scope ReadScope, commitAction ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	if a == nil || a.owner == nil || a.templates == nil {
		return ActionReviewMaterial{}, projectconnection.ErrUnavailable
	}
	request, err := decodeProjectConnectionRequest(commitAction, raw)
	if err != nil {
		return ActionReviewMaterial{}, err
	}
	template, err := a.templates.ResolveProjectTemplate(ctx, scope)
	if err != nil {
		return ActionReviewMaterial{}, projectconnection.ErrUnavailable
	}
	preview, err := a.owner.Preview(ctx, projectConnectionScope(scope, template), request)
	if err != nil {
		return ActionReviewMaterial{}, err
	}
	disclosureDigest, err := projectDisclosureDigest(commitAction, preview.Projection)
	if err != nil {
		return ActionReviewMaterial{}, projectconnection.ErrUnavailable
	}
	projection := preview.Projection
	projection.EntryCandidates = append([]string(nil), preview.Projection.EntryCandidates...)
	projection.CreatedFiles = append([]string(nil), preview.Projection.CreatedFiles...)
	return ActionReviewMaterial{
		CommitAction: commitAction, InputDigest: preview.InputDigest,
		OwnerRevisionDigest: preview.OwnerDigest, DisclosureDigest: disclosureDigest,
		ProjectConnection: &projection,
	}, nil
}

func (a *ProjectConnectionAdapter) Commit(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage, material ActionReviewMaterial) (CanonicalResult, error) {
	if a == nil || a.owner == nil || a.templates == nil {
		return CanonicalResult{}, projectconnection.ErrUnavailable
	}
	request, err := decodeProjectConnectionRequest(action, raw)
	if err != nil {
		return CanonicalResult{}, err
	}
	template, err := a.templates.ResolveProjectTemplate(ctx, scope)
	if err != nil {
		return CanonicalResult{}, projectconnection.ErrUnavailable
	}
	result, err := a.owner.Commit(ctx, projectConnectionScope(scope, template), request, material.InputDigest, material.OwnerRevisionDigest)
	if err != nil {
		return CanonicalResult{}, err
	}
	canonical := CanonicalResult{ProjectWorkspaceID: result.ProjectWorkspaceID}
	if scope.RunKind == RunKindRoot {
		canonical.HomeWorkspaceID = result.HomeWorkspaceID
	}
	return canonical, nil
}

func (a *ProjectConnectionAdapter) Read(ctx context.Context, scope ReadScope) (CanonicalStepRead, error) {
	if a == nil || a.owner == nil || a.templates == nil {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
	}
	template, err := a.templates.ResolveProjectTemplate(ctx, scope)
	if err != nil {
		return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
	}
	observed, ok := a.owner.ObservedResult(projectConnectionScope(scope, template), scope.HomeWorkspaceID, scope.ProjectWorkspaceID)
	if !ok {
		actions := make([]ActionID, 0, 2)
		if template.ProjectConnection != nil && template.ProjectConnection.Supports(projecttemplates.ProjectConnectionExistingProject) {
			actions = append(actions, ActionReviewExistingProject)
		}
		if template.ProjectConnection != nil && template.ProjectConnection.Supports(projecttemplates.ProjectConnectionNewProject) {
			actions = append(actions, ActionReviewNewProject)
		}
		if len(actions) == 0 {
			return CanonicalStepRead{BlockedReason: ReasonOwnerUnavailable}, nil
		}
		return CanonicalStepRead{AvailableActions: actions}, nil
	}
	result := CanonicalResult{ProjectWorkspaceID: observed.ProjectWorkspaceID}
	if scope.RunKind == RunKindRoot {
		result.HomeWorkspaceID = observed.HomeWorkspaceID
	}
	return CanonicalStepRead{Complete: true, AvailableActions: []ActionID{ActionOpenProject}, Result: result}, nil
}

func (a *ProjectConnectionAdapter) ConsequenceObserved(action ActionID, state CanonicalStepRead) bool {
	return (action == ActionConnectExistingProject || action == ActionCreateNewProject) && state.Complete &&
		state.Result.ProjectWorkspaceID != ""
}

func projectConnectionScope(scope ReadScope, template projecttemplates.Template) projectconnection.Scope {
	return projectconnection.Scope{OwnerUserID: scope.OwnerUserID, RunID: scope.RunID, Template: template}
}

func decodeProjectConnectionRequest(action ActionID, raw json.RawMessage) (projectconnection.Request, error) {
	if len(bytes.TrimSpace(raw)) == 0 || len(raw) > MaxActionInputBytes {
		return projectconnection.Request{}, projectconnection.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request projectconnection.Request
	if err := decoder.Decode(&request); err != nil {
		return projectconnection.Request{}, projectconnection.ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return projectconnection.Request{}, projectconnection.ErrInvalid
	}
	request = projectconnection.NormalizeRequest(request)
	expectedMode := projecttemplates.ProjectConnectionMode("")
	switch action {
	case ActionReviewExistingProject, ActionConnectExistingProject:
		expectedMode = projecttemplates.ProjectConnectionExistingProject
	case ActionReviewNewProject, ActionCreateNewProject:
		expectedMode = projecttemplates.ProjectConnectionNewProject
	default:
		return projectconnection.Request{}, projectconnection.ErrInvalid
	}
	if request.ModeID != expectedMode {
		return projectconnection.Request{}, projectconnection.ErrInvalid
	}
	return request, nil
}

func projectCommitForReview(action ActionID) (ActionID, bool) {
	switch action {
	case ActionReviewExistingProject:
		return ActionConnectExistingProject, true
	case ActionReviewNewProject:
		return ActionCreateNewProject, true
	default:
		return "", false
	}
}

func projectDisclosureDigest(action ActionID, projection projectconnection.Projection) (string, error) {
	encoded, err := json.Marshal(struct {
		Action     ActionID                     `json:"action"`
		Projection projectconnection.Projection `json:"projection"`
	}{Action: action, Projection: projection})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validProjectConnectionProjection(projection *projectconnection.Projection) bool {
	if projection == nil {
		return true
	}
	if len(projection.WorkspaceName) == 0 || len(projection.WorkspaceName) > 128 ||
		len(projection.ProjectName) > 128 || len(projection.ParentWorkspaceName) == 0 || len(projection.ParentWorkspaceName) > 128 ||
		len(projection.EntryName) > 255 ||
		len(projection.EntryCandidates) > maxEntryCandidatesProjection || len(projection.CreatedFiles) > maxCreatedFilesProjection ||
		len(projection.DefaultsStatement) > 512 {
		return false
	}
	switch projection.ModeID {
	case projecttemplates.ProjectConnectionExistingProject:
		if !filepath.IsAbs(projection.SelectedFolder) || projection.ProjectName != "" ||
			len(projection.CreatedFiles) != 0 || projection.DefaultsStatement != "" ||
			(projection.EntryName == "" && len(projection.EntryCandidates) < 2) {
			return false
		}
	case projecttemplates.ProjectConnectionNewProject:
		if projection.SelectedFolder != "" || projection.ProjectName == "" || projection.EntryName == "" ||
			len(projection.CreatedFiles) == 0 || projection.DefaultsStatement == "" || len(projection.EntryCandidates) != 0 {
			return false
		}
	default:
		return false
	}
	for _, candidate := range projection.EntryCandidates {
		if candidate == "" || len(candidate) > 255 || filepath.Base(candidate) != candidate || strings.ContainsAny(candidate, `/\\`) {
			return false
		}
	}
	for _, created := range projection.CreatedFiles {
		if created == "" || len(created) > 512 || filepath.IsAbs(created) || !filepath.IsLocal(filepath.FromSlash(created)) {
			return false
		}
	}
	return true
}

const (
	maxEntryCandidatesProjection = 64
	maxCreatedFilesProjection    = 512
)

func cloneProjectConnectionProjection(source *projectconnection.Projection) *projectconnection.Projection {
	if source == nil {
		return nil
	}
	clone := *source
	clone.EntryCandidates = append([]string(nil), source.EntryCandidates...)
	clone.CreatedFiles = append([]string(nil), source.CreatedFiles...)
	return &clone
}
