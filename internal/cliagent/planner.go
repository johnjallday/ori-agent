package cliagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// StepPlanner uses an LLM to decompose tasks into micro-steps and manage
// inter-step context via summarization.
type StepPlanner struct {
	provider llm.Provider
	model    string
}

// NewStepPlanner creates a StepPlanner backed by the given LLM provider and model.
func NewStepPlanner(provider llm.Provider, model string) *StepPlanner {
	return &StepPlanner{provider: provider, model: model}
}

// DecomposeTask asks the LLM to break a task into numbered steps.
func (p *StepPlanner) DecomposeTask(ctx context.Context, taskPrompt string) ([]StepPlan, error) {
	systemPrompt := `You are a task planner. Break the given task into a sequence of concrete, actionable steps.
Each step should be a single unit of work that can be completed by a CLI coding agent in one invocation.
Return ONLY a JSON array of objects with fields: "step_number" (int), "description" (string), "expected_outcome" (string).
Do not include any other text or markdown. Keep it to 3-7 steps.`

	resp, err := p.provider.Chat(ctx, llm.ChatRequest{
		Model:        p.model,
		SystemPrompt: systemPrompt,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: taskPrompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("decompose task: %w", err)
	}

	return parseStepPlans(resp.Content)
}

// GenerateStepPrompt builds the prompt for the next CLI invocation, incorporating
// the step plan and summarized previous results.
func (p *StepPlanner) GenerateStepPrompt(step StepPlan, previousSummaries []string) string {
	var b strings.Builder

	if len(previousSummaries) > 0 {
		b.WriteString("## Context from previous steps\n\n")
		for i, summary := range previousSummaries {
			fmt.Fprintf(&b, "### Step %d result\n%s\n\n", i+1, summary)
		}
	}

	b.WriteString("## Current task\n\n")
	b.WriteString(step.Description)
	b.WriteString("\n\n")

	if step.ExpectedOutcome != "" {
		b.WriteString("## Expected outcome\n\n")
		b.WriteString(step.ExpectedOutcome)
		b.WriteString("\n")
	}

	return b.String()
}

// SummarizeStepResult uses the LLM to produce a concise summary of a step's
// output and file changes for use as context in subsequent steps.
func (p *StepPlanner) SummarizeStepResult(ctx context.Context, result StepResult) (string, error) {
	var input strings.Builder
	fmt.Fprintf(&input, "Step %d output:\n%s\n", result.StepNumber, result.Output)

	if len(result.FilesChanged) > 0 {
		input.WriteString("\nFiles changed:\n")
		for _, fc := range result.FilesChanged {
			fmt.Fprintf(&input, "  %s %s", fc.ChangeType, fc.Path)
			if fc.LinesAdded > 0 || fc.LinesRemoved > 0 {
				fmt.Fprintf(&input, " (+%d -%d)", fc.LinesAdded, fc.LinesRemoved)
			}
			input.WriteString("\n")
		}
	}

	resp, err := p.provider.Chat(ctx, llm.ChatRequest{
		Model:        p.model,
		SystemPrompt: "Summarize the following step result in 2-3 sentences. Focus on what was accomplished and any important details for subsequent steps. Return only the summary text.",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: input.String()},
		},
	})
	if err != nil {
		// Fall back to a basic summary if LLM fails
		return fmt.Sprintf("Step %d: %s (status: %s)", result.StepNumber, truncate(result.Output, 200), result.Status), nil
	}

	return strings.TrimSpace(resp.Content), nil
}

// EvaluateCompletion asks the LLM whether the task is done based on accumulated results.
// Returns (done, rationale, error).
func (p *StepPlanner) EvaluateCompletion(ctx context.Context, taskPrompt string, summaries []string) (bool, string, error) {
	var input strings.Builder
	fmt.Fprintf(&input, "Original task: %s\n\nCompleted steps:\n", taskPrompt)
	for i, s := range summaries {
		fmt.Fprintf(&input, "Step %d: %s\n", i+1, s)
	}

	resp, err := p.provider.Chat(ctx, llm.ChatRequest{
		Model:        p.model,
		SystemPrompt: `Evaluate whether the original task is complete based on the step results. Return ONLY a JSON object with fields: "done" (boolean), "rationale" (string explaining why or why not). No other text.`,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: input.String()},
		},
	})
	if err != nil {
		return false, "", fmt.Errorf("evaluate completion: %w", err)
	}

	return parseCompletionEval(resp.Content)
}

// parseStepPlans parses JSON step plans from LLM output.
func parseStepPlans(content string) ([]StepPlan, error) {
	content = strings.TrimSpace(content)

	// Strip markdown code fences if present
	content = stripCodeFences(content)

	var plans []StepPlan
	if err := json.Unmarshal([]byte(content), &plans); err != nil {
		return nil, fmt.Errorf("parse step plans: %w (content: %s)", err, truncate(content, 200))
	}
	if len(plans) == 0 {
		return nil, fmt.Errorf("step planner returned empty plan")
	}
	return plans, nil
}

// parseCompletionEval parses the completion evaluation JSON from LLM output.
func parseCompletionEval(content string) (bool, string, error) {
	content = strings.TrimSpace(content)
	content = stripCodeFences(content)

	var eval struct {
		Done      bool   `json:"done"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(content), &eval); err != nil {
		return false, "", fmt.Errorf("parse completion eval: %w", err)
	}
	return eval.Done, eval.Rationale, nil
}

// stripCodeFences removes markdown code fence wrappers if present.
func stripCodeFences(s string) string {
	return llm.StripCodeFence(s)
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
