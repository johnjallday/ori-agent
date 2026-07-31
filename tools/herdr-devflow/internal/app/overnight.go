package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/audit"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overnight"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/systempower"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeclient"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

// This file is the `wt herd overnight` command family.
//
// `start` is the only command here that can create anything, and it cannot do
// so without the user reading a summary and answering it. Everything else
// inspects or stops. No command in this file prompts an agent, registers a
// wake, or sleeps anything: a confirmed run is a durable intention, and the
// supervisor is what acts on it.

func (a *App) overnight(ctx context.Context, opts options, args []string) int {
	if len(args) == 0 {
		a.writeError(fmt.Errorf("overnight requires start, list, show, status, watch, report, or cancel"), opts.json)
		return 2
	}
	switch args[0] {
	case "start":
		return a.overnightStart(ctx, opts, args[1:])
	case "list":
		return a.overnightList(opts, args[1:])
	case "show", "status":
		return a.overnightShow(opts, args[1:])
	case "cancel":
		return a.overnightCancel(ctx, opts, args[1:])
	case "report":
		return a.overnightReport(opts, args[1:])
	case "watch":
		return a.overnightWatch(ctx, opts, args[1:])
	default:
		a.writeError(fmt.Errorf("unknown overnight command %q", args[0]), opts.json)
		return 2
	}
}

type overnightStartArgs struct {
	request overnight.Request
	confirm bool
	json    bool
	dryRun  bool
}

func parseOvernightStartArgs(args []string) (overnightStartArgs, error) {
	var parsed overnightStartArgs
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--agent", "--feature":
			if index+1 >= len(args) {
				return overnightStartArgs{}, fmt.Errorf("%s requires a feature, optionally as feature:role", args[index])
			}
			index++
			parsed.request.Selections = append(parsed.request.Selections, args[index])
		case "--start", "--deadline", "--timezone":
			if index+1 >= len(args) {
				return overnightStartArgs{}, fmt.Errorf("%s requires a value", args[index])
			}
			flag := args[index]
			index++
			switch flag {
			case "--start":
				parsed.request.Start = args[index]
			case "--deadline":
				parsed.request.Deadline = args[index]
			default:
				parsed.request.Timezone = args[index]
			}
		case "--max-resumes":
			if index+1 >= len(args) {
				return overnightStartArgs{}, fmt.Errorf("--max-resumes requires a number")
			}
			index++
			value, err := strconv.Atoi(args[index])
			if err != nil {
				return overnightStartArgs{}, fmt.Errorf("--max-resumes must be a number")
			}
			parsed.request.MaxResumes = value
		case "--confirm":
			parsed.confirm = true
		case "--stay-awake":
			parsed.request.StayAwake = true
		case "--dry-run":
			parsed.dryRun = true
		case "--json":
			parsed.json = true
		default:
			return overnightStartArgs{}, fmt.Errorf("unknown overnight start option %q", args[index])
		}
	}
	return parsed, nil
}

// overnightStart builds a plan, shows it, and creates a run only after the user
// approves that exact plan.
func (a *App) overnightStart(ctx context.Context, opts options, args []string) int {
	parsed, err := parseOvernightStartArgs(args)
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
	if !runtime.config.Bridge.Enabled {
		a.writeResult(opts.json, map[string]any{"status": "disabled", "message": "Ori Herdr Devflow is disabled; no Overnight Run was created."})
		return 0
	}
	if len(parsed.request.Selections) == 0 {
		// There is deliberately no "enrol everything" path. An Overnight Run
		// controls agents unattended, and the set it controls is a decision,
		// not a default.
		a.writeError(errors.New("select the agents to enrol with --agent <feature>[:role]; an Overnight Run never enrols every agent"), opts.json)
		return 2
	}

	snapshot, err := a.overviewService(runtime).Collect(ctx)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	saved, err := state.New(runtime.paths.StateDir).Load()
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	plan, err := overnight.BuildPlan(snapshot, saved, parsed.request, runtime.config.Overnight, time.Now())
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	usage := claudeusage.NewAdapter(runtime.paths.UsageDir)
	for index := range plan.Participants {
		participant := &plan.Participants[index]
		proof, proofErr := usage.BuildPlanProof(participant.Binding.NativeSession.Value, time.Now().UTC(), plan.DeadlineAt)
		if proofErr != nil {
			a.writeError(proofErr, opts.json)
			return 1
		}
		participant.PlanProof = proof
	}

	if opts.json && !parsed.confirm {
		// A machine caller gets the plan to inspect, and still cannot start a
		// run without saying so explicitly.
		a.writeResult(true, map[string]any{"status": "plan", "plan": planPayload(plan)})
		if !plan.Startable() {
			return 1
		}
		return 0
	}
	if err := overnight.RenderConfirmation(a.stdout, plan); err != nil {
		a.writeError(err, false)
		return 1
	}
	if !plan.Startable() {
		fmt.Fprintln(a.stdout, "\nThis Overnight Run was not created.")
		return 1
	}
	if parsed.dryRun {
		fmt.Fprintln(a.stdout, "\nDry run: nothing was created.")
		return 0
	}

	confirmation := overnight.ConfirmationFlag
	if !parsed.confirm {
		approved, err := a.confirmOvernight()
		if err != nil {
			// Refusing to guess is the whole point of this gate: a plan that
			// could not be confirmed is a plan that was not confirmed.
			a.writeError(fmt.Errorf("this Overnight Run needs an answer: %w; re-run with --confirm from a script", err), false)
			return 1
		}
		if !approved {
			fmt.Fprintln(a.stdout, "Declined. Nothing was created, changed, or prompted.")
			return 0
		}
		confirmation = overnight.ConfirmationInteractive
	}

	service := &overnight.Service{Store: state.New(runtime.paths.StateDir)}
	run, err := service.Create(ctx, plan, confirmation)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if run.WakeMode != model.WakeModeStayAwake && run.StartAt.After(time.Now().UTC()) {
		wake, wakeErr := a.newOvernightWake(wakeprotocol.PurposeOvernightStart)
		if wakeErr != nil || !wake.Owner().Ready {
			detail := "standalone Herdr wake service is not ready; run wt herd wake doctor or recreate this run with --stay-awake"
			run.Wake = model.WakeOwnership{CandidateID: run.ID, Source: string(wakeprotocol.SourceOvernight), Purpose: string(wakeprotocol.PurposeOvernightStart), RequestedAt: run.StartAt.Add(-runtime.config.WakeLead()), Uncertain: true, Detail: detail}
			run, _ = service.RecordWake(ctx, run.ID, run.Wake, detail)
			a.writeError(errors.New(detail), opts.json)
			return 1
		}
		requested := run.StartAt.Add(-runtime.config.WakeLead())
		intent := model.WakeOwnership{CandidateID: run.ID, Source: string(wakeprotocol.SourceOvernight), Purpose: string(wakeprotocol.PurposeOvernightStart), RequestedAt: requested}
		if run, err = service.RecordWake(ctx, run.ID, intent, ""); err != nil {
			a.writeError(err, opts.json)
			return 1
		}
		evidence, registerErr := wake.RegisterCandidate(ctx, run.ID, requested, "scheduled Overnight Run start")
		if registerErr == nil {
			evidence, registerErr = wake.VerifyCandidate(ctx, run.ID, requested)
		}
		intent.RegisteredAt = time.Now().UTC()
		intent.ProgrammedAt = evidence.ProgrammedAt
		intent.VerifiedAt = evidence.VerifiedAt
		intent.ProtocolVersion = evidence.ProtocolVersion
		intent.DaemonBuild = evidence.DaemonBuild
		intent.HelperBuild = evidence.HelperBuild
		intent.Verified = registerErr == nil
		intent.Uncertain = registerErr != nil && errors.Is(registerErr, wakeclient.ErrUncertain)
		intent.Detail = evidence.Message
		if registerErr != nil {
			_, cancelErr := wake.CancelCandidate(ctx, run.ID)
			if cancelErr != nil {
				intent.Uncertain = true
				intent.Detail = "pre-start wake cancellation was not proven; run wt herd wake doctor"
			}
			run, _ = service.RecordWake(ctx, run.ID, intent, "future start wake was not verified; inspect with wt herd wake doctor or cancel this run")
			a.writeError(fmt.Errorf("standalone pre-start wake: %w", registerErr), opts.json)
			return 1
		}
		if run, err = service.RecordWake(ctx, run.ID, intent, ""); err != nil {
			_, _ = wake.CancelCandidate(ctx, run.ID)
			a.writeError(err, opts.json)
			return 1
		}
	}
	a.recordAudit(runtime, auditOvernightEvent(run, "created"))
	if opts.json {
		a.writeResult(true, map[string]any{"status": "scheduled", "run": runPayload(run)})
		return 0
	}
	fmt.Fprintf(a.stdout, "\nOvernight Run %s is scheduled. Nothing has been prompted yet.\n", run.ID)
	fmt.Fprintf(a.stdout, "Inspect it with: wt herd overnight show %s\n", run.ID)
	fmt.Fprintf(a.stdout, "Cancel it with:  wt herd overnight cancel %s\n", run.ID)
	return 0
}

// confirmOvernight asks the one question that must never be inferred.
func (a *App) confirmOvernight() (bool, error) {
	if a.stdin == nil {
		return false, errors.New("no input is available to confirm on")
	}
	fmt.Fprint(a.stdout, "\nStart this Overnight Run? [y/N] ")
	reader := bufio.NewReader(a.stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, errors.New("no answer was given")
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func (a *App) overnightList(opts options, args []string) int {
	for _, argument := range args {
		if argument != "--json" {
			a.writeError(fmt.Errorf("unknown overnight list option %q", argument), opts.json)
			return 2
		}
		opts.json = true
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	service := &overnight.Service{Store: state.New(runtime.paths.StateDir)}
	runs, err := service.List(runtime.paths.RepositoryID)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		payloads := make([]map[string]any, 0, len(runs))
		for _, run := range runs {
			payloads = append(payloads, runPayload(run))
		}
		a.writeResult(true, map[string]any{"runs": payloads})
		return 0
	}
	if err := overnight.RenderRunList(a.stdout, runs); err != nil {
		a.writeError(err, false)
		return 1
	}
	return 0
}

func (a *App) overnightShow(opts options, args []string) int {
	var id string
	for _, argument := range args {
		switch {
		case argument == "--json":
			opts.json = true
		case strings.HasPrefix(argument, "--"):
			a.writeError(fmt.Errorf("unknown overnight option %q", argument), opts.json)
			return 2
		default:
			id = argument
		}
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	service := &overnight.Service{Store: state.New(runtime.paths.StateDir)}

	run, err := a.resolveRun(service, runtime.paths.RepositoryID, id)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, map[string]any{"run": runPayload(run)})
		return 0
	}
	if err := overnight.RenderRun(a.stdout, run); err != nil {
		a.writeError(err, false)
		return 1
	}
	return 0
}

// resolveRun accepts an explicit identity, or none at all when exactly one run
// is active. Guessing between several active runs is never safe.
func (a *App) resolveRun(service *overnight.Service, repositoryID, id string) (model.OvernightRun, error) {
	if id != "" {
		run, err := service.Get(id)
		if errors.Is(err, overnight.ErrNotFound) {
			return model.OvernightRun{}, fmt.Errorf("no Overnight Run named %q exists", id)
		}
		return run, err
	}
	run, found, err := service.Active(repositoryID)
	if err != nil {
		return model.OvernightRun{}, err
	}
	if !found {
		return model.OvernightRun{}, errors.New("no Overnight Run is active; name one explicitly or run wt herd overnight list")
	}
	return run, nil
}

func (a *App) overnightCancel(ctx context.Context, opts options, args []string) int {
	var id string
	for _, argument := range args {
		switch {
		case argument == "--json":
			opts.json = true
		case strings.HasPrefix(argument, "--"):
			a.writeError(fmt.Errorf("unknown overnight cancel option %q", argument), opts.json)
			return 2
		default:
			id = argument
		}
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	service := &overnight.Service{Store: state.New(runtime.paths.StateDir)}
	target, err := a.resolveRun(service, runtime.paths.RepositoryID, id)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	run, err := service.Cancel(ctx, target.ID)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if run.Wake.CandidateID != "" && !run.Wake.Canceled {
		purpose := wakeprotocol.Purpose(run.Wake.Purpose)
		if purpose == "" {
			purpose = wakeprotocol.PurposeClaudeReset
		}
		wake, wakeErr := a.newOvernightWake(purpose)
		if wakeErr == nil {
			_, wakeErr = wake.CancelCandidate(ctx, run.Wake.CandidateID)
		}
		if wakeErr != nil {
			run.Wake.Uncertain = true
			run.Wake.Detail = "exact standalone wake cancellation was not proven; run wt herd wake doctor"
			run, _ = service.RecordWake(ctx, run.ID, run.Wake, "")
			a.recordAudit(runtime, auditOvernightEvent(run, "wake_withdrawal_uncertain"))
			a.writeError(fmt.Errorf("Overnight Run canceled, but standalone wake withdrawal is uncertain: %w", wakeErr), opts.json)
			return 1
		}
		run.Wake.Canceled = true
		run.Wake.Uncertain = false
		run.Wake.Detail = ""
		if run.Uncertainty == "cancellation requested; the wake candidate has not been confirmed withdrawn" {
			run.Uncertainty = ""
		}
		run, err = service.RecordWake(ctx, run.ID, run.Wake, "")
		if err != nil {
			a.writeError(err, opts.json)
			return 1
		}
	}
	if run.Assertion.ID != "" && run.Assertion.ReleasedAt.IsZero() {
		power := &systempower.Service{GOOS: a.goos}
		if releaseErr := power.ReleaseIdleSleepAssertion(ctx, run.Assertion.ID); releaseErr != nil {
			run.Assertion.Uncertain = true
			run.Assertion.Detail = "run-owned idle-sleep assertion release was not proven"
			run, _ = service.RecordAssertion(ctx, run.ID, run.Assertion)
			a.writeError(fmt.Errorf("Overnight Run canceled, but stay-awake assertion release is uncertain: %w", releaseErr), opts.json)
			return 1
		}
		run.Assertion.ReleasedAt = time.Now().UTC()
		run.Assertion.Uncertain = false
		run.Assertion.Detail = "run-owned idle-sleep assertion released"
		var assertionErr error
		run, assertionErr = service.RecordAssertion(ctx, run.ID, run.Assertion)
		if assertionErr != nil {
			a.writeError(assertionErr, opts.json)
			return 1
		}
	}
	a.recordAudit(runtime, auditOvernightEvent(run, "canceled"))
	if opts.json {
		a.writeResult(true, map[string]any{"status": string(run.State), "run": runPayload(run)})
		return 0
	}
	fmt.Fprintf(a.stdout, "Overnight Run %s: %s\n", run.ID, run.State.Label())
	fmt.Fprintln(a.stdout, "Agent sessions and Git worktrees were left untouched.")
	if run.Uncertainty != "" {
		fmt.Fprintf(a.stdout, "Uncertain: %s\n", run.Uncertainty)
	}
	if run.Wake.Uncertain {
		fmt.Fprintln(a.stdout, "This run's wake candidate has not been confirmed withdrawn; the supervisor reconciles it on its next launch.")
	}
	return 0
}

// planPayload is the JSON contract for a proposed run. It carries identities
// and progress, never prompt text or terminal content.
func planPayload(plan overnight.Plan) map[string]any {
	participants := make([]map[string]any, 0, len(plan.Participants))
	for _, participant := range plan.Participants {
		participants = append(participants, map[string]any{
			"position":         participant.Position,
			"feature":          participant.Feature.Name,
			"worktree":         participant.Feature.Path,
			"role":             participant.Binding.Role,
			"kind":             participant.Binding.AgentKind,
			"native_session":   participant.Binding.NativeSession.Value,
			"plan_proof_valid": participant.PlanProof.PlanBacked,
			"working":          participant.Working,
			"next_task":        participant.Checkpoint.NextOrdinal,
			"manual_stop":      participant.Checkpoint.ManualOrdinal,
		})
	}
	excluded := make([]map[string]any, 0, len(plan.Excluded))
	for _, agent := range plan.Excluded {
		excluded = append(excluded, map[string]any{
			"feature": agent.Feature, "role": agent.Role, "kind": agent.Kind,
			"scope": agent.Scope, "reason": agent.Reason,
		})
	}
	return map[string]any{
		"startable":    plan.Startable(),
		"start_at":     plan.StartAt,
		"deadline_at":  plan.DeadlineAt,
		"timezone":     plan.Timezone,
		"max_resumes":  plan.MaxResumes,
		"wake_mode":    string(plan.WakeMode),
		"participants": participants,
		"excluded":     excluded,
		"warnings":     plan.Warnings,
		"conflicts":    plan.Conflicts,
	}
}

// runPayload is the JSON contract for a persisted run.
func runPayload(run model.OvernightRun) map[string]any {
	participants := make([]map[string]any, 0, len(run.Participants))
	for _, participant := range run.Participants {
		entry := map[string]any{
			"id":                   participant.ID,
			"position":             participant.Position,
			"state":                string(participant.State),
			"feature":              participant.Feature.Name,
			"worktree":             participant.Feature.Path,
			"role":                 participant.Binding.Role,
			"native_session":       participant.Binding.NativeSession.Value,
			"plan_proof_valid":     participant.PlanProof.PlanBacked,
			"subtasks_total":       participant.Checkpoint.SubtasksTotal,
			"subtasks_completed":   participant.Checkpoint.SubtasksCompleted,
			"acknowledged_resumes": participant.AcknowledgedResumes,
			"outcome":              string(participant.Outcome),
			"recovery":             participant.Recovery,
		}
		if participant.Delivery.State != "" {
			entry["delivery_state"] = string(participant.Delivery.State)
		}
		if participant.Limit != nil {
			entry["limit_class"] = participant.Limit.Class
			entry["limit_reset_at"] = participant.Limit.ResetAt
			entry["limit_sleepable"] = participant.Limit.Sleepable
		}
		participants = append(participants, entry)
	}
	return map[string]any{
		"id":                   run.ID,
		"state":                string(run.State),
		"created_at":           run.CreatedAt,
		"start_at":             run.StartAt,
		"deadline_at":          run.DeadlineAt,
		"timezone":             run.Timezone,
		"max_resumes":          run.MaxResumes,
		"acknowledged_resumes": run.AcknowledgedResumes,
		"active_participant":   run.ActiveParticipant,
		"confirmation":         run.Confirmation,
		"wake_mode":            string(run.WakeMode),
		"terminal_reason":      string(run.TerminalReason),
		"uncertainty":          run.Uncertainty,
		"participants":         participants,
		"wake": map[string]any{
			"candidate_id":  run.Wake.CandidateID,
			"source":        run.Wake.Source,
			"purpose":       run.Wake.Purpose,
			"requested_at":  run.Wake.RequestedAt,
			"programmed_at": run.Wake.ProgrammedAt,
			"verified":      run.Wake.Verified,
			"canceled":      run.Wake.Canceled,
			"uncertain":     run.Wake.Uncertain,
		},
		"stay_awake_assertion": map[string]any{
			"id":          run.Assertion.ID,
			"verified_at": run.Assertion.VerifiedAt,
			"released_at": run.Assertion.ReleasedAt,
			"uncertain":   run.Assertion.Uncertain,
			"detail":      run.Assertion.Detail,
		},
	}
}

// auditOvernightEvent records a run transition without naming anything private.
// The audit log is for reconstructing what the run decided, not what any agent
// said or was told.
func auditOvernightEvent(run model.OvernightRun, stage string) audit.Event {
	return audit.Event{
		Operation: "overnight",
		Feature:   run.ID,
		Stage:     stage,
		Outcome:   string(run.State),
	}
}

// overnightReport writes the morning report.
//
// It renders what the run recorded rather than recomputing anything from live
// state: by the time someone reads this the agents may be closed, and a report
// whose content depended on when it was opened would be no record at all.
func (a *App) overnightReport(opts options, args []string) int {
	var id string
	for _, argument := range args {
		switch {
		case argument == "--json":
			opts.json = true
		case strings.HasPrefix(argument, "--"):
			a.writeError(fmt.Errorf("unknown overnight report option %q", argument), opts.json)
			return 2
		default:
			id = argument
		}
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	service := &overnight.Service{Store: state.New(runtime.paths.StateDir)}
	run, err := a.resolveReportableRun(service, runtime.paths.RepositoryID, id)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}

	report := run.Report
	if report == nil {
		// A run still in flight has no durable report yet; build a provisional
		// one from what it has recorded so far, and say which it is.
		provisional := overnight.BuildReport(run, time.Now())
		report = &provisional
	}
	if opts.json {
		payload := overnight.ReportPayload(run, *report)
		payload["final"] = run.Report != nil
		a.writeResult(true, map[string]any{"report": payload})
		return 0
	}
	if run.Report == nil {
		fmt.Fprintln(a.stdout, "This run has not finished; the summary below is provisional.")
	}
	if err := overnight.RenderReport(a.stdout, run, *report); err != nil {
		a.writeError(err, false)
		return 1
	}
	return 0
}

// resolveReportableRun prefers an explicit identity, then the active run, then
// the most recent finished one — which is what somebody typing `report` with no
// arguments in the morning almost always means.
func (a *App) resolveReportableRun(service *overnight.Service, repositoryID, id string) (model.OvernightRun, error) {
	if id != "" || func() bool { _, found, _ := service.Active(repositoryID); return found }() {
		return a.resolveRun(service, repositoryID, id)
	}
	runs, err := service.List(repositoryID)
	if err != nil {
		return model.OvernightRun{}, err
	}
	if len(runs) == 0 {
		return model.OvernightRun{}, errors.New("no Overnight Run has been created for this repository")
	}
	return runs[0], nil
}

// overnightWatch re-renders one run until interrupted. It only reads.
func (a *App) overnightWatch(ctx context.Context, opts options, args []string) int {
	var id string
	for _, argument := range args {
		switch {
		case argument == "--json":
			a.writeError(errors.New("overnight watch cannot be combined with --json"), opts.json)
			return 2
		case strings.HasPrefix(argument, "--"):
			a.writeError(fmt.Errorf("unknown overnight watch option %q", argument), opts.json)
			return 2
		default:
			id = argument
		}
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	service := &overnight.Service{Store: state.New(runtime.paths.StateDir)}
	interval := runtime.config.WatchPollInterval()
	rendered := false

	for {
		run, err := a.resolveRun(service, runtime.paths.RepositoryID, id)
		if err != nil {
			a.writeError(err, false)
			return 1
		}
		if rendered && a.statusColorEnabled(false) {
			fmt.Fprint(a.stdout, "\x1b[2J\x1b[H")
		}
		rendered = true
		if err := overnight.RenderRun(a.stdout, run); err != nil {
			a.writeError(err, false)
			return 1
		}
		if run.State.Terminal() {
			// Watching a finished run forever would just reprint it.
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(interval):
		}
	}
}
