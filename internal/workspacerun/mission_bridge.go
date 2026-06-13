package workspacerun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// MissionBridge wires the workspace mission cadence + manual triggers into the
// run lifecycle. It implements workspace.MissionTrigger, so the TaskScheduler
// can call it directly when a workspace's mission is due. The shape mirrors
// TaskRunBridge: a thin adapter that wraps the mission as a synthetic
// workspace.Task, runs it through the OriAgentExecutor pipeline, then parses
// the result back into Opportunity records.
//
// Why a synthetic Task: the OriAgentExecutor expects an
// OriAgentExecutorConfig.TaskPayload because the underlying TaskHandler is
// already wired for tool-calling, MCP/skill resolution, and trace capture.
// Reusing that machinery for missions means we get the autonomy gate plus
// tool plumbing "for free" — we just have to feed the gate the workspace's
// AutonomyPolicy and the binding side-effect classifications (done elsewhere).
type MissionBridge struct {
	store            Store
	service          *Service
	workspaceStore   workspace.Store
	agents           agentStoreReader
	opportunityStore workspace.OpportunityStore
	maxOpenInContext int           // cap on how many opportunities we feed into the prompt
	executionTimeout time.Duration // upper bound on a single mission run
}

// agentStoreReader is a small interface so MissionBridge depends only on what
// it needs from the global agent store. Lets tests inject a stub without
// pulling the entire agent.Store contract into the package.
type agentStoreReader interface {
	GetAgent(name string) (*agent.Agent, bool)
}

// MissionBridgeConfig groups dependencies so the constructor doesn't grow a
// long positional argument list as we add knobs.
type MissionBridgeConfig struct {
	RunStore         Store
	Service          *Service
	WorkspaceStore   workspace.Store
	Agents           agentStoreReader
	OpportunityStore workspace.OpportunityStore
	// MaxOpenOpportunitiesInPrompt caps how many open opportunities are
	// injected into the mission system prompt to keep token usage bounded.
	// 0 means use the default (50).
	MaxOpenOpportunitiesInPrompt int
	// ExecutionTimeout bounds a single mission run end-to-end. 0 means
	// default (15 minutes).
	ExecutionTimeout time.Duration
}

// NewMissionBridge constructs a MissionBridge from its dependencies. Returns
// nil if any required dependency is missing — callers should check and refuse
// to wire a partial bridge into the scheduler.
func NewMissionBridge(cfg MissionBridgeConfig) *MissionBridge {
	if cfg.RunStore == nil || cfg.Service == nil || cfg.WorkspaceStore == nil || cfg.OpportunityStore == nil {
		return nil
	}
	maxOpen := cfg.MaxOpenOpportunitiesInPrompt
	if maxOpen <= 0 {
		maxOpen = 50
	}
	timeout := cfg.ExecutionTimeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	return &MissionBridge{
		store:            cfg.RunStore,
		service:          cfg.Service,
		workspaceStore:   cfg.WorkspaceStore,
		agents:           cfg.Agents,
		opportunityStore: cfg.OpportunityStore,
		maxOpenInContext: maxOpen,
		executionTimeout: timeout,
	}
}

// TriggerMissionRun is the workspace.MissionTrigger implementation. End-to-end:
//  1. Load workspace + resolve Workspace Manager (entry agent).
//  2. Gather open opportunities for prompt context.
//  3. Build the composite mission system prompt.
//  4. Create a Run with OriginMission + cycle ordinal + autonomy-derived Policy.
//  5. Execute the Run synchronously (matches TaskRunBridge.ExecuteTaskRun).
//  6. Read the result artifact, parse it via workspace.ParseMissionOutput.
//  7. Upsert opportunities (dedup-merging happens inside the store).
//  8. Apply ApplyMissionRunOutcome inside an atomic workspace Update.
//
// Returns the run ID even on parse/persist failures (the run completed) so the
// caller can navigate to the run detail page to investigate.
func (b *MissionBridge) TriggerMissionRun(ctx context.Context, workspaceID string, cycleOrdinal int) (string, error) {
	return b.TriggerMissionRunOpts(ctx, workspaceID, cycleOrdinal, workspace.MissionRunOptions{})
}

// TriggerMissionRunOpts is TriggerMissionRun with event-trigger extras: an
// optional triggering event injected into the mission prompt, and the
// cadence-heartbeat hold. The zero-value opts reproduce TriggerMissionRun
// exactly; the cadence scheduler keeps calling the plain method.
func (b *MissionBridge) TriggerMissionRunOpts(ctx context.Context, workspaceID string, cycleOrdinal int, opts workspace.MissionRunOptions) (string, error) {
	if b == nil {
		return "", fmt.Errorf("mission bridge not configured")
	}
	ws, err := b.workspaceStore.Get(workspaceID)
	if err != nil {
		return "", fmt.Errorf("load workspace: %w", err)
	}
	agentName := strings.TrimSpace(ws.EntryAgentName())
	if agentName == "" {
		return "", fmt.Errorf("workspace %q has no entry agent; mission cannot run", workspaceID)
	}

	// Compose the system prompt: existing manager prompt + mission framing.
	// If the global agent store knows this agent we use its system prompt;
	// otherwise we proceed with an empty base (the mission framing alone
	// is still useful, though less personalized).
	var basePrompt string
	if b.agents != nil {
		if ag, ok := b.agents.GetAgent(agentName); ok && ag != nil {
			basePrompt = ag.Settings.SystemPrompt
		}
	}

	openOpps, err := b.opportunityStore.List(workspaceID)
	if err != nil {
		// Surface as a soft failure: the run can still proceed without
		// backlog context; the agent will just be more likely to duplicate.
		logger.Warn("mission bridge: failed to load opportunity backlog; proceeding without it",
			logger.Fields{"workspace_id": workspaceID, "error": err})
		openOpps = nil
	}
	openOpps = filterOpenOpportunities(openOpps, b.maxOpenInContext)

	systemPrompt := workspace.BuildMissionSystemPrompt(workspace.MissionPromptInputs{
		WorkspaceManagerPrompt: basePrompt,
		Mission:                ws.Mission,
		CycleOrdinal:           cycleOrdinal,
		OpenOpportunities:      openOpps,
		TriggeringEvent:        opts.Event,
	})

	// Build a synthetic task to feed into the OriAgentExecutor. The task is
	// ephemeral — never persisted on the workspace — but it carries the
	// composed prompt as Description and a stable ID so trace events can be
	// referenced post-hoc.
	task := workspace.Task{
		ID:          "mission-" + uuid.NewString(),
		WorkspaceID: workspaceID,
		From:        "system:mission",
		To:          agentName,
		Description: systemPrompt,
		Status:      workspace.TaskStatusPending,
		CreatedAt:   time.Now(),
		// Mission-origin marker. The LLMTaskHandler reads these context
		// keys at the tool-call boundary to apply the autonomy gate. The
		// runner has no other way to know it's executing a mission run
		// (the synthetic task itself is otherwise indistinguishable from
		// a normal task).
		Context: map[string]any{
			workspace.MissionTaskContextOriginKey:  workspace.MissionTaskContextOriginValue,
			workspace.MissionTaskContextPolicyKey:  string(ws.AutonomyPolicy),
			workspace.MissionTaskContextOrdinalKey: cycleOrdinal,
		},
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return "", fmt.Errorf("marshal mission task payload: %w", err)
	}
	rawPayload := RawConfig(payload)
	cfgPayload, err := json.Marshal(OriAgentExecutorConfig{TaskPayload: &rawPayload})
	if err != nil {
		return "", fmt.Errorf("marshal ori_agent executor config: %w", err)
	}
	rawConfig := RawConfig(cfgPayload)

	policy := buildMissionRunPolicy(ws.AutonomyPolicy)

	runCtx, cancel := context.WithTimeout(ctx, b.executionTimeout)
	defer cancel()

	run, err := b.service.CreateRun(runCtx, workspaceID, CreateRunRequest{
		ProfileID: ProfileGeneral,
		Executor: Executor{
			Kind:   ExecutorKindOriAgent,
			Ref:    agentName,
			Config: &rawConfig,
		},
		Prompt:            systemPrompt,
		Scope:             Scope{},
		Policy:            policy,
		ContextPlan:       DefaultTaskContextPlan(),
		ValidationRequest: &ValidationRequest{Profile: ValidationProfileNone},
		OriginType:        OriginMission,
		CycleOrdinal:      cycleOrdinal,
	})
	if err != nil {
		// Run wasn't created — record an outcome here so state advances and the
		// scheduler doesn't storm. This is the single place this failure is
		// counted: the scheduler only records an outcome when NextMissionRunAt
		// is still in the past (i.e. control never reached here), so it will not
		// double-count the advance we just made.
		_ = b.workspaceStore.Update(workspaceID, func(w *workspace.Workspace) error {
			workspace.ApplyMissionRunOutcome(w, workspace.MissionRunOutcome{StartedAt: time.Now(), Succeeded: false, HoldCadence: opts.HoldCadence})
			return nil
		})
		return "", fmt.Errorf("create mission run: %w", err)
	}

	started := time.Now()
	execErr := b.service.ExecuteRun(runCtx, workspaceID, run.ID)

	// Read the resulting artifact regardless of execErr — partial output
	// can still be parsed and is more useful than nothing.
	artifacts, listErr := b.store.ListArtifacts(ctx, workspaceID, run.ID)
	if listErr != nil {
		logger.Warn("mission bridge: list artifacts after run", logger.Fields{
			"workspace_id": workspaceID, "run_id": run.ID, "error": listErr,
		})
	}
	result, _ := taskResultFromArtifacts(artifacts)

	if result != "" {
		opps, parseErr := workspace.ParseMissionOutput(workspaceID, run.ID, result)
		if parseErr != nil {
			logger.Warn("mission bridge: parse output", logger.Fields{
				"workspace_id": workspaceID, "run_id": run.ID, "error": parseErr,
			})
		}
		for _, opp := range opps {
			if _, _, err := b.opportunityStore.Upsert(opp); err != nil {
				logger.Warn("mission bridge: upsert opportunity", logger.Fields{
					"workspace_id": workspaceID, "run_id": run.ID, "title": opp.Title, "error": err,
				})
			}
		}
	}

	// Update mission tracking counters. Failure on execErr increments
	// MissionFailureCount; success path increments MissionExecutionCount.
	succeeded := execErr == nil
	_ = b.workspaceStore.Update(workspaceID, func(w *workspace.Workspace) error {
		workspace.ApplyMissionRunOutcome(w, workspace.MissionRunOutcome{
			StartedAt:   started,
			Succeeded:   succeeded,
			HoldCadence: opts.HoldCadence,
		})
		return nil
	})

	if execErr != nil {
		return run.ID, fmt.Errorf("execute mission run: %w", execErr)
	}
	return run.ID, nil
}

// buildMissionRunPolicy turns the workspace's autonomy policy into the
// workspacerun.Policy fields the run lifecycle consults. The per-tool
// autonomy gate is enforced inside the executor via the workspace's
// AutonomyPolicy + binding classifications; this function sets the
// run-level coarse defaults.
func buildMissionRunPolicy(p workspace.AutonomyPolicy) Policy {
	hints := workspace.AutonomyPolicyRunHints(p)
	return Policy{
		Mutation:        hints.Mutation,
		Approval:        hints.Approval,
		ExternalEffects: hints.ExternalEffects,
	}
}

// filterOpenOpportunities returns at most maxOpen items that are still open
// (new or snoozed). Resolved and dismissed opportunities are skipped so the
// agent doesn't waste context on already-handled issues.
func filterOpenOpportunities(in []workspace.Opportunity, maxOpen int) []workspace.Opportunity {
	if len(in) == 0 {
		return nil
	}
	out := make([]workspace.Opportunity, 0, len(in))
	for _, o := range in {
		if !o.IsOpen() {
			continue
		}
		out = append(out, o)
		if maxOpen > 0 && len(out) >= maxOpen {
			break
		}
	}
	return out
}

// Compile-time guarantee that MissionBridge satisfies workspace.MissionTrigger.
var _ workspace.MissionTrigger = (*MissionBridge)(nil)
