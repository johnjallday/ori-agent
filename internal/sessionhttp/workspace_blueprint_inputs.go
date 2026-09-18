package sessionhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
)

// createWorkspaceInputs is the outcome of validating a create request's
// blueprint_inputs. Declaration is carried alongside the values so the record
// written onto the workspace can keep the labels and units that go with them.
type createWorkspaceInputs struct {
	declaration *projecttemplates.InputsDeclaration
	values      map[string]string
	// provided is the client's raw map, passed on to instantiation so the one
	// validator runs against the same bytes rather than a re-encoded copy.
	provided map[string]json.RawMessage
}

func (inputs *createWorkspaceInputs) hasValues() bool {
	return inputs != nil && len(inputs.values) > 0
}

// resolveCreateWorkspaceInputs decides what a create request's declared inputs
// resolve to, or why they are refused.
//
// Inputs belong to one flow only: scaffolding a new project from a blueprint.
// Every other way to create a workspace — a group, Blank, an attached existing
// project — has no scaffold to write them into, so supplying them there is a
// mistake worth naming rather than a value to ignore. Requirements 38 and 39.
func resolveCreateWorkspaceInputs(
	req createWorkspaceRequest,
	kind session.WorkspaceKind,
	template projecttemplates.Template,
	templateResolved bool,
) (*createWorkspaceInputs, error) {
	provided := req.BlueprintInputs
	supplied := len(provided) > 0

	var refusal string
	switch {
	case kind == session.WorkspaceKindGroup:
		refusal = "blueprint inputs are not available for groups; a group has no project to write them into"
	case req.Blank:
		refusal = "blueprint inputs are not available for a blank workspace; choose a blueprint that asks for them"
	case req.ProjectConnection != nil:
		refusal = "blueprint inputs are not available when using an existing project; its own project file is kept as it is"
	case !templateResolved:
		refusal = "blueprint inputs require a blueprint; the selected one is unavailable"
	case template.HasInvalidInputs():
		refusal = fmt.Sprintf("this blueprint's inputs are unavailable: %s", template.InputsError)
	}
	if refusal != "" {
		if supplied {
			return nil, errors.New(refusal)
		}
		return nil, nil
	}
	if !template.HasInputs() && !supplied {
		return nil, nil
	}

	// A blueprint that declares inputs records values even when the user
	// changed nothing, so "they took the defaults" and "nobody was asked" do
	// not look the same afterwards.
	values, err := projecttemplates.ResolveInputValues(template.Inputs, provided)
	if err != nil {
		return nil, blueprintInputMessage(err)
	}
	if len(values) == 0 {
		return nil, nil
	}
	return &createWorkspaceInputs{declaration: template.Inputs, values: values, provided: provided}, nil
}

// blueprintInputMessage strips the wrapper so the response reads as the field
// problem it is ("Tempo must be between 40 and 240") rather than repeating the
// package's own error vocabulary at the user.
func blueprintInputMessage(err error) error {
	message := err.Error()
	if prefix := projecttemplates.ErrInputValue.Error() + ": "; strings.HasPrefix(message, prefix) {
		message = strings.TrimPrefix(message, prefix)
	}
	return errors.New(message)
}

// recordCreateWorkspaceInputs stores the chosen values on the workspace so its
// agents and its UI can read back what it was created with. Both stores are
// written: workspace.json is canonical, and the session copy is what the
// detail response reads.
func recordCreateWorkspaceInputs(sharedData map[string]any, inputs *createWorkspaceInputs) {
	if !inputs.hasValues() || sharedData == nil {
		return
	}
	projecttemplates.SetBlueprintInputs(sharedData, inputs.declaration, inputs.values)
}

// workspaceBlueprintInputsPayload projects the recorded values for the
// workspace detail response. A record that cannot be read is omitted rather
// than failing the read of everything else about the workspace.
func workspaceBlueprintInputsPayload(sharedData map[string]any) any {
	if len(sharedData) == 0 {
		return nil
	}
	stored, err := projecttemplates.GetBlueprintInputs(sharedData)
	if err != nil || stored == nil {
		return nil
	}
	return stored
}
