package setupjourney

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func cloneLaunchCopy(source *specialist.WorkspaceLaunchCopy) *specialist.WorkspaceLaunchCopy {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}
func cloneHomePreparation(source *projectconnection.HomePreparation) *projectconnection.HomePreparation {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}
func validHomePreparation(value *projectconnection.HomePreparation) bool {
	return value == nil || (len(value.Name) > 0 && len(value.Name) <= 128 && len(value.TemplateID) > 0 && len(value.TemplateID) <= 256 && validateCanonicalRef(value.HomeID, true))
}
func isPreparationAction(action ActionID) bool {
	return action == ActionReviewCreateGroup || action == ActionCreateGroup || action == ActionAcknowledgePreparation
}
func groupName(raw json.RawMessage) (string, error) {
	var input struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", ErrInvalid
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "", ErrInvalid
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n/\\") || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalid
	}
	return name, nil
}
func preparationInputDigest(action ActionID, raw json.RawMessage) (string, error) {
	if action == ActionAcknowledgePreparation {
		if err := decodeEmptyActionInput(raw); err != nil {
			return "", err
		}
		return Digest([]byte("acknowledge_preparation:v1")), nil
	}
	name, err := groupName(raw)
	if err != nil {
		return "", err
	}
	return Digest([]byte("create_group:v1:" + name)), nil
}
func (a *ProjectConnectionAdapter) prepareGroup(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (ActionReviewMaterial, error) {
	if !scope.WorkspaceLaunch || a == nil || a.owner == nil || a.templates == nil {
		return ActionReviewMaterial{}, ErrInvalid
	}
	template, err := a.templates.ResolveProjectTemplate(ctx, scope)
	if err != nil {
		return ActionReviewMaterial{}, projectconnection.ErrUnavailable
	}
	home, err := a.owner.HomePreparation(projectConnectionScope(scope, template))
	if err != nil {
		return ActionReviewMaterial{}, projectconnection.ErrUnavailable
	}
	digest, err := preparationInputDigest(action, raw)
	if err != nil {
		return ActionReviewMaterial{}, err
	}
	ownerDigest := Digest([]byte(template.ID + ":" + scope.IntegrationVersion + ":" + home.HomeID + ":" + home.Name))
	if action == ActionCreateGroup {
		if home.Exists {
			return ActionReviewMaterial{}, ErrConflict
		}
		home.Name, err = groupName(raw)
		if err != nil {
			return ActionReviewMaterial{}, err
		}
	} else if action != ActionAcknowledgePreparation || !home.Exists {
		return ActionReviewMaterial{}, ErrInvalid
	}
	disclosure, err := json.Marshal(home)
	if err != nil {
		return ActionReviewMaterial{}, ErrInvalid
	}
	return ActionReviewMaterial{CommitAction: action, InputDigest: digest, OwnerRevisionDigest: ownerDigest, DisclosureDigest: Digest(disclosure), Group: &home}, nil
}
func (a *ProjectConnectionAdapter) commitGroup(ctx context.Context, scope ReadScope, action ActionID, raw json.RawMessage) (CanonicalResult, error) {
	template, err := a.templates.ResolveProjectTemplate(ctx, scope)
	if err != nil {
		return CanonicalResult{}, projectconnection.ErrUnavailable
	}
	var home projectconnection.HomePreparation
	switch action {
	case ActionCreateGroup:
		name, nameErr := groupName(raw)
		if nameErr != nil {
			return CanonicalResult{}, nameErr
		}
		home, err = a.owner.CreateHome(projectConnectionScope(scope, template), name)
	case ActionAcknowledgePreparation:
		home, err = a.owner.AcknowledgePreparation(projectConnectionScope(scope, template))
	default:
		return CanonicalResult{}, ErrInvalid
	}
	if err != nil {
		return CanonicalResult{}, projectConnectionFailure(err)
	}
	result := CanonicalResult{}
	if scope.RunKind == RunKindRoot {
		result.HomeWorkspaceID = home.HomeID
	}
	return result, nil
}

// PreparationCheck reports application prerequisites only. There is no project
// identity, grant, runner destination, provider error text, or live verdict here.
type PreparationCheck struct {
	Ready bool `json:"ready"`
}

func (s *Service) CheckPreparation(ctx context.Context, userID, runID string) (*PreparationCheck, error) {
	projection, err := s.Read(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	scope, err := s.authorizedActionScope(ctx, userID, projection.RunID)
	if err != nil {
		return nil, err
	}
	if !scope.WorkspaceLaunch || scope.HomeWorkspaceID == "" {
		return nil, failure(ReasonActionUnavailable, projection.StateRevision)
	}
	for _, step := range projection.Steps {
		if step.Kind == specialist.SetupStepIntegrationInstall && step.Status != StepComplete {
			return nil, failure(ReasonOwnerUnavailable, projection.StateRevision)
		}
	}
	adapter, ok := s.actionAdapters[specialist.SetupStepProjectConnect].(*ProjectConnectionAdapter)
	if !ok || adapter.CheckPrerequisites == nil {
		return nil, failure(ReasonOwnerUnavailable, projection.StateRevision)
	}
	template, err := adapter.templates.ResolveProjectTemplate(ctx, scope)
	if err != nil {
		return nil, failure(ReasonOwnerUnavailable, projection.StateRevision)
	}
	ready, err := adapter.CheckPrerequisites(ctx, template)
	if err != nil {
		return nil, failure(ReasonOwnerUnavailable, projection.StateRevision)
	}
	return &PreparationCheck{Ready: ready}, nil
}
