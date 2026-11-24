package chathttp

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Action Preview: one immediate action, shown before it runs (FR-20).
//
// This used to be called an "action plan" and could carry delegation steps,
// planner tasks, and multi-agent workflows — approved by one click in a chat
// bubble that left no durable record. Two things wearing the word "plan" meant
// a user could approve what looked like a plan and get work no audit could
// later explain.
//
// It is now exactly what its name says: a preview of ONE action the user
// already named. Anything larger is Plan work and is routed there by
// ClassifyPlanningRoute before this code is reached. The constructor refuses
// the rest rather than trusting that routing was correct — a preview that
// silently accepted a task tree would reintroduce the whole problem.

// ActionPreviewStep is the single action.
type ActionPreviewStep struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Details   string `json:"details,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ActionPreview is one immediate action awaiting approval.
type ActionPreview struct {
	ID        string              `json:"id"`
	Request   string              `json:"request"`
	RouteMode string              `json:"route_mode"`
	Summary   string              `json:"summary"`
	Steps     []ActionPreviewStep `json:"steps"`
	CreatedAt time.Time           `json:"created_at"`
}

// ErrNotAnImmediateAction reports that a request is Plan work and must not be
// previewed inline.
var ErrNotAnImmediateAction = errors.New("this request needs a plan, not an action preview")

// previewKinds are the step kinds an Action Preview may contain.
//
// Written as an allowlist rather than a denylist on purpose: a new step kind
// added elsewhere in the app defaults to REFUSED here, so widening the preview
// is a deliberate edit to this list rather than something that happens by
// accident.
var previewKinds = map[string]bool{
	"tool_call":      true,
	"assistant_chat": true,
}

// newActionPreviewID returns an ID that cannot be confused with a durable Plan.
//
// The old implementation returned "plan_<uuid>" — the exact prefix
// workspaceplan uses for real Plans. A log line, a URL, or a support question
// naming that ID was ambiguous between a record that survives and one that does
// not.
func newActionPreviewID() string {
	return "preview_" + uuid.NewString()
}

// NewActionPreview builds a preview for one immediate action, or refuses.
//
// The refusal is the point. Routing decides where a request goes; this checks
// the decision held, so a bug upstream produces a visible error rather than an
// inline approval for work that should have been a Plan.
func NewActionPreview(request, routeMode, summary string, step ActionPreviewStep) (*ActionPreview, error) {
	if !previewKinds[strings.TrimSpace(step.Kind)] {
		return nil, fmt.Errorf("%w: %q is not an immediate action", ErrNotAnImmediateAction, step.Kind)
	}
	return &ActionPreview{
		ID:        newActionPreviewID(),
		Request:   strings.TrimSpace(request),
		RouteMode: routeMode,
		Summary:   strings.TrimSpace(summary),
		Steps:     []ActionPreviewStep{step},
		CreatedAt: time.Now(),
	}, nil
}

// directToolPreview previews an explicitly named `/tool` command.
func directToolPreview(request string, cmd *DirectToolCommand) (*ActionPreview, error) {
	toolName := ""
	args := ""
	if cmd != nil {
		toolName = strings.TrimSpace(cmd.ToolName)
		args = strings.TrimSpace(cmd.Args)
	}
	if toolName == "" {
		return nil, fmt.Errorf("%w: no tool was named", ErrNotAnImmediateAction)
	}

	return NewActionPreview(
		request,
		routeModeDirectTool,
		fmt.Sprintf("`%s` is ready to run after approval.", toolName),
		ActionPreviewStep{
			Kind:      "tool_call",
			Title:     fmt.Sprintf("Run `%s`", toolName),
			Details:   "Run the tool you asked for.",
			ToolName:  toolName,
			Arguments: args,
		},
	)
}

// utilityToolPreview previews a single utility tool the router selected.
func utilityToolPreview(request string, decision UtilityRouteDecision) (*ActionPreview, error) {
	toolName := strings.TrimSpace(decision.ToolName)
	if toolName == "" {
		return nil, fmt.Errorf("%w: the router named no tool", ErrNotAnImmediateAction)
	}

	return NewActionPreview(
		request,
		string(UtilityRouteDirect),
		fmt.Sprintf("`%s` is ready to run after approval.", toolName),
		ActionPreviewStep{
			Kind:      "tool_call",
			Title:     fmt.Sprintf("Run `%s`", toolName),
			Details:   strings.TrimSpace(decision.Reason),
			ToolName:  toolName,
			Arguments: strings.TrimSpace(decision.ToolArgs),
		},
	)
}

// formatActionPreviewMessage renders the preview as chat text.
//
// It says "action", never "plan": the word plan belongs to the durable record,
// and reusing it here is what made the two indistinguishable in the first
// place.
func formatActionPreviewMessage(preview *ActionPreview) string {
	if preview == nil || len(preview.Steps) == 0 {
		return "This action is ready for approval."
	}

	step := preview.Steps[0]
	var sb strings.Builder
	sb.WriteString("**Next action**\n")
	title := strings.TrimSpace(step.Title)
	if title == "" {
		title = "Run the requested action"
	}
	sb.WriteString(title)
	if details := strings.TrimSpace(step.Details); details != "" {
		sb.WriteString("\n")
		sb.WriteString(details)
	}
	if summary := strings.TrimSpace(preview.Summary); summary != "" {
		sb.WriteString("\n\n")
		sb.WriteString(summary)
	}
	return strings.TrimSpace(sb.String())
}

// manualNextStepGiveWork names the one manual next step chat points at for work
// it will not start: the workspace's Give work dialog, where the person writes
// the first ticket themselves. It is a value for clients to read, not a route.
const manualNextStepGiveWork = "give_work"

// manualNextStepMessage is what chat says for a request that is several steps.
//
// Chat has no Plan to open any more and starts nothing for this work: not a
// ticket, not an agent, not a run, and not an inline approval. The reply says so
// plainly, gives the reason it is more than one action, and names the manual
// next step. It never says "plan": there is nothing by that name to open.
func manualNextStepMessage(classification PlanningClassification) string {
	reason := strings.TrimSpace(classification.Reason)
	if reason == "" {
		reason = "This is more than a single action."
	}
	return reason + " I haven't started anything.\n\n" +
		"To track it as work, open the workspace and use **Give work** to write the first ticket. " +
		"If you'd like me to do one specific step now, tell me which."
}
