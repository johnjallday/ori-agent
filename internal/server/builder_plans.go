package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceplan"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
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

	b.workspacePlanMaterializer = workspaceplan.NewMaterializer(
		b.workspacePlanService,
		b.workspaceStore,
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
		// No precondition checker is wired yet, so a Plan that names an
		// enforced precondition stops automatic dispatch at it and hands the
		// step back to the user. That is the fail-closed direction.
		workspaceplan.WithGates(nil, b.resolvePlanAvailability),
	}
	if b.workspaceRunBridge != nil {
		options = append(options, workspaceplan.WithDispatcher(
			planTaskDispatcher{bridge: b.workspaceRunBridge}))
	}

	b.workspacePlanExecutor = workspaceplan.NewExecutor(
		b.workspacePlanService, b.workspaceStore, options...)
	b.workspacePlanHandler.SetExecutor(b.workspacePlanExecutor)
	b.workspacePlanHandler.SetSlots(slots)

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
