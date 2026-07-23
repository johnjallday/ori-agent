// Package app implements the small, explicit CLI used by `wt herd` and the
// installed Herdr plugin. It is intentionally independent of the Ori server.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/agents"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

const PluginID = "ori.devflow"

type Dependencies struct {
	Stdout      io.Writer
	Stderr      io.Writer
	Getwd       func() (string, error)
	LookupEnv   func(string) (string, bool)
	LookPath    func(string) (string, error)
	Runner      herdr.Runner
	BuildHelper func(context.Context, string, string) error
	GOOS        string
}

type App struct {
	stdout      io.Writer
	stderr      io.Writer
	getwd       func() (string, error)
	lookupEnv   func(string) (string, bool)
	lookPath    func(string) (string, error)
	runner      herdr.Runner
	buildHelper func(context.Context, string, string) error
	goos        string
}

func New(deps Dependencies) *App {
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
	if deps.Getwd == nil {
		deps.Getwd = os.Getwd
	}
	if deps.LookupEnv == nil {
		deps.LookupEnv = os.LookupEnv
	}
	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	if deps.Runner == nil {
		deps.Runner = herdr.ExecRunner{}
	}
	if deps.BuildHelper == nil {
		deps.BuildHelper = buildHelper
	}
	if deps.GOOS == "" {
		deps.GOOS = runtime.GOOS
	}
	return &App{
		stdout:      deps.Stdout,
		stderr:      deps.Stderr,
		getwd:       deps.Getwd,
		lookupEnv:   deps.LookupEnv,
		lookPath:    deps.LookPath,
		runner:      deps.Runner,
		buildHelper: deps.BuildHelper,
		goos:        deps.GOOS,
	}
}

type options struct {
	repoRoot string
	config   string
	home     string
	herdrBin string
	json     bool
}

// Run returns a shell exit status. No command edits a global Herdr integration
// or writes to a project checkout other than its explicit `wt herd` source.
func (a *App) Run(ctx context.Context, args []string) int {
	opts, command, commandArgs, err := parseArgs(args)
	if err != nil {
		a.writeError(err, false)
		return 2
	}
	if command == "" || command == "help" || command == "--help" || command == "-h" {
		a.writeHelp()
		return 0
	}

	switch command {
	case "setup":
		return a.setup(ctx, opts)
	case "doctor":
		return a.doctor(ctx, opts)
	case "handoff":
		return a.handoff(ctx, opts, commandArgs, false)
	case "retry":
		return a.handoff(ctx, opts, commandArgs, true)
	case "add":
		return a.addAgent(ctx, opts, commandArgs)
	case "prompt":
		return a.promptAgent(ctx, opts, commandArgs)
	case "rename":
		return a.renameAgent(ctx, opts, commandArgs)
	case "focus":
		return a.focusAgent(ctx, opts, commandArgs)
	case "read":
		return a.readAgent(ctx, opts, commandArgs)
	case "rebind":
		return a.rebindAgent(ctx, opts, commandArgs)
	case "plugin":
		return a.plugin(ctx, opts, commandArgs)
	default:
		a.writeError(fmt.Errorf("unknown command %q", command), opts.json)
		return 2
	}
}

type handoffArgs struct {
	feature  string
	worktree string
	branch   string
	resend   bool
}

type controlContextArgs struct {
	feature  string
	worktree string
}

func parseControlContext(args []string) (controlContextArgs, []string, error) {
	var contextArgs controlContextArgs
	remaining := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == "--" {
			remaining = append(remaining, args[index+1:]...)
			break
		}
		switch args[index] {
		case "--feature", "--worktree":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return controlContextArgs{}, nil, fmt.Errorf("%s requires a value", args[index])
			}
			if args[index] == "--feature" {
				contextArgs.feature = args[index+1]
			} else {
				contextArgs.worktree = args[index+1]
			}
			index++
		default:
			remaining = append(remaining, args[index])
		}
	}
	return contextArgs, remaining, nil
}

func parseAddAgentArgs(args []string) (agents.AddRequest, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return agents.AddRequest{}, err
	}
	var kind string
	var positional []string
	for index := 0; index < len(remaining); index++ {
		switch remaining[index] {
		case "--kind":
			if index+1 >= len(remaining) || strings.HasPrefix(remaining[index+1], "--") {
				return agents.AddRequest{}, fmt.Errorf("--kind requires a value")
			}
			kind = remaining[index+1]
			index++
		default:
			if strings.HasPrefix(remaining[index], "--") {
				return agents.AddRequest{}, fmt.Errorf("unknown add option %q", remaining[index])
			}
			positional = append(positional, remaining[index])
		}
	}
	if len(positional) != 1 {
		return agents.AddRequest{}, fmt.Errorf("add requires one role: wt herd add <role> [--kind <kind>]")
	}
	return agents.AddRequest{Context: agents.ContextRequest{FeatureName: contextArgs.feature, WorktreePath: contextArgs.worktree}, Role: positional[0], Kind: kind}, nil
}

func parsePromptAgentArgs(args []string) (agents.PromptRequest, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return agents.PromptRequest{}, err
	}
	var target string
	var positional []string
	for index := 0; index < len(remaining); index++ {
		switch remaining[index] {
		case "--target":
			if index+1 >= len(remaining) || strings.HasPrefix(remaining[index+1], "--") {
				return agents.PromptRequest{}, fmt.Errorf("--target requires a value")
			}
			target = remaining[index+1]
			index++
		default:
			positional = append(positional, remaining[index])
		}
	}
	if len(positional) == 0 {
		return agents.PromptRequest{}, fmt.Errorf("prompt requires text")
	}
	role := ""
	if len(positional) > 1 {
		role = positional[0]
		positional = positional[1:]
	}
	return agents.PromptRequest{Context: agents.ContextRequest{FeatureName: contextArgs.feature, WorktreePath: contextArgs.worktree}, Role: role, Target: target, Text: strings.Join(positional, " ")}, nil
}

func parseTargetAgentArgs(args []string, command string, allowLines bool) (agents.TargetRequest, int, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return agents.TargetRequest{}, 0, err
	}
	var target string
	lines := 120
	var positional []string
	for index := 0; index < len(remaining); index++ {
		switch remaining[index] {
		case "--target":
			if index+1 >= len(remaining) || strings.HasPrefix(remaining[index+1], "--") {
				return agents.TargetRequest{}, 0, fmt.Errorf("--target requires a value")
			}
			target = remaining[index+1]
			index++
		case "--lines":
			if !allowLines {
				return agents.TargetRequest{}, 0, fmt.Errorf("--lines is only available with read")
			}
			if index+1 >= len(remaining) || strings.HasPrefix(remaining[index+1], "--") {
				return agents.TargetRequest{}, 0, fmt.Errorf("--lines requires a value")
			}
			parsed, parseErr := strconv.Atoi(remaining[index+1])
			if parseErr != nil || parsed < 1 || parsed > 1000 {
				return agents.TargetRequest{}, 0, fmt.Errorf("--lines must be a number between 1 and 1000")
			}
			lines = parsed
			index++
		default:
			if strings.HasPrefix(remaining[index], "--") {
				return agents.TargetRequest{}, 0, fmt.Errorf("unknown %s option %q", command, remaining[index])
			}
			positional = append(positional, remaining[index])
		}
	}
	if len(positional) > 1 {
		return agents.TargetRequest{}, 0, fmt.Errorf("%s accepts at most one role", command)
	}
	role := ""
	if len(positional) == 1 {
		role = positional[0]
	}
	return agents.TargetRequest{Context: agents.ContextRequest{FeatureName: contextArgs.feature, WorktreePath: contextArgs.worktree}, Role: role, Target: target}, lines, nil
}

func parseRenameAgentArgs(args []string) (agents.RenameRequest, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return agents.RenameRequest{}, err
	}
	if len(remaining) != 2 {
		return agents.RenameRequest{}, fmt.Errorf("rename requires <role> <new-role>")
	}
	return agents.RenameRequest{Context: agents.ContextRequest{FeatureName: contextArgs.feature, WorktreePath: contextArgs.worktree}, Role: remaining[0], NewRole: remaining[1]}, nil
}

func parseRebindAgentArgs(args []string) (agents.RebindRequest, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return agents.RebindRequest{}, err
	}
	var target string
	var positional []string
	for index := 0; index < len(remaining); index++ {
		if remaining[index] == "--target" {
			if index+1 >= len(remaining) || strings.HasPrefix(remaining[index+1], "--") {
				return agents.RebindRequest{}, fmt.Errorf("--target requires a value")
			}
			target = remaining[index+1]
			index++
			continue
		}
		if strings.HasPrefix(remaining[index], "--") {
			return agents.RebindRequest{}, fmt.Errorf("unknown rebind option %q", remaining[index])
		}
		positional = append(positional, remaining[index])
	}
	if len(positional) != 1 || target == "" {
		return agents.RebindRequest{}, fmt.Errorf("rebind requires <role> --target <live-target>")
	}
	return agents.RebindRequest{Context: agents.ContextRequest{FeatureName: contextArgs.feature, WorktreePath: contextArgs.worktree}, Role: positional[0], Target: target}, nil
}

func parseHandoffArgs(args []string, retry bool) (handoffArgs, error) {
	var parsed handoffArgs
	for len(args) > 0 {
		switch args[0] {
		case "--feature", "--worktree", "--branch":
			if len(args) < 2 || strings.HasPrefix(args[1], "--") {
				return handoffArgs{}, fmt.Errorf("%s requires a value", args[0])
			}
			switch args[0] {
			case "--feature":
				parsed.feature = args[1]
			case "--worktree":
				parsed.worktree = args[1]
			case "--branch":
				parsed.branch = args[1]
			}
			args = args[2:]
		case "--resend":
			if !retry {
				return handoffArgs{}, fmt.Errorf("--resend is only available with retry")
			}
			parsed.resend = true
			args = args[1:]
		default:
			return handoffArgs{}, fmt.Errorf("unknown handoff option %q", args[0])
		}
	}
	if !retry && (parsed.feature == "" || parsed.worktree == "") {
		return handoffArgs{}, fmt.Errorf("handoff requires --feature and --worktree")
	}
	return parsed, nil
}

func parseArgs(args []string) (options, string, []string, error) {
	var opts options
	for len(args) > 0 {
		switch args[0] {
		case "--json":
			opts.json = true
			args = args[1:]
		case "--repo-root", "--config", "--home", "--herdr-bin":
			if len(args) < 2 || strings.HasPrefix(args[1], "--") {
				return options{}, "", nil, fmt.Errorf("%s requires a value", args[0])
			}
			switch args[0] {
			case "--repo-root":
				opts.repoRoot = args[1]
			case "--config":
				opts.config = args[1]
			case "--home":
				opts.home = args[1]
			case "--herdr-bin":
				opts.herdrBin = args[1]
			}
			args = args[2:]
		default:
			return opts, args[0], args[1:], nil
		}
	}
	return opts, "", nil, nil
}

type runtimeContext struct {
	paths  worktree.Paths
	config config.Config
	herdr  *herdr.Client
}

func (a *App) load(opts options) (runtimeContext, error) {
	lookup := a.withOverrides(opts)
	repoRoot := opts.repoRoot
	if repoRoot == "" {
		if value, ok := lookup(worktree.RepoOverrideEnv); ok && strings.TrimSpace(value) != "" {
			repoRoot = value
		}
	}
	if repoRoot == "" {
		cwd, err := a.getwd()
		if err != nil {
			return runtimeContext{}, fmt.Errorf("resolve working directory: %w", err)
		}
		repoRoot, err = worktree.FindRepoRoot(cwd)
		if err != nil {
			return runtimeContext{}, fmt.Errorf("resolve repository: %w", err)
		}
	}

	paths, err := worktree.Resolve(repoRoot, lookup)
	if err != nil {
		return runtimeContext{}, err
	}
	cfg, err := config.Load(paths.ConfigPath, lookup)
	if err != nil {
		return runtimeContext{}, err
	}

	binary := opts.herdrBin
	if binary == "" {
		if value, ok := lookup("HERDR_BIN_PATH"); ok && strings.TrimSpace(value) != "" {
			binary = value
		} else if value, ok := lookup("HERDR_DEVFLOW_HERDR_BIN"); ok && strings.TrimSpace(value) != "" {
			binary = value
		}
	}
	socketPath, _ := lookup("HERDR_SOCKET_PATH")
	return runtimeContext{
		paths:  paths,
		config: cfg,
		herdr:  herdr.New(binary, socketPath, a.runner),
	}, nil
}

func (a *App) withOverrides(opts options) func(string) (string, bool) {
	return func(key string) (string, bool) {
		switch key {
		case worktree.ConfigOverrideEnv:
			if opts.config != "" {
				return opts.config, true
			}
		case worktree.HomeOverrideEnv:
			if opts.home != "" {
				return opts.home, true
			}
		}
		return a.lookupEnv(key)
	}
}

func (a *App) setup(ctx context.Context, opts options) int {
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	if !runtime.config.Bridge.Enabled {
		a.writeResult(opts.json, map[string]any{"status": "disabled", "config": runtime.paths.ConfigPath, "message": "Ori Herdr Devflow is disabled by configuration; no Herdr state was changed."})
		return 0
	}
	if err := a.installRuntime(ctx, runtime.paths); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if err := verifyCompatibility(ctx, runtime.herdr, runtime.config); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if _, err := a.lookPath(runtime.config.Primary.Kind); err != nil {
		a.writeError(&model.StageError{
			Stage:    "agent executable",
			Code:     model.ErrAgentUnavailable,
			Message:  fmt.Sprintf("configured %s executable was not found on PATH", runtime.config.Primary.Kind),
			Recovery: fmt.Sprintf("install %s, then run wt herd setup", runtime.config.Primary.Kind),
			Cause:    err,
		}, opts.json)
		return 1
	}

	plugin, err := ensurePlugin(ctx, runtime.herdr, runtime.paths.PluginRuntimeDir)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	store := state.New(runtime.paths.StateDir)
	bridgeState, err := store.Load()
	if err != nil {
		a.writeError(&model.StageError{Stage: "state", Code: model.ErrStateCorrupt, Message: "bridge state could not be read", Recovery: "move the local state file aside, then run wt herd setup", Cause: err}, opts.json)
		return 1
	}
	if err := store.Save(bridgeState); err != nil {
		a.writeError(&model.StageError{Stage: "state", Code: model.ErrStateCorrupt, Message: "bridge state could not be saved", Recovery: "check the local bridge state directory permissions, then run wt herd doctor", Cause: err}, opts.json)
		return 1
	}

	a.writeResult(opts.json, map[string]any{
		"status":        "ready",
		"repository_id": runtime.paths.RepositoryID,
		"config":        runtime.paths.ConfigPath,
		"helper":        runtime.paths.HelperPath,
		"plugin": map[string]any{
			"id":      plugin.PluginID,
			"root":    plugin.PluginRoot,
			"enabled": plugin.Enabled,
		},
		"scheduler": "not registered until a continuation is scheduled",
		"integrations": map[string]string{
			"policy": "not changed by setup",
			"claude": "inspect with: herdr integration status; install manually with: herdr integration install claude",
			"codex":  "inspect with: herdr integration status; install manually with: herdr integration install codex",
		},
	})
	return 0
}

func (a *App) handoff(ctx context.Context, opts options, args []string, retry bool) int {
	parsed, err := parseHandoffArgs(args, retry)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}
	if !runtime.config.Bridge.Enabled {
		a.writeResult(opts.json, map[string]any{"status": "disabled", "message": "Ori Herdr Devflow is disabled; the Git worktree remains ready without a Herdr handoff."})
		return 0
	}
	if err := verifyCompatibility(ctx, runtime.herdr, runtime.config); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if retry {
		if parsed.worktree == "" {
			cwd, cwdErr := a.getwd()
			if cwdErr != nil {
				a.writeError(&model.StageError{Stage: "retry", Code: model.ErrWorktreeInvalid, Message: "could not resolve the current worktree", Recovery: "pass --worktree <path>", Cause: cwdErr}, opts.json)
				return 1
			}
			parsed.worktree, cwdErr = worktree.FindRepoRoot(cwd)
			if cwdErr != nil {
				a.writeError(&model.StageError{Stage: "retry", Code: model.ErrWorktreeInvalid, Message: "retry must run from a feature Git worktree", Recovery: "pass --worktree <path>", Cause: cwdErr}, opts.json)
				return 1
			}
		}
		if parsed.feature == "" {
			parsed.feature = a.featureForPath(runtime.paths.StateDir, runtime.paths.RepositoryID, parsed.worktree)
			if parsed.feature == "" {
				parsed.feature = filepath.Base(parsed.worktree)
			}
		}
	}
	service := &agents.Service{
		Config:       runtime.config,
		RepositoryID: runtime.paths.RepositoryID,
		GitCommonDir: runtime.paths.GitCommonDir,
		Client:       runtime.herdr,
		Store:        state.New(runtime.paths.StateDir),
	}
	result, err := service.Handoff(ctx, agents.HandoffRequest{
		FeatureName:  parsed.feature,
		WorktreePath: parsed.worktree,
		Branch:       parsed.branch,
		Resend:       parsed.resend,
	})
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	a.writeResult(opts.json, map[string]any{
		"status":           "ready",
		"feature":          result.Feature.Name,
		"worktree":         result.Feature.Path,
		"workspace_id":     result.WorkspaceID,
		"primary_agent":    result.Primary.Name,
		"primary_role":     result.Primary.Role,
		"prompt_delivered": result.PromptDelivered,
		"prompt_skipped":   result.PromptSkipped,
	})
	return 0
}

func (a *App) loadAgentControl(ctx context.Context, opts options) (*agents.Service, runtimeContext, int) {
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return nil, runtimeContext{}, 1
	}
	if !runtime.config.Bridge.Enabled {
		a.writeResult(opts.json, map[string]any{"status": "disabled", "message": "Ori Herdr Devflow is disabled; no Herdr agent was changed."})
		return nil, runtime, 0
	}
	if err := verifyCompatibility(ctx, runtime.herdr, runtime.config); err != nil {
		a.writeError(err, opts.json)
		return nil, runtime, 1
	}
	return &agents.Service{
		Config:       runtime.config,
		RepositoryID: runtime.paths.RepositoryID,
		GitCommonDir: runtime.paths.GitCommonDir,
		Client:       runtime.herdr,
		Store:        state.New(runtime.paths.StateDir),
	}, runtime, -1
}

func defaultControlContext(request *agents.ContextRequest, runtime runtimeContext) {
	if request.FeatureName == "" && request.WorktreePath == "" {
		request.WorktreePath = runtime.paths.RepoRoot
	}
}

func (a *App) addAgent(ctx context.Context, opts options, args []string) int {
	request, err := parseAddAgentArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadAgentControl(ctx, opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&request.Context, runtime)
	result, err := service.Add(ctx, request)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, map[string]any{"status": "ready", "feature": result.Feature.Name, "agent": result.Agent, "reused": result.Reused})
	} else if result.Reused {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: using existing %s agent %s for %s\n", result.Agent.Role, result.Agent.Name, result.Feature.Name)
	} else {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: added %s agent %s for %s\n", result.Agent.Role, result.Agent.Name, result.Feature.Name)
	}
	return 0
}

func (a *App) promptAgent(ctx context.Context, opts options, args []string) int {
	request, err := parsePromptAgentArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadAgentControl(ctx, opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&request.Context, runtime)
	result, err := service.Prompt(ctx, request)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, map[string]any{"status": "ready", "feature": result.Feature.Name, "agent": result.Agent, "prompt_delivered": true})
	} else {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: prompt delivered to %s (%s) in %s\n", result.Agent.Role, result.Agent.Name, result.Feature.Name)
	}
	return 0
}

func (a *App) renameAgent(ctx context.Context, opts options, args []string) int {
	request, err := parseRenameAgentArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadAgentControl(ctx, opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&request.Context, runtime)
	result, err := service.Rename(ctx, request)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, map[string]any{"status": "ready", "feature": result.Feature.Name, "agent": result.Agent})
	} else {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: renamed agent to %s (%s) in %s\n", result.Agent.Role, result.Agent.Name, result.Feature.Name)
	}
	return 0
}

func (a *App) focusAgent(ctx context.Context, opts options, args []string) int {
	request, _, err := parseTargetAgentArgs(args, "focus", false)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadAgentControl(ctx, opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&request.Context, runtime)
	agent, feature, err := service.Focus(ctx, request)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, map[string]any{"status": "ready", "feature": feature.Name, "agent": agent, "focused": true})
	} else {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: focused %s (%s) in %s\n", agent.Role, agent.Name, feature.Name)
	}
	return 0
}

func (a *App) readAgent(ctx context.Context, opts options, args []string) int {
	request, lines, err := parseTargetAgentArgs(args, "read", true)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadAgentControl(ctx, opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&request.Context, runtime)
	result, err := service.Read(ctx, request, lines)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, map[string]any{"status": "ready", "feature": result.Feature.Name, "agent": result.Agent, "text": result.Text})
	} else {
		fmt.Fprint(a.stdout, result.Text)
	}
	return 0
}

func (a *App) rebindAgent(ctx context.Context, opts options, args []string) int {
	request, err := parseRebindAgentArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadAgentControl(ctx, opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&request.Context, runtime)
	result, err := service.Rebind(ctx, request)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, map[string]any{"status": "ready", "feature": result.Feature.Name, "agent": result.Agent, "rebound": true})
	} else {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: rebound %s to %s in %s\n", result.Agent.Role, result.Agent.Name, result.Feature.Name)
	}
	return 0
}

func (a *App) featureForPath(stateDir, repositoryID, candidatePath string) string {
	store := state.New(stateDir)
	bridgeState, err := store.Load()
	if err != nil {
		return ""
	}
	for key, featureState := range bridgeState.Features {
		if !strings.HasPrefix(key, repositoryID+":") {
			continue
		}
		if sameFilesystemPath(featureState.Feature.Path, candidatePath) {
			return featureState.Feature.Name
		}
	}
	return ""
}

func sameFilesystemPath(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil {
		left = leftResolved
	}
	if rightErr == nil {
		right = rightResolved
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func verifyCompatibility(ctx context.Context, client *herdr.Client, cfg config.Config) error {
	installed, err := client.Version(ctx)
	if err != nil {
		return err
	}
	minimum, _ := config.ParseVersion(cfg.Bridge.MinHerdrVersion)
	if !installed.AtLeast(minimum) {
		return &model.StageError{
			Stage:    "version",
			Code:     model.ErrHerdrIncompatible,
			Message:  fmt.Sprintf("Herdr %s is older than the required %s", installed.Raw, minimum.Raw),
			Recovery: "update Herdr, then run wt herd setup",
		}
	}
	schema, err := client.Schema(ctx)
	if err != nil {
		return err
	}
	for _, method := range []string{"plugin.link", "plugin.enable", "session.snapshot", "worktree.open", "agent.start", "agent.view.set", "events.subscribe"} {
		if !schema.Supports(method) {
			return &model.StageError{
				Stage:    "schema",
				Code:     model.ErrSchemaUnsupported,
				Message:  fmt.Sprintf("Herdr API schema does not provide %s", method),
				Recovery: "update Herdr to 0.7.5 or newer, then run wt herd doctor",
			}
		}
	}
	return nil
}

func ensurePlugin(ctx context.Context, client *herdr.Client, runtimePluginDir string) (herdr.PluginInfo, error) {
	plugins, err := client.PluginList(ctx, PluginID)
	if err == nil && len(plugins) == 1 && samePath(plugins[0].PluginRoot, runtimePluginDir) {
		if plugins[0].Enabled {
			return plugins[0], nil
		}
		return client.EnablePlugin(ctx, PluginID)
	}
	plugin, linkErr := client.LinkPlugin(ctx, runtimePluginDir)
	if linkErr != nil {
		return herdr.PluginInfo{}, linkErr
	}
	if plugin.Enabled {
		return plugin, nil
	}
	return client.EnablePlugin(ctx, PluginID)
}

func (a *App) installRuntime(ctx context.Context, paths worktree.Paths) error {
	if err := os.MkdirAll(filepath.Dir(paths.HelperPath), 0700); err != nil {
		return &model.StageError{Stage: "runtime", Code: model.ErrStateCorrupt, Message: "could not create the user-local helper directory", Recovery: "check HERDR_DEVFLOW_HOME permissions", Cause: err}
	}
	if err := os.MkdirAll(paths.PluginRuntimeDir, 0700); err != nil {
		return &model.StageError{Stage: "runtime", Code: model.ErrStateCorrupt, Message: "could not create the user-local plugin directory", Recovery: "check HERDR_DEVFLOW_HOME permissions", Cause: err}
	}
	if err := a.buildHelper(ctx, paths.RepoRoot, paths.HelperPath); err != nil {
		return &model.StageError{Stage: "helper build", Code: model.ErrPluginUnavailable, Message: "could not build the stable Ori Devflow helper", Recovery: "run make herdr-devflow and then wt herd setup", Cause: err}
	}
	for sourceName, mode := range map[string]os.FileMode{"herdr-plugin.toml": 0644, "plugin.sh": 0755} {
		source := filepath.Join(paths.PluginSourceDir, sourceName)
		destination := filepath.Join(paths.PluginRuntimeDir, sourceName)
		if err := copyFileAtomic(source, destination, mode); err != nil {
			return &model.StageError{Stage: "plugin runtime", Code: model.ErrPluginUnavailable, Message: "could not install the stable Herdr plugin runtime", Recovery: "run wt herd setup from the Ori repository", Cause: err}
		}
	}
	info, err := os.Stat(paths.HelperPath)
	if err != nil || info.Mode()&0111 == 0 {
		return &model.StageError{Stage: "helper executable", Code: model.ErrPluginUnavailable, Message: "stable helper is not executable", Recovery: "run wt herd setup", Cause: err}
	}
	return nil
}

func buildHelper(ctx context.Context, repoRoot, destination string) error {
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".herdr-devflow-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	defer os.Remove(temporaryPath)
	command := exec.CommandContext(ctx, "go", "build", "-o", temporaryPath, "./tools/herdr-devflow/cmd/herdr-devflow")
	command.Dir = repoRoot
	if err := command.Run(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0755); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func copyFileAtomic(source, destination string, mode os.FileMode) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".plugin-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

type diagnostic struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Recovery string `json:"recovery,omitempty"`
}

func (a *App) doctor(ctx context.Context, opts options) int {
	runtime, err := a.load(opts)
	if err != nil {
		a.writeDiagnostics(opts.json, []diagnostic{{Name: "config", Status: "FAIL", Detail: stageConfigError(err).Error(), Recovery: "fix .herdr/devflow.toml, then run wt herd doctor"}})
		return 1
	}
	diagnostics := []diagnostic{{Name: "config", Status: "PASS", Detail: runtime.paths.ConfigPath}}
	if !runtime.config.Bridge.Enabled {
		diagnostics = append(diagnostics, diagnostic{Name: "bridge", Status: "WARN", Detail: "disabled by configuration", Recovery: "set [bridge].enabled = true to opt in"})
		a.writeDiagnostics(opts.json, diagnostics)
		return 0
	}
	if info, err := os.Stat(runtime.paths.HelperPath); err == nil && info.Mode()&0111 != 0 {
		diagnostics = append(diagnostics, diagnostic{Name: "stable helper", Status: "PASS", Detail: runtime.paths.HelperPath})
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "stable helper", Status: "WARN", Detail: "not installed", Recovery: "wt herd setup"})
	}
	if info, err := os.Stat(runtime.paths.StateDir); err == nil && info.IsDir() && info.Mode().Perm()&0007 == 0 {
		diagnostics = append(diagnostics, diagnostic{Name: "state permissions", Status: "PASS", Detail: runtime.paths.StateDir})
	} else if err == nil {
		diagnostics = append(diagnostics, diagnostic{Name: "state permissions", Status: "WARN", Detail: "state directory is accessible by other users", Recovery: "chmod 700 " + runtime.paths.StateDir})
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "state permissions", Status: "WARN", Detail: "state directory has not been created", Recovery: "wt herd setup"})
	}

	version, versionErr := runtime.herdr.Version(ctx)
	if versionErr != nil {
		diagnostics = append(diagnostics, diagnosticFromError("Herdr binary", versionErr))
	} else {
		minimum, _ := config.ParseVersion(runtime.config.Bridge.MinHerdrVersion)
		if version.AtLeast(minimum) {
			diagnostics = append(diagnostics, diagnostic{Name: "Herdr binary", Status: "PASS", Detail: version.Raw})
		} else {
			diagnostics = append(diagnostics, diagnostic{Name: "Herdr binary", Status: "FAIL", Detail: "version " + version.Raw + " is below " + minimum.Raw, Recovery: "update Herdr, then run wt herd doctor"})
		}
	}
	if schema, schemaErr := runtime.herdr.Schema(ctx); schemaErr != nil {
		diagnostics = append(diagnostics, diagnosticFromError("Herdr schema", schemaErr))
	} else if schema.Supports("plugin.link") && schema.Supports("agent.view.set") && schema.Supports("events.subscribe") {
		diagnostics = append(diagnostics, diagnostic{Name: "Herdr schema", Status: "PASS", Detail: fmt.Sprintf("protocol %d", schema.Protocol)})
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "Herdr schema", Status: "FAIL", Detail: "required structured API methods are absent", Recovery: "update Herdr, then run wt herd doctor"})
	}
	if runtime.herdr.SocketPath != "" {
		if _, socketErr := runtime.herdr.Ping(ctx); socketErr != nil {
			diagnostics = append(diagnostics, diagnosticFromError("Herdr socket", socketErr))
		} else {
			diagnostics = append(diagnostics, diagnostic{Name: "Herdr socket", Status: "PASS", Detail: "local JSONL socket responded"})
		}
	} else if _, serverErr := runtime.herdr.ServerStatus(ctx); serverErr != nil {
		diagnostics = append(diagnostics, diagnosticFromError("Herdr server", serverErr))
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "Herdr server", Status: "PASS", Detail: "structured server status responded"})
	}
	if plugins, pluginErr := runtime.herdr.PluginList(ctx, PluginID); pluginErr != nil {
		diagnostics = append(diagnostics, diagnosticFromError("Herdr plugin", pluginErr))
	} else if len(plugins) == 1 && samePath(plugins[0].PluginRoot, runtime.paths.PluginRuntimeDir) && plugins[0].Enabled {
		diagnostics = append(diagnostics, diagnostic{Name: "Herdr plugin", Status: "PASS", Detail: PluginID + " linked to stable runtime"})
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "Herdr plugin", Status: "WARN", Detail: "not linked/enabled at the stable runtime", Recovery: "wt herd setup"})
	}
	if _, err := a.lookPath(runtime.config.Primary.Kind); err == nil {
		diagnostics = append(diagnostics, diagnostic{Name: "agent executable", Status: "PASS", Detail: runtime.config.Primary.Kind})
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "agent executable", Status: "FAIL", Detail: runtime.config.Primary.Kind + " is not on PATH", Recovery: "install " + runtime.config.Primary.Kind + ", then run wt herd setup"})
	}
	if integrationStatus, integrationErr := runtime.herdr.IntegrationStatus(ctx); integrationErr != nil {
		diagnostics = append(diagnostics, diagnostic{Name: "agent integrations", Status: "WARN", Detail: "could not read current integration status; setup did not change integrations", Recovery: "herdr integration status; herdr integration install claude; herdr integration install codex"})
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "agent integrations", Status: "PASS", Detail: integrationStatus, Recovery: "setup never installs or changes integrations; use herdr integration install claude|codex explicitly"})
	}
	if a.goos == "darwin" {
		plist := filepath.Join(userHomeOrEmpty(), "Library", "LaunchAgents", "com.ori.herdr-devflow.plist")
		if _, err := os.Stat(plist); err == nil {
			diagnostics = append(diagnostics, diagnostic{Name: "scheduler", Status: "PASS", Detail: "LaunchAgent registered"})
		} else {
			diagnostics = append(diagnostics, diagnostic{Name: "scheduler", Status: "WARN", Detail: "no continuation dispatcher is registered", Recovery: "create a one-time continuation with wt herd continue ..."})
		}
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "scheduler", Status: "WARN", Detail: "one-time continuation scheduling is macOS-only", Recovery: "run scheduling commands on macOS"})
	}
	a.writeDiagnostics(opts.json, diagnostics)
	for _, item := range diagnostics {
		if item.Status == "FAIL" {
			return 1
		}
	}
	return 0
}

func (a *App) plugin(ctx context.Context, opts options, args []string) int {
	if len(args) == 0 {
		a.writeError(fmt.Errorf("plugin requires startup, setup, refresh, or board"), opts.json)
		return 2
	}
	// Plugin actions are deliberately passive in this milestone. They never
	// install integrations, create worktrees, start agents, or change keybinds.
	switch args[0] {
	case "startup", "refresh":
		a.writeResult(opts.json, map[string]any{"status": "ok", "message": "Ori Devflow plugin is ready; use wt herd status after a feature handoff."})
		return 0
	case "setup":
		a.writeResult(opts.json, map[string]any{"status": "manual_setup_required", "message": "Run wt herd setup from an Ori Git worktree to refresh the stable helper."})
		return 0
	case "board":
		a.writeResult(opts.json, map[string]any{"status": "empty", "message": "No managed Ori Devflow features are registered yet."})
		return 0
	default:
		a.writeError(fmt.Errorf("unknown plugin command %q", args[0]), opts.json)
		return 2
	}
}

func (a *App) writeHelp() {
	fmt.Fprint(a.stdout, `Ori Herdr Devflow bridge

Usage:
  wt herd setup                 Install/update the stable local helper and linked plugin
  wt herd doctor                Check config, Herdr, plugin, agent, scheduler, and state readiness
  wt herd handoff --feature NAME --worktree PATH [--branch NAME]
                                Open an existing Git worktree and launch its primary agent
  wt herd retry [--feature NAME] [--worktree PATH] [--branch NAME] [--resend]
                                Resume only missing handoff stages; --resend repeats a confirmed prompt
  wt herd add <role> [--kind KIND] [--feature NAME|--worktree PATH]
                                Start one explicit secondary role agent in the managed workspace
  wt herd prompt [role] <text> [--target TARGET] [--feature NAME|--worktree PATH]
                                Prompt the selected feature-scoped agent (primary role by default)
  wt herd rename <role> <new-role> [--feature NAME|--worktree PATH]
  wt herd focus [role] [--target TARGET] [--feature NAME|--worktree PATH]
  wt herd read [role] [--target TARGET] [--lines N] [--feature NAME|--worktree PATH]
  wt herd rebind <role> --target TARGET [--feature NAME|--worktree PATH]
  scripts/herdr-devflow.sh ...  Invoke the helper directly

Global options (before the command):
  --repo-root PATH  Resolve configuration from a specific Git worktree
  --config PATH     Use an explicit devflow TOML file
  --home PATH       Use an explicit user-local runtime root (tests/development)
  --herdr-bin PATH  Use an explicit Herdr executable
  --json            Emit a machine-readable result
`)
}

func (a *App) writeResult(asJSON bool, value any) {
	if asJSON {
		encoded, _ := json.MarshalIndent(value, "", "  ")
		fmt.Fprintln(a.stdout, string(encoded))
		return
	}
	encoded, _ := json.MarshalIndent(value, "", "  ")
	var pretty map[string]any
	if json.Unmarshal(encoded, &pretty) == nil {
		if status, ok := pretty["status"].(string); ok {
			fmt.Fprintf(a.stdout, "Ori Herdr Devflow: %s\n", status)
		}
		if message, ok := pretty["message"].(string); ok {
			fmt.Fprintln(a.stdout, message)
		}
		if helper, ok := pretty["helper"].(string); ok {
			fmt.Fprintf(a.stdout, "Stable helper: %s\n", helper)
		}
		if configPath, ok := pretty["config"].(string); ok {
			fmt.Fprintf(a.stdout, "Config: %s\n", configPath)
		}
		return
	}
	fmt.Fprintln(a.stdout, string(encoded))
}

func (a *App) writeDiagnostics(asJSON bool, diagnostics []diagnostic) {
	if asJSON {
		a.writeResult(true, map[string]any{"diagnostics": diagnostics})
		return
	}
	for _, item := range diagnostics {
		fmt.Fprintf(a.stdout, "%s  %-20s %s\n", item.Status, item.Name, item.Detail)
		if item.Recovery != "" {
			fmt.Fprintf(a.stdout, "      recovery: %s\n", item.Recovery)
		}
	}
}

func (a *App) writeError(err error, asJSON bool) {
	var stageErr *model.StageError
	if errors.As(err, &stageErr) {
		if asJSON {
			a.writeResult(true, map[string]any{"error": map[string]string{"stage": stageErr.Stage, "code": string(stageErr.Code), "message": stageErr.Message, "recovery": stageErr.Recovery}})
			return
		}
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow %s (%s): %s\n", stageErr.Stage, stageErr.Code, stageErr.Message)
		if stageErr.Recovery != "" {
			fmt.Fprintf(a.stderr, "Recovery: %s\n", stageErr.Recovery)
		}
		return
	}
	if asJSON {
		a.writeResult(true, map[string]any{"error": map[string]string{"code": "invalid_command", "message": err.Error()}})
		return
	}
	fmt.Fprintf(a.stderr, "Ori Herdr Devflow: %s\n", err)
}

func stageConfigError(err error) *model.StageError {
	return &model.StageError{Stage: "config", Code: model.ErrConfigInvalid, Message: err.Error(), Recovery: "fix .herdr/devflow.toml, then run wt herd doctor", Cause: err}
}

func diagnosticFromError(name string, err error) diagnostic {
	var stageErr *model.StageError
	if errors.As(err, &stageErr) {
		return diagnostic{Name: name, Status: "FAIL", Detail: stageErr.Message, Recovery: stageErr.Recovery}
	}
	return diagnostic{Name: name, Status: "FAIL", Detail: "unavailable", Recovery: "wt herd doctor"}
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func userHomeOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
