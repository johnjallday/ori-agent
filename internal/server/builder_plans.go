package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceplan"
	"github.com/johnjallday/ori-agent/internal/workspacepolicy"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// Wiring for the Workspace Planning Workflow's two lookups: which model to
// plan with, and what the workspace actually has to plan around.
//
// Both are resolved per call rather than captured at build time. Planning
// settings and the agent roster change while the server runs, and a Plan
// drafted after such a change should reflect it without a restart.

// attachWorkspacePlanMaterializer wires materialization once the workspace
// store exists.
//
// This runs late on purpose. Handlers are constructed in an earlier phase, when
// b.workspaceStore is still nil; building the materializer there would capture
// that nil and leave every materialization refused with "not configured" — a
// silent no-op rather than a visible failure. The store's identity is captured
// here, after it is real.
func (b *ServerBuilder) attachWorkspacePlanMaterializer() {
	if b.workspacePlanHandler == nil || b.workspacePlanService == nil || b.workspaceStore == nil {
		return
	}

	// Reconciliation is built first because the materializer consults it: a
	// version that revises approved work must not be materialized as though it
	// were a first approval, which would duplicate every retained Task.
	reconciler := workspaceplan.NewReconciler(
		b.workspacePlanService,
		b.workspaceStore,
		planTaskMutator{store: b.workspaceStore},
	)
	b.workspacePlanHandler.SetReconciler(reconciler)

	b.workspacePlanMaterializer = workspaceplan.NewMaterializer(
		b.workspacePlanService,
		b.workspaceStore,
		workspaceplan.WithReconciler(reconciler),
		// Artifacts are written into the workspace's own files root, and the
		// writer re-checks containment there rather than trusting the path it
		// was handed (FR-97).
		workspaceplan.WithArtifactWriter(
			workspaceplan.NewFileArtifactWriter(b.workspaceStore.GetFilesPath)),
	)
	b.workspacePlanHandler.SetMaterializer(b.workspacePlanMaterializer)
}

// planTaskDispatcher starts a plan's Task through the existing Task-to-Run
// bridge.
//
// Plan execution decides WHICH task runs next; the bridge decides how a task
// runs. Keeping that boundary means plan-dispatched work produces exactly the
// same Run records as any other task, with the same traces and results
// (FR-100).
type planTaskDispatcher struct {
	bridge *workspacerun.TaskRunBridge
}

func (d planTaskDispatcher) DispatchTask(ctx context.Context, workspaceID string, task workspace.Task) (string, error) {
	if d.bridge == nil {
		return "", fmt.Errorf("workspace run bridge is not configured")
	}
	// An unassigned task has no agent to run it. Picking one here would be an
	// assignment nobody approved, so it is refused with something the user can
	// act on (FR-86).
	agent := strings.TrimSpace(task.To)
	if agent == "" {
		return "", fmt.Errorf("task %q has no assignee; assign it before starting", task.Description)
	}

	result, err := d.bridge.ExecuteTaskRun(ctx, agent, task)
	if err != nil {
		return result.RunID, err
	}
	return result.RunID, nil
}

// planTaskMutator applies plan-driven task changes through the workspace
// store's canonical update path, so a cancel or retry goes through the same
// locking and persistence as every other task mutation (FR-112).
type planTaskMutator struct {
	store workspace.Store
}

func (m planTaskMutator) MutateTask(workspaceID, taskID string, fn func(*workspace.Task) error) error {
	if m.store == nil {
		return fmt.Errorf("workspace store is not configured")
	}
	return m.store.Update(workspaceID, func(ws *workspace.Workspace) error {
		return ws.MutateTask(taskID, fn)
	})
}

// chatPlanOpener lets chat start a durable Plan without handing it the plan
// service.
//
// It exposes exactly one operation. Chat routes work into a Plan and links to
// it; every other verb — edit, review, compare, approve — stays on Plan Detail,
// and the narrowness of this type is what makes that a fact rather than a
// convention (FR-19, FR-149).
type chatPlanOpener struct {
	service *workspaceplan.Service
}

func (o chatPlanOpener) OpenPlan(ctx context.Context, workspaceID, request, actor string) (string, error) {
	if o.service == nil {
		return "", fmt.Errorf("workspace planning is not configured")
	}
	plan, err := o.service.Create(ctx, workspaceID, workspaceplan.CreateInput{
		// The request is stored exactly as the user typed it, because the
		// clarifying and drafting that follow are about THAT sentence (FR-21).
		Request: request,
		Origin: workspaceplan.Origin{
			Kind:      workspaceplan.OriginChat,
			Actor:     actor,
			AgentName: actor,
		},
	})
	if err != nil {
		return "", err
	}
	return plan.ID, nil
}

// workspacePlanPreflight returns the compiled enforcement behind a Plan's
// execution preconditions.
//
// Returning nil when policy resolution is unavailable is deliberate and it
// fails CLOSED: with no checker, workspaceplan treats every enforced
// precondition as unverifiable and stops automatic dispatch at it. A plan that
// named a precondition then waits for a person instead of running on an
// unchecked promise.
func (b *ServerBuilder) workspacePlanPreflight() workspaceplan.PreconditionChecker {
	if b.workspacePlanPolicy == nil {
		return nil
	}
	// The repository-inspection recorder is not wired yet, so that control
	// fails closed on its own inside the preflight. Branch enforcement — the
	// half that has a real check behind it — works now.
	return workspacepolicy.NewPreflight(b.workspacePlanPolicy.Policy, nil)
}

// resolvePlanGuidance returns the ADVISORY half of a workspace's planning
// policy: what the planner is asked for.
//
// Nothing here is checked after the fact, which is exactly why it is kept in
// its own function feeding its own resolver. A field that reached both this and
// the policy snapshot would be presented to the user as enforced while being,
// in fact, a sentence in a prompt (FR-124, FR-125, FR-129).
func (b *ServerBuilder) resolvePlanGuidance(ctx context.Context, workspaceID string) workspaceplan.GuidanceInput {
	if b.workspacePlanPolicy == nil {
		return workspaceplan.GuidanceInput{}
	}
	policy, _ := b.workspacePlanPolicy.Policy(ctx, workspaceID)
	return workspaceplan.GuidanceInput{
		Style:              policy.Guidance.Style,
		ClarificationDepth: policy.Guidance.ClarificationDepth,
		PreferredArtifacts: policy.Guidance.PreferredArtifacts,
		DetailLevel:        policy.Guidance.DetailLevel,
	}
}

// resolvePlanPolicy returns the ENFORCED half, as the snapshot a review version
// records.
//
// Only controls that are actually active are recorded as enforced. A control
// the user enabled in a workspace that cannot honor it is reported in
// Unavailable instead, so a later audit can explain the plan's behavior without
// having to reconstruct which workspace it ran in (FR-127, FR-144).
func (b *ServerBuilder) resolvePlanPolicy(ctx context.Context, workspaceID string) workspaceplan.PolicySnapshot {
	if b.workspacePlanPolicy == nil {
		return workspaceplan.PolicySnapshot{}
	}
	policy, _ := b.workspacePlanPolicy.Policy(ctx, workspaceID)

	snapshot := workspaceplan.PolicySnapshot{
		Profile:     policy.Profile,
		Preset:      policy.Preset,
		Enforced:    policy.ActiveControls(),
		Unavailable: policy.UnavailableControls(),
		CapturedAt:  time.Now().UTC(),
	}
	if mode, found := policy.Control(workspacesettings.ControlExecutionMode); found && mode.Active() {
		snapshot.ExecutionMode = workspaceplan.ExecutionMode(
			executionModeFor(ctx, b, workspaceID))
	}
	return snapshot
}

// executionModeFor reads the workspace's default execution mode. It is a
// separate lookup because the mode is a setting value, not a boolean control,
// and squeezing it into the on/off control map would lose it.
func executionModeFor(_ context.Context, b *ServerBuilder, workspaceID string) string {
	if b.workspaceStore == nil {
		return string(workspaceplan.ExecutionStepThrough)
	}
	ws, err := b.workspaceStore.Get(workspaceID)
	if err != nil || ws == nil {
		return string(workspaceplan.ExecutionStepThrough)
	}
	mode := workspacesettings.Extract(ws.SharedData).Planning.DefaultExecutionMode
	if mode == string(workspaceplan.ExecutionAuto) {
		return mode
	}
	return string(workspaceplan.ExecutionStepThrough)
}

// workspacePlanSlotStore returns durable slot arbitration when a database is
// available, and in-memory arbitration otherwise.
//
// The in-memory fallback still enforces one plan per workspace; it just does
// not survive a restart. Degrading to "no arbitration" would be worse than
// degrading to "arbitration that forgets".
func (b *ServerBuilder) workspacePlanSlotStore() workspaceplan.SlotStore {
	if b.sessionStore != nil {
		return workspaceplan.NewSQLiteSlotStore(b.sessionStore.DB())
	}
	return workspaceplan.NewMemorySlotStore()
}

// attachWorkspacePlanExecutor wires plan execution once the workspace store and
// run bridge exist. Like the materializer, it must run after those are real
// rather than during handler construction.
func (b *ServerBuilder) attachWorkspacePlanExecutor() {
	if b.workspacePlanHandler == nil || b.workspacePlanService == nil || b.workspaceStore == nil {
		return
	}

	// One plan executes per workspace. The slot sits ABOVE the task executor:
	// standalone tasks keep their own scheduler, global maximum, and provider
	// limits entirely untouched (FR-100, FR-106).
	slots := workspaceplan.NewSlotCoordinator(
		b.workspacePlanSlotStore(),
		workspaceplan.WithSlotOwner("ori-server"),
	)
	b.workspacePlanSlots = slots

	// Progress is derived from live task state every time a plan is read, so
	// the plan never carries a stale copy of how its work is going (FR-12).
	b.workspacePlanService.SetProgressSource(
		workspaceplan.NewTaskProgressSource(b.workspaceStore,
			workspaceplan.WithSlotReporter(slots)))

	options := []workspaceplan.ExecutorOption{
		workspaceplan.WithTaskMutator(planTaskMutator{store: b.workspaceStore}),
		workspaceplan.WithSlots(slots),
		// Gates read the live roster on every dispatch, not a snapshot: an
		// agent removed mid-plan must stop the next step, not the one after
		// the server restarts (FR-118).
		//
		// The precondition checker resolves the workspace's effective policy
		// at check time too, so a branch switched in a terminal takes effect on
		// the next dispatch rather than at the next restart (FR-136).
		workspaceplan.WithGates(b.workspacePlanPreflight(), b.resolvePlanAvailability),
	}
	if b.workspaceRunBridge != nil {
		options = append(options, workspaceplan.WithDispatcher(
			planTaskDispatcher{bridge: b.workspaceRunBridge}))
	}

	b.workspacePlanExecutor = workspaceplan.NewExecutor(
		b.workspacePlanService, b.workspaceStore, options...)
	b.workspacePlanHandler.SetExecutor(b.workspacePlanExecutor)
	b.workspacePlanHandler.SetSlots(slots)

	// Chat can now open a durable Plan for work it must not approve inline.
	if b.chatHandler != nil {
		b.chatHandler.SetPlanOpener(chatPlanOpener{service: b.workspacePlanService})
	}

	// Guidance and enforced policy are wired HERE rather than during handler
	// construction, for the same reason as everything else in this function:
	// b.workspacePlanPolicy does not exist until the workspace store does. Two
	// separate setters keep the halves separate all the way to the handler.
	b.workspacePlanHandler.SetGuidanceResolver(b.resolvePlanGuidance)
	b.workspacePlanHandler.SetPolicyResolver(b.resolvePlanPolicy)

	// Automatic execution runs in the background, so it outlives the request
	// that started it and must be stopped explicitly at shutdown.
	b.workspacePlanAuto = workspaceplan.NewAutoRunner(b.workspacePlanExecutor)
	b.workspacePlanHandler.SetAutoRunner(b.workspacePlanAuto)
	b.server.workspacePlanAuto = b.workspacePlanAuto
}

// resolvePlanningProvider returns the provider and model to plan with.
//
// Planning uses the configured system model: it is a structured-output task
// with no conversation, so it should not consume a workspace agent's model
// choice. Any failure here means generation is unavailable right now, which
// the caller reports distinctly from a failure — everything that does not need
// a model keeps working (FR-58, FR-177).
func (b *ServerBuilder) resolvePlanningProvider(context.Context) (llm.Provider, string, error) {
	if b.llmFactory == nil || b.configManager == nil {
		return nil, "", fmt.Errorf("no planning model is configured")
	}

	providerName, modelName := b.configManager.GetSystemModel()
	result, err := b.llmFactory.GetSystemModelProvider(providerName, modelName)
	if err != nil {
		return nil, "", err
	}
	if result == nil || result.Provider == nil {
		return nil, "", fmt.Errorf("no planning model is configured")
	}
	return result.Provider, result.Model, nil
}

// resolvePlanAvailability reports the agents a workspace can assign work to.
//
// The distinction between a nil slice and an empty one is load-bearing:
// validation treats nil as "not checked" and empty as "nothing is available".
// Returning empty for a workspace we failed to load would invalidate every
// assignment in it, so an unreadable workspace returns nil instead (FR-46,
// FR-48).
func (b *ServerBuilder) resolvePlanAvailability(_ context.Context, workspaceID string) workspaceplan.ValidationContext {
	if b.workspaceStore == nil {
		return workspaceplan.ValidationContext{}
	}
	workspace, err := b.workspaceStore.Get(workspaceID)
	if err != nil || workspace == nil {
		return workspaceplan.ValidationContext{}
	}

	// An empty (non-nil) slice is the honest answer for a workspace that
	// genuinely has no agents: the planner is then told to leave every
	// assignee empty rather than inventing one.
	//
	// Names are deduplicated because a workspace may hold several instances of
	// one agent type, and the planner assigns by name.
	seen := make(map[string]struct{}, len(workspace.AgentInstances))
	agents := make([]string, 0, len(workspace.AgentInstances))
	for _, instance := range workspace.AgentInstances {
		if instance.Name == "" {
			continue
		}
		if _, exists := seen[instance.Name]; exists {
			continue
		}
		seen[instance.Name] = struct{}{}
		agents = append(agents, instance.Name)
	}
	return workspaceplan.ValidationContext{AvailableAgents: agents}
}
