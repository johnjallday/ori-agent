// Package app implements the small, explicit CLI used by `wt herd` and the
// installed Herdr plugin. It is intentionally independent of the Ori server.
package app

import (
	"bufio"
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
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/agents"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/audit"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/claudeusage"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/cleanup"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overnight"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/overview"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/planning"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/scheduler"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/state"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/status"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/systempower"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeclient"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeinstall"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeservice"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

const (
	PluginID              = "ori.devflow"
	defaultCleanupTimeout = 5 * time.Second
)

type Dependencies struct {
	Stdout io.Writer
	Stderr io.Writer
	// Stdin is where a recorder invoked by Claude Code reads its payload.
	Stdin     io.Reader
	Getwd     func() (string, error)
	LookupEnv func(string) (string, bool)
	LookPath  func(string) (string, error)
	Runner    herdr.Runner
	// GitHubRunner overrides how `gh` is invoked. Tests inject deterministic
	// responses through it so no suite depends on a network, an authenticated
	// CLI, or the repository's real Issues.
	GitHubRunner        github.Runner
	BuildHelper         func(context.Context, string, string) error
	GOOS                string
	UserHomeDir         func() (string, error)
	Getuid              func() int
	LaunchctlRun        func(context.Context, string, ...string) error
	NewContinuationWake func() (WakeCoordinator, error)
	NewOvernightWake    func(wakeprotocol.Purpose) (WakeCoordinator, error)
	IsInteractive       func() bool
	WakeLifecycle       WakeLifecycle
}

// WakeCoordinator is the continuation helper's source-scoped view of the
// standalone Herdr Wake Service.
type WakeCoordinator interface {
	RegisterCandidate(context.Context, string, time.Time, string) (wakeclient.Evidence, error)
	VerifyCandidate(context.Context, string, time.Time) (wakeclient.Evidence, error)
	CancelCandidate(context.Context, string) (wakeclient.Evidence, error)
	Owner() wakeclient.OwnerReadiness
}

// overnightWakeAdapter keeps the Overnight supervisor's legacy-shaped wake
// boundary while every operation still goes through the typed standalone
// client. The source/purpose is fixed by NewOvernightWake at construction.
type overnightWakeAdapter struct{ WakeCoordinator }

func (w overnightWakeAdapter) Register(id string, wakeAt time.Time, detail string) error {
	_, err := w.RegisterCandidate(context.Background(), id, wakeAt, detail)
	return err
}

func (w overnightWakeAdapter) Verify(ctx context.Context, id string, wakeAt time.Time) (time.Time, error) {
	evidence, err := w.VerifyCandidate(ctx, id, wakeAt)
	return evidence.ProgrammedAt, err
}

func (w overnightWakeAdapter) Cancel(id string) error {
	_, err := w.CancelCandidate(context.Background(), id)
	return err
}

func (w overnightWakeAdapter) RegisterCandidate(ctx context.Context, id string, wakeAt time.Time, detail string) (wakeclient.Evidence, error) {
	return w.WakeCoordinator.RegisterCandidate(ctx, id, wakeAt, detail)
}

func (w overnightWakeAdapter) VerifyCandidate(ctx context.Context, id string, wakeAt time.Time) (wakeclient.Evidence, error) {
	return w.WakeCoordinator.VerifyCandidate(ctx, id, wakeAt)
}

func (w overnightWakeAdapter) CancelCandidate(ctx context.Context, id string) (wakeclient.Evidence, error) {
	return w.WakeCoordinator.CancelCandidate(ctx, id)
}

// WakeLifecycle is injectable so CLI tests can prove that confirmation gates
// all administrator actions without invoking sudo or touching system paths.
type WakeLifecycle interface {
	PrepareInstall(context.Context, string, int) (wakeinstall.PreparedInstall, error)
	Install(context.Context, wakeinstall.PreparedInstall) (wakeinstall.Status, error)
	Status(context.Context) (wakeinstall.Status, error)
	Doctor(context.Context) ([]wakeinstall.Diagnostic, error)
	Uninstall(context.Context, int) (wakeinstall.Status, error)
}

type App struct {
	stdout              io.Writer
	stderr              io.Writer
	stdin               io.Reader
	getwd               func() (string, error)
	lookupEnv           func(string) (string, bool)
	lookPath            func(string) (string, error)
	runner              herdr.Runner
	githubRunner        github.Runner
	buildHelper         func(context.Context, string, string) error
	goos                string
	userHomeDir         func() (string, error)
	getuid              func() int
	launchctlRun        func(context.Context, string, ...string) error
	newContinuationWake func() (WakeCoordinator, error)
	newOvernightWake    func(wakeprotocol.Purpose) (WakeCoordinator, error)
	isInteractive       func() bool
	wakeLifecycle       WakeLifecycle
	cleanupTimeout      time.Duration
}

func New(deps Dependencies) *App {
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
	if deps.Stdin == nil {
		deps.Stdin = os.Stdin
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
	if deps.UserHomeDir == nil {
		deps.UserHomeDir = os.UserHomeDir
	}
	if deps.Getuid == nil {
		deps.Getuid = os.Getuid
	}
	if deps.NewContinuationWake == nil {
		deps.NewContinuationWake = func() (WakeCoordinator, error) {
			return wakeclient.DefaultForSource(wakeprotocol.SourceContinuation)
		}
	}
	if deps.NewOvernightWake == nil {
		deps.NewOvernightWake = func(purpose wakeprotocol.Purpose) (WakeCoordinator, error) {
			return wakeclient.DefaultForPurpose(wakeprotocol.SourceOvernight, purpose)
		}
	}
	if deps.IsInteractive == nil {
		deps.IsInteractive = func() bool {
			input, inputOK := deps.Stdin.(*os.File)
			output, outputOK := deps.Stdout.(*os.File)
			if !inputOK || !outputOK {
				return false
			}
			inputInfo, inputErr := input.Stat()
			outputInfo, outputErr := output.Stat()
			return inputErr == nil && outputErr == nil &&
				inputInfo.Mode()&os.ModeCharDevice != 0 &&
				outputInfo.Mode()&os.ModeCharDevice != 0
		}
	}
	if deps.WakeLifecycle == nil {
		deps.WakeLifecycle = wakeinstall.NewManager()
	}
	return &App{
		stdout:              deps.Stdout,
		stderr:              deps.Stderr,
		stdin:               deps.Stdin,
		getwd:               deps.Getwd,
		lookupEnv:           deps.LookupEnv,
		lookPath:            deps.LookPath,
		runner:              deps.Runner,
		githubRunner:        deps.GitHubRunner,
		buildHelper:         deps.BuildHelper,
		goos:                deps.GOOS,
		userHomeDir:         deps.UserHomeDir,
		getuid:              deps.Getuid,
		launchctlRun:        deps.LaunchctlRun,
		newContinuationWake: deps.NewContinuationWake,
		newOvernightWake:    deps.NewOvernightWake,
		isInteractive:       deps.IsInteractive,
		wakeLifecycle:       deps.WakeLifecycle,
		cleanupTimeout:      defaultCleanupTimeout,
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
	case "continue":
		return a.continueAgent(ctx, opts, commandArgs)
	case "schedule":
		return a.schedule(ctx, opts, commandArgs)
	case "status", "overview":
		return a.status(ctx, opts, commandArgs)
	case "go":
		return a.goAgent(ctx, opts, commandArgs)
	case "feature-overview":
		return a.overview(ctx, opts, commandArgs)
	case "issue":
		return a.issue(ctx, opts, commandArgs)
	case "backlog":
		return a.backlog(ctx, opts, commandArgs)
	case "ready":
		return a.ready(ctx, opts, commandArgs)
	case "target":
		return a.handoffTarget(ctx, opts)
	case "cleanup":
		return a.cleanup(ctx, opts, commandArgs)
	case "dispatch":
		return a.dispatch(ctx, opts)
	case "plugin":
		return a.plugin(ctx, opts, commandArgs)
	case "claude-usage":
		return a.claudeUsage(ctx, opts, commandArgs)
	case "overnight":
		return a.overnight(ctx, opts, commandArgs)
	case "wake":
		return a.wake(ctx, opts, commandArgs)
	default:
		a.writeError(fmt.Errorf("unknown command %q", command), opts.json)
		return 2
	}
}

type wakeCommandArgs struct {
	command string
	yes     bool
	json    bool
}

func parseWakeCommandArgs(args []string) (wakeCommandArgs, error) {
	if len(args) == 0 {
		return wakeCommandArgs{}, fmt.Errorf("wake requires install, status, doctor, or uninstall")
	}
	parsed := wakeCommandArgs{command: args[0]}
	switch parsed.command {
	case "install", "status", "doctor", "uninstall":
	default:
		return wakeCommandArgs{}, fmt.Errorf("unknown wake command %q", parsed.command)
	}
	for _, argument := range args[1:] {
		switch argument {
		case "--yes":
			if parsed.command != "install" && parsed.command != "uninstall" {
				return wakeCommandArgs{}, fmt.Errorf("--yes is only valid with wake install or uninstall")
			}
			parsed.yes = true
		case "--json":
			parsed.json = true
		default:
			return wakeCommandArgs{}, fmt.Errorf("unknown wake %s option %q", parsed.command, argument)
		}
	}
	return parsed, nil
}

func (a *App) wake(ctx context.Context, opts options, args []string) int {
	parsed, err := parseWakeCommandArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	asJSON := opts.json || parsed.json
	switch parsed.command {
	case "status":
		status, err := a.wakeLifecycle.Status(ctx)
		if err != nil {
			a.writeError(err, asJSON)
			return 1
		}
		a.writeWakeStatus(asJSON, "status", status)
		if status.Supported && status.Installed && status.Running && status.Compatible {
			return 0
		}
		return 1
	case "doctor":
		diagnostics, err := a.wakeLifecycle.Doctor(ctx)
		if err != nil {
			a.writeError(err, asJSON)
			return 1
		}
		a.writeWakeDiagnostics(asJSON, diagnostics)
		for _, diagnostic := range diagnostics {
			if diagnostic.Status == "FAIL" {
				return 1
			}
		}
		return 0
	case "install":
		return a.wakeInstall(ctx, opts, parsed, asJSON)
	case "uninstall":
		return a.wakeUninstall(ctx, opts, parsed, asJSON)
	default:
		return 2
	}
}

func (a *App) wakeInstall(
	ctx context.Context,
	opts options,
	parsed wakeCommandArgs,
	asJSON bool,
) int {
	if a.goos != "darwin" {
		a.writeError(wakeservice.ErrUnsupported, asJSON)
		return 1
	}
	repoRoot, err := a.wakeRepoRoot(opts)
	if err != nil {
		a.writeError(err, asJSON)
		return 1
	}
	prepared, err := a.wakeLifecycle.PrepareInstall(ctx, repoRoot, a.getuid())
	if err != nil {
		a.writeError(err, asJSON)
		return 1
	}
	defer func() { _ = prepared.Cleanup() }()

	preview := wakeInstallPreview(prepared)
	if asJSON {
		if !parsed.yes {
			a.writeResult(true, map[string]any{
				"status":  "confirmation_required",
				"install": preview,
				"error":   "wake install requires --yes in JSON or other non-interactive use",
			})
			return 2
		}
	} else {
		writeWakeInstallPreview(a.stdout, preview)
	}
	if !a.confirmWakeAction(parsed.yes, asJSON, "Install the standalone Herdr Wake Service?") {
		if !parsed.yes && !a.isInteractive() {
			a.writeError(fmt.Errorf("wake install requires an interactive confirmation or --yes"), asJSON)
			return 2
		}
		if !asJSON {
			fmt.Fprintln(a.stdout, "Installation canceled; no administrator action was requested.")
		}
		return 1
	}
	status, err := a.wakeLifecycle.Install(ctx, prepared)
	if err != nil {
		a.writeError(err, asJSON)
		return 1
	}
	if asJSON {
		a.writeResult(true, map[string]any{
			"status": "installed", "install": preview, "wake_service": status,
		})
	} else {
		a.writeWakeStatus(false, "installed", status)
	}
	return 0
}

func (a *App) wakeUninstall(
	ctx context.Context,
	opts options,
	parsed wakeCommandArgs,
	asJSON bool,
) int {
	if a.goos != "darwin" {
		a.writeError(wakeservice.ErrUnsupported, asJSON)
		return 1
	}
	// The standalone daemon is intentionally not removed while saved work
	// still depends on one of its exact candidates. Refusing is safer than
	// converting a future continuation or Overnight Run into an unowned wake.
	runtime, runtimeErr := a.load(opts)
	var dependents []string
	if runtimeErr == nil {
		bridgeState, stateErr := state.New(runtime.paths.StateDir).Load()
		if stateErr != nil {
			a.writeError(fmt.Errorf("read wake dependents before uninstall: %w", stateErr), asJSON)
			return 1
		}
		for _, run := range bridgeState.Runs {
			if !run.State.Terminal() && run.WakeMode != model.WakeModeStayAwake {
				dependents = append(dependents, "Overnight Run "+run.ID)
			}
		}
		for _, featureState := range bridgeState.Features {
			for _, schedule := range featureState.Schedules {
				if schedule.WakeRequired && schedule.State.IsUnresolved() {
					dependents = append(dependents, "continuation "+schedule.ID)
				}
			}
		}
	}
	if len(dependents) > 0 {
		a.writeError(fmt.Errorf("cannot uninstall the standalone wake service while %s still depends on it; cancel the listed work first", strings.Join(dependents, ", ")), asJSON)
		return 1
	}
	status, err := a.wakeLifecycle.Status(ctx)
	if err != nil {
		a.writeError(err, asJSON)
		return 1
	}
	preview := map[string]any{
		"label":           wakeservice.LaunchDaemonLabel,
		"executable_path": wakeservice.ExecutablePath,
		"plist_path":      wakeservice.PlistPath,
		"socket_path":     wakeservice.SocketPath,
		"state_path":      wakeservice.StateDir,
		"installed":       status.Installed,
	}
	if asJSON {
		if !parsed.yes {
			a.writeResult(true, map[string]any{
				"status":    "confirmation_required",
				"uninstall": preview,
				"error":     "wake uninstall requires --yes in JSON or other non-interactive use",
			})
			return 2
		}
	} else {
		fmt.Fprintf(a.stdout, "Herdr Wake Service uninstall\n")
		fmt.Fprintf(a.stdout, "  LaunchDaemon: %s\n", wakeservice.LaunchDaemonLabel)
		fmt.Fprintf(a.stdout, "  Executable:   %s\n", wakeservice.ExecutablePath)
		fmt.Fprintf(a.stdout, "  Plist:        %s\n", wakeservice.PlistPath)
		fmt.Fprintf(a.stdout, "  Socket:       %s\n", wakeservice.SocketPath)
		fmt.Fprintf(a.stdout, "  Private state:%s\n", wakeservice.StateDir)
		fmt.Fprintln(a.stdout, "  The user-level Herdr dispatcher, agents, worktrees, and Ori files are not removed.")
	}
	if !a.confirmWakeAction(parsed.yes, asJSON, "Uninstall the standalone Herdr Wake Service?") {
		if !parsed.yes && !a.isInteractive() {
			a.writeError(fmt.Errorf("wake uninstall requires an interactive confirmation or --yes"), asJSON)
			return 2
		}
		if !asJSON {
			fmt.Fprintln(a.stdout, "Uninstall canceled; no administrator action was requested.")
		}
		return 1
	}
	removed, err := a.wakeLifecycle.Uninstall(ctx, a.getuid())
	if err != nil {
		a.writeError(err, asJSON)
		return 1
	}
	if asJSON {
		a.writeResult(true, map[string]any{
			"status": "uninstalled", "uninstall": preview, "wake_service": removed,
		})
	} else {
		a.writeWakeStatus(false, "uninstalled", removed)
	}
	return 0
}

func (a *App) wakeRepoRoot(opts options) (string, error) {
	if opts.repoRoot != "" {
		return worktree.FindRepoRoot(opts.repoRoot)
	}
	if value, ok := a.withOverrides(opts)(worktree.RepoOverrideEnv); ok &&
		strings.TrimSpace(value) != "" {
		return worktree.FindRepoRoot(value)
	}
	cwd, err := a.getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return worktree.FindRepoRoot(cwd)
}

func (a *App) confirmWakeAction(yes, asJSON bool, question string) bool {
	if yes {
		return true
	}
	if asJSON || !a.isInteractive() {
		return false
	}
	fmt.Fprintf(a.stdout, "%s [y/N] ", question)
	answer, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func wakeInstallPreview(prepared wakeinstall.PreparedInstall) map[string]any {
	return map[string]any{
		"label":           wakeservice.LaunchDaemonLabel,
		"executable_path": wakeservice.ExecutablePath,
		"plist_path":      wakeservice.PlistPath,
		"socket_path":     wakeservice.SocketPath,
		"state_path":      wakeservice.StateDir,
		"pmset_owner":     wakeservice.PMSetOwner,
		"pmset_type":      wakeservice.PMSetEventType,
		"allowed_uid":     prepared.AllowedUID,
		"artifact_digest": prepared.ArtifactDigest,
		"build":           prepared.BuildVersion,
		"capability":      "list, schedule, verify, reconcile, and exact-cancel only Herdr-owned macOS wake events",
		"uninstall":       "wt herd wake uninstall",
	}
}

func writeWakeInstallPreview(writer io.Writer, preview map[string]any) {
	fmt.Fprintln(writer, "Herdr Wake Service installation")
	fmt.Fprintf(writer, "  LaunchDaemon: %s\n", preview["label"])
	fmt.Fprintf(writer, "  Executable:   %s\n", preview["executable_path"])
	fmt.Fprintf(writer, "  Plist:        %s\n", preview["plist_path"])
	fmt.Fprintf(writer, "  Socket:       %s\n", preview["socket_path"])
	fmt.Fprintf(writer, "  Private state:%s\n", preview["state_path"])
	fmt.Fprintf(writer, "  Allowed UID:  %v\n", preview["allowed_uid"])
	fmt.Fprintf(writer, "  Artifact:     %s (%s)\n", preview["artifact_digest"], preview["build"])
	fmt.Fprintf(writer, "  Capability:   %s\n", preview["capability"])
	fmt.Fprintf(writer, "  Uninstall:    %s\n", preview["uninstall"])
	fmt.Fprintln(writer, "  Authorization: normal macOS administrator approval via /usr/bin/sudo -k")
	fmt.Fprintln(writer, "  No password, Keychain item, authorization cache, askpass program, or sudoers rule is stored.")
}

func (a *App) writeWakeStatus(asJSON bool, operation string, status wakeinstall.Status) {
	if asJSON {
		a.writeResult(true, map[string]any{"status": operation, "wake_service": status})
		return
	}
	fmt.Fprintf(a.stdout, "Herdr Wake Service: %s\n", status.Detail)
	fmt.Fprintf(a.stdout, "  supported=%t installed=%t running=%t compatible=%t\n",
		status.Supported, status.Installed, status.Running, status.Compatible)
	if status.DaemonBuild != "" {
		fmt.Fprintf(a.stdout, "  protocol=%d state=%d build=%s allowed_uid=%d\n",
			status.ProtocolVersion, status.StateVersion, status.DaemonBuild, status.AllowedUID)
	}
	if !status.LastSelfTestAt.IsZero() {
		fmt.Fprintf(a.stdout, "  last_self_test=%s\n", status.LastSelfTestAt.Format(time.RFC3339))
	}
	if status.WakeState != nil {
		state := status.WakeState
		fmt.Fprintf(a.stdout, "  candidates=%d reconciled_at=%s\n", len(state.Candidates), state.ReconciledAt.Format(time.RFC3339))
		if state.Programmed != nil {
			fmt.Fprintf(a.stdout, "  programmed=%s/%s at %s\n", state.Programmed.Source, state.Programmed.Purpose, state.Programmed.WakeAt.Format(time.RFC3339))
		}
	} else if status.StateDetail != "" {
		fmt.Fprintf(a.stdout, "  candidate_inventory=%s\n", status.StateDetail)
	}
}

func (a *App) writeWakeDiagnostics(asJSON bool, diagnostics []wakeinstall.Diagnostic) {
	if asJSON {
		a.writeResult(true, map[string]any{"status": "diagnostics", "diagnostics": diagnostics})
		return
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(a.stdout, "[%s] %s: %s\n", diagnostic.Status, diagnostic.Name, diagnostic.Detail)
		if diagnostic.Recovery != "" {
			fmt.Fprintf(a.stdout, "       recovery: %s\n", diagnostic.Recovery)
		}
	}
}

type handoffArgs struct {
	feature  string
	worktree string
	branch   string
	kind     string
	resend   bool
	noPrompt bool
}

type controlContextArgs struct {
	feature  string
	worktree string
}

type statusArgs struct {
	context   controlContextArgs
	current   bool
	watch     bool
	noColor   bool
	json      bool
	clearView bool
}

type cleanupArgs struct {
	worktree string
	override bool
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

func parseStatusArgs(args []string) (statusArgs, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return statusArgs{}, err
	}
	parsed := statusArgs{context: contextArgs}
	for _, argument := range remaining {
		switch argument {
		case "--current":
			parsed.current = true
		case "--watch":
			parsed.watch = true
		case "--no-color":
			parsed.noColor = true
		case "--json":
			parsed.json = true
		case "--clear-view":
			parsed.clearView = true
		default:
			return statusArgs{}, fmt.Errorf("unknown status option %q", argument)
		}
	}
	if parsed.current && (parsed.context.feature != "" || parsed.context.worktree != "") {
		return statusArgs{}, fmt.Errorf("--current cannot be combined with --feature or --worktree")
	}
	if parsed.clearView && parsed.watch {
		return statusArgs{}, fmt.Errorf("--clear-view cannot be combined with --watch")
	}
	return parsed, nil
}

func parseCleanupArgs(args []string) (cleanupArgs, error) {
	var parsed cleanupArgs
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--worktree":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return cleanupArgs{}, fmt.Errorf("--worktree requires a value")
			}
			parsed.worktree = args[index+1]
			index++
		case "--override":
			parsed.override = true
		default:
			return cleanupArgs{}, fmt.Errorf("unknown cleanup option %q", args[index])
		}
	}
	if parsed.worktree == "" {
		return cleanupArgs{}, fmt.Errorf("cleanup requires --worktree")
	}
	return parsed, nil
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

type continueArgs struct {
	context agents.ContextRequest
	role    string
	at      string
	prompt  string
	wake    bool
}

func parseContinueArgs(args []string) (continueArgs, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return continueArgs{}, err
	}
	parsed := continueArgs{context: agents.ContextRequest{FeatureName: contextArgs.feature, WorktreePath: contextArgs.worktree}}
	var positional []string
	for index := 0; index < len(remaining); index++ {
		switch remaining[index] {
		case "--at", "--prompt":
			if index+1 >= len(remaining) || strings.HasPrefix(remaining[index+1], "--") {
				return continueArgs{}, fmt.Errorf("%s requires a value", remaining[index])
			}
			if remaining[index] == "--at" {
				parsed.at = remaining[index+1]
			} else {
				parsed.prompt = remaining[index+1]
			}
			index++
		case "--wake":
			parsed.wake = true
		default:
			if strings.HasPrefix(remaining[index], "--") {
				return continueArgs{}, fmt.Errorf("unknown continue option %q", remaining[index])
			}
			positional = append(positional, remaining[index])
		}
	}
	if len(positional) > 1 {
		return continueArgs{}, fmt.Errorf("continue accepts at most one role")
	}
	if len(positional) == 1 {
		parsed.role = positional[0]
	}
	if parsed.at == "" {
		return continueArgs{}, fmt.Errorf("continue requires --at <RFC3339-or-local-time>")
	}
	return parsed, nil
}

type scheduleArgs struct {
	context agents.ContextRequest
	command string
	id      string
}

func parseScheduleArgs(args []string) (scheduleArgs, error) {
	contextArgs, remaining, err := parseControlContext(args)
	if err != nil {
		return scheduleArgs{}, err
	}
	if len(remaining) == 0 {
		return scheduleArgs{}, fmt.Errorf("schedule requires list, show <schedule-id>, or cancel <schedule-id>")
	}
	parsed := scheduleArgs{context: agents.ContextRequest{FeatureName: contextArgs.feature, WorktreePath: contextArgs.worktree}, command: remaining[0]}
	switch parsed.command {
	case "list":
		if len(remaining) != 1 {
			return scheduleArgs{}, fmt.Errorf("schedule list accepts no additional arguments")
		}
	case "show", "cancel":
		if len(remaining) != 2 {
			return scheduleArgs{}, fmt.Errorf("schedule %s requires one schedule id", parsed.command)
		}
		parsed.id = remaining[1]
	default:
		return scheduleArgs{}, fmt.Errorf("unknown schedule command %q", parsed.command)
	}
	return parsed, nil
}

func parseHandoffArgs(args []string, retry bool) (handoffArgs, error) {
	var parsed handoffArgs
	for len(args) > 0 {
		switch args[0] {
		case "--feature", "--worktree", "--branch", "--kind":
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
			case "--kind":
				if retry {
					return handoffArgs{}, fmt.Errorf("--kind is only available with handoff; retry uses the recorded primary kind")
				}
				parsed.kind = args[1]
			}
			args = args[2:]
		case "--resend":
			if !retry {
				return handoffArgs{}, fmt.Errorf("--resend is only available with retry")
			}
			parsed.resend = true
			args = args[1:]
		case "--no-prompt":
			if retry {
				return handoffArgs{}, fmt.Errorf("--no-prompt is only available with handoff; retry uses the recorded decision")
			}
			parsed.noPrompt = true
			args = args[1:]
		default:
			return handoffArgs{}, fmt.Errorf("unknown handoff option %q", args[0])
		}
	}
	if !retry && (parsed.feature == "" || parsed.worktree == "") {
		return handoffArgs{}, fmt.Errorf("handoff requires --feature and --worktree")
	}
	if parsed.kind != "" && !config.IsSupportedAgentKind(parsed.kind) {
		return handoffArgs{}, fmt.Errorf("--kind %q is not supported by Herdr", parsed.kind)
	}
	if parsed.resend && parsed.noPrompt {
		return handoffArgs{}, fmt.Errorf("--resend and --no-prompt cannot be combined")
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

	return runtimeContext{
		paths:  paths,
		config: cfg,
		herdr:  a.newHerdrClient(opts, lookup),
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

func (a *App) newHerdrClient(opts options, lookup func(string) (string, bool)) *herdr.Client {
	binary := opts.herdrBin
	if binary == "" {
		if value, ok := lookup("HERDR_BIN_PATH"); ok && strings.TrimSpace(value) != "" {
			binary = value
		} else if value, ok := lookup("HERDR_DEVFLOW_HERDR_BIN"); ok && strings.TrimSpace(value) != "" {
			binary = value
		}
	}
	socketPath, _ := lookup("HERDR_SOCKET_PATH")
	return herdr.New(binary, socketPath, a.runner)
}

func (a *App) runtimeRootFor(opts options) (string, error) {
	lookup := a.withOverrides(opts)
	if value, ok := lookup(worktree.HomeOverrideEnv); ok && strings.TrimSpace(value) != "" {
		root, err := filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", worktree.HomeOverrideEnv, err)
		}
		return filepath.Clean(root), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "herdr", "ori-devflow"), nil
}

func (a *App) dispatcherStore(opts options) (*state.Store, error) {
	runtimeRoot, err := a.runtimeRootFor(opts)
	if err != nil {
		return nil, err
	}
	return state.New(filepath.Join(runtimeRoot, "state")), nil
}

func (a *App) installScheduler(ctx context.Context, runtime runtimeContext) (string, error) {
	if a.goos != "darwin" {
		return scheduler.InstallLaunchAgent(ctx, scheduler.LaunchdConfig{GOOS: a.goos})
	}
	if info, err := os.Stat(runtime.paths.HelperPath); err != nil || info.Mode()&0111 == 0 {
		return "", &model.StageError{Stage: "scheduler setup", Code: model.ErrPluginUnavailable, Message: "the stable Ori Devflow helper is not installed or executable", Recovery: "run wt herd setup before scheduling a continuation", Cause: err}
	}
	home, err := a.userHomeDir()
	if err != nil {
		return "", &model.StageError{Stage: "scheduler setup", Code: model.ErrSchedulerUnsupported, Message: "could not resolve the current macOS home directory", Recovery: "run wt herd setup from a logged-in macOS account", Cause: err}
	}
	herdrBinary := runtime.herdr.Binary
	if herdrBinary == herdr.DefaultBinary {
		if path, lookupErr := a.lookPath(herdrBinary); lookupErr == nil {
			herdrBinary = path
		}
	}
	return scheduler.InstallLaunchAgent(ctx, scheduler.LaunchdConfig{
		GOOS:        a.goos,
		HomeDir:     home,
		UID:         a.getuid(),
		HelperPath:  runtime.paths.HelperPath,
		RuntimeRoot: runtime.paths.RuntimeRoot,
		HerdrBinary: herdrBinary,
		Run:         a.launchctlRun,
	})
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
	// Linking/relinking a plugin can happen while Herdr is already running.
	// Reapply display-only state now instead of waiting for a server restart or
	// a later hook; an unavailable live session remains a non-fatal stale view.
	statusService := a.statusService(runtime)
	if snapshot, snapshotErr := statusService.Snapshot(ctx, status.Options{}); snapshotErr == nil {
		a.rehydrateStatus(ctx, runtime, statusService, snapshot)
	} else {
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow warning: status metadata was not refreshed after plugin setup: %v\n", snapshotErr)
	}
	// Plugin actions receive HERDR_SOCKET_PATH from the host. Invoking the
	// installed refresh action is what restores the source-owned Agent view
	// immediately after a relink, rather than waiting for the next restart.
	if _, refreshErr := runtime.herdr.InvokePluginAction(ctx, PluginID+".refresh"); refreshErr != nil {
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow warning: plugin refresh was not started after setup: %v\n", refreshErr)
	}
	schedulerStatus := "unsupported on this platform; one-time continuations require macOS"
	if a.goos == "darwin" {
		plist, schedulerErr := a.installScheduler(ctx, runtime)
		if schedulerErr != nil {
			a.writeError(schedulerErr, opts.json)
			return 1
		}
		schedulerStatus = "LaunchAgent registered: " + plist
	}
	a.recordAudit(runtime, audit.Event{Operation: "setup", Stage: "runtime", Outcome: "ready"})

	wakeStatus, wakeStatusErr := a.wakeLifecycle.Status(ctx)
	wakeView := map[string]any{
		"installed": false,
		"ready":     false,
		"install":   "wt herd wake install",
		"fallback":  "use --stay-awake for an Overnight Run that must not depend on hardware wake",
	}
	warnings := []string{}
	if wakeStatusErr == nil {
		wakeView["installed"] = wakeStatus.Installed
		wakeView["ready"] = wakeStatus.Installed && wakeStatus.Running &&
			wakeStatus.Compatible && wakeStatus.AllowedUID == a.getuid() &&
			!wakeStatus.LastSelfTestAt.IsZero()
		wakeView["detail"] = wakeStatus.Detail
	} else {
		wakeView["detail"] = "wake-service status could not be read"
	}
	if ready, _ := wakeView["ready"].(bool); !ready {
		warnings = append(
			warnings,
			"Standalone wake support is not ready. Run wt herd wake install, or use --stay-awake for an Overnight Run.",
		)
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
		"scheduler":    schedulerStatus,
		"wake_service": wakeView,
		"warnings":     warnings,
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
		PrimaryKind:  parsed.kind,
		Resend:       parsed.resend,
		SkipPrompt:   parsed.noPrompt,
	})
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	a.refreshStatusDisplay(ctx, runtime)
	handoffOutcome := "prompt-delivered"
	if result.PromptSkipped {
		handoffOutcome = "prompt-skipped"
	}
	a.recordAudit(runtime, audit.Event{Operation: "handoff", Feature: result.Feature.Name, Role: result.Primary.Role, Stage: "bootstrap", Outcome: handoffOutcome})
	payload := map[string]any{
		"status":           "ready",
		"feature":          result.Feature.Name,
		"worktree":         result.Feature.Path,
		"workspace_id":     result.WorkspaceID,
		"tab_id":           result.TabID,
		"tab_reused":       result.TabReused,
		"primary_agent":    result.Primary.Name,
		"primary_role":     result.Primary.Role,
		"primary_kind":     result.Primary.Kind,
		"prompt_delivered": result.PromptDelivered,
		"prompt_skipped":   result.PromptSkipped,
	}
	if result.WorkspaceLabel != "" {
		payload["workspace_label"] = result.WorkspaceLabel
	}
	if len(result.Warnings) > 0 {
		payload["warnings"] = result.Warnings
	}
	a.writeResult(opts.json, payload)
	return 0
}

// handoffTarget reports where the next handoff would put a feature's tab. The
// guided start flow shows it in the confirmation summary, so it is read-only
// and always exits 0: not being able to name the workspace is worth a word in
// the summary, never a failed command.
//
// Human output is one tab-separated line rather than the usual result block,
// because its only consumer is a shell that has to split it.
func (a *App) handoffTarget(ctx context.Context, opts options) int {
	report := func(status, workspaceID, label, detail string) int {
		if opts.json {
			payload := map[string]any{"status": status}
			if workspaceID != "" {
				payload["workspace_id"] = workspaceID
			}
			if label != "" {
				payload["workspace_label"] = label
			}
			if detail != "" {
				payload["detail"] = detail
			}
			a.writeResult(true, payload)
			return 0
		}
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", status, workspaceID, label)
		return 0
	}

	runtime, err := a.load(opts)
	if err != nil {
		return report("unavailable", "", "", "the bridge configuration could not be read")
	}
	if !runtime.config.Bridge.Enabled {
		return report("disabled", "", "", "Ori Herdr Devflow is disabled by configuration")
	}
	// Compatibility is deliberately not verified here. A version or schema
	// mismatch is the handoff's problem to report; refusing to name the target
	// would only make the summary less informative before the user has decided
	// anything.
	workspace, err := runtime.herdr.FocusedWorkspace(ctx)
	if err != nil {
		var stageErr *model.StageError
		detail := "Herdr could not be reached"
		if errors.As(err, &stageErr) {
			detail = stageErr.Message
		}
		return report("unavailable", "", "", detail)
	}
	label := workspace.Label
	if label == "" {
		label = workspace.WorkspaceID
	}
	return report("ready", workspace.WorkspaceID, label, "")
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

// loadScheduleControl intentionally does not contact Herdr. Schedule list,
// show, and cancel remain useful when the detached dispatcher or Herdr server
// is unavailable; exact live-agent verification happens only at creation and
// delivery time.
func (a *App) loadScheduleControl(opts options) (*agents.Service, runtimeContext, int) {
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return nil, runtimeContext{}, 1
	}
	if !runtime.config.Bridge.Enabled {
		a.writeResult(opts.json, map[string]any{"status": "disabled", "message": "Ori Herdr Devflow is disabled; no continuation schedule was changed."})
		return nil, runtime, 0
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
	a.refreshStatusDisplay(ctx, runtime)
	addOutcome := "created"
	if result.Reused {
		addOutcome = "reused"
	}
	a.recordAudit(runtime, audit.Event{Operation: "add", Feature: result.Feature.Name, Role: result.Agent.Role, Stage: "role-agent", Outcome: addOutcome})
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
	a.recordAudit(runtime, audit.Event{Operation: "prompt", Feature: result.Feature.Name, Role: result.Agent.Role, Stage: "agent-prompt", Outcome: "delivered"})
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
	a.refreshStatusDisplay(ctx, runtime)
	a.recordAudit(runtime, audit.Event{Operation: "rename", Feature: result.Feature.Name, Role: result.Agent.Role, Stage: "agent-name", Outcome: "renamed"})
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
	a.recordAudit(runtime, audit.Event{Operation: "focus", Feature: feature.Name, Role: agent.Role, Stage: "agent", Outcome: "focused"})
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
	// The event proves an intentional read without accepting or storing any
	// terminal text in the audit record.
	a.recordAudit(runtime, audit.Event{Operation: "read", Feature: result.Feature.Name, Role: result.Agent.Role, Stage: "agent-output", Outcome: "retrieved"})
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
	a.refreshStatusDisplay(ctx, runtime)
	a.recordAudit(runtime, audit.Event{Operation: "rebind", Feature: result.Feature.Name, Role: result.Agent.Role, Stage: "native-session", Outcome: "rebound"})
	if opts.json {
		a.writeResult(true, map[string]any{"status": "ready", "feature": result.Feature.Name, "agent": result.Agent, "rebound": true})
	} else {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: rebound %s to %s in %s\n", result.Agent.Role, result.Agent.Name, result.Feature.Name)
	}
	return 0
}

func (a *App) continueAgent(ctx context.Context, opts options, args []string) int {
	parsed, err := parseContinueArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadAgentControl(ctx, opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&parsed.context, runtime)
	target, err := service.ResolveScheduleTarget(ctx, parsed.context, parsed.role)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	dueAt, timezone, err := scheduler.ParseDueAt(parsed.at, time.Now(), time.Local)
	if err != nil {
		a.writeError(&model.StageError{Stage: "schedule time", Code: model.ErrScheduleInvalid, Message: err.Error(), Recovery: "use --at 2026-07-24T09:30:00-04:00 or --at '2026-07-24 09:30'"}, opts.json)
		return 2
	}
	prompt := parsed.prompt
	promptSummary := "default planning-aware continuation prompt"
	if prompt == "" {
		prompt = agents.ContinuationPrompt(target.Feature, target.Agent.Role)
	} else {
		promptSummary = fmt.Sprintf("custom continuation prompt (%d characters)", len([]rune(prompt)))
	}
	preview := map[string]any{
		"feature":        target.Feature.Name,
		"worktree":       target.Feature.Path,
		"role":           target.Agent.Role,
		"agent":          target.Agent.Name,
		"agent_kind":     target.Agent.Kind,
		"due_at":         dueAt.Format(time.RFC3339),
		"timezone":       timezone,
		"retry_until":    dueAt.Add(runtime.config.RetryWindow()).Format(time.RFC3339),
		"prompt_summary": promptSummary,
		"wake_required":  parsed.wake,
	}
	if !opts.json {
		fmt.Fprintln(a.stdout, "Continuation preview (not saved yet):")
		fmt.Fprintf(a.stdout, "  Feature: %s\n  Worktree: %s\n  Role: %s\n  Agent: %s (%s)\n  Due: %s (%s)\n  Retry until: %s\n  Prompt: %s\n", target.Feature.Name, target.Feature.Path, target.Agent.Role, target.Agent.Name, target.Agent.Kind, dueAt.Format(time.RFC3339), timezone, dueAt.Add(runtime.config.RetryWindow()).Format(time.RFC3339), promptSummary)
		if parsed.wake {
			fmt.Fprintln(a.stdout, "  Wake: required; the standalone Herdr Wake Service must directly verify its macOS event")
		}
	}
	// Register before saving so unsupported platforms cannot leave behind a
	// continuation that nothing is capable of dispatching. Registration is
	// idempotent and always uses the stable installed helper path.
	if _, err := a.installScheduler(ctx, runtime); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	var wake WakeCoordinator
	if parsed.wake {
		wake, err = a.newContinuationWake()
		if err != nil {
			a.writeError(continuationWakeError("standalone Herdr wake service is unavailable", err), opts.json)
			return 1
		}
		readiness := wake.Owner()
		if !readiness.Ready {
			a.writeError(continuationWakeError(readiness.Detail, nil), opts.json)
			return 1
		}
	}
	scheduleService := &scheduler.Service{Store: state.New(runtime.paths.StateDir)}
	record, err := scheduleService.Create(ctx, scheduler.CreateRequest{
		Feature:      target.Feature,
		Agent:        target.Agent,
		DueAt:        dueAt,
		Timezone:     timezone,
		Prompt:       prompt,
		RetryWindow:  runtime.config.RetryWindow(),
		WakeRequired: parsed.wake,
	})
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if parsed.wake {
		ref := scheduler.ScheduleRef{RepositoryID: target.Feature.RepositoryID, FeatureName: target.Feature.Name}
		registeredAt := time.Time{}
		failWake := func(message string, cause error, evidence wakeclient.Evidence) int {
			rollbackAt := time.Now().UTC()
			rollback, cancelErr := wake.CancelCandidate(ctx, record.ID)
			rollbackProven := cancelErr == nil || wakeNotFound(cancelErr)
			uncertain := errors.Is(cause, wakeclient.ErrUncertain) || !rollbackProven
			rollbackResult := string(rollback.Result)
			rollbackDetail := rollback.Message
			rollbackVerifiedAt := time.Time{}
			if rollbackProven {
				rollbackVerifiedAt = time.Now().UTC()
				if rollbackResult == "" {
					rollbackResult = string(wakeprotocol.ResultSuccess)
				}
			} else {
				if rollbackResult == "" {
					rollbackResult = string(wakeprotocol.ResultUncertain)
				}
				if rollbackDetail == "" && cancelErr != nil {
					rollbackDetail = "exact standalone wake cancellation was not proven"
				}
			}
			result := string(evidence.Result)
			code := string(evidence.Code)
			if result == "" {
				if uncertain {
					result = string(wakeprotocol.ResultUncertain)
					code = string(wakeprotocol.CodeUncertain)
				} else {
					result = string(wakeprotocol.ResultRefusal)
					code = string(wakeprotocol.CodeVerificationFailed)
				}
			}
			_, markErr := scheduleService.RecordWakeEvidence(ctx, ref, record.ID, scheduler.WakeEvidenceUpdate{
				RegisteredAt:        registeredAt,
				ProgrammedAt:        evidence.ProgrammedAt,
				VerifiedAt:          evidence.VerifiedAt,
				ProtocolVersion:     evidence.ProtocolVersion,
				DaemonBuild:         evidence.DaemonBuild,
				HelperBuild:         evidence.HelperBuild,
				Result:              result,
				Code:                code,
				Failure:             message,
				Uncertain:           uncertain,
				RollbackAttemptedAt: rollbackAt,
				RollbackVerifiedAt:  rollbackVerifiedAt,
				RollbackResult:      rollbackResult,
				RollbackDetail:      rollbackDetail,
			})
			if markErr != nil {
				a.writeError(markErr, opts.json)
			} else {
				a.writeError(continuationWakeError(message, cause), opts.json)
			}
			return 1
		}
		detail := fmt.Sprintf("Herdr continuation for %s:%s", target.Feature.Name, target.Agent.Role)
		registered, registerErr := wake.RegisterCandidate(ctx, record.ID, record.DueAt, detail)
		if registerErr != nil {
			return failWake("standalone wake service did not accept the continuation wake", registerErr, registered)
		}
		registeredAt = time.Now().UTC()
		verified, verifyErr := wake.VerifyCandidate(ctx, record.ID, record.DueAt)
		if verifyErr != nil {
			return failWake("standalone wake service did not directly verify the continuation wake", verifyErr, verified)
		}
		updated, wakeResultErr := scheduleService.RecordWakeEvidence(ctx, ref, record.ID, scheduler.WakeEvidenceUpdate{
			RegisteredAt:    registeredAt,
			ProgrammedAt:    verified.ProgrammedAt,
			VerifiedAt:      verified.VerifiedAt,
			ProtocolVersion: verified.ProtocolVersion,
			DaemonBuild:     verified.DaemonBuild,
			HelperBuild:     verified.HelperBuild,
			Result:          string(verified.Result),
			Code:            string(verified.Code),
		})
		if wakeResultErr != nil {
			_, _ = wake.CancelCandidate(ctx, record.ID)
			a.writeError(wakeResultErr, opts.json)
			return 1
		}
		record = updated
	}
	a.refreshStatusDisplay(ctx, runtime)
	a.recordAudit(runtime, audit.Event{Operation: "continue", Feature: target.Feature.Name, Role: target.Agent.Role, Stage: "schedule", Outcome: "scheduled"})
	if opts.json {
		a.writeResult(true, map[string]any{"status": "scheduled", "preview": preview, "schedule": scheduleView(target.Feature, record)})
	} else {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow: scheduled %s for %s at %s (%s)\n", record.ID, target.Agent.Name, record.DueAt.Format(time.RFC3339), record.Timezone)
		if record.WakeRequired {
			fmt.Fprintf(a.stdout, "Ori Herdr Devflow: standalone macOS wake verified for %s\n", record.WakeProgrammedAt.Format(time.RFC3339))
		}
	}
	return 0
}

func continuationWakeError(message string, cause error) *model.StageError {
	if strings.TrimSpace(message) == "" {
		message = "macOS wake scheduling is not ready"
	}
	return &model.StageError{
		Stage:    "schedule wake",
		Code:     model.ErrWakeUnavailable,
		Message:  message,
		Recovery: "run wt herd wake doctor, repair with wt herd wake install if needed, then inspect or cancel this continuation",
		Cause:    cause,
	}
}

func wakeNotFound(err error) bool {
	var operationError *wakeclient.OperationError
	return errors.As(err, &operationError) && operationError.Code == wakeprotocol.CodeNotFound
}

func (a *App) withdrawContinuationWake(
	ctx context.Context,
	record model.Schedule,
) (wakeclient.Evidence, error) {
	if !record.WakeRequired {
		return wakeclient.Evidence{}, nil
	}
	wake, err := a.newContinuationWake()
	if err != nil {
		return wakeclient.Evidence{}, err
	}
	id := record.WakeCandidateID
	if id == "" {
		id = record.ID
	}
	return wake.CancelCandidate(ctx, id)
}

func (a *App) schedule(ctx context.Context, opts options, args []string) int {
	parsed, err := parseScheduleArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	service, runtime, exit := a.loadScheduleControl(opts)
	if service == nil {
		return exit
	}
	defaultControlContext(&parsed.context, runtime)
	feature, err := service.ResolveScheduleFeature(ctx, parsed.context)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	scheduleService := &scheduler.Service{Store: state.New(runtime.paths.StateDir)}
	ref := scheduler.ScheduleRef{RepositoryID: feature.RepositoryID, FeatureName: feature.Name}
	switch parsed.command {
	case "list":
		records, listErr := scheduleService.List(ctx, ref)
		if listErr != nil {
			a.writeError(listErr, opts.json)
			return 1
		}
		views := make([]map[string]any, 0, len(records))
		for _, record := range records {
			views = append(views, scheduleView(feature, record))
		}
		if opts.json {
			a.writeResult(true, map[string]any{"status": "ready", "feature": feature.Name, "schedules": views})
		} else if len(views) == 0 {
			fmt.Fprintf(a.stdout, "Ori Herdr Devflow: no continuation schedules for %s\n", feature.Name)
		} else {
			fmt.Fprintf(a.stdout, "Continuation schedules for %s:\n", feature.Name)
			for _, view := range views {
				fmt.Fprintf(a.stdout, "  %s  %-10s %s (%s)  due %s  attempts %d\n", view["id"], view["state"], view["role"], view["agent"], view["due_at"], view["attempts"])
			}
		}
		return 0
	case "show":
		record, showErr := scheduleService.Show(ctx, ref, parsed.id)
		if showErr != nil {
			a.writeError(showErr, opts.json)
			return 1
		}
		view := scheduleView(feature, record)
		if opts.json {
			a.writeResult(true, map[string]any{"status": "ready", "schedule": view})
		} else {
			encoded, _ := json.MarshalIndent(view, "", "  ")
			fmt.Fprintln(a.stdout, string(encoded))
		}
		return 0
	case "cancel":
		record, cancelErr := scheduleService.Cancel(ctx, ref, parsed.id)
		if cancelErr != nil {
			a.writeError(cancelErr, opts.json)
			return 1
		}
		var wakeErr error
		if record.WakeRequired {
			withdrawal, withdrawalErr := a.withdrawContinuationWake(ctx, record)
			wakeErr = withdrawalErr
			withdrawnAt := time.Time{}
			if wakeErr == nil {
				withdrawnAt = time.Now().UTC()
			}
			if _, recordErr := scheduleService.RecordWakeWithdrawal(ctx, ref, record.ID, time.Now().UTC(), withdrawnAt, string(withdrawal.Result), withdrawal.Message, wakeErr != nil); recordErr != nil {
				a.writeError(recordErr, opts.json)
				return 1
			}
		}
		a.refreshStatusDisplay(ctx, runtime)
		a.recordAudit(runtime, audit.Event{Operation: "schedule", Feature: feature.Name, Role: record.Role, Stage: "cancel", Outcome: "canceled"})
		if wakeErr != nil {
			a.writeError(&model.StageError{
				Stage:    "schedule cancel",
				Code:     model.ErrWakeUnavailable,
				Message:  "the continuation prompt was canceled, but its macOS wake was not confirmed withdrawn",
				Recovery: "run wt herd wake doctor, then inspect this continuation with wt herd schedule show " + record.ID + " before allowing this Mac to sleep",
				Cause:    wakeErr,
			}, opts.json)
			return 1
		}
		if opts.json {
			a.writeResult(true, map[string]any{"status": "canceled", "schedule": scheduleView(feature, record)})
		} else {
			fmt.Fprintf(a.stdout, "Ori Herdr Devflow: canceled continuation %s for %s\n", record.ID, feature.Name)
		}
		return 0
	default:
		a.writeError(fmt.Errorf("unknown schedule command %q", parsed.command), opts.json)
		return 2
	}
}

// status lists only agents Herdr reports as open right now. Repository plans,
// saved bridge records, Git state, and GitHub delivery state belong to
// `wt status`; coupling them here made a live-agent check both noisy and stale.
func (a *App) status(ctx context.Context, opts options, args []string) int {
	parsed, err := parseStatusArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	if parsed.json {
		opts.json = true
	}
	if parsed.watch && opts.json {
		a.writeError(fmt.Errorf("status --watch cannot be combined with --json"), opts.json)
		return 2
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}

	// Clearing the source-scoped Herdr view remains the one explicit mutation
	// on this command. A normal roster read never refreshes metadata or bridge
	// state as a side effect.
	if parsed.clearView {
		metadata := a.statusService(runtime)
		if err := metadata.ClearManagedView(ctx); err != nil {
			a.writeError(err, opts.json)
			return 1
		}
		a.writeResult(opts.json, map[string]any{"status": "view_cleared", "message": "Cleared the source-scoped Ori Devflow Herdr view."})
		return 0
	}

	include, err := statusAgentFilter(ctx, runtime, parsed)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if parsed.watch {
		return a.watchLiveAgentRoster(ctx, runtime.herdr, include, runtime.config.WatchPollInterval(), parsed.noColor)
	}

	roster, err := collectLiveAgentRoster(ctx, runtime.herdr, include)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if err := a.writeLiveAgentRoster(roster, opts.json); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	return 0
}

// selectorFunc resolves a selector against one collected snapshot. Watch
// re-resolves per snapshot because a worktree can appear or be removed while
// the board is open.
type selectorFunc func(overview.Snapshot) (overview.Selector, error)

// featureSelector narrows to one exact slug, failing when the repository has no
// such feature. An explicit `--feature` is a claim about a name, so a name that
// does not exist stays an error.
func featureSelector(slug string) selectorFunc {
	if strings.TrimSpace(slug) == "" {
		return func(overview.Snapshot) (overview.Selector, error) { return overview.SelectAll(), nil }
	}
	return func(snapshot overview.Snapshot) (overview.Selector, error) {
		if _, ok := snapshot.Feature(slug); !ok {
			return overview.Selector{}, fmt.Errorf("no feature named %q was found", slug)
		}
		return overview.SelectFeature(slug), nil
	}
}

type overviewArgs struct {
	feature string
	json    bool
	noColor bool
	watch   bool
}

func parseOverviewArgs(args []string) (overviewArgs, error) {
	var parsed overviewArgs
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			parsed.json = true
		case "--no-color":
			parsed.noColor = true
		case "--watch":
			parsed.watch = true
		case "--feature":
			if index+1 >= len(args) {
				return overviewArgs{}, fmt.Errorf("--feature requires a value")
			}
			index++
			parsed.feature = args[index]
		default:
			return overviewArgs{}, fmt.Errorf("unknown overview option %q", args[index])
		}
	}
	if parsed.feature != "" && !planning.ValidSlug(parsed.feature) {
		return overviewArgs{}, fmt.Errorf("--feature must be a canonical feature slug")
	}
	return parsed, nil
}

// overview renders the feature-first snapshot behind `wt status`. It is
// intentionally separate from the live roster behind `wt herd status` and
// `wt herd overview`. It is read-only: no
// planning file, backlog entry, Git object, bridge binding, or Herdr view is
// written by this command.
//
// The snapshot deliberately exits nonzero while any required source is
// unavailable. A board that cannot see remote delivery state must not report
// success, because a green exit code is how scripts decide nothing is wrong.
func (a *App) overview(ctx context.Context, opts options, args []string) int {
	parsed, err := parseOverviewArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	if parsed.json {
		opts.json = true
	}
	if parsed.watch && opts.json {
		a.writeError(fmt.Errorf("overview --watch cannot be combined with --json"), opts.json)
		return 2
	}
	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), opts.json)
		return 1
	}

	selectFor := featureSelector(parsed.feature)
	service := a.overviewService(runtime)
	if parsed.watch {
		return a.overviewWatch(ctx, service, selectFor, runtime.config.WatchPollInterval(), parsed.noColor, false)
	}
	snapshot, err := service.Collect(ctx)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	selector, err := selectFor(snapshot)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if err := a.renderOverview(snapshot, false, selector, opts.json, parsed.noColor); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	if !snapshot.Complete {
		return 1
	}
	return 0
}

// overviewService builds the collector used by `wt status` and the Herdr board.
func (a *App) overviewService(runtime runtimeContext) *overview.Service {
	return overview.NewService(overview.Config{
		RepoRoot: runtime.paths.RepoRoot,
		Remote: github.New(github.Options{
			Dir:            runtime.paths.RepoRoot,
			Timeout:        runtime.config.GitHubTimeout(),
			CandidateLimit: runtime.config.Status.GitHubCandidateLimit,
		}),
		RemoteRefreshInterval: runtime.config.GitHubRefreshInterval(),
		Agents:                runtime.herdr,
		Bridge:                state.New(runtime.paths.StateDir),
		ClaudeReadiness:       claudeReadiness(runtime.paths.UsageDir),
		RunMembership:         overnightMembership(runtime.paths.StateDir, runtime.paths.RepositoryID),
	})
}

// overnightMembership tells the shared snapshot which agents an Overnight Run
// has enrolled, so every surface that lists agents says the same thing about
// them. It reads the same durable records the overnight commands do — there is
// no second view of a run to disagree with the first.
func overnightMembership(stateDir, repositoryID string) overview.RunMembershipFunc {
	if stateDir == "" {
		return nil
	}
	service := &overnight.Service{Store: state.New(stateDir)}
	return func() map[string]overview.RunMembership {
		run, found, err := service.Active(repositoryID)
		if err != nil || !found {
			return nil
		}
		membership := map[string]overview.RunMembership{}
		for _, participant := range run.Participants {
			if participant.Binding.NativeSession.Value == "" {
				continue
			}
			membership[participant.Binding.NativeSession.Value] = overview.RunMembership{
				RunID:         run.ID,
				State:         string(participant.State),
				QueuePosition: participant.Position,
				Active:        participant.ID == run.ActiveParticipant,
			}
		}
		return membership
	}
}

// claudeReadiness adapts the Claude usage adapter to the snapshot's narrow
// question. It reads only records the Claude-side recorder already persisted:
// no Claude process is contacted, and nothing is spent, to answer it.
func claudeReadiness(usageDir string) overview.ClaudeReadinessFunc {
	if usageDir == "" {
		return nil
	}
	adapter := claudeusage.NewAdapter(usageDir)
	return func(sessionID string) overview.ClaudeReadinessReport {
		readiness := adapter.Readiness(sessionID, time.Now())
		return overview.ClaudeReadinessReport{
			Ready:    readiness.Ready,
			Reason:   readiness.Reason,
			AuthMode: string(readiness.AuthMode),
		}
	}
}

// renderOverview writes one snapshot to the selected surface. Narrowing to a
// feature renders its detail report; standing in a checkout that implements no
// feature renders the repository's active work plus every unscoped agent. JSON
// always emits the normalized snapshot, narrowed the same way the human view is.
func (a *App) renderOverview(snapshot overview.Snapshot, expanded bool, selector overview.Selector, jsonOutput, noColor bool) error {
	options := overview.RenderOptions{NoColor: !a.statusColorEnabled(noColor)}
	narrowed := snapshot.Narrow(selector)
	if selector.Kind == overview.SelectorFeature {
		found, ok := narrowed.Feature(selector.Feature)
		if !ok {
			return fmt.Errorf("no feature named %q was found", selector.Feature)
		}
		if jsonOutput {
			a.writeResult(true, narrowed)
			return nil
		}
		return overview.RenderDetail(a.stdout, narrowed, found, options)
	}
	if jsonOutput {
		a.writeResult(true, narrowed)
		return nil
	}
	if expanded {
		return overview.RenderExpanded(a.stdout, narrowed, options)
	}
	return overview.RenderCompact(a.stdout, narrowed, options)
}

// overviewWatch renders the board on the fast local clock. The remote query is
// separately rate limited inside the service, so a board left open all day
// re-reads local files often and GitHub rarely.
func (a *App) overviewWatch(ctx context.Context, service *overview.Service, selectFor selectorFunc, interval time.Duration, noColor, expanded bool) int {
	colorEnabled := a.statusColorEnabled(noColor)
	rendered := false
	emit := func(snapshot overview.Snapshot) {
		if rendered && colorEnabled {
			// We never enter raw or alternate-screen mode, so Ctrl-C leaves a
			// normal terminal showing the last complete board.
			fmt.Fprint(a.stdout, "\x1b[2J\x1b[H")
		}
		rendered = true
		// The selector is re-resolved per snapshot: a worktree can be created
		// or removed while the board is open.
		selector, err := selectFor(snapshot)
		if err != nil {
			fmt.Fprintln(a.stdout, err.Error())
			return
		}
		if err := a.renderOverview(snapshot, expanded, selector, false, noColor); err != nil {
			fmt.Fprintln(a.stdout, err.Error())
		}
	}
	if err := service.Watch(ctx, interval, emit); err != nil {
		a.writeError(err, false)
		return 1
	}
	return 0
}

// cleanup is intentionally narrow and is called by wt done before it makes
// any archival, backlog, dirty-check, or Git removal changes. It never asks
// Herdr to create or remove a Git worktree.
func (a *App) cleanup(ctx context.Context, opts options, args []string) int {
	parsed, err := parseCleanupArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	runtime, err := a.load(opts)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			a.writeCleanupResult(opts.json, cleanup.Result{Outcome: cleanup.OutcomeSkipped, Detail: "no checked-in Herdr Devflow configuration exists for this worktree"})
			return 0
		}
		result := cleanup.Result{Outcome: cleanup.OutcomeUnavailable, Detail: "bridge configuration cannot be read, so Herdr cleanup safety cannot be verified"}
		if parsed.override {
			result.Outcome = cleanup.OutcomeOverridden
			result.Overridden = true
			result.Detail = "explicit Herdr-safety override accepted: bridge configuration cannot be read; the Git worktree may be orphaned from live Herdr state"
			if runtimeRoot, rootErr := a.runtimeRootFor(opts); rootErr == nil {
				a.recordCleanupAuditAt(filepath.Join(runtimeRoot, "logs"), result)
			}
		}
		a.writeCleanupResult(opts.json, result)
		if result.Outcome == cleanup.OutcomeOverridden {
			return 0
		}
		return cleanup.ExitNeedsOverride
	}
	if !runtime.config.Bridge.Enabled {
		a.writeCleanupResult(opts.json, cleanup.Result{Outcome: cleanup.OutcomeSkipped, Detail: "Herdr Devflow is disabled for this worktree"})
		return 0
	}
	service := cleanup.Service{
		Store:        state.New(runtime.paths.StateDir),
		Client:       runtime.herdr,
		RepositoryID: runtime.paths.RepositoryID,
		GitCommonDir: runtime.paths.GitCommonDir,
		HerdrTimeout: a.cleanupTimeout,
	}
	result := service.Preflight(ctx, cleanup.Request{WorktreePath: parsed.worktree, Override: parsed.override})
	a.recordCleanupAudit(runtime, result)
	a.writeCleanupResult(opts.json, result)
	switch result.Outcome {
	case cleanup.OutcomeReady, cleanup.OutcomeSkipped, cleanup.OutcomeOverridden:
		return 0
	case cleanup.OutcomeBlocked:
		return cleanup.ExitBlocked
	default:
		return cleanup.ExitNeedsOverride
	}
}

func (a *App) recordAudit(runtime runtimeContext, event audit.Event) {
	a.recordAuditAt(runtime.paths.LogDir, event)
}

func (a *App) recordAuditAt(logDir string, event audit.Event) {
	if err := (audit.Logger{Dir: logDir}).Record(audit.Event{
		Operation: event.Operation,
		Feature:   event.Feature,
		Role:      event.Role,
		Stage:     event.Stage,
		Outcome:   event.Outcome,
		Warning:   event.Warning,
	}); err != nil {
		fmt.Fprintln(a.stderr, "Ori Herdr Devflow warning: could not record the local audit event")
	}
}

func (a *App) recordCleanupAudit(runtime runtimeContext, result cleanup.Result) {
	a.recordCleanupAuditAt(runtime.paths.LogDir, result)
}

func (a *App) recordCleanupAuditAt(logDir string, result cleanup.Result) {
	warning := ""
	if result.Overridden {
		warning = "orphan-risk"
	}
	a.recordAuditAt(logDir, audit.Event{
		Operation: "cleanup",
		Feature:   result.Feature.Name,
		Stage:     "workspace-close",
		Outcome:   string(result.Outcome),
		Warning:   warning,
	})
}

func (a *App) writeCleanupResult(asJSON bool, result cleanup.Result) {
	if asJSON {
		a.writeResult(true, result)
		return
	}
	switch result.Outcome {
	case cleanup.OutcomeReady:
		fmt.Fprintln(a.stdout, "Ori Herdr Devflow cleanup: safe to continue.")
	case cleanup.OutcomeSkipped:
		fmt.Fprintln(a.stdout, "Ori Herdr Devflow cleanup: no managed Herdr state requires cleanup.")
	case cleanup.OutcomeBlocked:
		fmt.Fprintln(a.stdout, "Ori Herdr Devflow cleanup: refused because managed work is still active.")
	case cleanup.OutcomeOverridden:
		fmt.Fprintln(a.stdout, "WARNING: explicit Herdr-safety override accepted; continuing may orphan live Herdr state from this Git worktree.")
	default:
		fmt.Fprintln(a.stdout, "Ori Herdr Devflow cleanup: Herdr safety could not be verified; preserving the Git worktree.")
	}
	if result.Feature.Name != "" {
		fmt.Fprintf(a.stdout, "  Feature: %s\n", result.Feature.Name)
	}
	if result.WorkspaceID != "" {
		fmt.Fprintf(a.stdout, "  Herdr workspace: %s\n", result.WorkspaceID)
	}
	if result.TabID != "" {
		fmt.Fprintf(a.stdout, "  Herdr tab: %s\n", result.TabID)
	}
	if result.Detail != "" {
		fmt.Fprintf(a.stdout, "  Detail: %s\n", result.Detail)
	}
	for _, agent := range result.Agents {
		fmt.Fprintf(a.stdout, "  Agent %s (%s): %s\n", agent.Role, agent.Name, agent.Status)
		fmt.Fprintf(a.stdout, "    Focus: %s\n    Read: %s\n", agent.FocusCommand, agent.ReadCommand)
	}
	for _, schedule := range result.Schedules {
		fmt.Fprintf(a.stdout, "  Schedule %s: %s\n    Show: %s\n", schedule.ID, schedule.State, schedule.ShowCommand)
		if schedule.CancelCommand != "" {
			fmt.Fprintf(a.stdout, "    Cancel: %s\n", schedule.CancelCommand)
		}
	}
}

func (a *App) statusService(runtime runtimeContext) *status.Service {
	service := &status.Service{
		Store:             state.New(runtime.paths.StateDir),
		Client:            runtime.herdr,
		SourceID:          runtime.config.Bridge.SourceID,
		ViewSource:        status.PluginViewSource,
		WatchPollInterval: runtime.config.WatchPollInterval(),
	}
	if runtime.herdr.SocketPath != "" {
		service.Subscribe = func(ctx context.Context, subscriptions []map[string]any) (status.EventStream, error) {
			stream, err := runtime.herdr.Subscribe(ctx, subscriptions)
			if err != nil {
				return nil, err
			}
			return stream, nil
		}
	}
	return service
}

func (a *App) rehydrateStatus(ctx context.Context, runtime runtimeContext, service *status.Service, snapshot status.Snapshot) {
	if runtime.config.Metadata.Enabled {
		if err := service.RehydrateMetadata(ctx, snapshot); err != nil {
			fmt.Fprintf(a.stderr, "Ori Herdr Devflow warning: status metadata was not refreshed: %v\n", err)
		}
	}
	// The managed Herdr agent view is no longer auto-applied here: it used to
	// silently filter Herdr's Agents panel down to devflow-tracked panes on
	// every status/setup/plugin refresh. `wt herd status --clear-view` still
	// clears a view left over from before this change or applied manually.
}

// refreshStatusDisplay is best-effort after a command changes managed local
// state. It keeps schedule/task tokens current without turning an otherwise
// successful agent or scheduler operation into a failure.
func (a *App) refreshStatusDisplay(ctx context.Context, runtime runtimeContext) {
	service := a.statusService(runtime)
	snapshot, err := service.Snapshot(ctx, status.Options{})
	if err != nil {
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow warning: status display was not refreshed: %v\n", err)
		return
	}
	a.rehydrateStatus(ctx, runtime, service, snapshot)
}

func (a *App) writeStatusSnapshot(asJSON, noColor bool, snapshot status.Snapshot) {
	if asJSON {
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			fmt.Fprintln(a.stderr, "Ori Herdr Devflow warning: could not encode status snapshot")
			return
		}
		fmt.Fprintln(a.stdout, string(encoded))
		return
	}
	fmt.Fprint(a.stdout, status.RenderHuman(snapshot, status.RenderOptions{Color: a.statusColorEnabled(noColor)}))
}

func (a *App) statusColorEnabled(noColor bool) bool {
	if noColor {
		return false
	}
	if _, disabled := a.lookupEnv("NO_COLOR"); disabled {
		return false
	}
	return a.stdoutIsTerminal()
}

func (a *App) stdoutIsTerminal() bool {
	file, ok := a.stdout.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// dispatch is intentionally detached from repository/config resolution. It
// reads only the user-local state root supplied by launchd and never attempts
// to create workspaces, panes, agents, or sessions.
func (a *App) dispatch(ctx context.Context, opts options) int {
	store, err := a.dispatcherStore(opts)
	if err != nil {
		a.writeError(&model.StageError{Stage: "schedule dispatch", Code: model.ErrStateCorrupt, Message: "could not resolve the user-local scheduler state", Recovery: "run wt herd doctor from the Ori repository", Cause: err}, opts.json)
		return 1
	}
	service := &scheduler.Service{Store: store, Client: a.newHerdrClient(opts, a.withOverrides(opts))}
	results, err := service.DispatchDue(ctx)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	for _, result := range results {
		if !result.Schedule.WakeRequired {
			continue
		}
		switch result.Schedule.State {
		case model.ScheduleDelivered, model.ScheduleFailed, model.ScheduleUncertain, model.ScheduleCanceled:
			withdrawal, wakeErr := a.withdrawContinuationWake(ctx, result.Schedule)
			withdrawnAt := time.Time{}
			if wakeErr == nil {
				withdrawnAt = time.Now().UTC()
			}
			ref := scheduler.ScheduleRef{RepositoryID: result.Feature.RepositoryID, FeatureName: result.Feature.Name}
			if _, recordErr := service.RecordWakeWithdrawal(ctx, ref, result.Schedule.ID, time.Now().UTC(), withdrawnAt, string(withdrawal.Result), withdrawal.Message, wakeErr != nil); recordErr != nil {
				fmt.Fprintf(a.stderr, "Ori Herdr Devflow warning: could not persist wake withdrawal evidence for %s\n", result.Schedule.ID)
			}
			if wakeErr != nil {
				fmt.Fprintf(a.stderr, "Ori Herdr Devflow warning: continuation wake %s was not confirmed withdrawn\n", result.Schedule.ID)
			}
		}
	}
	// The same detached dispatcher advances Overnight Runs. Giving runs their
	// own daemon would mean two processes each believing they owned the queue
	// and the system wake; there is one dispatcher, and it does both jobs.
	supervised := a.dispatchOvernight(ctx, opts, store)

	views := make([]map[string]any, 0, len(results))
	for _, result := range results {
		runtimeRoot, rootErr := a.runtimeRootFor(opts)
		if rootErr == nil {
			a.recordAuditAt(filepath.Join(runtimeRoot, "logs"), audit.Event{
				Operation: "dispatch",
				Feature:   result.Feature.Name,
				Role:      result.Schedule.Role,
				Stage:     "delivery",
				Outcome:   string(result.Schedule.State),
			})
		}
		views = append(views, scheduleView(result.Feature, result.Schedule))
	}
	if opts.json {
		a.writeResult(true, map[string]any{
			"status": "ok", "processed": len(views), "schedules": views, "overnight": supervised,
		})
	} else if len(views) > 0 || len(supervised) > 0 {
		fmt.Fprintf(a.stdout, "Ori Herdr Devflow dispatcher: processed %d schedule(s), %d Overnight Run(s)\n",
			len(views), len(supervised))
	} else {
		fmt.Fprintln(a.stdout, "Ori Herdr Devflow dispatcher: no due schedules")
	}
	return 0
}

// dispatchOvernight advances every non-terminal Overnight Run by one step.
//
// It is best-effort and never fails the dispatcher: a run that cannot be
// advanced right now is a run that stays where it is, and a scheduler outage
// must not also stop one-time continuations from being delivered.
func (a *App) dispatchOvernight(ctx context.Context, opts options, store *state.Store) []map[string]any {
	runtimeRoot, err := a.runtimeRootFor(opts)
	if err != nil {
		return nil
	}
	// The dispatcher runs detached, without a repository checkout, so the
	// Overnight defaults come from the built-in configuration rather than a
	// project file it cannot resolve.
	runtime := runtimeContext{config: config.Default()}
	service := &overnight.Service{Store: store}
	runs, err := service.List("")
	if err != nil {
		return nil
	}
	client := a.newHerdrClient(opts, a.withOverrides(opts))
	supervisor := &overnight.Supervisor{
		Store:  store,
		Agents: client,
		Prompt: client,
		Usage:  claudeusage.NewAdapter(filepath.Join(runtimeRoot, "usage")),
		Power:  &systempower.Service{GOOS: a.goos},
		Git:    worktree.GitRunner,
	}
	// Without a reachable standalone wake service the supervisor still runs; it simply
	// can never sleep, which is the correct degraded behavior rather than a
	// reason to stop supervising.
	approvalGranted := false
	if wake, err := a.newOvernightWake(wakeprotocol.PurposeClaudeReset); err == nil {
		supervisor.Wake = overnightWakeAdapter{wake}
		// Readiness comes from the independently installed standalone daemon;
		// the dispatcher never grants this capability to itself.
		approvalGranted = wake.Owner().Ready
	}
	if wake, err := a.newOvernightWake(wakeprotocol.PurposeOvernightStart); err == nil {
		supervisor.StartWake = overnightWakeAdapter{wake}
	}

	advanced := make([]map[string]any, 0)
	for _, run := range runs {
		if run.State.Terminal() {
			continue
		}
		updated, err := a.advanceOvernightRun(ctx, supervisor, run.ID, runtime.config, approvalGranted)
		if err != nil {
			fmt.Fprintf(a.stderr, "Ori Herdr Devflow warning: Overnight Run %s was not advanced\n", run.ID)
			continue
		}
		a.recordAuditAt(filepath.Join(runtimeRoot, "logs"), auditOvernightEvent(updated, "dispatch"))
		advanced = append(advanced, map[string]any{
			"id": updated.ID, "state": string(updated.State), "active": updated.ActiveParticipant,
		})
	}
	return advanced
}

func scheduleView(feature model.Feature, record model.Schedule) map[string]any {
	return map[string]any{
		"id":                   record.ID,
		"feature":              feature.Name,
		"worktree":             record.FeaturePath,
		"role":                 record.Role,
		"agent":                record.AgentName,
		"agent_kind":           record.AgentKind,
		"workspace_id":         record.WorkspaceID,
		"pane_id":              record.PaneID,
		"terminal_id":          record.TerminalID,
		"native_session_bound": record.NativeSession.Value != "",
		"due_at":               record.DueAt.Format(time.RFC3339),
		"timezone":             record.Timezone,
		"retry_until":          record.RetryUntil.Format(time.RFC3339),
		"state":                record.State,
		"attempts":             record.Attempts,
		"last_checked_at":      formatOptionalTime(record.LastCheckedAt),
		"last_attempt_at":      formatOptionalTime(record.LastAttemptAt),
		"delivered_at":         formatOptionalTime(record.DeliveredAt),
		"failure_reason":       record.FailureReason,
		"recovery":             record.RecoveryCommand,
		"wake_required":        record.WakeRequired,
		"wake_candidate_id":    record.WakeCandidateID,
		"wake_source":          record.WakeSource,
		"wake_purpose":         record.WakePurpose,
		"wake_requested_at":    formatOptionalTime(record.WakeRequestedAt),
		"wake_registered_at":   formatOptionalTime(record.WakeRegisteredAt),
		"wake_programmed_at":   formatOptionalTime(record.WakeProgrammedAt),
		"wake_verified_at":     formatOptionalTime(record.WakeVerifiedAt),
		"wake_protocol":        record.WakeProtocol,
		"wake_daemon_build":    record.WakeDaemonBuild,
		"wake_helper_build":    record.WakeHelperBuild,
		"wake_result":          record.WakeResult,
		"wake_code":            record.WakeCode,
		"wake_uncertain":       record.WakeUncertain,
		"wake_rollback_at":     formatOptionalTime(record.WakeRollbackAt),
		"wake_rollback_ok_at":  formatOptionalTime(record.WakeRollbackOKAt),
		"wake_rollback_result": record.WakeRollbackState,
		"wake_rollback_detail": record.WakeRollbackInfo,
		"wake_withdrawn_at":    formatOptionalTime(record.WakeWithdrawnAt),
		"wake_failure_reason":  record.WakeFailureReason,
		"prompt_summary":       fmt.Sprintf("stored continuation prompt (%d characters)", len([]rune(record.Prompt))),
	}
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
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
	if missing := herdr.MissingRequiredSchemaMethods(schema); len(missing) > 0 {
		return &model.StageError{
			Stage:    "schema",
			Code:     model.ErrSchemaUnsupported,
			Message:  fmt.Sprintf("Herdr API schema does not provide %s", missing[0]),
			Recovery: "update Herdr to 0.7.5 or newer, then run wt herd doctor",
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
	defer func() { _ = os.Remove(temporaryPath) }()
	// #nosec G204 -- fixed go subcommand/package; temporaryPath is created in the private stable runtime directory above.
	command := exec.CommandContext(ctx, "go", "build", "-o", temporaryPath, "./tools/herdr-devflow/cmd/herdr-devflow")
	command.Dir = repoRoot
	if err := command.Run(); err != nil {
		return err
	}
	// #nosec G302 -- the stable helper must be executable by the current user and launchd; its directory is user-private.
	if err := os.Chmod(temporaryPath, 0755); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func copyFileAtomic(source, destination string, mode os.FileMode) error {
	// #nosec G304 -- source is a fixed repository plugin asset selected by the validated repository root during setup.
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
	defer func() { _ = os.Remove(temporaryPath) }()
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

// githubDiagnostic checks the one required remote dependency of the feature
// overview: an installed, authenticated `gh` that can read the pull-request
// and check fields the board needs. It performs the same read-only query the
// overview does, so a PASS here means the overview will work.
func (a *App) githubDiagnostic(ctx context.Context, runtime runtimeContext) diagnostic {
	if _, err := a.lookPath("gh"); err != nil {
		return diagnostic{
			Name: "GitHub CLI", Status: "FAIL",
			Detail:   "the GitHub CLI (gh) is not installed or not on PATH",
			Recovery: "install the GitHub CLI: https://cli.github.com",
		}
	}
	client := github.New(github.Options{
		Dir:            runtime.paths.RepoRoot,
		Timeout:        runtime.config.GitHubTimeout(),
		CandidateLimit: 1,
	})
	result, err := client.ListPullRequests(ctx, "dev")
	if err != nil {
		var remoteErr *github.Error
		if errors.As(err, &remoteErr) {
			return diagnostic{
				Name: "GitHub access", Status: "FAIL",
				Detail: remoteErr.Detail, Recovery: remoteErr.Recovery(),
			}
		}
		return diagnostic{
			Name: "GitHub access", Status: "FAIL",
			Detail: "the GitHub query failed", Recovery: "run: gh auth status",
		}
	}
	// A repository with no pull requests is healthy; the fields are only
	// verifiable when at least one exists.
	if len(result.PullRequests) == 0 {
		return diagnostic{Name: "GitHub access", Status: "PASS", Detail: "authenticated; no pull requests to sample"}
	}
	sample := result.PullRequests[0]
	if sample.Head == "" || sample.State == "" {
		return diagnostic{
			Name: "GitHub access", Status: "WARN",
			Detail:   "the pull-request fields the overview needs were not returned",
			Recovery: "update the GitHub CLI, then run: wt herd doctor",
		}
	}
	return diagnostic{Name: "GitHub access", Status: "PASS", Detail: "authenticated; PR and check fields readable"}
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

	diagnostics = append(diagnostics, a.githubDiagnostic(ctx, runtime))

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
	} else if missing := herdr.MissingRequiredSchemaMethods(schema); len(missing) == 0 {
		diagnostics = append(diagnostics, diagnostic{Name: "Herdr schema", Status: "PASS", Detail: fmt.Sprintf("protocol %d", schema.Protocol)})
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "Herdr schema", Status: "FAIL", Detail: "required structured API method is absent: " + missing[0], Recovery: "update Herdr to 0.7.5 or newer, then run wt herd doctor"})
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
		home, homeErr := a.userHomeDir()
		if homeErr != nil {
			diagnostics = append(diagnostics, diagnostic{Name: "scheduler", Status: "WARN", Detail: "could not resolve the macOS home directory", Recovery: "run wt herd setup from a logged-in macOS account"})
		} else if _, err := os.Stat(scheduler.LaunchAgentPath(home)); err == nil {
			diagnostics = append(diagnostics, diagnostic{Name: "scheduler", Status: "PASS", Detail: "LaunchAgent registered"})
		} else {
			diagnostics = append(diagnostics, diagnostic{Name: "scheduler", Status: "WARN", Detail: "no continuation dispatcher is registered", Recovery: "run wt herd setup or create a one-time continuation with wt herd continue ..."})
		}
	} else {
		diagnostics = append(diagnostics, diagnostic{Name: "scheduler", Status: "WARN", Detail: "one-time continuation scheduling is macOS-only", Recovery: "run scheduling commands on macOS"})
	}
	diagnostics = append(diagnostics, a.claudeUsageDiagnostics(runtime)...)
	diagnostics = append(diagnostics, a.wakeDiagnostics(ctx)...)
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
	// Plugin actions are passive observers. They never install integrations,
	// create worktrees, start agents, or change keybindings.
	switch args[0] {
	case "startup", "refresh":
		return a.pluginRefresh(ctx, opts)
	case "setup":
		a.writeResult(opts.json, map[string]any{"status": "manual_setup_required", "message": "Run wt herd setup from an Ori Git worktree to refresh the stable helper."})
		return 0
	case "board":
		return a.pluginBoard(ctx, opts)
	default:
		a.writeError(fmt.Errorf("unknown plugin command %q", args[0]), opts.json)
		return 2
	}
}

func (a *App) pluginStatusService(opts options) (*status.Service, *herdr.Client, error) {
	store, err := a.dispatcherStore(opts)
	if err != nil {
		return nil, nil, err
	}
	client := a.newHerdrClient(opts, a.withOverrides(opts))
	service := &status.Service{
		Store:             store,
		Client:            client,
		SourceID:          PluginID,
		ViewSource:        status.PluginViewSource,
		WatchPollInterval: 2 * time.Second,
	}
	if client.SocketPath != "" {
		service.Subscribe = func(ctx context.Context, subscriptions []map[string]any) (status.EventStream, error) {
			stream, err := client.Subscribe(ctx, subscriptions)
			if err != nil {
				return nil, err
			}
			return stream, nil
		}
	}
	return service, client, nil
}

func (a *App) pluginRefresh(ctx context.Context, opts options) int {
	service, _, err := a.pluginStatusService(opts)
	if err != nil {
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow plugin warning: could not open local status state: %v\n", err)
		return 0
	}
	snapshot, err := service.Snapshot(ctx, status.Options{})
	if err != nil {
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow plugin warning: could not refresh status: %v\n", err)
		return 0
	}
	a.rehydratePluginStatus(ctx, service, snapshot)
	a.writeResult(opts.json, map[string]any{"status": "ready", "managed_agents": len(snapshot.Rows), "stale": snapshot.Stale})
	return 0
}

// pluginBoard renders the Herdr board from the shared overview snapshot.
//
// The board deliberately does not build its own inventory: constructing a
// second one is precisely how the board and the CLI used to disagree about
// progress, divergence, and agent state. Herdr's display metadata is still
// applied afterwards, and only ever as a separate write.
func (a *App) pluginBoard(ctx context.Context, opts options) int {
	metadata, client, err := a.pluginStatusService(opts)
	if err != nil {
		a.writeError(&model.StageError{Stage: "plugin board", Code: model.ErrStateCorrupt, Message: "could not open the user-local Ori Devflow status state", Recovery: "run wt herd setup from an Ori Git worktree", Cause: err}, opts.json)
		return 1
	}

	service, buildErr := a.pluginOverviewService(opts, client)
	if buildErr != nil {
		// Without a resolvable repository the board cannot collect anything,
		// but the legacy view is still better than a blank pane.
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow plugin warning: %v\n", buildErr)
		return a.pluginLegacyBoard(ctx, opts, metadata)
	}

	firstSnapshot := true
	wasStale := false
	rendered := false
	emit := func(snapshot overview.Snapshot) {
		// Reconnects and the first render are the two points where display
		// metadata may need rebuilding.
		if firstSnapshot || (wasStale && !snapshot.Stale) {
			a.rehydratePluginStatusFromState(ctx, metadata)
		}
		firstSnapshot = false
		wasStale = snapshot.Stale
		if rendered && !opts.json && a.statusColorEnabled(false) {
			fmt.Fprint(a.stdout, "\x1b[2J\x1b[H")
		}
		rendered = true
		if err := a.renderOverview(snapshot, true, overview.SelectAll(), opts.json, false); err != nil {
			fmt.Fprintln(a.stderr, err.Error())
		}
	}
	if err := service.Watch(ctx, 2*time.Second, emit); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	return 0
}

// pluginLegacyBoard is the fallback for a plugin invocation that cannot resolve
// a repository checkout, which is the one case the shared collector cannot
// serve.
func (a *App) pluginLegacyBoard(ctx context.Context, opts options, service *status.Service) int {
	emit := func(snapshot status.Snapshot) {
		a.rehydratePluginStatus(ctx, service, snapshot)
		a.writeStatusSnapshot(opts.json, false, snapshot)
	}
	if err := service.Watch(ctx, status.Options{}, emit); err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	return 0
}

// pluginOverviewService resolves a repository from the bridge's saved feature
// paths. The plugin is launched by Herdr, not from a checkout, so it has no
// working directory to infer one from.
func (a *App) pluginOverviewService(opts options, client *herdr.Client) (*overview.Service, error) {
	store, err := a.dispatcherStore(opts)
	if err != nil {
		return nil, fmt.Errorf("could not open local bridge state: %w", err)
	}
	saved, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("could not read local bridge state: %w", err)
	}
	repoRoot := ""
	for _, feature := range saved.Features {
		if feature.Feature.Path == "" {
			continue
		}
		if root, findErr := worktree.FindRepoRoot(feature.Feature.Path); findErr == nil {
			repoRoot = root
			break
		}
	}
	if repoRoot == "" {
		return nil, fmt.Errorf("no repository checkout could be resolved from saved bridge state")
	}
	return overview.NewService(overview.Config{
		RepoRoot: repoRoot,
		Remote: github.New(github.Options{
			Dir:     repoRoot,
			Timeout: github.DefaultTimeout,
		}),
		RemoteRefreshInterval: config.MinGitHubRefreshInterval,
		Agents:                client,
		Bridge:                store,
	}), nil
}

// rehydratePluginStatusFromState republishes display metadata using the legacy
// status service, which owns every Herdr write.
func (a *App) rehydratePluginStatusFromState(ctx context.Context, service *status.Service) {
	snapshot, err := service.Snapshot(ctx, status.Options{})
	if err != nil {
		return
	}
	a.rehydratePluginStatus(ctx, service, snapshot)
}

func (a *App) rehydratePluginStatus(ctx context.Context, service *status.Service, snapshot status.Snapshot) {
	if err := service.RehydrateMetadata(ctx, snapshot); err != nil {
		fmt.Fprintf(a.stderr, "Ori Herdr Devflow plugin warning: status metadata was not refreshed: %v\n", err)
	}
	// The managed Herdr agent view is no longer auto-applied here; see
	// rehydrateStatus for why.
}

func (a *App) writeHelp() {
	fmt.Fprint(a.stdout, `Ori Herdr Devflow bridge

Usage:
  wt herd setup                 Install/update the stable local helper and linked plugin
  wt herd doctor                Check config, Herdr, plugin, agent, scheduler, and state readiness
  wt herd wake install [--yes]  Stage, explain, authorize, install, and self-test the root wake service
  wt herd wake status [--json]  Report fixed files, daemon health, compatibility, UID, and self-test
  wt herd wake doctor [--json]  Diagnose standalone wake installation and health
  wt herd wake uninstall [--yes]
                                Remove only the standalone Herdr wake service after safety checks
  wt herd handoff --feature NAME --worktree PATH [--branch NAME] [--kind KIND] [--no-prompt]
                                Add a tab for an existing Git worktree in the focused workspace
                                and launch its primary agent there. --no-prompt starts the agent
                                without the bootstrap prompt, for ad-hoc work that has no PRD or
                                task list to point at; the choice is recorded for later retries.
  wt herd retry [--feature NAME] [--worktree PATH] [--branch NAME] [--resend]
                                Resume the recorded primary kind; --resend repeats a confirmed prompt
  wt herd add <role> [--kind KIND] [--feature NAME|--worktree PATH]
                                Start one explicit secondary role agent in the managed workspace
  wt herd prompt [role] <text> [--target TARGET] [--feature NAME|--worktree PATH]
                                Prompt the selected feature-scoped agent (primary role by default)
  wt herd rename <role> <new-role> [--feature NAME|--worktree PATH]
  wt herd focus [role] [--target TARGET] [--feature NAME|--worktree PATH]
  wt herd read [role] [--target TARGET] [--lines N] [--feature NAME|--worktree PATH]
  wt herd rebind <role> --target TARGET [--feature NAME|--worktree PATH]
  wt herd continue [role] --at TIME [--prompt TEXT] [--wake] [--feature NAME|--worktree PATH]
                                Schedule one safe continuation for an existing managed agent
  wt herd schedule list [--feature NAME|--worktree PATH]
  wt herd schedule show <schedule-id> [--feature NAME|--worktree PATH]
  wt herd schedule cancel <schedule-id> [--feature NAME|--worktree PATH]
                                Inspect or cancel local one-time continuations without prompting
  wt herd status [--current|--feature NAME|--worktree PATH] [--watch] [--json] [--no-color]
                                List only the coding agents Herdr reports as open now.
                                Shows agent, kind, live status, and worktree; no planning,
                                bridge history, Git, GitHub, or saved missing-agent rows.
  wt herd go                  Interactively select and focus one open Herdr agent.
                                Works directly and from the wt REPL.
  wt herd overview [same options]
                                Compatibility alias for wt herd status.
  wt herd status --clear-view  Clear only the Ori Devflow source-scoped Herdr agent view
  ./scripts/devops/issue.sh [--all] [--json]
                                List this repository's open GitHub Issues — the raw capture
                                list, before any grooming. The default scope is the Issues
                                you authored (author:@me); --all keeps the repository and
                                the open state and drops only that author filter.
                                Every invocation queries GitHub: there is no cache and no
                                local backlog file, so a failure is reported as a failure
                                rather than as an empty list. Issue bodies are not listed;
                                read one with ./scripts/devops/issue.sh view.
  ./scripts/devops/issue.sh view <number|url> [--json]
                                Show one Issue of this repository in full, open or closed:
                                state, author, labels, timestamps, URL, and body. The body
                                is printed as Markdown text — no HTML is rendered, no link
                                is followed, and no attachment is downloaded.
  ./scripts/devops/issue.sh add "<title>" [--body "<text>"] [--json]
                                Create one Issue in this repository from a required title
                                and an optional Markdown body. It sets nothing else: no
                                label, assignee, milestone, Issue type, Project, or
                                parent, and it opens no browser or editor.
  ./scripts/devops/backlog.sh [--json]
                                Read the Backlog column of the project board linked to this
                                repository: captured work that has not been groomed yet,
                                where GitHub's auto-add workflow puts every new Issue.
                                Read-only — it never moves, ranks, or closes a card.
  ./scripts/devops/ready.sh [--json]
                                Read the Ready column of that same board, in the order it
                                was ranked: work a grooming agent has researched and
                                specced, so it is buildable now. Ready is not approved —
                                choosing what to build stays with you. Read-only.
  wt herd target [--json]       Name the workspace a new feature's tab would be added to.
                                Read-only and always exits 0; reports disabled or
                                unavailable instead of failing.
  wt herd overnight start --agent NAME[:ROLE] [--start HH:MM] [--deadline HH:MM]
                          [--timezone ZONE] [--max-resumes N] [--stay-awake] [--dry-run] [--confirm] [--json]
                                Plan an Overnight Run over explicitly selected Claude agents.
                                Prints the full consequences and creates nothing until you agree.
  wt herd overnight list [--json]        List Overnight Runs, newest first
  wt herd overnight show [ID] [--json]   Show one run's queue, cycles, wake, and next action
  wt herd overnight watch [ID]           Re-render one run until it finishes or you interrupt
  wt herd overnight report [ID] [--json] The morning summary: what moved, what stopped, what next
  wt herd overnight cancel [ID] [--json] Stop future prompts; agents and worktrees are untouched
  wt herd claude-usage install  Print the Claude settings that let Ori observe usage windows.
                                Prints only; it never edits your Claude configuration.
  wt herd claude-usage status   Report whether Claude usage records are being written
  wt herd dispatch              Run due local schedules (used by the macOS LaunchAgent)
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
		// Warnings accompany a successful result, so they have no error path to
		// travel on and would otherwise be visible only under --json.
		if warnings, ok := pretty["warnings"].([]any); ok {
			for _, warning := range warnings {
				if text, ok := warning.(string); ok {
					fmt.Fprintf(a.stdout, "Warning: %s\n", text)
				}
			}
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

// advanceOvernightRun moves one run forward by one step, including the parts of
// the cycle that happen outside an ordinary tick.
//
// The three phases are separate calls on purpose. A tick decides what to do; the
// sleep sequence has effects outside this process and needs its own ordering
// guarantees; and resuming happens after a wake, when the only thing that can be
// trusted is what was written down before the machine slept.
func (a *App) advanceOvernightRun(ctx context.Context, supervisor *overnight.Supervisor,
	runID string, cfg config.Config, approvalGranted bool,
) (model.OvernightRun, error) {
	run, err := supervisor.Tick(ctx, runID)
	if err != nil {
		return model.OvernightRun{}, err
	}
	if run.State.Terminal() && run.WakeMode == model.WakeModeStayAwake && run.Assertion.ID != "" {
		return supervisor.ReleaseStayAwake(ctx, runID)
	}
	switch run.State {
	case model.RunLimitDetected, model.RunPreparingSleep:
		if run.WakeMode == model.WakeModeStayAwake {
			return supervisor.EnsureStayAwake(ctx, runID)
		}
		return supervisor.PrepareAndSleep(ctx, runID, overnight.SleepConfig{
			WakeLead:        cfg.WakeLead(),
			ApprovalGranted: approvalGranted,
			StayAwake:       run.WakeMode == model.WakeModeStayAwake,
		})
	case model.RunSleeping, model.RunWaking, model.RunWaitingForReset:
		if run.WakeMode == model.WakeModeStayAwake {
			if participant, ok := run.Active(); ok && participant.Limit != nil && participant.Limit.ResetAt.After(time.Now().UTC()) {
				return supervisor.EnsureStayAwake(ctx, runID)
			}
			if run.Assertion.ID != "" {
				released, err := supervisor.ReleaseStayAwake(ctx, runID)
				if err != nil {
					return model.OvernightRun{}, err
				}
				// A failed release is recorded as uncertain.  Do not continue the
				// run until a later dispatcher pass can prove that this run's
				// assertion is gone; otherwise the durable record would claim a
				// completed lifecycle that the host still contradicts.
				if released.Assertion.Uncertain {
					return released, nil
				}
			}
		}
		return supervisor.Resume(ctx, runID)
	default:
		return run, nil
	}
}
