package server

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/dailybriefhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalassistanthttp"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// sessionSourceAdapter adapts session.HybridStore.ListSessions (which
// returns a paginated *session.ListResult) to dailybrief.SessionSource
// (which only needs the bounded item list — Daily Brief always requests a
// small, fixed Limit and never paginates).
type sessionSourceAdapter struct {
	store session.HybridStore
}

func (a *sessionSourceAdapter) ListSessions(ctx context.Context, filter *session.SessionFilter, opts *session.ListOptions) ([]session.SessionListItem, error) {
	result, err := a.store.ListSessions(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Sessions, nil
}

// dailyBriefSchedulerPollInterval balances catch-up promptness (a user who
// opens the app well after their scheduled time should see a fresh brief
// soon) against needless churn; the schedule itself is date/time-grained,
// not sub-minute, so a coarser poll than the 1-minute task scheduler is fine.
const dailyBriefSchedulerPollInterval = 5 * time.Minute

// systemModelChatCompleter adapts the configured system model to
// dailybrief.ChatCompleter, resolving the provider/model fresh on every call
// (not cached at server startup) so a user changing the system model in
// Settings takes effect on the next generation without a restart.
type systemModelChatCompleter struct {
	configManager *config.Manager
	llmFactory    *llm.Factory
}

// personalAssistantModelReader reports capability only; it never returns model
// names, credentials, or provider errors through the PAF API.
type personalAssistantModelReader struct {
	configManager *config.Manager
	llmFactory    *llm.Factory
}

func (r *personalAssistantModelReader) PersonalAssistantModelAvailability() personalassistant.SourceAvailability {
	if r == nil || r.configManager == nil || r.llmFactory == nil {
		return personalassistant.SourceAvailability{
			Status: personalassistant.AvailabilityDependencyError, Reason: "service_unavailable",
		}
	}
	if !r.configManager.IsSystemModelConfigured() {
		return personalassistant.SourceAvailability{
			Status: personalassistant.AvailabilityNotConfigured, Reason: "model_not_configured",
		}
	}
	providerName, modelName := r.configManager.GetSystemModel()
	result, err := r.llmFactory.GetSystemModelProvider(providerName, modelName)
	if err != nil || result.Provider == nil {
		return personalassistant.SourceAvailability{
			Status: personalassistant.AvailabilityUnavailable, Reason: "configured_model_unavailable",
		}
	}
	if checker, ok := result.Provider.(llm.ModelPresenceChecker); ok && !checker.HasModel(result.Model) {
		return personalassistant.SourceAvailability{
			Status: personalassistant.AvailabilityUnavailable, Reason: "configured_model_unavailable",
		}
	}
	return personalassistant.SourceAvailability{Available: true, Status: personalassistant.AvailabilityAvailable}
}

func (c *systemModelChatCompleter) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if c.configManager == nil || c.llmFactory == nil {
		return nil, fmt.Errorf("dailybrief: system model is not configured")
	}
	providerName, modelName := c.configManager.GetSystemModel()
	result, err := c.llmFactory.GetSystemModelProvider(providerName, modelName)
	if err != nil {
		return nil, err
	}
	req.Model = result.Model
	return result.Provider.Chat(ctx, req)
}

// personalHQWorkspaceLister adapts internal/personalhq to
// dailybrief.WorkspaceLister: in v1 there is at most one designated HQ per
// user, and only when it currently resolves (never a stale/invalid
// designation — that's the personalhq repair flow's job, not the
// scheduler's).
type personalHQWorkspaceLister struct {
	service *personalhq.Service
}

func (l *personalHQWorkspaceLister) ListScheduledWorkspaces(ctx context.Context) ([]dailybrief.ScheduledWorkspace, error) {
	if l.service == nil {
		return nil, nil
	}
	status, err := l.service.Status(ctx, userprofile.LocalUserID)
	if err != nil {
		return nil, err
	}
	if !status.Valid {
		return nil, nil
	}
	return []dailybrief.ScheduledWorkspace{{WorkspaceID: status.WorkspaceID, UserID: userprofile.LocalUserID}}, nil
}

// initializeDailyBrief wires the Daily Brief domain (tasks 5/6) into the
// running server: durable storage, a Synthesizer backed by the configured
// system model (falling back to deterministic content when none is
// configured or it fails), a background scheduler driven by the current
// Personal HQ designation, the HTTP handler, and the Action Center
// notification hook. Requires the session store, workspace store, and
// Personal HQ service to already be wired; a no-op otherwise.
func (b *ServerBuilder) initializeDailyBrief() {
	if b.sessionStore == nil || b.workspaceStore == nil || b.personalHQService == nil {
		return
	}
	store := dailybrief.NewSQLiteStore(b.sessionStore.DB())
	opportunityStore := workspace.NewOpportunityStore(b.workspaceStore)
	sessionSource := &sessionSourceAdapter{store: b.sessionStore}
	workspaceStore := b.workspaceStore

	resolver := func(ctx context.Context, req dailybrief.GenerationRequest, cfg dailybrief.Config) (dailybrief.Snapshot, *dailybrief.Revision, error) {
		sources := dailybrief.SnapshotSources{
			Workspaces:    workspaceStore,
			Opportunities: opportunityStore,
			Sessions:      sessionSource,
			// Read lazily off the builder: the mailbox source is wired during
			// vault init, which runs before this resolver is ever invoked.
			Mailbox:   b.dailyBriefMailbox,
			FollowUps: b.followUpService,
		}
		snap := dailybrief.BuildSnapshot(ctx, sources, cfg, req.UserID, time.Now())
		previous, err := store.GetCurrentRevision(ctx, cfg.WorkspaceID)
		if err != nil {
			previous = nil // ErrRevisionNotFound just means no prior brief yet.
		}
		return snap, previous, nil
	}

	synthesizer := &dailybrief.Synthesizer{
		Chat:     &systemModelChatCompleter{configManager: b.configManager, llmFactory: b.llmFactory},
		Resolver: resolver,
	}
	briefService := dailybrief.NewService(store, synthesizer)

	// Action Center notification: fires only for a successful/partial
	// scheduled revision when the user opted in (PRD FR63/FR65). The
	// title embeds the local date so each day's notification is a distinct
	// Action Center item rather than merging into yesterday's via the
	// title-based dedup key (internal/workspace/opportunities.go DedupKey).
	briefService.SetOnRevisionReady(func(cfg dailybrief.Config, rev *dailybrief.Revision) {
		created, err := briefService.RecordNotificationIfEnabled(context.Background(), cfg, rev)
		if err != nil {
			logger.Warn("dailybrief: failed to record notification", logger.Fields{"revision_id": rev.ID, "error": err})
			return
		}
		if !created {
			return
		}
		opp := workspace.Opportunity{
			WorkspaceID:       cfg.WorkspaceID,
			Title:             fmt.Sprintf("Daily Brief ready — %s", rev.LocalDate),
			Summary:           "Your scheduled Daily Brief has been generated.",
			Priority:          "medium",
			Status:            workspace.OpportunityNew,
			RecommendedAction: "Open Home to view your Daily Brief.",
		}
		if _, _, err := opportunityStore.Upsert(opp); err != nil {
			logger.Warn("dailybrief: failed to create action center notification", logger.Fields{"revision_id": rev.ID, "error": err})
			return
		}
		// Bounded, field-only observable event (PRD FR138) — IDs only, no
		// brief prose/summary content.
		logger.Info("dailybrief: notified", logger.Fields{"workspace_id": cfg.WorkspaceID, "revision_id": rev.ID})
	})

	b.dailyBriefService = briefService
	b.dailyBriefHandler = dailybriefhttp.NewHandler(briefService, b.personalHQService, b.userProvider)
	b.dailyBriefScheduler = dailybrief.NewScheduler(briefService, &personalHQWorkspaceLister{service: b.personalHQService}, dailyBriefSchedulerPollInterval)

	// PAF reads aggregate the relationship, HQ, Daily Brief configuration, and
	// model capability through narrow interfaces. Construction happens here so
	// it reuses this exact Daily Brief store rather than creating a parallel
	// routine source.
	b.personalAssistantStore = personalassistant.NewSQLiteStore(b.sessionStore.DB())
	b.personalAssistantService = personalassistant.NewService(
		b.onboardingMgr,
		b.personalAssistantStore,
		b.personalHQService,
		store,
		&personalAssistantModelReader{configManager: b.configManager, llmFactory: b.llmFactory},
	)
	b.personalAssistantHire = personalassistant.NewHireCoordinator(
		b.onboardingMgr, b.personalAssistantStore, b.sessionHandler,
		b.personalHQService, briefService,
	)
	b.personalAssignment = personalassistant.NewAssignmentService(b.personalAssistantStore)
	assignmentTickets := workspace.NewTicketService(b.workspaceStore)
	assignmentTickets.SetEventBus(b.eventBus)
	assignmentWriter := personalassistant.NewCanonicalWriter(assignmentTickets)
	assignmentWriter.SetFollowUpService(b.followUpService)
	b.personalAssignment.SetCanonicalWriter(assignmentWriter)
	b.personalAssignment.SetBriefService(briefService)
	b.personalAssistantHandler = personalassistanthttp.NewHandler(b.personalAssistantService, b.userProvider)
	b.personalAssistantHandler.SetHireService(b.personalAssistantHire)
	b.personalAssistantHandler.SetAssignmentService(b.personalAssignment)
	b.personalAssistantHandler.SetTodayService(personalassistant.NewTodayService(
		b.personalAssistantService, briefService, b.workspaceStore, b.followUpService,
	))
}
