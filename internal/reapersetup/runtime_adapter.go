package reapersetup

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const ReaperLiveControlCapability = "reaper_live_control"

var ErrRuntimeGrantUnavailable = errors.New("REAPER runtime grant prerequisites are unavailable")

// CLIAgentProviderResolver resolves provider identity from trusted agent stores,
// never from a grant request body or task prompt.
type CLIAgentProviderResolver interface {
	ProviderForAgent(context.Context, string, workspace.AgentInstance) (provider string, isCLI bool, err error)
}

const (
	ReasonUnsupportedPlatform  = "unsupported_platform"
	ReasonApplicationMissing   = "reaper_app_missing"
	ReasonApplicationUnknown   = "reaper_app_unknown"
	ReasonWebRemoteMissing     = "web_remote_unconfigured"
	ReasonWebRemoteInvalid     = "web_remote_invalid"
	ReasonRunnerMissing        = "runner_missing"
	ReasonRunnerInvalid        = "runner_invalid"
	ReasonPluginMissing        = "reaper_plugin_missing"
	ReasonPluginDisabled       = "reaper_plugin_disabled"
	ReasonPluginDetached       = "reaper_plugin_detached"
	ReasonCLIAgentRequired     = "cli_agent_required"
	ReasonGrantMissing         = "reaper_access_required"
	ReasonGrantDenied          = "reaper_access_denied"
	ReasonProjectMissing       = "reaper_project_missing"
	ReasonReaperOffline        = "reaper_offline"
	ReasonTransportUnavailable = "web_remote_unavailable"
	ReasonTransportMalformed   = "web_remote_malformed"
	ReasonWrongProject         = "wrong_project"
	ReasonVerificationTimeout  = "verification_timeout"
	ReasonRunnerFailure        = "runner_failure"
	ReasonWriteDenied          = "runner_write_denied"
)

// RuntimeAdapter is the compiled implementation behind the inert
// reaper_live_control adapter ID. Every probe input comes from platform code or
// canonical workspace state; blueprint and browser data can select only this
// stable adapter key.
type RuntimeAdapter struct {
	source  RuntimeWorkspaceSource
	plugins PluginInspector
	agents  CLIAgentProviderResolver
	roots   RunnerRootResolver
	probes  ProbeSet
}

func NewRuntimeAdapter(source RuntimeWorkspaceSource, plugins PluginInspector, agents CLIAgentProviderResolver, roots RunnerRootResolver, probes ProbeSet) *RuntimeAdapter {
	return &RuntimeAdapter{source: source, plugins: plugins, agents: agents, roots: roots, probes: probes}
}

func (a *RuntimeAdapter) ID() string { return ReaperLiveControlCapability }

func (a *RuntimeAdapter) EvaluateDurable(ctx context.Context, request runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	check := a.evaluatePrerequisites(ctx, request)
	if check.blocked != nil {
		return *check.blocked, nil
	}
	result := runtimecapability.DurableResult{
		State:                runtimecapability.DurableConfigured,
		Summary:              "Local REAPER control prerequisites are configured.",
		VerificationRequired: request.Persisted.FirstVerifiedAt == nil,
	}
	if result.VerificationRequired {
		result.ReasonCode = runtimecapability.ReasonVerificationRequired
		result.Summary = "Run the harmless REAPER connection test to finish live-control setup."
		result.Action = runtimeAction(request.WorkspaceID, "test_reaper_connection", "Test REAPER connection")
	}
	return result, nil
}

func (a *RuntimeAdapter) CheckLive(ctx context.Context, request runtimecapability.EvaluationRequest) (runtimecapability.LiveResult, error) {
	check := a.evaluatePrerequisites(ctx, request)
	if check.blocked != nil {
		return runtimecapability.LiveResult{
			State:      runtimecapability.LiveUnavailable,
			ReasonCode: check.blocked.ReasonCode,
			Summary:    check.blocked.Summary,
			Action:     check.blocked.Action,
		}, nil
	}
	transport := a.probes.Transport.CheckTransport(ctx, check.web)
	if result := liveTransportResult(request.WorkspaceID, transport); result != nil {
		return *result, nil
	}
	expected, err := AuthoritativeProject(a.source, request.WorkspaceID)
	if err != nil {
		return runtimecapability.LiveResult{
			State:      runtimecapability.LiveUnavailable,
			ReasonCode: ReasonProjectMissing,
			Summary:    "The workspace's REAPER project is unavailable.",
			Action:     runtimeAction(request.WorkspaceID, "review_project", "Review workspace project"),
		}, nil
	}
	verification := a.probes.Verifier.VerifyProject(ctx, VerificationTarget{
		ExpectedProject: expected,
		WebRemote:       check.web,
		Runner:          check.runner,
		Timeout:         4 * time.Second,
	})
	return liveVerificationResult(request.WorkspaceID, verification), nil
}

func (a *RuntimeAdapter) Verify(ctx context.Context, request runtimecapability.VerificationRequest) (runtimecapability.VerificationResult, error) {
	before := a.evaluatePrerequisites(ctx, request.EvaluationRequest)
	if before.blocked != nil {
		return runtimecapability.VerificationResult{
			LiveState:  runtimecapability.LiveNotChecked,
			ReasonCode: before.blocked.ReasonCode,
			Summary:    before.blocked.Summary,
			Action:     before.blocked.Action,
		}, nil
	}
	transport := a.probes.Transport.CheckTransport(ctx, before.web)
	if live := liveTransportResult(request.WorkspaceID, transport); live != nil {
		return runtimecapability.VerificationResult{LiveState: live.State, ReasonCode: live.ReasonCode, Summary: live.Summary, Action: live.Action}, nil
	}
	if transport.Port > 0 {
		before.web.Port = transport.Port
	}
	expected, err := AuthoritativeProject(a.source, request.WorkspaceID)
	if err != nil {
		return runtimecapability.VerificationResult{
			LiveState:  runtimecapability.LiveWrongTarget,
			ReasonCode: ReasonProjectMissing,
			Summary:    "The workspace's REAPER project is unavailable.",
			Action:     runtimeAction(request.WorkspaceID, "review_project", "Review workspace project"),
		}, nil
	}
	verified := a.probes.Verifier.VerifyProject(ctx, VerificationTarget{
		ExpectedProject: expected,
		WebRemote:       before.web,
		Runner:          before.runner,
		Timeout:         6 * time.Second,
	})
	if verified.State != VerificationSucceeded {
		live := liveVerificationResult(request.WorkspaceID, verified)
		return runtimecapability.VerificationResult{LiveState: live.State, ReasonCode: live.ReasonCode, Summary: live.Summary, Action: live.Action}, nil
	}
	// A successful runner response is not enough. Re-evaluate every durable
	// prerequisite after it completes so a concurrent revoke/removal cannot be
	// recorded as configured.
	after := a.evaluatePrerequisites(ctx, request.EvaluationRequest)
	if after.blocked != nil {
		return runtimecapability.VerificationResult{
			LiveState:  runtimecapability.LiveNotChecked,
			ReasonCode: after.blocked.ReasonCode,
			Summary:    after.blocked.Summary,
			Action:     after.blocked.Action,
		}, nil
	}
	return runtimecapability.VerificationResult{
		Succeeded:  true,
		LiveState:  runtimecapability.LiveAvailable,
		ReasonCode: "verified",
		Summary:    "The trusted REAPER connection test completed for this workspace project.",
	}, nil
}

func (a *RuntimeAdapter) ValidateGrant(ctx context.Context, request runtimecapability.GrantValidationRequest) error {
	if a == nil || a.agents == nil || a.roots == nil || workspace.NormalizeRuntimeIdentifier(request.Requirement.Key) == "" || request.Requirement.Adapter != ReaperLiveControlCapability {
		return ErrRuntimeGrantUnavailable
	}
	provider, isCLI, err := a.agents.ProviderForAgent(ctx, request.WorkspaceID, request.Agent)
	if err != nil || !isCLI || !supportedCLIProvider(provider) {
		return ErrRuntimeGrantUnavailable
	}
	if _, err := a.roots.Resolve(); err != nil {
		return ErrRuntimeGrantUnavailable
	}
	return nil
}

func (a *RuntimeAdapter) ResolveExecutionScope(ctx context.Context, request runtimecapability.ExecutionScopeRequest) (runtimecapability.CapabilityExecutionScope, error) {
	if err := a.ValidateGrant(ctx, runtimecapability.GrantValidationRequest(request)); err != nil {
		return runtimecapability.CapabilityExecutionScope{}, ErrRuntimeGrantUnavailable
	}
	root, err := a.roots.Resolve()
	if err != nil {
		return runtimecapability.CapabilityExecutionScope{}, ErrRuntimeGrantUnavailable
	}
	return runtimecapability.CapabilityExecutionScope{
		AdditionalWritableRoots: []string{root},
		NetworkPosture:          runtimecapability.CapabilityNetworkLocal,
	}, nil
}

type prerequisiteCheck struct {
	blocked *runtimecapability.DurableResult
	web     WebRemoteObservation
	runner  RunnerObservation
}

func (a *RuntimeAdapter) evaluatePrerequisites(ctx context.Context, request runtimecapability.EvaluationRequest) prerequisiteCheck {
	if a == nil || a.source == nil || a.plugins == nil || a.agents == nil ||
		a.probes.Application == nil || a.probes.WebRemote == nil || a.probes.Runner == nil || a.probes.Transport == nil || a.probes.Verifier == nil {
		return prerequisiteFailure(request, runtimecapability.ReasonAdapterUnavailable, "Guided REAPER runtime checks are unavailable in this build.", runtimeAction(request.WorkspaceID, "review_runtime_setup", "Review live-control setup"))
	}

	application := a.probes.Application.DetectApplication(ctx)
	switch application.State {
	case ProbeUnsupported:
		return prerequisiteFailure(request, ReasonUnsupportedPlatform, "Guided REAPER verification is not supported on this platform. File-only work remains available.", runtimeAction(request.WorkspaceID, "review_runtime_mode", "Review live-control setup"))
	case ProbeMissing:
		return prerequisiteFailure(request, ReasonApplicationMissing, "REAPER is not installed in a supported macOS application location.", runtimeAction(request.WorkspaceID, "download_reaper", "Download REAPER"))
	case ProbeReady:
	default:
		return prerequisiteFailure(request, ReasonApplicationUnknown, "Ori could not verify the REAPER application installation.", runtimeAction(request.WorkspaceID, "check_reaper_installation", "Check REAPER installation"))
	}

	web := a.probes.WebRemote.DetectWebRemote(ctx)
	switch web.State {
	case ProbeReady:
	case ProbeMissing:
		return prerequisiteFailure(request, ReasonWebRemoteMissing, "REAPER Web Remote is not configured and enabled.", runtimeAction(request.WorkspaceID, "enable_web_remote", "Enable Web Remote"))
	case ProbeInvalid:
		return prerequisiteFailure(request, ReasonWebRemoteInvalid, "REAPER Web Remote configuration is not usable.", runtimeAction(request.WorkspaceID, "enable_web_remote", "Review Web Remote settings"))
	default:
		return prerequisiteFailure(request, ReasonWebRemoteInvalid, "Ori could not verify REAPER Web Remote configuration.", runtimeAction(request.WorkspaceID, "check_web_remote", "Check Web Remote"))
	}

	pluginResults, err := a.plugins.Inspect(request.WorkspaceID, []string{ReaperPluginName})
	if err != nil || len(pluginResults) == 0 {
		return prerequisiteFailure(request, runtimecapability.ReasonCheckFailed, "Ori could not check this workspace's REAPER plugin.", runtimeAction(request.WorkspaceID, "review_runtime_setup", "Review live-control setup"))
	}
	pluginResult := pluginResults[0]
	switch {
	case !pluginResult.Installed || pluginResult.State == pluginworkspace.PluginStateMissing:
		return prerequisiteFailure(request, ReasonPluginMissing, "The REAPER plugin is not installed.", runtimeAction(request.WorkspaceID, "install_reaper_plugin", "Install REAPER plugin"))
	case !pluginResult.Enabled || pluginResult.State == pluginworkspace.PluginStateDisabled:
		return prerequisiteFailure(request, ReasonPluginDisabled, "The REAPER plugin is installed but disabled.", runtimeAction(request.WorkspaceID, "enable_reaper_plugin", "Enable REAPER plugin"))
	case len(pluginResult.Missing) > 0 || pluginResult.State == pluginworkspace.PluginStateDetached:
		return prerequisiteFailure(request, ReasonPluginDetached, "The REAPER plugin is not attached to this workspace.", runtimeAction(request.WorkspaceID, "attach_reaper_plugin", "Attach REAPER plugin"))
	}

	// Runner setup is supplied by the installed plugin, so plugin repair must be
	// the first actionable blocker when both are absent. Asking someone to load a
	// runner that has not been installed yet would offer a button that cannot
	// succeed.
	runner := a.probes.Runner.DetectRunner(ctx)
	switch runner.State {
	case ProbeReady:
	case ProbeMissing:
		return prerequisiteFailure(request, ReasonRunnerMissing, "The Ori REAPER runner is not registered yet.", runtimeAction(request.WorkspaceID, "set_up_runner", "Set up runner"))
	default:
		return prerequisiteFailure(request, ReasonRunnerInvalid, "The Ori REAPER runner registration is not usable.", runtimeAction(request.WorkspaceID, "set_up_runner", "Set up runner"))
	}

	ws, err := a.source.GetFolderWorkspace(request.WorkspaceID)
	if err != nil || ws == nil {
		return prerequisiteFailure(request, runtimecapability.ReasonCheckFailed, "Ori could not check this workspace's REAPER configuration.", runtimeAction(request.WorkspaceID, "review_runtime_setup", "Review live-control setup"))
	}
	instance, ok := effectiveRuntimeAgent(ws)
	if !ok {
		return prerequisiteFailure(request, ReasonCLIAgentRequired, "Choose a Codex or Claude Code workspace agent for local REAPER control.", runtimeAction(request.WorkspaceID, "choose_reaper_agent", "Choose compatible agent"))
	}
	provider, isCLI, err := a.agents.ProviderForAgent(ctx, request.WorkspaceID, instance)
	if err != nil || !isCLI || !supportedCLIProvider(provider) {
		return prerequisiteFailure(request, ReasonCLIAgentRequired, "Choose a Codex or Claude Code workspace agent for local REAPER control.", runtimeAction(request.WorkspaceID, "choose_reaper_agent", "Choose compatible agent"))
	}

	state := ws.GetRuntimeState()
	grant, found := state.RuntimeGrant(request.Requirement.Key, instance.ID)
	switch {
	case found && grant.Active():
	case found:
		return prerequisiteFailure(request, ReasonGrantDenied, "REAPER access for the selected workspace agent was revoked.", runtimeAction(request.WorkspaceID, "grant_reaper_access", "Grant REAPER access"))
	default:
		return prerequisiteFailure(request, ReasonGrantMissing, "The selected workspace agent does not have REAPER access.", runtimeAction(request.WorkspaceID, "grant_reaper_access", "Grant REAPER access"))
	}

	return prerequisiteCheck{web: web, runner: runner}
}

func prerequisiteFailure(request runtimecapability.EvaluationRequest, reason, summary string, action *runtimecapability.Action) prerequisiteCheck {
	state := runtimecapability.DurableInProgress
	if request.Persisted.FirstVerifiedAt != nil {
		state = runtimecapability.DurableNeedsAttention
	}
	return prerequisiteCheck{blocked: &runtimecapability.DurableResult{State: state, ReasonCode: reason, Summary: summary, Action: action}}
}

func effectiveRuntimeAgent(ws *workspace.Workspace) (workspace.AgentInstance, bool) {
	if ws == nil {
		return workspace.AgentInstance{}, false
	}
	name := effectiveSetupAgent(ws)
	var nodeID string
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if task.Context != nil && task.Context[TaskContextTemplateSetup] == true {
			nodeID = strings.TrimSpace(task.AssignedNodeID)
			break
		}
	}
	var matches []workspace.AgentInstance
	for _, instance := range ws.AgentInstances {
		if nodeID != "" && strings.TrimSpace(instance.NodeID) == nodeID {
			return instance, true
		}
		if strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(name)) {
			matches = append(matches, instance)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return workspace.AgentInstance{}, false
}

func supportedCLIProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex", "claude_code":
		return true
	default:
		return false
	}
}

func runtimeAction(workspaceID, code, label string) *runtimecapability.Action {
	workspaceID = strings.TrimSpace(workspaceID)
	code = workspace.NormalizeRuntimeIdentifier(code)
	if code == "" || strings.TrimSpace(label) == "" {
		return nil
	}
	return &runtimecapability.Action{
		Token: code,
		Code:  code,
		Label: strings.TrimSpace(label),
		URL:   "/workspaces/" + url.PathEscape(workspaceID) + "?runtime_setup=1&action=" + url.QueryEscape(code),
	}
}

func liveTransportResult(workspaceID string, observation LiveTransportObservation) *runtimecapability.LiveResult {
	switch observation.State {
	case TransportAvailable:
		return nil
	case TransportOffline:
		return &runtimecapability.LiveResult{State: runtimecapability.LiveOffline, ReasonCode: ReasonReaperOffline, Summary: "Ori could not reach any configured REAPER Web Remote interface.", Action: runtimeAction(workspaceID, "open_check_reaper", "Open or check REAPER")}
	case TransportMalformed:
		return &runtimecapability.LiveResult{State: runtimecapability.LiveCheckFailed, ReasonCode: ReasonTransportMalformed, Summary: "REAPER Web Remote returned an unexpected response.", Action: runtimeAction(workspaceID, "check_reaper_connection", "Check REAPER connection")}
	case TransportUnavailable:
		return &runtimecapability.LiveResult{State: runtimecapability.LiveUnavailable, ReasonCode: ReasonTransportUnavailable, Summary: "REAPER Web Remote is unavailable.", Action: runtimeAction(workspaceID, "check_reaper_connection", "Check REAPER connection")}
	default:
		return &runtimecapability.LiveResult{State: runtimecapability.LiveCheckFailed, ReasonCode: runtimecapability.ReasonCheckFailed, Summary: "Ori could not check REAPER right now.", Action: runtimeAction(workspaceID, "check_reaper_connection", "Check REAPER connection")}
	}
}

func liveVerificationResult(workspaceID string, observation VerificationObservation) runtimecapability.LiveResult {
	switch observation.State {
	case VerificationSucceeded:
		return runtimecapability.LiveResult{State: runtimecapability.LiveAvailable, ReasonCode: "connected", Summary: "REAPER is connected to this workspace project now."}
	case VerificationWrongProject:
		return runtimecapability.LiveResult{State: runtimecapability.LiveWrongTarget, ReasonCode: ReasonWrongProject, Summary: "REAPER has a different project open.", Action: runtimeAction(workspaceID, "open_correct_project", "Open the workspace project")}
	case VerificationProjectMissing:
		return runtimecapability.LiveResult{State: runtimecapability.LiveWrongTarget, ReasonCode: ReasonProjectMissing, Summary: "The workspace project is not open in REAPER.", Action: runtimeAction(workspaceID, "open_correct_project", "Open the workspace project")}
	case VerificationTimedOut:
		return runtimecapability.LiveResult{State: runtimecapability.LiveCheckFailed, ReasonCode: ReasonVerificationTimeout, Summary: "The REAPER runner did not answer before the connection check timed out.", Action: runtimeAction(workspaceID, "check_reaper_connection", "Check REAPER connection")}
	case VerificationPermissionDenied:
		return runtimecapability.LiveResult{State: runtimecapability.LiveUnavailable, ReasonCode: ReasonWriteDenied, Summary: "Ori could not use the dedicated REAPER runner exchange.", Action: runtimeAction(workspaceID, "grant_reaper_access", "Review REAPER access")}
	case VerificationRunnerFailed:
		return runtimecapability.LiveResult{State: runtimecapability.LiveCheckFailed, ReasonCode: ReasonRunnerFailure, Summary: "The REAPER runner reported that the harmless check failed.", Action: runtimeAction(workspaceID, "set_up_runner", "Check runner setup")}
	case VerificationMalformed:
		return runtimecapability.LiveResult{State: runtimecapability.LiveCheckFailed, ReasonCode: ReasonRunnerFailure, Summary: "The REAPER runner returned an unexpected response.", Action: runtimeAction(workspaceID, "set_up_runner", "Check runner setup")}
	default:
		return runtimecapability.LiveResult{State: runtimecapability.LiveCheckFailed, ReasonCode: runtimecapability.ReasonCheckFailed, Summary: "Ori could not check REAPER right now.", Action: runtimeAction(workspaceID, "check_reaper_connection", "Check REAPER connection")}
	}
}

var _ runtimecapability.Adapter = (*RuntimeAdapter)(nil)
var _ runtimecapability.LiveChecker = (*RuntimeAdapter)(nil)
var _ runtimecapability.Verifier = (*RuntimeAdapter)(nil)
var _ runtimecapability.GrantAuthorizer = (*RuntimeAdapter)(nil)
var _ runtimecapability.ExecutionScopeProvider = (*RuntimeAdapter)(nil)
