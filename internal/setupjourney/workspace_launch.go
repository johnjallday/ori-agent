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
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
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
	copy.AvailableCompositions = append([]string(nil), source.AvailableCompositions...)
	return &copy
}
func validHomePreparation(value *projectconnection.HomePreparation) bool {
	if value == nil {
		return true
	}
	if len(value.Name) == 0 || len(value.Name) > 128 || len(value.TemplateID) == 0 || len(value.TemplateID) > 256 || !validateCanonicalRef(value.HomeID, true) {
		return false
	}
	if value.GroupTemplateID != "" && !projecttemplates.ValidManagedGroupTemplateID(value.GroupTemplateID) {
		return false
	}
	if value.GroupPolicy == "" {
		return len(value.AvailableCompositions) == 0
	}
	expected := map[string][]string{
		"none": {"standalone"}, "recommended": {"grouped", "standalone"}, "required": {"grouped"},
	}
	want, ok := expected[value.GroupPolicy]
	if !ok || len(want) != len(value.AvailableCompositions) {
		return false
	}
	for index := range want {
		if value.AvailableCompositions[index] != want[index] {
			return false
		}
	}
	return true
}
func isPreparationAction(action ActionID) bool {
	return action == ActionReviewCreateGroup || action == ActionCreateGroup
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
func preparationInputDigest(_ ActionID, raw json.RawMessage) (string, error) {
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
	if action != ActionCreateGroup {
		return ActionReviewMaterial{}, ErrInvalid
	}
	if home.Exists {
		return ActionReviewMaterial{}, ErrConflict
	}
	home.Name, err = groupName(raw)
	if err != nil {
		return ActionReviewMaterial{}, err
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
	if action != ActionCreateGroup {
		return CanonicalResult{}, ErrInvalid
	}
	name, nameErr := groupName(raw)
	if nameErr != nil {
		return CanonicalResult{}, nameErr
	}
	home, err := a.owner.CreateHome(projectConnectionScope(scope, template), name)
	if err != nil {
		return CanonicalResult{}, projectConnectionFailure(err)
	}
	result := CanonicalResult{}
	if scope.RunKind == RunKindRoot {
		result.HomeWorkspaceID = home.HomeID
	}
	return result, nil
}
