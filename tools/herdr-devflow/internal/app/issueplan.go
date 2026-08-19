package app

// This file is the `wt herd issue-plan` command: the Go half of
// `wt plan --issue <N>`. `wt.sh` resolves the exact ori-agent-dev worktree
// and forwards it here as separate argument words, never as a shell string,
// so a title, label, or Issue body containing shell metacharacters is data
// this command reads rather than syntax anything runs.
//
// Confirmation happens here, not in the shell, the same way `wt herd
// overnight start` confirms its own plan: BuildIssuePlan performs the one
// fresh GitHub read and every other read-only check, the plan is rendered,
// and only an explicit answer (interactive "y", or --yes) reaches
// ExecuteIssuePlan. Declining leaves every file, bridge record, tab, and
// agent exactly as it was.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/agents"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/audit"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
)

type issuePlanArgs struct {
	issueNumber int
	worktree    string
	yes         bool
	json        bool
}

func parseIssuePlanArgs(args []string) (issuePlanArgs, error) {
	var parsed issuePlanArgs
	var issueSeen, worktreeSeen bool
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--issue":
			if index+1 >= len(args) {
				return issuePlanArgs{}, fmt.Errorf("--issue requires a positive Issue number")
			}
			if issueSeen {
				return issuePlanArgs{}, fmt.Errorf("issue-plan accepts --issue only once")
			}
			index++
			number, err := strconv.Atoi(args[index])
			if err != nil || number <= 0 {
				return issuePlanArgs{}, fmt.Errorf("--issue requires a positive Issue number")
			}
			parsed.issueNumber = number
			issueSeen = true
		case "--worktree":
			if index+1 >= len(args) {
				return issuePlanArgs{}, fmt.Errorf("--worktree requires a value")
			}
			if worktreeSeen {
				return issuePlanArgs{}, fmt.Errorf("issue-plan accepts --worktree only once")
			}
			index++
			parsed.worktree = args[index]
			worktreeSeen = true
		case "--yes", "--confirm":
			parsed.yes = true
		case "--json":
			parsed.json = true
		default:
			return issuePlanArgs{}, fmt.Errorf("unknown issue-plan option %q", args[index])
		}
	}
	if !issueSeen {
		return issuePlanArgs{}, fmt.Errorf("issue-plan requires --issue <positive-number>")
	}
	if !worktreeSeen || strings.TrimSpace(parsed.worktree) == "" {
		return issuePlanArgs{}, fmt.Errorf("issue-plan requires --worktree <dev-worktree-path>")
	}
	return parsed, nil
}

func (a *App) issuePlan(ctx context.Context, opts options, args []string) int {
	parsed, err := parseIssuePlanArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	if parsed.json {
		opts.json = true
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}

	service := &agents.Service{
		Config:       runtime.config,
		RepositoryID: runtime.paths.RepositoryID,
		GitCommonDir: runtime.paths.GitCommonDir,
		Client:       runtime.herdr,
		Store:        state.New(runtime.paths.StateDir),
		Issues: github.New(github.Options{
			Dir:     parsed.worktree,
			Timeout: runtime.config.GitHubTimeout(),
		}),
	}

	plan, err := service.BuildIssuePlan(ctx, agents.IssuePlanRequest{
		IssueNumber:     parsed.issueNumber,
		DevWorktreePath: parsed.worktree,
	})
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}

	if !plan.Startable() {
		if opts.json {
			a.writeResult(true, map[string]any{"status": "complete", "plan": issuePlanPayload(plan), "next_step": "wt start " + plan.Slug})
			return 0
		}
		a.planPrint("%s\nPlanning for this Issue is already complete. Nothing was changed.\n", agents.IssuePlanSummary(plan))
		return 0
	}

	if opts.json && !parsed.yes {
		a.writeResult(true, map[string]any{"status": "plan", "plan": issuePlanPayload(plan)})
		return 0
	}

	a.planPrint("%s\n! marks steps that are not undone by declining later: writing tasks/issue-%s.md and tasks/tasks-%s.md, and starting a Pi planner.\n",
		agents.IssuePlanSummary(plan), plan.Slug, plan.Slug)

	if !parsed.yes {
		approved, err := a.confirmIssuePlan()
		if err != nil {
			a.writeError(fmt.Errorf("this plan needs an answer: %w; re-run with --yes from a script", err), false)
			return 1
		}
		if !approved {
			a.planPrint("Nothing was changed.\n")
			return 0
		}
	}

	result, err := service.ExecuteIssuePlan(ctx, plan)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	a.recordAudit(runtime, audit.Event{Operation: "issue-plan", Feature: plan.Slug, Stage: "plan", Outcome: issuePlanOutcome(result)})

	if opts.json {
		a.writeResult(true, issuePlanResultPayload(result))
		return 0
	}
	renderIssuePlanResult(a, result)
	return 0
}

// planPrint writes one piece of human output for this command. A CLI's
// terminal write failing is not actionable — there is nowhere left to report
// it — so the error is dropped deliberately, once, here, rather than being
// ignored silently at every call site.
func (a *App) planPrint(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stdout, format, args...)
}

func (a *App) confirmIssuePlan() (bool, error) {
	if a.stdin == nil {
		return false, errors.New("no input is available to confirm on")
	}
	a.planPrint("\nProceed? [y/N] ")
	reader := bufio.NewReader(a.stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, errors.New("no answer was given")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func renderIssuePlanResult(a *App, result agents.IssuePlanResult) {
	if result.SnapshotWritten {
		a.planPrint("\nWrote %s\n", result.Plan.SnapshotPath)
	}
	if result.StarterWritten {
		a.planPrint("Wrote %s\n", result.Plan.TaskListPath)
	}
	if result.Degraded {
		a.planPrint("\nThe planning files are ready; %s\n", result.DegradedMessage)
		if result.DegradedRecovery != "" {
			a.planPrint("  Retry: %s\n", result.DegradedRecovery)
		}
		return
	}
	a.planPrint("\nPi planner: %s\n", result.Planner.Name)
	if result.PromptDelivered {
		a.planPrint("Planning prompt delivered.\n")
	} else if result.PromptSkipped {
		a.planPrint("Planning prompt already delivered; resumed the existing session.\n")
	}
	for _, warning := range result.Warnings {
		a.planPrint("Warning: %s\n", warning)
	}
}

func issuePlanOutcome(result agents.IssuePlanResult) string {
	switch {
	case result.Degraded:
		return "degraded:" + result.DegradedStage
	case result.PromptDelivered:
		return "prompt-delivered"
	case result.PromptSkipped:
		return "prompt-skipped"
	default:
		return "ready"
	}
}

func issuePlanPayload(plan agents.IssuePlan) map[string]any {
	payload := map[string]any{
		"issue_number":    plan.IssueNumber,
		"title":           plan.Title,
		"url":             plan.URL,
		"issue_state":     plan.IssueState,
		"labels":          plan.Labels,
		"route":           string(plan.Route),
		"feature":         plan.Slug,
		"dev_worktree":    plan.DevWorktreePath,
		"snapshot_path":   plan.SnapshotPath,
		"task_list_path":  plan.TaskListPath,
		"artifact_state":  string(plan.ArtifactState),
		"planner_kind":    plan.PlannerKind,
		"workspace_state": plan.WorkspaceState,
	}
	if plan.PRDPath != "" {
		payload["prd_path"] = plan.PRDPath
	}
	if plan.WorkspaceLabel != "" {
		payload["workspace_label"] = plan.WorkspaceLabel
	}
	if len(plan.Warnings) > 0 {
		payload["warnings"] = plan.Warnings
	}
	return payload
}

func issuePlanResultPayload(result agents.IssuePlanResult) map[string]any {
	payload := map[string]any{
		"status":           "ready",
		"plan":             issuePlanPayload(result.Plan),
		"snapshot_written": result.SnapshotWritten,
		"starter_written":  result.StarterWritten,
	}
	if result.Degraded {
		payload["status"] = "degraded"
		payload["degraded_stage"] = result.DegradedStage
		payload["degraded_message"] = result.DegradedMessage
		if result.DegradedRecovery != "" {
			payload["degraded_recovery"] = result.DegradedRecovery
		}
		return payload
	}
	payload["tab_id"] = result.TabID
	payload["tab_reused"] = result.TabReused
	payload["planner"] = result.Planner.Name
	payload["prompt_delivered"] = result.PromptDelivered
	payload["prompt_skipped"] = result.PromptSkipped
	if len(result.Warnings) > 0 {
		payload["warnings"] = result.Warnings
	}
	return payload
}
