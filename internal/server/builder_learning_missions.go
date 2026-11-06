package server

import (
	"context"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/learningmissionshttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// One service owns all learning effects. No model/tool registration is made.
func (b *ServerBuilder) initializeLearningMissions() {
	if b.sessionStore == nil || b.sessionStore.DB() == nil || b.workspaceStore == nil || b.workspaceRunStore == nil ||
		b.taskHandler == nil || b.workspaceRunBridge == nil || b.resetWork == nil || b.userProvider == nil || b.orchestrationHandler == nil {
		return
	}
	b.taskHandler.SetRecipeRunner(workspace.LearningDraftRecipe, learningDraftRunner{store: b.workspaceStore})
	store := learningmissions.NewSQLiteStore(b.sessionStore.DB())
	tickets := workspace.NewTicketService(b.workspaceStore)
	tickets.SetEventBus(b.eventBus)
	source := learningMissionSource{workspaces: b.workspaceStore, tickets: tickets, runs: b.workspaceRunStore, learning: store,
		recipeAvailable: func() bool { return b.taskHandler.CanRunRecipe(workspace.LearningDraftRecipe) }}
	source.checkAssignee = func(ctx context.Context, id, assignee string) error {
		if workspace.RequireWorkspaceExecution(ctx, b.workspaceStore, id, false) != nil {
			return learningmissions.ErrUnavailable
		}
		ws, err := b.workspaceStore.Get(id)
		if err != nil || !ws.HasAgent(assignee) {
			return learningmissions.ErrUnavailable
		}
		task := workspace.Task{WorkspaceID: id, To: assignee, Context: map[string]any{workspace.TaskContextFirstTaskRecipe: workspace.LearningDraftRecipe}}
		if executor, reason := b.taskHandler.RecipeExecutorDisclosure(task); executor == nil || reason != "" {
			return learningmissions.ErrUnavailable
		}
		return nil
	}
	source.checkWorkspace = func(ctx context.Context, id string) error {
		ws, err := b.workspaceStore.Get(id)
		if err != nil {
			return learningmissions.ErrUnavailable
		}
		for _, agent := range ws.AgentInstances {
			if source.checkAssignee(ctx, id, agent.Name) == nil {
				return nil
			}
		}
		return learningmissions.ErrUnavailable
	}
	service := learningmissions.NewService(store, b.resetWork, source)
	handler := &learningMissionHandler{Handler: learningmissionshttp.NewHandler(service, b.userProvider), source: source,
		users: b.userProvider, executor: b.taskHandler}
	b.learningMissions = service
	b.learningMissionsHandler = handler
	b.orchestrationHandler.SetTicketStartReview(handler.startReview)
	b.workspaceRunBridge.SetRunReservation(func(ctx context.Context, task workspace.Task, runID string) error {
		return reserveLearningRun(ctx, service, store, source, task, runID)
	})
	reconcile := func(workspaceID string) {
		ctx := context.Background()
		owner, err := b.userProvider.CurrentUserID(ctx)
		if err == nil {
			err = service.Reconcile(ctx, owner, workspaceID)
		}
		if err != nil {
			logger.Debug("Learning references could not be reconciled", logger.Fields{"reason": "references_unavailable"})
		}
	}
	if b.eventBus != nil {
		b.eventBus.Subscribe(func(event workspace.Event) { reconcile(event.WorkspaceID) }, func(event workspace.Event) bool {
			switch event.Type {
			case workspace.EventTicketCreated, workspace.EventTicketUpdated, workspace.EventTicketStateChanged, workspace.EventTicketDeleted, workspace.EventTaskCompleted, workspace.EventTaskFailed:
				return true
			}
			return false
		})
	}
	reconcile("") // one bounded lookup on startup, never generation/execution
}

// Missing, stopped, cleared or already-pinned learning is ordinary work. Only
// an eligible first attempt reserves learning evidence; it never adopts retries.
func reserveLearningRun(ctx context.Context, service *learningmissions.Service, store *learningmissions.SQLiteStore, source learningMissionSource, task workspace.Task, runID string) error {
	binding, ok := workspace.LearningBinding(task)
	if !ok || workspace.FirstTaskRecipe(&task) != workspace.LearningDraftRecipe || task.Context[workspace.TaskContextReviewedExecutor] == nil {
		return nil
	}
	i, err := store.Instance(ctx, binding.OwnerID, binding.InstanceID)
	if err != nil {
		if learningmissions.IsUnavailable(err) {
			return nil
		}
		return err
	}
	if i.Epoch != binding.Epoch || i.State != learningmissions.Active || i.Progress.RunID != "" {
		return nil
	}
	if i.Progress.TicketID != task.ID || i.Progress.InputDigest != learningDigest([]byte(task.Details)) || source.MatchTicket(ctx, binding.OwnerID, i) != nil {
		// Editing the Ticket does not widen its recipe, but it is no longer the
		// accepted practice input. Do not bind an unrelated attempt to learning.
		return nil
	}
	err = service.ReserveRun(ctx, binding.OwnerID, i.ID, task.ID, runID, binding.Epoch)
	if err != nil {
		// Stop/clear can win between the read above and reservation. That
		// revokes practice tracking, not the already reviewed ordinary start.
		after, readErr := store.Instance(ctx, binding.OwnerID, binding.InstanceID)
		if learningmissions.IsUnavailable(readErr) || (readErr == nil && (after.Epoch != binding.Epoch || after.State != learningmissions.Active || after.Progress.RunID != "")) {
			return nil
		}
	}
	return err
}

type learningMissionHandler struct {
	*learningmissionshttp.Handler
	source   learningMissionSource
	users    userprofile.UserProvider
	executor *workspace.LLMTaskHandler
}

func (h *learningMissionHandler) Register(mux *http.ServeMux) {
	h.Handler.Register(mux)
	mux.HandleFunc("GET /api/workspaces/{studioID}/tickets/{ticketID}/execution", h.execution)
}

func (h *learningMissionHandler) execution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := learningmissionshttp.LocalOwner(r, h.users, false)
	if err != nil {
		http.Error(w, "unavailable", http.StatusForbidden)
		return
	}
	ws, err := h.source.owned(owner, r.PathValue("studioID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	task, err := ws.GetTask(r.PathValue("ticketID"))
	if err != nil || workspace.FirstTaskRecipe(task) != workspace.LearningDraftRecipe {
		http.NotFound(w, r)
		return
	}
	if workspace.RequireWorkspaceExecution(r.Context(), h.source.workspaces, ws.ID, false) != nil || h.executor.RecipeReadiness(*task) != "" {
		http.Error(w, "execution_unavailable", http.StatusConflict)
		return
	}
	executor, reason := h.executor.RecipeExecutorDisclosure(*task)
	if executor == nil || reason != "" {
		http.Error(w, "execution_unavailable", http.StatusConflict)
		return
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, struct {
		Executor   *workspace.RecipeExecutor `json:"executor"`
		Input      string                    `json:"input"`
		Disclosure string                    `json:"disclosure"`
	}{executor, task.Details, "One isolated model call, up to 1200 output tokens and 30 seconds. Only this input is sent to the assigned model; provider charges may apply. No tools, files or external actions. Starting work is separate from starting learning."})
}

func (h *learningMissionHandler) startReview(r *http.Request, studioID, ticketID, assignee, digest string) (func() error, error) {
	ws, err := h.source.workspaces.Get(studioID)
	if err != nil {
		return nil, learningmissions.ErrUnavailable
	}
	task, err := ws.GetTask(ticketID)
	if err != nil {
		return nil, learningmissions.ErrUnavailable
	}
	if workspace.FirstTaskRecipe(task) != workspace.LearningDraftRecipe {
		return nil, nil
	}
	owner, err := learningmissionshttp.LocalOwner(r, h.users, true)
	if err != nil {
		return nil, err
	}
	if _, err := h.source.owned(owner, studioID); err != nil {
		return nil, err
	}
	executor, reason := h.executor.RecipeExecutorDisclosure(*task)
	if executor == nil || reason != "" || digest != executor.Digest || (assignee != "" && assignee != task.To) {
		return nil, learningmissions.ErrConflict
	}
	return func() error {
		return h.executor.ReviewLearningExecutor(r.Context(), studioID, ticketID, assignee, digest)
	}, nil
}
