package chathttp

import (
	"encoding/json"
	"fmt"
	"strings"
)

type WorkflowStepType string

const (
	WorkflowStepShowPlan      WorkflowStepType = "show_plan"
	WorkflowStepAskChoice     WorkflowStepType = "ask_choice"
	WorkflowStepAskForm       WorkflowStepType = "ask_form"
	WorkflowStepExecuteAction WorkflowStepType = "execute_action"
	WorkflowStepHandoff       WorkflowStepType = "handoff"
	WorkflowStepFinalAnswer   WorkflowStepType = "final_answer"
)

type WorkflowResponseType string

const (
	WorkflowResponseChoice WorkflowResponseType = "choice"
	WorkflowResponseForm   WorkflowResponseType = "form"
	WorkflowResponseText   WorkflowResponseType = "text"
)

type WorkflowStep struct {
	WorkflowID             string           `json:"workflow_id"`
	StepID                 string           `json:"step_id"`
	StepType               WorkflowStepType `json:"step_type"`
	Title                  string           `json:"title"`
	Summary                string           `json:"summary,omitempty"`
	PreviewMarkdown        string           `json:"preview_markdown,omitempty"`
	Choices                []WorkflowChoice `json:"choices,omitempty"`
	Form                   *WorkflowForm    `json:"form,omitempty"`
	FreeTextAllowed        bool             `json:"free_text_allowed,omitempty"`
	SuggestedPrimaryAction string           `json:"suggested_primary_action,omitempty"`
}

type WorkflowChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type WorkflowForm struct {
	ID                 string              `json:"id"`
	Kind               string              `json:"kind,omitempty"`
	Title              string              `json:"title"`
	Subtitle           string              `json:"subtitle,omitempty"`
	Summary            string              `json:"summary,omitempty"`
	SubmitLabel        string              `json:"submit_label,omitempty"`
	SubmitInstructions string              `json:"submit_instructions,omitempty"`
	Fields             []WorkflowFormField `json:"fields,omitempty"`
}

type WorkflowFormField struct {
	ID          string                    `json:"id"`
	Type        string                    `json:"type"`
	Label       string                    `json:"label"`
	HelpText    string                    `json:"help_text,omitempty"`
	Placeholder string                    `json:"placeholder,omitempty"`
	Required    bool                      `json:"required,omitempty"`
	Rows        int                       `json:"rows,omitempty"`
	Options     []WorkflowFormOption      `json:"options,omitempty"`
	VisibleWhen *PlanningFormVisibility   `json:"visible_when,omitempty"`
	FileConfig  *PlanningFormFileQuestion `json:"file_config,omitempty"`
}

type WorkflowFormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type WorkflowUserResponse struct {
	WorkflowID   string               `json:"workflow_id"`
	StepID       string               `json:"step_id"`
	ResponseType WorkflowResponseType `json:"response_type"`
	ChoiceID     string               `json:"choice_id,omitempty"`
	ChoiceLabel  string               `json:"choice_label,omitempty"`
	ChoiceNumber string               `json:"choice_number,omitempty"`
	Form         *WorkflowFormData    `json:"form,omitempty"`
	Text         string               `json:"text,omitempty"`
}

type WorkflowFormData struct {
	FormID          string                   `json:"form_id"`
	FormKind        string                   `json:"form_kind,omitempty"`
	FormTitle       string                   `json:"form_title,omitempty"`
	OriginalRequest string                   `json:"original_request,omitempty"`
	Answers         []WorkflowFormAnswer     `json:"answers,omitempty"`
	Attachments     []WorkflowFormAttachment `json:"attachments,omitempty"`
}

type WorkflowFormAnswer struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Type         string `json:"type,omitempty"`
	Value        string `json:"value,omitempty"`
	DisplayValue string `json:"display_value,omitempty"`
	Required     bool   `json:"required,omitempty"`
}

type WorkflowFormAttachment struct {
	ID                string `json:"id"`
	Label             string `json:"label,omitempty"`
	AttachmentKind    string `json:"attachment_kind,omitempty"`
	UploadModalOpened bool   `json:"upload_modal_opened,omitempty"`
}

func buildWorkflowStepFromPlanningForm(form *PlanningForm, sessionID string) *WorkflowStep {
	if form == nil {
		return nil
	}

	workflowID := buildWorkflowID(sessionID, form.ID)
	fields := make([]WorkflowFormField, 0, len(form.Questions))
	for _, question := range form.Questions {
		field := WorkflowFormField{
			ID:          question.ID,
			Type:        question.Type,
			Label:       question.Label,
			HelpText:    question.HelpText,
			Placeholder: question.Placeholder,
			Required:    question.Required,
			Rows:        question.Rows,
			VisibleWhen: question.VisibleWhen,
			FileConfig:  question.FileConfig,
		}
		if len(question.Options) > 0 {
			field.Options = make([]WorkflowFormOption, 0, len(question.Options))
			for _, option := range question.Options {
				field.Options = append(field.Options, WorkflowFormOption{
					Value: option.Value,
					Label: option.Label,
				})
			}
		}
		fields = append(fields, field)
	}

	return &WorkflowStep{
		WorkflowID:      workflowID,
		StepID:          buildWorkflowStepID(form.ID),
		StepType:        WorkflowStepAskForm,
		Title:           strings.TrimSpace(form.Title),
		Summary:         strings.TrimSpace(form.Summary),
		PreviewMarkdown: strings.TrimSpace(form.Subtitle),
		Form: &WorkflowForm{
			ID:                 form.ID,
			Kind:               form.Kind,
			Title:              form.Title,
			Subtitle:           form.Subtitle,
			Summary:            form.Summary,
			SubmitLabel:        form.SubmitLabel,
			SubmitInstructions: form.SubmitInstructions,
			Fields:             fields,
		},
		FreeTextAllowed: false,
	}
}

func buildPromptFromWorkflowResponse(resp *WorkflowUserResponse) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("workflow_response is required")
	}
	switch resp.ResponseType {
	case WorkflowResponseForm:
		if resp.Form == nil {
			return "", fmt.Errorf("workflow_response.form is required")
		}
		if strings.TrimSpace(resp.Form.FormID) == "" {
			return "", fmt.Errorf("workflow_response.form.form_id is required")
		}

		payload := map[string]any{
			"form_id":          strings.TrimSpace(resp.Form.FormID),
			"form_kind":        strings.TrimSpace(resp.Form.FormKind),
			"form_title":       strings.TrimSpace(resp.Form.FormTitle),
			"original_request": strings.TrimSpace(resp.Form.OriginalRequest),
			"answers":          resp.Form.Answers,
			"attachments":      resp.Form.Attachments,
		}

		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to encode workflow form response: %w", err)
		}

		return strings.Join([]string{
			"Structured planning form submission:",
			string(encoded),
			"",
			"Follow-up instructions:",
			"Use this structured planning intake to continue the conversation.",
		}, "\n"), nil
	case WorkflowResponseChoice:
		if strings.TrimSpace(resp.ChoiceID) == "" && strings.TrimSpace(resp.ChoiceLabel) == "" {
			return "", fmt.Errorf("workflow_response.choice_id or workflow_response.choice_label is required")
		}

		payload := map[string]any{
			"workflow_id":   strings.TrimSpace(resp.WorkflowID),
			"step_id":       strings.TrimSpace(resp.StepID),
			"choice_id":     strings.TrimSpace(resp.ChoiceID),
			"choice_label":  strings.TrimSpace(resp.ChoiceLabel),
			"choice_number": strings.TrimSpace(resp.ChoiceNumber),
			"text":          strings.TrimSpace(resp.Text),
		}

		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to encode workflow choice response: %w", err)
		}

		return strings.Join([]string{
			"Structured workflow choice selection:",
			string(encoded),
			"",
			"Follow-up instructions:",
			"Treat this as the user's selected next step and continue from the current conversation state.",
			"Do not ask the user to repeat the option number or restate the same choice.",
		}, "\n"), nil
	case WorkflowResponseText:
		text := strings.TrimSpace(resp.Text)
		if text == "" {
			return "", fmt.Errorf("workflow_response.text is required")
		}
		return text, nil
	default:
		return "", fmt.Errorf("unsupported workflow_response type %q", resp.ResponseType)
	}
}

func buildWorkflowID(sessionID, formID string) string {
	baseSessionID := strings.TrimSpace(sessionID)
	baseFormID := strings.TrimSpace(formID)
	if baseSessionID == "" {
		baseSessionID = "workspace"
	}
	if baseFormID == "" {
		baseFormID = "step"
	}
	return "workflow:" + baseSessionID + ":" + baseFormID
}

func buildWorkflowStepID(formID string) string {
	baseFormID := strings.TrimSpace(formID)
	if baseFormID == "" {
		baseFormID = "step"
	}
	return "step:" + baseFormID
}
