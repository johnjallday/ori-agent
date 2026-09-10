package economy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FarmTask is the slice of a task the economy cares about: what to call it, what
// its cadence is, and what its last run said. Everything else about a task is
// none of this package's business.
type FarmTask struct {
	WorkspaceID     string
	TaskID          string
	Name            string
	Schedule        *workspace.ScheduleConfig
	ScheduleEnabled bool
	// LastSummary is the last run's summary, shown in the harvest popover so a
	// pile can be triaged without opening every result (FR37).
	LastSummary string
}

// TaskSource is the economy's read-only window onto tasks. It is an interface so
// the service's rules can be tested without a workspace store, and so this
// package never gains the ability to modify a task.
type TaskSource interface {
	// Farms lists every task that is a Farm right now.
	Farms() ([]FarmTask, error)
	// Task returns one task, or false when it does not exist.
	Task(workspaceID, taskID string) (FarmTask, bool)
	// CountCompletedTasks counts finished tasks across every workspace. Used
	// only to size the one-time backfill grant (FR32).
	CountCompletedTasks() (int64, error)
}

// SettingsSource reads the two economy settings. Creative mode zeroes every
// cost; the daily energy figure is what the Energy bar fills against.
type SettingsSource interface {
	EconomyCreativeMode() bool
	EconomyDailyEnergyTokens() int64
}

// EnergySource reports how many tokens this machine pushed today, across every
// provider. Display only: nothing in this package ever blocks on it (FR31).
type EnergySource interface {
	TokensUsedToday() int64
}

// Service owns the economy's rules: what earns, what costs, and what the Home
// surface is told.
//
// It is safe to use as a nil pointer. Callers hold a *Service rather than an
// interface precisely so that "the economy is switched off" and "the economy is
// not wired" are the same, silent, do-nothing state at every call site.
type Service struct {
	store    Store
	tasks    TaskSource
	settings SettingsSource
	energy   EnergySource
	now      func() time.Time

	// mu guards the in-memory anti-gaming state only. It is never held across a
	// database call.
	mu            sync.Mutex
	recentMessage recentMessage
	chatHour      hourlyBucket
}

// recentMessage and hourlyBucket mirror the evolution service's anti-gaming
// state (internal/evolution/service.go). The logic is copied rather than shared:
// evolution buckets XP per agent, the economy buckets Craft for the one user,
// and coupling the two would make either one's tuning the other's problem.
type recentMessage struct {
	fingerprint string
	at          time.Time
}

type hourlyBucket struct {
	hourStart time.Time
	awarded   int64
}

// NewService builds the economy service. store is required; every other
// dependency is optional and degrades to a specific, documented behavior:
// without tasks there are no Farms to list, without settings creative mode is
// off and the energy figure is the default, without energy the bar reads zero.
func NewService(store Store, tasks TaskSource, settings SettingsSource, energy EnergySource) *Service {
	return &Service{
		store:    store,
		tasks:    tasks,
		settings: settings,
		energy:   energy,
		now:      time.Now,
	}
}

// SetTaskSource wires the workspace-backed task reader after construction.
// Handlers are built before the workspace store exists in the server builder, so
// this is set late rather than captured early — capturing a nil dependency at
// construction time is a silent no-op, not an error.
func (s *Service) SetTaskSource(tasks TaskSource) {
	if s == nil {
		return
	}
	s.tasks = tasks
}

// SetEnergySource wires the cost tracker after construction, for the same reason.
func (s *Service) SetEnergySource(energy EnergySource) {
	if s == nil {
		return
	}
	s.energy = energy
}

// SetSettingsSource wires settings after construction, for the same reason.
func (s *Service) SetSettingsSource(settings SettingsSource) {
	if s == nil {
		return
	}
	s.settings = settings
}

// SetClock overrides the clock. Tests only.
func (s *Service) SetClock(now func() time.Time) {
	if s == nil || now == nil {
		return
	}
	s.now = now
}

// Available reports whether the economy can do anything at all.
func (s *Service) Available() bool {
	return s != nil && s.store != nil
}

// CreativeMode reports whether every cost is currently waived (FR24).
func (s *Service) CreativeMode() bool {
	if s == nil || s.settings == nil {
		return false
	}
	return s.settings.EconomyCreativeMode()
}

func (s *Service) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// HandleEvent is the whole earning path. It is subscribed to the workspace event
// bus, which delivers on its own goroutine, so it may block on the database
// without slowing anything down — and every write it makes is idempotent, so a
// redelivered event costs nothing.
//
// Failed, timed-out, cancelled, and blocked tasks earn nothing for a structural
// reason rather than a checked one: they publish their own event types, and this
// method only listens for completion (FR9).
func (s *Service) HandleEvent(ev workspace.Event) {
	if !s.Available() {
		return
	}
	ctx := context.Background()
	switch ev.Type {
	case workspace.EventMessageSent:
		s.creditChatMessage(ctx, ev)
	case workspace.EventTaskCompleted:
		s.recordCompletedTask(ctx, ev)
	case workspace.EventTaskDeleted:
		s.dropPendingForDeletedTask(ctx, ev)
	}
}

// SubscribedEventTypes is what the server subscribes this service to.
func SubscribedEventTypes() []workspace.EventType {
	return []workspace.EventType{
		workspace.EventMessageSent,
		workspace.EventTaskCompleted,
		workspace.EventTaskDeleted,
	}
}

// creditChatMessage pays for a chat message the user sent (FR5), subject to the
// hourly cap and the duplicate window (FR6).
func (s *Service) creditChatMessage(ctx context.Context, ev workspace.Event) {
	now := s.clock()
	fingerprint := eventString(ev, "message_fingerprint")

	s.mu.Lock()
	duplicate := s.isDuplicateLocked(fingerprint, now)
	var award int64
	if !duplicate {
		award = s.applyHourlyCapLocked(CraftPerChatMessage, now)
	}
	s.mu.Unlock()

	if award <= 0 {
		return
	}

	if _, err := s.store.Credit(ctx, Entry{
		Resource:    ResourceCraft,
		Amount:      award,
		Reason:      ReasonChat,
		RefKind:     RefKindMessage,
		RefID:       eventReference(ev, now),
		WorkspaceID: ev.WorkspaceID,
	}, now); err != nil {
		logger.Warn("Failed to credit chat Craft", logger.Fields{"error": err})
	}
}

// isDuplicateLocked reports whether this message repeats the previous one inside
// the duplicate window, and records it either way so the next message compares
// against what was actually just sent.
//
// A message with no fingerprint is never a duplicate: an unfingerprinted event
// carries no evidence either way, and refusing to pay on no evidence would
// silently break earning for any producer that has not been updated.
func (s *Service) isDuplicateLocked(fingerprint string, now time.Time) bool {
	if fingerprint == "" {
		return false
	}
	previous := s.recentMessage
	s.recentMessage = recentMessage{fingerprint: fingerprint, at: now}
	if previous.fingerprint == "" {
		return false
	}
	return previous.fingerprint == fingerprint && now.Sub(previous.at) <= ChatDuplicateWindow
}

// applyHourlyCapLocked returns how much of the requested Craft fits under this
// hour's cap, and books it.
func (s *Service) applyHourlyCapLocked(requested int64, now time.Time) int64 {
	if requested <= 0 {
		return 0
	}
	hourStart := now.Truncate(time.Hour)
	if s.chatHour.hourStart != hourStart {
		s.chatHour = hourlyBucket{hourStart: hourStart}
	}
	remaining := ChatCraftHourlyCap - s.chatHour.awarded
	if remaining <= 0 {
		return 0
	}
	awarded := requested
	if awarded > remaining {
		awarded = remaining
	}
	s.chatHour.awarded += awarded
	return awarded
}

// recordCompletedTask turns a finished run into either Craft or a pending
// Harvest, depending on whether the task is a Farm (FR7, FR10).
//
// The classification comes from the event's own `scheduled` key rather than from
// re-reading the task, because by the time this runs the task may already have
// been edited — and because a run should be paid for as the kind of run it was.
// A producer that does not set the key is treated as a hand-run task, which is
// the honest reading of "someone marked this complete".
func (s *Service) recordCompletedTask(ctx context.Context, ev workspace.Event) {
	taskID := eventString(ev, "task_id")
	if taskID == "" {
		return
	}
	now := s.clock()
	runKey := s.runKey(ev, now)

	if !eventBool(ev, "scheduled") {
		if _, err := s.store.Credit(ctx, Entry{
			Resource:    ResourceCraft,
			Amount:      CraftPerManualTask,
			Reason:      ReasonManualTask,
			RefKind:     RefKindTask,
			RefID:       taskID + "@" + runKey,
			WorkspaceID: ev.WorkspaceID,
		}, now); err != nil {
			logger.Warn("Failed to credit manual task Craft", logger.Fields{"task_id": taskID, "error": err})
		}
		return
	}

	// A Farm run is not paid for until the user opens its result (FR10, FR11).
	if _, err := s.store.AddPending(ctx, PendingRun{
		TaskID:      taskID,
		WorkspaceID: ev.WorkspaceID,
		RunKey:      runKey,
		ProducedAt:  now,
	}); err != nil {
		logger.Warn("Failed to record pending Harvest", logger.Fields{"task_id": taskID, "error": err})
	}
}

// runKey identifies one run. The run's own id is preferred; a completion with no
// run record falls back to its timestamp, which is unique enough for a run that
// by definition happened once (FR10).
func (s *Service) runKey(ev workspace.Event, now time.Time) string {
	if runID := eventString(ev, "run_id"); runID != "" {
		return runID
	}
	at := ev.Timestamp
	if at.IsZero() {
		at = now
	}
	return at.UTC().Format(time.RFC3339Nano)
}

// dropPendingForDeletedTask discards a deleted task's uncollected runs without
// paying for them: nobody reviewed that output, and the task it belonged to is
// gone (FR12).
func (s *Service) dropPendingForDeletedTask(ctx context.Context, ev workspace.Event) {
	taskID := eventString(ev, "task_id")
	if taskID == "" {
		return
	}
	if err := s.store.DeletePendingForTask(ctx, taskID); err != nil {
		logger.Warn("Failed to drop pending Harvest for deleted task",
			logger.Fields{"task_id": taskID, "error": err})
	}
}

// AwardQuestCraft pays for an onboarding quest the user just finished.
//
// This is the cold start. A brand-new install earns nothing from the first-run
// backfill, so without it the first Farm is 25 chat messages away — two clock
// hours, once the hourly cap is applied — and the loop the whole feature is
// about cannot be reached in one sitting.
//
// It is NOT a starter grant. Every quest that pays is real hand-work (send a
// request, create a workspace, run a task), which is exactly what Craft is for.
// A quest outside the paying set returns false and writes nothing.
//
// Call this only for a LIVE completion. The progression engine's backfill marks
// an established install's quests complete silently, without firing its
// onComplete callback, which is what stops this from re-paying for history the
// economy's own backfill already accounted for. The ledger reference is the
// quest id, so even a double-delivered completion pays once.
func (s *Service) AwardQuestCraft(ctx context.Context, questID string) (int64, bool) {
	if !s.Available() {
		return 0, false
	}
	questID = strings.TrimSpace(questID)
	amount, rewarded := StarterQuestCraft(questID)
	if !rewarded {
		return 0, false
	}

	inserted, err := s.store.Credit(ctx, Entry{
		Resource: ResourceCraft,
		Amount:   amount,
		Reason:   ReasonQuest,
		RefKind:  RefKindQuest,
		RefID:    questID,
	}, s.clock())
	if err != nil {
		logger.Warn("Failed to credit quest Craft", logger.Fields{"quest": questID, "error": err})
		return 0, false
	}
	if !inserted {
		// Already paid for. Not an error — a quest can only be finished once,
		// and reporting a second award would let a caller toast a phantom one.
		return 0, false
	}
	logger.Info("Quest Craft awarded", logger.Fields{"quest": questID, "craft": amount})
	return amount, true
}

// BankResult is what a harvest returns: how many runs were collected, and the
// balances afterwards, so the HUD can update from one response.
type BankResult struct {
	Banked  int   `json:"banked"`
	Craft   int64 `json:"craft"`
	Harvest int64 `json:"harvest"`
}

// BankPendingForTask collects one Farm's pending runs (FR11, FR26).
//
// Banking nothing is a success. Every path that opens a result modal calls this,
// including the ones that open a result the user has already read, so "nothing
// to collect" is the common case rather than an error.
func (s *Service) BankPendingForTask(ctx context.Context, workspaceID, taskID string) (BankResult, error) {
	if !s.Available() {
		return BankResult{}, ErrStoreUnavailable
	}
	banked, err := s.store.BankPending(ctx, workspaceID, taskID, s.clock())
	if err != nil {
		return BankResult{}, err
	}
	balances, err := s.store.Balances(ctx)
	if err != nil {
		return BankResult{}, err
	}
	return BankResult{Banked: banked, Craft: balances.Craft, Harvest: balances.Harvest}, nil
}

// Energy is the read-only token gauge (FR25, FR31).
type Energy struct {
	UsedToday   int64 `json:"used_today"`
	DailyFigure int64 `json:"daily_figure"`
}

// FarmView is one Farm as Home sees it.
type FarmView struct {
	WorkspaceID    string `json:"workspace_id"`
	TaskID         string `json:"task_id"`
	Name           string `json:"name"`
	Tier           int    `json:"tier"`
	TierName       string `json:"tier_name"`
	PendingHarvest int    `json:"pending_harvest"`
	LastSummary    string `json:"last_summary,omitempty"`
}

// Overview is the GET /api/economy payload (FR25).
type Overview struct {
	Craft              int64          `json:"craft"`
	Harvest            int64          `json:"harvest"`
	CreativeMode       bool           `json:"creative_mode"`
	Energy             Energy         `json:"energy"`
	Farms              []FarmView     `json:"farms"`
	PendingByWorkspace map[string]int `json:"pending_by_workspace"`
}

// Overview builds everything Home needs in one read.
//
// A failure to list Farms is not a failure of the whole call: balances and piles
// still render, and the map simply shows no Farm badges. The HUD going blank
// because one workspace folder is unreadable would be a worse outcome than a
// missing badge.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	if !s.Available() {
		return Overview{}, ErrStoreUnavailable
	}
	balances, err := s.store.Balances(ctx)
	if err != nil {
		return Overview{}, err
	}
	pendingByWorkspace, err := s.store.PendingByWorkspace(ctx)
	if err != nil {
		return Overview{}, err
	}
	pendingByTask, err := s.store.PendingByTask(ctx)
	if err != nil {
		return Overview{}, err
	}

	overview := Overview{
		Craft:              balances.Craft,
		Harvest:            balances.Harvest,
		CreativeMode:       s.CreativeMode(),
		Energy:             s.energySnapshot(),
		Farms:              s.farmViews(pendingByTask),
		PendingByWorkspace: pendingByWorkspace,
	}
	if overview.PendingByWorkspace == nil {
		overview.PendingByWorkspace = map[string]int{}
	}
	return overview, nil
}

func (s *Service) energySnapshot() Energy {
	energy := Energy{DailyFigure: DefaultDailyEnergyTokens}
	if s.settings != nil {
		if figure := s.settings.EconomyDailyEnergyTokens(); figure > 0 {
			energy.DailyFigure = figure
		}
	}
	if s.energy != nil {
		used := s.energy.TokensUsedToday()
		if used > 0 {
			energy.UsedToday = used
		}
	}
	return energy
}

func (s *Service) farmViews(pendingByTask map[string]int) []FarmView {
	views := []FarmView{}
	if s.tasks == nil {
		return views
	}
	farms, err := s.tasks.Farms()
	if err != nil {
		logger.Warn("Failed to list Farms for the economy overview", logger.Fields{"error": err})
		return views
	}
	now := s.clock()
	for _, farm := range farms {
		tier := TierFor(farm.Schedule, now)
		views = append(views, FarmView{
			WorkspaceID:    farm.WorkspaceID,
			TaskID:         farm.TaskID,
			Name:           farm.Name,
			Tier:           tier,
			TierName:       TierName(tier),
			PendingHarvest: pendingByTask[farm.TaskID],
			LastSummary:    farm.LastSummary,
		})
	}
	// Stable order so the popover and the badge title do not reshuffle between
	// polls: most pending first, then by name.
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].PendingHarvest != views[j].PendingHarvest {
			return views[i].PendingHarvest > views[j].PendingHarvest
		}
		if views[i].Name != views[j].Name {
			return views[i].Name < views[j].Name
		}
		return views[i].TaskID < views[j].TaskID
	})
	return views
}

// Backfill grandfathers an install that predates the feature, once (FR32, FR33).
//
// It grants Craft for work already done so the loop is reachable on day one
// instead of after twenty-five chat messages, and writes a zero-cost build entry
// for every Farm that already exists so those Farms show their badge and are
// never charged for a build they made before there was a price.
//
// The guard is the presence of a backfill entry rather than an empty ledger, so
// an install that has already started earning is not re-granted on the next
// restart.
func (s *Service) Backfill(ctx context.Context) error {
	if !s.Available() {
		return ErrStoreUnavailable
	}
	already, err := s.store.HasReason(ctx, ReasonBackfill)
	if err != nil {
		return fmt.Errorf("check economy backfill guard: %w", err)
	}
	if already {
		return nil
	}

	now := s.clock()
	grant := s.backfillGrant(ctx)
	if _, err := s.store.Credit(ctx, Entry{
		Resource: ResourceCraft,
		Amount:   grant,
		Reason:   ReasonBackfill,
		RefKind:  RefKindBackfill,
		RefID:    "install",
	}, now); err != nil {
		return fmt.Errorf("write economy backfill grant: %w", err)
	}

	s.grandfatherExistingFarms(ctx, now)
	logger.Info("City Economy backfill complete", logger.Fields{"craft_granted": grant})
	return nil
}

// backfillGrant sizes the one-time grant from work the user has already done,
// capped so a long-running install cannot start with a dozen free Farms.
//
// A count that cannot be read contributes zero rather than aborting the
// backfill: a smaller grant is a far better failure than an install that retries
// the whole backfill on every restart.
func (s *Service) backfillGrant(ctx context.Context) int64 {
	var completedTasks int64
	if s.tasks != nil {
		count, err := s.tasks.CountCompletedTasks()
		if err != nil {
			logger.Warn("Failed to count completed tasks for the economy backfill",
				logger.Fields{"error": err})
		} else {
			completedTasks = count
		}
	}
	messages, err := s.store.CountUserChatMessages(ctx)
	if err != nil {
		logger.Warn("Failed to count chat messages for the economy backfill",
			logger.Fields{"error": err})
		messages = 0
	}

	grant := CraftPerManualTask*completedTasks + CraftPerChatMessage*messages
	if grant > BackfillGrantCap {
		grant = BackfillGrantCap
	}
	if grant < 0 {
		grant = 0
	}
	return grant
}

func (s *Service) grandfatherExistingFarms(ctx context.Context, now time.Time) {
	if s.tasks == nil {
		return
	}
	farms, err := s.tasks.Farms()
	if err != nil {
		logger.Warn("Failed to list Farms to grandfather", logger.Fields{"error": err})
		return
	}
	for _, farm := range farms {
		if _, err := s.store.Credit(ctx, Entry{
			Resource:    ResourceCraft,
			Amount:      0,
			Reason:      ReasonGrandfathered,
			RefKind:     RefKindBuild,
			RefID:       farm.TaskID,
			WorkspaceID: farm.WorkspaceID,
		}, now); err != nil {
			logger.Warn("Failed to grandfather an existing Farm",
				logger.Fields{"task_id": farm.TaskID, "error": err})
		}
	}
}

// eventString reads a string field from an event payload.
func eventString(ev workspace.Event, key string) string {
	if ev.Data == nil {
		return ""
	}
	value, ok := ev.Data[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// eventBool reads a bool field from an event payload. Anything that is not
// explicitly true reads as false.
func eventBool(ev workspace.Event, key string) bool {
	if ev.Data == nil {
		return false
	}
	value, _ := ev.Data[key].(bool)
	return value
}

// eventReference is the idempotency reference for an event-driven credit: the
// event's own id, falling back to the moment it was handled when a producer did
// not set one.
func eventReference(ev workspace.Event, now time.Time) string {
	if id := strings.TrimSpace(ev.ID); id != "" {
		return id
	}
	return now.UTC().Format(time.RFC3339Nano)
}
