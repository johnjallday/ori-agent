package workspacerun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/userprofile"
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
// already wired for tool calling, MCP/skill resolution, and trace capture.
// Reusing that machinery for missions means we get the autonomy gate plus
// tool plumbing "for free" — we just have to feed the gate the workspace's
// AutonomyPolicy and the binding side-effect classifications (done elsewhere).
type MissionBridge struct {
	store            Store
	service          *Service
	workspaceStore   workspace.Store
	agents           agentStoreReader
	opportunityStore workspace.OpportunityStore
	backlog          *workspace.BacklogService
	maxOpenInContext int           // cap on how many opportunities we feed into the prompt
	executionTimeout time.Duration // upper bound on a single mission run
	// curatorProtection reads a File Janitor folder's stored protection fact
	// for its backup precondition. Optional; set once at startup.
	curatorProtection filecuratorguidance.ProtectionReader
}

// SetCuratorProtectionReader lets a File Janitor workspace's goal runs carry
// its folder's backup precondition (backup-first FR9). The reader needs the
// Today scope, which exists only after the bridge, so it is set here rather
// than in MissionBridgeConfig. Unset, goal runs are what they were without it.
// Startup wiring only.
func (b *MissionBridge) SetCuratorProtectionReader(reader filecuratorguidance.ProtectionReader) {
	if b != nil {
		b.curatorProtection = reader
	}
}

// missionPreconditions are the host facts a goal run must answer first. A
// File Janitor workspace whose folder has no observed backup has one, unless
// its task is already open or the user dismissed it. A failed or foreign read
// is none: the run goes on as it would without the reader.
func (b *MissionBridge) missionPreconditions(ctx context.Context, ws *workspace.Workspace) []workspace.MissionPrecondition {
	if b.curatorProtection == nil {
		return nil
	}
	if _, ok := ws.FileCurator(); !ok {
		return nil
	}
	userID := ws.OwnerUserID
	if userID == "" {
		userID = userprofile.LocalUserID
	}
	fact, err := b.curatorProtection.ReadProtection(ctx, userID, ws.ID)
	if err != nil {
		logger.Warn("mission bridge: protection fact could not be read; no precondition", logger.Fields{
			"workspace_id": ws.ID, "error": err,
		})
		return nil
	}
	if fact.WorkspaceID != ws.ID {
		return nil
	}
	precondition, ok := workspace.BackupPrecondition(ws.Name, fact)
	if !ok {
		return nil
	}
	key := workspace.NormalizeSuggestionTitle(precondition.SuggestedTitle)
	if _, open := workspace.OpenWorkTitles(ws)[key]; open {
		return nil
	}
	if _, dismissed := ws.DismissedSuggestionKeys()[key]; dismissed {
		return nil
	}
	return []workspace.MissionPrecondition{precondition}
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
	// Backlog writes suggested tasks as Backlog tickets. Optional: nil uses a
	// bare service over WorkspaceStore (no events, no BACKLOG.md render).
	Backlog *workspace.BacklogService
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
	backlog := cfg.Backlog
	if backlog == nil {
		backlog = workspace.NewBacklogService(cfg.WorkspaceStore)
	}
	return &MissionBridge{
		store:            cfg.RunStore,
		service:          cfg.Service,
		workspaceStore:   cfg.WorkspaceStore,
		agents:           cfg.Agents,
		opportunityStore: cfg.OpportunityStore,
		backlog:          backlog,
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
	runID, execute, err := b.StartMissionRun(ctx, workspaceID, cycleOrdinal, opts)
	if err != nil {
		return runID, err
	}
	return runID, execute()
}

// StartMissionRun prepares a goal run and creates its run record, returning
// the run ID at once and a function that executes the run to completion
// (model call, reading the answer, storing suggestions, recording the run).
// The scheduler calls execute on its own goroutine, which is what lets Run now
// answer 202 with a run ID instead of holding the request open for minutes
// (workspace-goal-suggestions FR26). ctx bounds the run; execute applies the
// bridge's execution timeout on top of it.
func (b *MissionBridge) StartMissionRun(ctx context.Context, workspaceID string, cycleOrdinal int, opts workspace.MissionRunOptions) (string, func() error, error) {
	if b == nil {
		return "", nil, fmt.Errorf("mission bridge not configured")
	}
	ws, err := b.workspaceStore.Get(workspaceID)
	if err != nil {
		return "", nil, fmt.Errorf("load workspace: %w", err)
	}
	agentName := strings.TrimSpace(ws.EntryAgentName())
	if agentName == "" {
		return "", nil, fmt.Errorf("workspace %q has no entry agent; mission cannot run", workspaceID)
	}

	// Compose the system prompt: existing manager prompt + mission framing.
	// If the global agent store knows this agent we use its system prompt;
	// otherwise we proceed with an empty base (the mission framing alone
	// is still useful, though less personalized).
	var basePrompt string
	if b.agents != nil {
		// Resolved so an entry-agent reference stored under a retired assistant
		// name still contributes its prompt instead of silently falling back to
		// the less personalized mission framing alone (FR57).
		if ag, _, ok := store.ResolveAgent(b.agents, agentName); ok {
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
	openWork, recentlyCompleted := workspace.MissionWorkSnapshot(ws)

	origin := missionRunOrigin(opts)
	autonomy := workspace.EffectiveAutonomyPolicy(ws.AutonomyPolicy)
	if origin == workspace.MissionOriginHireFirstLook {
		// A hired agent's first look only reads, whatever the workspace
		// allows later: the user agreed to a look, not to changes.
		autonomy = workspace.AutonomyWatch
	}
	// The ordering rule applies to ordinary goal runs only (backup-first FR14).
	var preconditions []workspace.MissionPrecondition
	if origin != workspace.MissionOriginHireFirstLook {
		preconditions = b.missionPreconditions(ctx, ws)
	}

	systemPrompt := workspace.BuildMissionSystemPrompt(workspace.MissionPromptInputs{
		WorkspaceManagerPrompt: basePrompt,
		Mission:                ws.Mission,
		CycleOrdinal:           cycleOrdinal,
		OpenOpportunities:      openOpps,
		TriggeringEvent:        opts.Event,
		AutonomyPolicy:         autonomy,
		AgentNames:             missionAgentNames(ws),
		OpenWork:               openWork,
		RecentlyCompleted:      recentlyCompleted,
		DismissedTitles:        ws.DismissedSuggestionTitles(),
		Origin:                 origin,
		Preconditions:          preconditions,
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
			workspace.MissionTaskContextOriginKey:    workspace.MissionTaskContextOriginValue,
			workspace.MissionTaskContextPolicyKey:    string(autonomy),
			workspace.MissionTaskContextOrdinalKey:   cycleOrdinal,
			workspace.MissionTaskContextRunOriginKey: origin,
		},
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return "", nil, fmt.Errorf("marshal mission task payload: %w", err)
	}
	rawPayload := RawConfig(payload)
	cfgPayload, err := json.Marshal(OriAgentExecutorConfig{TaskPayload: &rawPayload})
	if err != nil {
		return "", nil, fmt.Errorf("marshal ori_agent executor config: %w", err)
	}
	rawConfig := RawConfig(cfgPayload)

	policy := buildMissionRunPolicy(autonomy)

	run, err := b.service.CreateRun(ctx, workspaceID, CreateRunRequest{
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
		createdAt := time.Now()
		_ = b.workspaceStore.Update(workspaceID, func(w *workspace.Workspace) error {
			workspace.ApplyMissionRunOutcome(w, workspace.MissionRunOutcome{StartedAt: createdAt, Succeeded: false, HoldCadence: opts.HoldCadence})
			workspace.RecordMissionRun(w, workspace.MissionRunRecord{
				Origin: missionRunOrigin(opts), StartedAt: createdAt, FinishedAt: createdAt, Error: err.Error(),
			})
			return nil
		})
		return "", nil, fmt.Errorf("create mission run: %w", err)
	}

	// The goal's charter parts as this run's prompt saw them, for the record.
	goalParts := workspace.ParseGoalCharter(ws.Mission).Present
	execute := func() error {
		return b.executeMissionRun(ctx, workspaceID, run.ID, opts, preconditions, goalParts)
	}
	return run.ID, execute, nil
}

// executeMissionRun runs a created goal run to completion and records it.
// preconditions are those its prompt carried; goalParts the charter parts its
// goal had.
func (b *MissionBridge) executeMissionRun(ctx context.Context, workspaceID, runID string, opts workspace.MissionRunOptions, preconditions []workspace.MissionPrecondition, goalParts []string) error {
	runCtx, cancel := context.WithTimeout(ctx, b.executionTimeout)
	defer cancel()

	started := time.Now()
	execErr := b.service.ExecuteRun(runCtx, workspaceID, runID)

	// Read the resulting artifact regardless of execErr — partial output
	// can still be parsed and is more useful than nothing.
	artifacts, listErr := b.store.ListArtifacts(ctx, workspaceID, runID)
	if listErr != nil {
		logger.Warn("mission bridge: list artifacts after run", logger.Fields{
			"workspace_id": workspaceID, "run_id": runID, "error": listErr,
		})
	}
	result, _ := taskResultFromArtifacts(artifacts)

	record := workspace.MissionRunRecord{RunID: runID, Origin: missionRunOrigin(opts), StartedAt: started}
	// A run that failed before it answered has nothing to read; one that
	// answered is read even when execution reported an error, because
	// partial output is more useful than nothing.
	var parseErr error
	stored := false
	if execErr == nil || strings.TrimSpace(result) != "" {
		parseErr = b.storeMissionOutput(workspaceID, runID, result, &record, preconditions)
		stored = parseErr == nil
	}
	if record.Assessment != "" {
		record.AssessmentParts = goalParts
	}
	if !stored {
		b.storeUnansweredPreconditions(workspaceID, runID, &record, preconditions)
	}
	if parseErr != nil {
		logger.Warn("mission bridge: goal run answer could not be read", logger.Fields{
			"workspace_id": workspaceID, "run_id": runID, "error": parseErr,
		})
		b.fileUnreadableAnswerFinding(workspaceID, runID, parseErr, record.OutputTail)
	}

	// A run counts as successful only when it executed AND its answer could
	// be read; an unreadable answer is a failure the user can see (FR21).
	succeeded := execErr == nil && parseErr == nil
	record.Succeeded = succeeded
	record.FinishedAt = time.Now()
	switch {
	case execErr != nil:
		record.Error = execErr.Error()
	case parseErr != nil:
		record.Error = parseErr.Error()
	}
	_ = b.workspaceStore.Update(workspaceID, func(w *workspace.Workspace) error {
		workspace.ApplyMissionRunOutcome(w, workspace.MissionRunOutcome{
			StartedAt:   started,
			Succeeded:   succeeded,
			HoldCadence: opts.HoldCadence,
		})
		workspace.RecordMissionRun(w, record)
		// A first look reports to the assistant instead; it changes nothing
		// beyond the Backlog and its own record.
		if record.TasksCreated > 0 && record.Origin != workspace.MissionOriginHireFirstLook {
			workspace.AppendOpportunity(w, suggestionSummaryRow(w, record))
		}
		return nil
	})

	if execErr != nil {
		return fmt.Errorf("execute mission run: %w", execErr)
	}
	if parseErr != nil {
		return fmt.Errorf("read goal run answer: %w", parseErr)
	}
	return nil
}

// storeMissionOutput reads a run's answer and stores what it suggests: task
// items as Backlog tickets (deduped, capped, unassigned), finding items as
// Action Center opportunities (capped; the store merges recurring titles).
// It fills the record's counts and returns an error only when the answer
// could not be read at all, after recording its tail for the user. A ticket
// that meets one of the run's preconditions is tagged with it.
func (b *MissionBridge) storeMissionOutput(workspaceID, runID, result string, record *workspace.MissionRunRecord, preconditions []workspace.MissionPrecondition) error {
	output, err := workspace.ParseMissionRunOutput(workspaceID, runID, result)
	if err != nil {
		record.ParseStatus = workspace.MissionParseFailed
		record.OutputTail = workspace.MissionOutputTail(result)
		return err
	}
	record.ParseStatus = output.ParseStatus()
	record.ItemsRejected = output.Rejected
	// Every origin keeps its assessment; Summary and NextWhy below are unchanged.
	record.Assessment = output.Assessment
	taskLimit, findingLimit := workspace.MissionSuggestionLimits(record.Origin)

	findings := output.Findings
	if len(findings) > findingLimit {
		record.DroppedByCap += len(findings) - findingLimit
		findings = findings[:findingLimit]
	}
	for _, opp := range findings {
		_, merged, err := b.opportunityStore.Upsert(opp)
		if err != nil {
			logger.Warn("mission bridge: upsert opportunity", logger.Fields{
				"workspace_id": workspaceID, "run_id": runID, "title": opp.Title, "error": err,
			})
			continue
		}
		if !merged {
			record.FindingsCreated++
		}
	}

	known := b.knownSuggestionTitles(workspaceID)
	suggestions, duplicates, dropped := workspace.SelectMissionSuggestions(output.Tasks, known, taskLimit)
	record.DuplicatesSkipped += duplicates
	record.DroppedByCap += dropped
	// A first look says what it found and which task comes next; other runs
	// keep their records as before unless their answer said so itself.
	if record.Origin == workspace.MissionOriginHireFirstLook || output.Summary != "" || output.Next != "" {
		record.Summary = workspace.MissionRunSummary(output, suggestions)
		record.NextWhy = output.NextWhy
	}
	now := time.Now()
	// met holds the preconditions a ticket of this run answers; first, those
	// tickets, which lead the Backlog.
	met := map[string]bool{}
	var first []string
	for _, suggestion := range suggestions {
		reason := workspace.MissionSuggestionContext(suggestion, runID)
		precondition, isPrecondition := workspace.MatchMissionPrecondition(suggestion.Title, preconditions)
		if isPrecondition {
			reason = workspace.TagMissionPrecondition(reason, precondition, workspace.MissionSuggestionSourceModel)
		}
		if created, ok := b.createSuggestedTicket(workspaceID, runID, now, record, suggestion, reason); ok && isPrecondition {
			met[precondition.Kind] = true
			first = append(first, created.ID)
		}
	}
	first = append(first, b.storeHostPreconditions(workspaceID, runID, now, record, preconditions, met, known)...)
	b.placeBacklogFirst(workspaceID, runID, first)
	return nil
}

// storeUnansweredPreconditions is a run with no answer to store, because it
// failed or answered unreadably: its unmet preconditions are still suggested,
// since they are the host's own facts and need no model.
func (b *MissionBridge) storeUnansweredPreconditions(workspaceID, runID string, record *workspace.MissionRunRecord, preconditions []workspace.MissionPrecondition) {
	if len(preconditions) == 0 {
		return
	}
	first := b.storeHostPreconditions(workspaceID, runID, time.Now(), record, preconditions, nil, b.knownSuggestionTitles(workspaceID))
	b.placeBacklogFirst(workspaceID, runID, first)
}

// storeHostPreconditions stores one ticket for each unmet precondition no
// ticket of this run met: compiled code storing a suggestion, which every
// autonomy level allows (FR11). Not when its task is open or dismissed
// (known), whatever the answer said. It returns the tickets it made.
func (b *MissionBridge) storeHostPreconditions(workspaceID, runID string, now time.Time, record *workspace.MissionRunRecord, preconditions []workspace.MissionPrecondition, met map[string]bool, known map[string]struct{}) []string {
	var made []string
	for _, precondition := range preconditions {
		if met[precondition.Kind] {
			continue
		}
		if _, skip := known[workspace.NormalizeSuggestionTitle(precondition.SuggestedTitle)]; skip {
			continue
		}
		suggestion := workspace.HostPreconditionSuggestion(precondition)
		reason := workspace.TagMissionPrecondition(workspace.MissionSuggestionContext(suggestion, runID), precondition, workspace.MissionSuggestionSourceHost)
		if created, ok := b.createSuggestedTicket(workspaceID, runID, now, record, suggestion, reason); ok {
			made = append(made, created.ID)
			logger.Info("mission bridge: stored a precondition task the run did not answer", logger.Fields{
				"workspace_id": workspaceID, "run_id": runID, "precondition": precondition.Kind,
			})
		}
	}
	return made
}

// knownSuggestionTitles are the titles a run must not suggest again: open
// work and what the user dismissed (FR18).
func (b *MissionBridge) knownSuggestionTitles(workspaceID string) map[string]struct{} {
	ws, err := b.workspaceStore.Get(workspaceID)
	if err != nil {
		return map[string]struct{}{}
	}
	known := workspace.OpenWorkTitles(ws)
	for key := range ws.DismissedSuggestionKeys() {
		known[key] = struct{}{}
	}
	return known
}

// createSuggestedTicket stores one suggestion as a Backlog ticket and counts it.
func (b *MissionBridge) createSuggestedTicket(workspaceID, runID string, now time.Time, record *workspace.MissionRunRecord, suggestion workspace.MissionTaskSuggestion, reason map[string]any) (*workspace.Task, bool) {
	created, err := b.backlog.Create(workspace.BacklogCreateInput{
		WorkspaceID: workspaceID,
		Description: missionTicketTitle(suggestion.Title),
		Details:     workspace.MissionSuggestionDetails(suggestion, runID, now),
		Priority:    workspace.MissionSuggestionPriority(suggestion.Priority),
		SourceType:  workspace.TicketSourceGoalSuggestion,
		SourceID:    runID,
		Context: map[string]any{
			workspace.MissionSuggestionContextKey: reason,
		},
	})
	if err != nil {
		logger.Warn("mission bridge: create suggested ticket", logger.Fields{
			"workspace_id": workspaceID, "run_id": runID, "title": suggestion.Title, "error": err,
		})
		return nil, false
	}
	record.TasksCreated++
	return created, true
}

// placeBacklogFirst moves a run's precondition tickets to the top of this
// workspace's Backlog order (FR12), keeping the rest in their order. Order is
// a rank, not priority, and Reorder takes the whole list. A failure leaves
// the order as Create made it; the tickets still carry high priority.
func (b *MissionBridge) placeBacklogFirst(workspaceID, runID string, first []string) {
	if len(first) == 0 {
		return
	}
	items, err := b.backlog.List(workspaceID, false)
	if err != nil {
		logger.Warn("mission bridge: list Backlog to place precondition first", logger.Fields{"workspace_id": workspaceID, "run_id": runID, "error": err})
		return
	}
	leading := map[string]bool{}
	for _, id := range first {
		leading[id] = true
	}
	order := append([]string{}, first...)
	for _, item := range items {
		if item.OwningWorkspaceID == workspaceID && !leading[item.Task.ID] {
			order = append(order, item.Task.ID)
		}
	}
	if _, err := b.backlog.Reorder(workspaceID, order); err != nil {
		logger.Warn("mission bridge: place precondition first in Backlog", logger.Fields{"workspace_id": workspaceID, "run_id": runID, "error": err})
	}
}

// unreadableAnswerFindingTitle is stable so repeated failures merge into one
// Action Center finding instead of flooding the inbox, the same way trigger
// failure findings do.
const unreadableAnswerFindingTitle = "Goal run answer could not be read"

// fileUnreadableAnswerFinding surfaces an unreadable goal-run answer in the
// Action Center (FR21).
func (b *MissionBridge) fileUnreadableAnswerFinding(workspaceID, runID string, parseErr error, tail string) {
	evidence := parseErr.Error()
	if excerpt := strings.TrimSpace(tail); excerpt != "" {
		if len(excerpt) > 300 {
			excerpt = excerpt[len(excerpt)-300:]
		}
		evidence += "\nEnd of the answer: " + excerpt
	}
	now := time.Now()
	if _, _, err := b.opportunityStore.Upsert(workspace.Opportunity{
		WorkspaceID:       workspaceID,
		SourceRunID:       runID,
		Title:             unreadableAnswerFindingTitle,
		Summary:           "The last goal run returned an answer Ori could not read, so it suggested nothing.",
		Evidence:          evidence,
		Priority:          "medium",
		Confidence:        "high",
		Status:            workspace.OpportunityNew,
		RecommendedAction: "Open Goal Settings to see the run's answer, then run it again or switch the workspace's model.",
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		logger.Warn("mission bridge: file unreadable-answer finding", logger.Fields{
			"workspace_id": workspaceID, "run_id": runID, "error": err,
		})
	}
}

// suggestionSummaryRow is the Action Center row for a run that suggested
// tasks: it keeps suggestions visible across workspaces and links to the
// workspace's Backlog drawer, where the tickets themselves live (FR22).
func suggestionSummaryRow(ws *workspace.Workspace, record workspace.MissionRunRecord) workspace.Opportunity {
	noun := "tasks"
	if record.TasksCreated == 1 {
		noun = "task"
	}
	name := strings.TrimSpace(ws.Name)
	if name == "" {
		name = "this workspace"
	}
	var titles []string
	for _, task := range ws.Tasks {
		if task.SourceType == workspace.TicketSourceGoalSuggestion && task.SourceID == record.RunID {
			titles = append(titles, "- "+task.Description)
		}
	}
	link := "/workspaces/" + url.PathEscape(ws.FolderSlug) + "?panel=backlog"
	if strings.TrimSpace(ws.FolderSlug) == "" {
		link = ""
	}
	return workspace.Opportunity{
		SourceRunID:       record.RunID,
		SourceType:        workspace.OpportunitySourceGoalSuggestions,
		SourceID:          record.RunID,
		SourceLabel:       "Open Backlog",
		SourceURL:         link,
		Title:             fmt.Sprintf("%d suggested %s in %s", record.TasksCreated, noun, name),
		Summary:           "A goal run added suggested tasks to this workspace's Backlog. Promote the ones you want; the rest can stay or go.",
		Evidence:          strings.Join(titles, "\n"),
		Priority:          "low",
		Confidence:        "high",
		RecommendedAction: "Open the Backlog to review the suggested tasks.",
		CreatedAt:         record.FinishedAt,
	}
}

// missionRunOrigin names what started a run for its record.
func missionRunOrigin(opts workspace.MissionRunOptions) string {
	if origin := strings.TrimSpace(opts.Origin); origin != "" {
		return origin
	}
	if opts.Event != nil {
		return workspace.MissionOriginEvent
	}
	return workspace.MissionOriginCadence
}

// missionTicketTitle bounds a suggestion title to what a ticket accepts.
func missionTicketTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= workspace.TicketTitleMaxLength {
		return title
	}
	return string(runes[:workspace.TicketTitleMaxLength-1]) + "…"
}

// missionAgentNames lists the workspace's agents for suggested_agent.
func missionAgentNames(ws *workspace.Workspace) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, instance := range ws.AgentInstances {
		name := strings.TrimSpace(instance.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
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
