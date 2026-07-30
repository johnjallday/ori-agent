package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/systempower"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeclient"
)

// This file is Ori's side of the Claude usage contract described in
// `docs/herdr-devflow-claude-usage-signal.md`.
//
// Claude Code will not answer a question about usage; it only pushes a payload
// into a statusLine command and a StopFailure hook. So Ori supplies a recorder
// for both, and the only thing it does is decode the two documented shapes and
// persist the handful of fields an Overnight Run is allowed to reason about.
//
// It never installs itself. Wiring a recorder into Claude Code means editing
// the user's own configuration, and nothing here does that as a side effect of
// setup, status, or doctor: `claude-usage install` prints the exact fragment
// and the user applies it.

// maxRecorderInput bounds what the recorder will read from stdin, so a payload
// far outside the documented contract cannot be buffered.
const maxRecorderInput = 1 << 20

// claudeUsage dispatches the `claude-usage` command family.
func (a *App) claudeUsage(ctx context.Context, opts options, args []string) int {
	if len(args) == 0 {
		a.writeError(fmt.Errorf("claude-usage requires record, status, or install"), opts.json)
		return 2
	}
	switch args[0] {
	case "record":
		return a.claudeUsageRecord(ctx, opts, args[1:])
	case "status":
		return a.claudeUsageStatus(opts, args[1:])
	case "install":
		return a.claudeUsageInstall(opts, args[1:])
	default:
		a.writeError(fmt.Errorf("unknown claude-usage command %q", args[0]), opts.json)
		return 2
	}
}

type recordArgs struct {
	statusline bool
	failure    claudeusage.FailureClass
	// wrapped is an existing statusLine command to run with the same payload,
	// so installing the recorder never costs the user their own status line.
	wrapped []string
}

func parseRecordArgs(args []string) (recordArgs, error) {
	var parsed recordArgs
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--statusline":
			parsed.statusline = true
		case "--stop-failure":
			if index+1 >= len(args) {
				return recordArgs{}, fmt.Errorf("--stop-failure requires an error class")
			}
			index++
			parsed.failure = claudeusage.FailureClass(args[index])
		case "--":
			parsed.wrapped = append(parsed.wrapped, args[index+1:]...)
			index = len(args)
		default:
			return recordArgs{}, fmt.Errorf("unknown record option %q", args[index])
		}
	}
	if parsed.statusline == (parsed.failure != "") {
		return recordArgs{}, fmt.Errorf("record requires exactly one of --statusline or --stop-failure")
	}
	if len(parsed.wrapped) > 0 && !parsed.statusline {
		return recordArgs{}, fmt.Errorf("only --statusline may wrap an existing command")
	}
	return parsed, nil
}

// claudeUsageRecord is invoked by Claude Code itself, many times a minute in
// the statusline case. It is deliberately silent and never fails the caller:
// a recorder that printed an error into the status line, or exited nonzero
// into a hook, would degrade the user's Claude session to report a problem
// that only affects Ori.
func (a *App) claudeUsageRecord(ctx context.Context, opts options, args []string) int {
	parsed, err := parseRecordArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	payload, err := io.ReadAll(io.LimitReader(a.stdin, maxRecorderInput))
	if err != nil {
		return 0
	}

	runtime, runtimeErr := a.load(opts)
	if runtimeErr == nil {
		store := claudeusage.NewStore(runtime.paths.UsageDir)
		now := time.Now().UTC()
		if parsed.statusline {
			if sample, err := claudeusage.DecodeStatusLine(payload, now); err == nil {
				_ = store.SaveSample(sample)
			}
		} else if failure, err := claudeusage.DecodeStopFailure(payload, parsed.failure, now); err == nil {
			_ = store.SaveFailure(failure)
		}
	}

	// Whatever happened above, the user's own status line still renders.
	a.runWrappedStatusLine(ctx, parsed.wrapped, payload)
	return 0
}

// runWrappedStatusLine feeds the same payload to the status line the user had
// before the recorder was installed and forwards its output unchanged.
func (a *App) runWrappedStatusLine(ctx context.Context, wrapped []string, payload []byte) {
	if len(wrapped) == 0 {
		return
	}
	// #nosec G204 -- the command comes from the user's own Claude settings,
	// which they wrote and which this process only relays.
	command := exec.CommandContext(ctx, wrapped[0], wrapped[1:]...)
	command.Stdin = strings.NewReader(string(payload))
	command.Stdout = a.stdout
	command.Stderr = a.stderr
	_ = command.Run()
}

// claudeUsageStatus reports what the recorder has observed, without naming a
// session's account, model, or content.
func (a *App) claudeUsageStatus(opts options, args []string) int {
	for _, argument := range args {
		if argument != "--json" {
			a.writeError(fmt.Errorf("unknown status option %q", argument), opts.json)
			return 2
		}
		opts.json = true
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	store := claudeusage.NewStore(runtime.paths.UsageDir)
	installed := store.Installed()
	if opts.json {
		a.writeResult(true, map[string]any{
			"installed":              installed,
			"usage_dir":              runtime.paths.UsageDir,
			"minimum_claude_version": claudeusage.MinimumClaudeVersion,
		})
		return 0
	}
	if installed {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: Claude usage records are being written to %s\n", runtime.paths.UsageDir)
		return 0
	}
	fmt.Fprintf(a.stdout, "Ori Herdr Devflow: no Claude usage records exist yet in %s\n", runtime.paths.UsageDir)
	fmt.Fprintln(a.stdout, "Run: wt herd claude-usage install   to see the Claude settings this needs.")
	return 0
}

// claudeUsageInstall prints the exact Claude settings fragment the recorder
// needs. It writes nothing: the file it describes is the user's own Claude
// configuration, and an Overnight Run that silently edited it would be taking
// an authority nobody granted.
func (a *App) claudeUsageInstall(opts options, args []string) int {
	for _, argument := range args {
		if argument != "--print" {
			a.writeError(fmt.Errorf("unknown install option %q; install only prints the settings fragment", argument), opts.json)
			return 2
		}
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	helper := runtime.paths.HelperPath
	fragment := map[string]any{
		"statusLine": map[string]any{
			"type": "command",
			"command": helper + " claude-usage record --statusline" +
				" -- <keep your existing statusLine command here>",
		},
		"hooks": map[string]any{
			"StopFailure": []any{map[string]any{
				"matcher": string(claudeusage.FailureRateLimit),
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": helper + " claude-usage record --stop-failure rate_limit",
				}},
			}},
		},
	}
	if opts.json {
		a.writeResult(true, map[string]any{"settings_fragment": fragment, "settings_path": "~/.claude/settings.json"})
		return 0
	}
	encoded, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		a.writeError(&model.StageError{
			Stage: "claude-usage install", Code: model.ErrConfigInvalid,
			Message: "could not render the Claude settings fragment", Cause: err,
		}, opts.json)
		return 1
	}
	fmt.Fprintln(a.stdout, "Add this to ~/.claude/settings.json to let Ori observe Claude's usage windows.")
	fmt.Fprintln(a.stdout, "Ori never edits that file for you, and it records only window state and session identity:")
	fmt.Fprintln(a.stdout, "no prompts, transcripts, credentials, or account details.")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, string(encoded))
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "The statusLine entry wraps the command you already have, so your status line keeps working.")
	fmt.Fprintf(a.stdout, "Records are written to %s and are readable only by you.\n", runtime.paths.UsageDir)
	return 0
}

// wakeDiagnostics reports what stands between a detected limit and a sleeping
// Mac, as separate checks. They are the questions a person asks at midnight —
// "would this actually sleep?" — and each has a different fix.
func (a *App) wakeDiagnostics() []diagnostic {
	if a.goos != "darwin" {
		return []diagnostic{{
			Name:     "Overnight sleep",
			Status:   "WARN",
			Detail:   "system sleep and wake are supported on macOS only",
			Recovery: "run Overnight Runs on macOS; every other command works here",
		}}
	}
	client, err := wakeclient.Default()
	if err != nil {
		return []diagnostic{{
			Name:     "wake coordinator",
			Status:   "WARN",
			Detail:   "Ori's shared wake coordinator could not be located",
			Recovery: "start Ori, then run wt herd doctor",
		}}
	}

	diagnostics := []diagnostic{}
	if client.Available() {
		diagnostics = append(diagnostics, diagnostic{
			Name: "wake coordinator", Status: "PASS", Detail: "the shared wake store is readable",
		})
	} else {
		diagnostics = append(diagnostics, diagnostic{
			Name: "wake coordinator", Status: "WARN",
			Detail:   "the shared wake store could not be read",
			Recovery: "start Ori, then run wt herd doctor",
		})
	}

	readiness := client.Owner()
	switch {
	case readiness.Ready:
		diagnostics = append(diagnostics, diagnostic{
			Name: "wake owner", Status: "PASS", Detail: "Ori is running and can program macOS wake events",
		})
	default:
		recovery := "open Ori and enable Mac wake scheduling"
		if !readiness.Running {
			recovery = "start Ori; the Herdr helper never programs wake events itself"
		}
		diagnostics = append(diagnostics, diagnostic{
			Name: "wake owner", Status: "WARN", Detail: readiness.Detail, Recovery: recovery,
		})
	}

	power := &systempower.Service{GOOS: a.goos}
	source := power.PowerSource(context.Background())
	if source.External() {
		diagnostics = append(diagnostics, diagnostic{
			Name: "power source", Status: "PASS", Detail: "this Mac is on external power",
		})
	} else {
		diagnostics = append(diagnostics, diagnostic{
			Name: "power source", Status: "WARN",
			Detail:   "this Mac is on " + source.Label() + "; an Overnight Run sleeps only on external power",
			Recovery: "connect power before starting an Overnight Run",
		})
	}
	return diagnostics
}

// claudeUsageDiagnostics reports Overnight readiness as separate checks rather
// than one verdict, because their fixes are different: a missing recorder is an
// install, a stale record is a session that stopped reporting, and an
// unsupported version is an upgrade.
//
// Every one of them is a WARN, never a FAIL. Nothing here stops the bridge from
// doing the work it already did before Overnight Runs existed — it only stops
// unattended execution, which is exactly the capability-scoped degradation the
// readiness model calls for.
func (a *App) claudeUsageDiagnostics(runtime runtimeContext) []diagnostic {
	store := claudeusage.NewStore(runtime.paths.UsageDir)
	if !store.Installed() {
		return []diagnostic{{
			Name:     "Claude usage recorder",
			Status:   "WARN",
			Detail:   "no Claude usage records exist, so no session can be run unattended",
			Recovery: "wt herd claude-usage install",
		}}
	}
	diagnostics := []diagnostic{{
		Name:   "Claude usage recorder",
		Status: "PASS",
		Detail: "records are being written to " + runtime.paths.UsageDir,
	}}

	// A recorder that writes records says nothing about whether any one session
	// is usable, so report the strongest thing observed rather than implying
	// every session is ready.
	adapter := claudeusage.NewAdapter(runtime.paths.UsageDir)
	bridgeState, err := state.New(runtime.paths.StateDir).Load()
	if err != nil {
		return diagnostics
	}
	ready, refused, reason := 0, 0, ""
	for _, feature := range bridgeState.Features {
		for _, agent := range feature.Agents {
			if agent.NativeSession.Value == "" {
				continue
			}
			readiness := adapter.Readiness(agent.NativeSession.Value, time.Now())
			if readiness.Ready {
				ready++
				continue
			}
			refused++
			if reason == "" {
				reason = readiness.Reason
			}
		}
	}
	switch {
	case ready > 0:
		diagnostics = append(diagnostics, diagnostic{
			Name:   "Claude overnight readiness",
			Status: "PASS",
			Detail: fmt.Sprintf("%d saved Claude session(s) report plan-backed capacity and a current usage window", ready),
		})
	case refused > 0:
		diagnostics = append(diagnostics, diagnostic{
			Name:     "Claude overnight readiness",
			Status:   "WARN",
			Detail:   reason,
			Recovery: "open the agent, let Claude render once, then run wt herd doctor",
		})
	default:
		diagnostics = append(diagnostics, diagnostic{
			Name:     "Claude overnight readiness",
			Status:   "WARN",
			Detail:   "no saved agent has a native Claude session to check",
			Recovery: "wt herd rebind --feature <name>",
		})
	}
	return diagnostics
}
