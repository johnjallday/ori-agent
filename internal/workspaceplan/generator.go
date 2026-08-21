package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Generator turns a request into validated Plan content.
//
// It is provider-independent by construction: it depends on the narrow
// PlanModel interface below rather than on any LLM package type, and it decodes
// responses into PlanContent rather than persisting a provider's response
// object (FR-18, FR-40).
//
// It also has no authority. Generation produces a candidate; only validation
// decides whether that candidate may become a draft, and only a user can
// approve one (FR-41, FR-59).
type Generator struct {
	model PlanModel
	// maxRepairAttempts bounds how many times invalid output may be sent back
	// for repair before the Plan is left as a draft with a useful error
	// (FR-44, FR-45).
	maxRepairAttempts int
}

// PlanModel is the only model capability this package needs: send a schema and
// a prompt, get JSON back. Keeping it this narrow is what makes the generator
// testable without a provider and swappable without touching planning logic.
type PlanModel interface {
	// GenerateStructured returns the model's raw JSON response. It returns
	// ErrModelUnavailable when no provider is configured or reachable.
	GenerateStructured(ctx context.Context, req StructuredRequest) (string, error)
}

// StructuredRequest is one structured-output call.
type StructuredRequest struct {
	// SchemaName and Schema identify the contract the response must satisfy.
	SchemaName string
	Schema     map[string]any
	// System is the instruction block; Prompt is the request and its context.
	System string
	Prompt string
}

// ErrModelUnavailable reports that no model could be reached. It is deliberately
// distinct from a validation failure: a Plan stays fully editable, reviewable,
// and approvable without a model, and only the generate controls report
// unavailability (FR-58, FR-177).
var ErrModelUnavailable = errors.New("no planning model is available")

// DefaultRepairAttempts is the bounded number of repair round-trips. Two is
// enough to fix the mistakes a model actually makes (a dangling ID, a missed
// required field) and small enough that a model which cannot satisfy the schema
// fails fast rather than looping (FR-44).
const DefaultRepairAttempts = 2

// NewGenerator returns a Plan generator over the given model.
func NewGenerator(model PlanModel, opts ...GeneratorOption) *Generator {
	generator := &Generator{model: model, maxRepairAttempts: DefaultRepairAttempts}
	for _, opt := range opts {
		opt(generator)
	}
	return generator
}

// GeneratorOption configures a Generator.
type GeneratorOption func(*Generator)

// WithRepairAttempts overrides the bounded repair limit.
func WithRepairAttempts(attempts int) GeneratorOption {
	return func(g *Generator) {
		if attempts >= 0 {
			g.maxRepairAttempts = attempts
		}
	}
}

// Available reports whether generation can be attempted at all. The UI uses it
// to disable generate controls while leaving editing, review, and approval
// alone (FR-58).
func (g *Generator) Available() bool { return g != nil && g.model != nil }

// GenerationInput is everything the planner is given. It carries what the
// application knows and the model does not, so the model is never asked to
// guess which agents or capabilities exist (FR-46).
type GenerationInput struct {
	// Request is the exact initiating request (FR-21).
	Request string
	// Objective and Content seed a revision with the current draft. Empty for
	// a first draft.
	Objective string
	Content   PlanContent
	// Answers are the clarification questions and the answers the user
	// authored. They are context for drafting and are never rewritten (FR-25).
	Answers []Clarification
	// Guidance is the model-guidance half of Workspace Settings — planning
	// style, clarification depth, preferred artifacts. It shapes drafting and
	// is never described to the user as a guarantee (FR-124, FR-125, FR-129).
	Guidance GuidanceInput
	// Validation carries the agents and capabilities that actually exist.
	Validation ValidationContext
	// WorkspaceContext is free-form context about the workspace.
	WorkspaceContext string
	// Sections optionally limits a revision to named sections, preserving
	// everything else (FR-54).
	Sections []string
}

// GuidanceInput is the subset of Workspace Settings that shapes drafting.
// Nothing here is enforcement: a model may ignore all of it, which is exactly
// why enforced policy lives elsewhere and is checked in compiled code
// (FR-124, FR-129).
type GuidanceInput struct {
	// Style is the planning style, for example "feature" or "investigation".
	Style string
	// ClarificationDepth is "minimal", "standard", or "deep".
	ClarificationDepth string
	// MaxQuestionsPerRound is enforced by the application after generation,
	// not merely requested of the model (FR-27).
	MaxQuestionsPerRound int
	// PreferredArtifacts names optional model-proposed outputs. Canonical PRD
	// and task-list exports are compiled separately through ArtifactPolicy.
	PreferredArtifacts []string
	// DetailLevel is a free-form preference such as "concise".
	DetailLevel string
}

// GenerationOutcome is what a generation attempt produced. Exactly one of
// Content or Questions is meaningful: a planner either drafts or asks (FR-22,
// FR-23).
type GenerationOutcome struct {
	// NeedsInput is true when the planner asked questions instead of drafting.
	NeedsInput bool
	Objective  string
	Content    PlanContent
	Questions  []Clarification
	// Attempts counts the model round-trips, including repairs.
	Attempts int
	// Issues carries the validation issues that survived the repair budget.
	// Non-empty means the caller must keep the Plan a draft (FR-45).
	Issues []ValidationIssue
}

// DefaultMaxQuestions bounds one clarification round when Workspace Settings
// name no limit. The cap is applied by the application after generation, so a
// model that ignores the instruction still cannot flood the user (FR-27).
const DefaultMaxQuestions = 5

// GenerateDraft produces validated Plan content for a clear request (FR-22).
//
// Invalid output is sent back for a bounded number of repairs, each carrying
// the specific issues to fix. When the budget is exhausted the outcome carries
// the issues and the caller keeps the Plan a draft with a useful error —
// unvalidated free text is never accepted as a fallback (FR-44, FR-45).
func (g *Generator) GenerateDraft(ctx context.Context, input GenerationInput) (GenerationOutcome, error) {
	if !g.Available() {
		return GenerationOutcome{}, ErrModelUnavailable
	}

	prompt := draftPrompt(input)
	var outcome GenerationOutcome

	for attempt := 0; attempt <= g.maxRepairAttempts; attempt++ {
		raw, err := g.model.GenerateStructured(ctx, StructuredRequest{
			SchemaName: "workspace_plan",
			Schema:     PlanContentSchema(),
			System:     draftSystemPrompt(input),
			Prompt:     prompt,
		})
		outcome.Attempts = attempt + 1
		if err != nil {
			return outcome, err
		}

		objective, content, decodeErr := DecodePlanContent([]byte(raw))
		if decodeErr != nil {
			// A response that is not even the right shape gets the same
			// bounded repair treatment as one that is.
			outcome.Issues = []ValidationIssue{{
				Code:    IssueInvalidEnum,
				Message: decodeErr.Error(),
			}}
			prompt = repairPrompt(input, outcome.Issues)
			continue
		}

		result := ValidatePlanContent(objective, content, input.Validation)
		if result.OK() {
			return GenerationOutcome{
				Objective: objective,
				Content:   content,
				Attempts:  outcome.Attempts,
			}, nil
		}

		outcome.Objective = objective
		outcome.Content = content
		outcome.Issues = result.Issues
		prompt = repairPrompt(input, result.Issues)
	}

	// The budget is spent. The caller keeps the Plan a draft and shows these
	// issues; it does not accept the content (FR-45).
	return outcome, fmt.Errorf("%w: the planner could not produce a valid plan after %d attempts",
		ErrValidation, outcome.Attempts)
}

// GenerateClarifications asks for the questions a planner needs answered before
// it can draft (FR-23).
//
// The configured maximum questions per round is enforced here rather than only
// requested in the prompt, so a model that asks twelve questions still shows
// the user the configured number (FR-27).
func (g *Generator) GenerateClarifications(ctx context.Context, input GenerationInput) ([]Clarification, error) {
	if !g.Available() {
		return nil, ErrModelUnavailable
	}

	raw, err := g.model.GenerateStructured(ctx, StructuredRequest{
		SchemaName: "workspace_plan_clarifications",
		Schema:     ClarificationSchema(),
		System:     clarificationSystemPrompt(input),
		Prompt:     clarificationPrompt(input),
	})
	if err != nil {
		return nil, err
	}

	questions, err := DecodeClarifications([]byte(raw))
	if err != nil {
		return nil, err
	}
	return CapQuestions(questions, input.Guidance.MaxQuestionsPerRound), nil
}

// CapQuestions enforces the per-round question limit. It keeps required
// questions first, so trimming never drops a question the plan actually needs
// in favor of a nice-to-have (FR-27).
func CapQuestions(questions []Clarification, max int) []Clarification {
	if max <= 0 {
		max = DefaultMaxQuestions
	}
	if len(questions) <= max {
		return questions
	}
	kept := make([]Clarification, 0, max)
	for _, question := range questions {
		if question.Required && len(kept) < max {
			kept = append(kept, question)
		}
	}
	for _, question := range questions {
		if !question.Required && len(kept) < max {
			kept = append(kept, question)
		}
	}
	return kept
}

// DecodeClarifications turns a model response into stable clarification
// records (FR-24).
func DecodeClarifications(raw []byte) ([]Clarification, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("%w: model returned no questions", ErrValidation)
	}

	var decoded struct {
		Questions []struct {
			ID       string   `json:"id"`
			Prompt   string   `json:"prompt"`
			Detail   string   `json:"detail"`
			Options  []string `json:"options"`
			Required bool     `json:"required"`
		} `json:"questions"`
	}
	if err := decodeStrictJSON(trimmed, &decoded); err != nil {
		return nil, fmt.Errorf("%w: model output did not match the clarification schema: %v",
			ErrValidation, err)
	}

	now := time.Now().UTC()
	questions := make([]Clarification, 0, len(decoded.Questions))
	for _, question := range decoded.Questions {
		prompt := strings.TrimSpace(question.Prompt)
		if prompt == "" {
			continue
		}
		questions = append(questions, Clarification{
			ID:        orNewID(question.ID, NewClarificationID),
			Prompt:    prompt,
			Detail:    strings.TrimSpace(question.Detail),
			Options:   trimAll(question.Options),
			Required:  question.Required,
			Status:    ClarificationOpen,
			CreatedAt: now,
		})
	}
	if len(questions) == 0 {
		return nil, fmt.Errorf("%w: model returned no usable questions", ErrValidation)
	}
	return questions, nil
}

// --- Prompt composition ----------------------------------------------------
//
// Prompts state what is available and what the model may not decide. They never
// claim the model's compliance is a guarantee: enforcement lives in compiled
// code, and the prompt says so where it matters (FR-47, FR-129).

func draftSystemPrompt(input GenerationInput) string {
	var b strings.Builder
	b.WriteString("You draft structured work plans for a workspace.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Return only the structured plan. Prose belongs in `explanation` and carries no meaning.\n")
	b.WriteString("- Express dependencies with the ids of other groups or items, never with text or ordering.\n")
	b.WriteString("- Reuse an existing id exactly when you revise that element; omit the id for new elements.\n")
	b.WriteString("- Never claim an agent, capability, tool, or output type that is not listed as available.\n")
	b.WriteString("- Leave `assignee` empty rather than guessing. An unassigned item becomes an unassigned task.\n")
	b.WriteString("- Do not propose `prd` or `task_list` artifacts. The application exports those from the approved typed Plan.\n")
	b.WriteString("- You are proposing work. You cannot approve it, create tasks, or start execution.\n")

	if style := strings.TrimSpace(input.Guidance.Style); style != "" {
		fmt.Fprintf(&b, "\nPreferred planning style: %s.\n", style)
	}
	if detail := strings.TrimSpace(input.Guidance.DetailLevel); detail != "" {
		fmt.Fprintf(&b, "Preferred level of detail: %s.\n", detail)
	}
	if len(input.Guidance.PreferredArtifacts) > 0 {
		fmt.Fprintf(&b, "Preferred artifacts: %s.\n", strings.Join(input.Guidance.PreferredArtifacts, ", "))
	}

	writeAvailability(&b, input.Validation)
	fmt.Fprintf(&b, "\nHard limits: at most %d task groups and %d task items.\n",
		MaxTaskGroups, MaxTaskItems)
	return b.String()
}

func clarificationSystemPrompt(input GenerationInput) string {
	max := input.Guidance.MaxQuestionsPerRound
	if max <= 0 {
		max = DefaultMaxQuestions
	}

	var b strings.Builder
	b.WriteString("You ask the few questions needed before a work plan can be drafted.\n\n")
	b.WriteString("Rules:\n")
	fmt.Fprintf(&b, "- Ask at most %d questions.\n", max)
	b.WriteString("- Ask only about product decisions you cannot reasonably infer.\n")
	b.WriteString("- Mark a question required only when the plan genuinely cannot be drafted without it.\n")
	b.WriteString("- Never ask for credentials, tokens, or secrets.\n")

	if depth := strings.TrimSpace(input.Guidance.ClarificationDepth); depth != "" {
		fmt.Fprintf(&b, "\nClarification depth preference: %s.\n", depth)
	}
	return b.String()
}

func writeAvailability(b *strings.Builder, vctx ValidationContext) {
	if len(vctx.AvailableAgents) > 0 {
		fmt.Fprintf(b, "\nAvailable agents: %s.\n", strings.Join(vctx.AvailableAgents, ", "))
	} else if vctx.AvailableAgents != nil {
		b.WriteString("\nNo agents are available; leave every assignee empty.\n")
	}
	if len(vctx.AvailableCapabilities) > 0 {
		fmt.Fprintf(b, "Available capabilities: %s.\n", strings.Join(vctx.AvailableCapabilities, ", "))
	} else if vctx.AvailableCapabilities != nil {
		b.WriteString("No capabilities are available; do not require any.\n")
	}
}

func draftPrompt(input GenerationInput) string {
	var b strings.Builder
	b.WriteString("Request:\n")
	b.WriteString(input.Request)
	b.WriteString("\n")

	if context := strings.TrimSpace(input.WorkspaceContext); context != "" {
		b.WriteString("\nWorkspace context:\n")
		b.WriteString(context)
		b.WriteString("\n")
	}
	writeAnswers(&b, input.Answers)

	if len(input.Content.Groups) > 0 {
		b.WriteString("\nCurrent plan (revise it; reuse every id you keep):\n")
		if encoded, err := marshalIndent(struct {
			Objective string      `json:"objective"`
			Content   PlanContent `json:"content"`
		}{input.Objective, input.Content}); err == nil {
			b.Write(encoded)
			b.WriteString("\n")
		}
	}
	if len(input.Sections) > 0 {
		// A targeted revision must leave everything else identified and
		// intact, which is only possible if ids come back unchanged (FR-55).
		fmt.Fprintf(&b, "\nRevise only these sections: %s. Return the whole plan, "+
			"leaving every other element and every id exactly as it is.\n",
			strings.Join(input.Sections, ", "))
	}
	return b.String()
}

func clarificationPrompt(input GenerationInput) string {
	var b strings.Builder
	b.WriteString("Request:\n")
	b.WriteString(input.Request)
	b.WriteString("\n")
	if context := strings.TrimSpace(input.WorkspaceContext); context != "" {
		b.WriteString("\nWorkspace context:\n")
		b.WriteString(context)
		b.WriteString("\n")
	}
	writeAnswers(&b, input.Answers)
	return b.String()
}

// writeAnswers includes what the user already told us, so a second round never
// re-asks an answered question.
func writeAnswers(b *strings.Builder, answers []Clarification) {
	answered := make([]Clarification, 0, len(answers))
	skipped := make([]Clarification, 0, len(answers))
	for _, question := range answers {
		switch question.Status {
		case ClarificationAnswered:
			answered = append(answered, question)
		case ClarificationSkipped:
			skipped = append(skipped, question)
		}
	}

	if len(answered) > 0 {
		b.WriteString("\nAnswers already given (treat as authoritative; do not re-ask):\n")
		for _, question := range answered {
			fmt.Fprintf(b, "- %s\n  %s\n", question.Prompt, question.Answer)
		}
	}
	if len(skipped) > 0 {
		b.WriteString("\nQuestions the user skipped (record your assumption instead of re-asking):\n")
		for _, question := range skipped {
			fmt.Fprintf(b, "- %s\n", question.Prompt)
		}
	}
}

// repairPrompt asks for a corrected plan, naming every issue at once so one
// round-trip can fix them all (FR-44).
func repairPrompt(input GenerationInput, issues []ValidationIssue) string {
	var b strings.Builder
	b.WriteString(draftPrompt(input))
	b.WriteString("\nYour previous response was rejected. Fix all of these and return the whole plan:\n")
	for _, issue := range issues {
		if issue.ID != "" {
			fmt.Fprintf(&b, "- [%s] %s (id: %s)\n", issue.Code, issue.Message, issue.ID)
			continue
		}
		fmt.Fprintf(&b, "- [%s] %s\n", issue.Code, issue.Message)
	}
	return b.String()
}
