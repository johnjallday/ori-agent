package sessionhttp

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

const (
	personalAssistantSupportSharedDataKey = "personal_assistant_presentation"
	personalAssistantSupportGroup         = "assistant_support"
	maxPersonalAssistantPromptFragment    = 2048
	maxPersonalAssistantCombinedPrompt    = 8192
)

type personalAssistantCreationContextKey struct{}

func withPersonalAssistantCreationOptions(ctx context.Context, options personalhq.AssistantCreationOptions) context.Context {
	copyOptions := options
	copyOptions.Appearance = options.Appearance.Clone()
	return context.WithValue(ctx, personalAssistantCreationContextKey{}, copyOptions)
}

func personalAssistantCreationOptions(ctx context.Context) (personalhq.AssistantCreationOptions, bool) {
	if ctx == nil {
		return personalhq.AssistantCreationOptions{}, false
	}
	options, ok := ctx.Value(personalAssistantCreationContextKey{}).(personalhq.AssistantCreationOptions)
	if ok {
		options.Appearance = options.Appearance.Clone()
	}
	return options, ok
}

// CreatePersonalAssistantHQ applies trusted PAF options to a private template
// copy, then delegates to the same in-process POST /api/workspaces creation path
// used by legacy Personal HQ setup.
func (h *Handler) CreatePersonalAssistantHQ(ctx context.Context, workspaceName string, options personalhq.AssistantCreationOptions) (*personalhq.AssistantWorkspaceResult, error) {
	if h == nil || h.store == nil || h.agentStore == nil {
		return nil, fmt.Errorf("session handler is not configured for personal assistant creation")
	}
	options.AssistantID = strings.TrimSpace(options.AssistantID)
	options.RequestID = strings.TrimSpace(options.RequestID)
	if options.AssistantID == "" || options.RequestID == "" {
		return nil, fmt.Errorf("personal assistant and hire request ids are required")
	}
	tpl, err := h.personalHQTemplate()
	if err != nil {
		return nil, err
	}
	if _, err := applyPersonalAssistantTemplateOptions(tpl, options); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(options.DisplayName)
	if existing, found, err := h.findPersonalAssistantHQ(ctx, options, name); err != nil {
		return nil, err
	} else if found {
		return existing, nil
	}
	// A profile under this name normally means a collision. But the assistant is
	// hired before HQ is built, so its own profile is expected to be here — and
	// reusing it is the whole point: the HQ entry agent must BE the hired
	// assistant, not a second agent that shares its name.
	//
	// Reuse is permitted only on provable bounded provenance. An unrelated
	// same-named profile, or one owned by another relationship, stays a conflict.
	// The template path then attaches the existing profile as-is, preserving its
	// saved prompt, model, and appearance rather than recreating or mutating it
	// from this request.
	if existing, exists := h.agentStore.GetAgent(name); exists {
		if _, err := assertProfileOwnedByAssistant(name, existing, options.AssistantID); err != nil {
			return nil, err
		}
	}

	workspaceID, err := h.createFromTemplate(
		withPersonalAssistantCreationOptions(ctx, options),
		workspaceName,
		personalhq.PersonalHQTemplateID,
	)
	if err != nil {
		return nil, err
	}
	workspace, err := h.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load created personal hq: %w", err)
	}
	for _, instance := range workspace.AgentInstances {
		if instance.EntryPoint && strings.EqualFold(strings.TrimSpace(instance.Name), name) {
			return &personalhq.AssistantWorkspaceResult{
				WorkspaceID: workspaceID, EntryAgentInstanceID: instance.ID,
				GlobalAgentProfileName: instance.Name,
			}, nil
		}
	}
	return nil, fmt.Errorf("created personal hq is missing its selected entry-agent instance")
}

func (h *Handler) findPersonalAssistantHQ(ctx context.Context, options personalhq.AssistantCreationOptions, name string) (*personalhq.AssistantWorkspaceResult, bool, error) {
	listed, err := h.store.ListWorkspaces(ctx)
	if err != nil {
		return nil, false, err
	}
	for i := range listed {
		workspace, getErr := h.store.GetWorkspace(ctx, listed[i].ID)
		if getErr != nil {
			continue
		}
		metadata, ok := workspace.SharedData[personalAssistantSupportSharedDataKey].(map[string]any)
		if !ok || strings.TrimSpace(fmt.Sprint(metadata["assistant_id"])) != options.AssistantID ||
			strings.TrimSpace(fmt.Sprint(metadata["request_id"])) != options.RequestID {
			continue
		}
		for _, instance := range workspace.AgentInstances {
			if instance.EntryPoint && strings.EqualFold(strings.TrimSpace(instance.Name), name) {
				return &personalhq.AssistantWorkspaceResult{
					WorkspaceID: workspace.ID, EntryAgentInstanceID: instance.ID,
					GlobalAgentProfileName: instance.Name,
				}, true, nil
			}
		}
		return nil, false, fmt.Errorf("existing personal assistant hq is missing its recorded entry agent")
	}
	return nil, false, nil
}

// applyPersonalAssistantTemplateOptions returns a private PAF copy of the
// personal-ops template. It never mutates the library object shared by later
// setup/plan requests.
func applyPersonalAssistantTemplateOptions(tpl projecttemplates.Template, options personalhq.AssistantCreationOptions) (projecttemplates.Template, error) {
	if strings.TrimSpace(tpl.ID) != personalhq.PersonalHQTemplateID {
		return tpl, fmt.Errorf("personal assistant creation requires the %q template", personalhq.PersonalHQTemplateID)
	}
	if len(tpl.Agents) < 2 || !strings.EqualFold(strings.TrimSpace(tpl.Agents[0].Name), "Personal Chief of Staff") {
		return tpl, fmt.Errorf("personal assistant creation requires the canonical Personal Chief of Staff entry spec")
	}

	name := strings.TrimSpace(options.DisplayName)
	if err := validateTemplateAgentOverrideName(name); err != nil {
		return tpl, err
	}
	if systemassistant.IsKnownName(name) {
		return tpl, fmt.Errorf("assistant name %q is reserved for Ori", name)
	}
	for _, spec := range tpl.Agents[1:] {
		if strings.EqualFold(strings.TrimSpace(spec.Name), name) {
			return tpl, fmt.Errorf("assistant name %q collides with the Personal HQ support roster", name)
		}
	}

	role := options.Role
	if role == "" {
		role = types.RoleOrchestrator
	}
	if role != types.RoleOrchestrator {
		return tpl, fmt.Errorf("personal assistant entry role must be orchestrator")
	}

	appearance := options.Appearance.Clone()
	if appearance == nil {
		appearance = types.NewAgentAppearance()
	} else {
		if !types.IsValidAppearanceMode(appearance.Mode) {
			return tpl, fmt.Errorf("personal assistant appearance mode %q is invalid", appearance.Mode)
		}
		if appearance.Generated != nil && strings.TrimSpace(appearance.Generated.Color) != "" {
			if _, ok := types.NormalizeAppearanceColor(appearance.Generated.Color); !ok {
				return tpl, fmt.Errorf("personal assistant generated appearance color is invalid")
			}
		}
		// Uploaded images are created only by the dedicated upload endpoint; a
		// hire request cannot smuggle a path into the agent record.
		if appearance.Uploaded != nil && strings.TrimSpace(appearance.Uploaded.Image) != "" {
			return tpl, fmt.Errorf("personal assistant uploaded appearance must use the appearance upload endpoint")
		}
		appearance.Normalize()
	}

	fragment := strings.TrimSpace(options.SystemPromptFragment)
	if len(fragment) > maxPersonalAssistantPromptFragment {
		return tpl, fmt.Errorf("personal assistant prompt fragment is too long")
	}

	next := tpl
	next.Agents = append([]projecttemplates.AgentSpec(nil), tpl.Agents...)
	entry := next.Agents[0]
	entry.Name = name
	entry.Role = string(role)
	entry.Appearance = appearance
	if fragment != "" {
		entry.SystemPrompt = strings.TrimSpace(entry.SystemPrompt) + "\n\n" + fragment
	}
	if len(entry.SystemPrompt) > maxPersonalAssistantCombinedPrompt {
		return tpl, fmt.Errorf("personal assistant combined prompt is too long")
	}
	next.Agents[0] = entry
	if err := projecttemplates.ValidateAgentPrompts(next.Agents); err != nil {
		return tpl, err
	}
	if err := validateTemplateAgentOverrideNames(next.Agents); err != nil {
		return tpl, err
	}
	return next, nil
}

// markPersonalAssistantPresentation records only truthful presentation
// metadata. It neither hides/removes Journal nor changes any role, prompt,
// model, tool, skill, or permission.
func markPersonalAssistantPresentation(ws *session.Workspace, options personalhq.AssistantCreationOptions) {
	if ws == nil {
		return
	}
	var supportIDs []string
	for _, instance := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(instance.Name), "Journal") {
			supportIDs = append(supportIDs, instance.ID)
		}
	}
	if ws.SharedData == nil {
		ws.SharedData = make(map[string]any)
	}
	ws.SharedData[personalAssistantSupportSharedDataKey] = map[string]any{
		"version":                    1,
		"assistant_id":               strings.TrimSpace(options.AssistantID),
		"request_id":                 strings.TrimSpace(options.RequestID),
		"support_group":              personalAssistantSupportGroup,
		"support_agent_instance_ids": supportIDs,
	}
}
