// Package scheduler persists and dispatches one-time continuation prompts. It
// deliberately never opens workspaces, splits panes, or starts agents.
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

const localTimeLayout = "2006-01-02 15:04"

var (
	scheduleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,63}$`)
	featurePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)
	rolePattern       = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	liveTargetPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,120}$`)
	timezonePattern   = regexp.MustCompile(`^[A-Za-z0-9._:+/-]{1,80}$`)
)

type Store interface {
	Load() (model.BridgeState, error)
	Save(model.BridgeState) error
	Lock(context.Context) (func(), error)
}

type Herdr interface {
	AgentGetInfo(context.Context, string) (herdr.AgentInfo, error)
	AgentListInfo(context.Context) ([]herdr.AgentInfo, error)
	AgentPromptInfo(context.Context, string, string, time.Duration) (herdr.AgentInfo, error)
}

type Service struct {
	Store         Store
	Client        Herdr
	Now           func() time.Time
	NewID         func() (string, error)
	PromptTimeout time.Duration
}

type CreateRequest struct {
	Feature      model.Feature
	Agent        model.RoleAgent
	DueAt        time.Time
	Timezone     string
	Prompt       string
	RetryWindow  time.Duration
	WakeRequired bool
}

// WakeEvidenceUpdate is the bounded standalone-daemon evidence persisted
// alongside one wake-enabled continuation. It contains no prompt or process
// output.
type WakeEvidenceUpdate struct {
	RegisteredAt        time.Time
	ProgrammedAt        time.Time
	VerifiedAt          time.Time
	ProtocolVersion     int
	DaemonBuild         string
	HelperBuild         string
	Result              string
	Code                string
	Failure             string
	Uncertain           bool
	RollbackAttemptedAt time.Time
	RollbackVerifiedAt  time.Time
	RollbackResult      string
	RollbackDetail      string
}

type DispatchResult struct {
	FeatureID string              `json:"feature_id"`
	Feature   model.Feature       `json:"feature"`
	Schedule  model.Schedule      `json:"schedule"`
	Outcome   model.ScheduleState `json:"outcome"`
}

type ScheduleRef struct {
	RepositoryID string
	FeatureName  string
}

// ParseDueAt accepts an offset-bearing RFC 3339 timestamp or a local
// YYYY-MM-DD HH:MM timestamp. The latter is interpreted in loc so users see
// an unambiguous normalized absolute time before state is written.
func ParseDueAt(raw string, now time.Time, loc *time.Location) (time.Time, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, "", fmt.Errorf("a one-time timestamp is required")
	}
	lower := strings.ToLower(value)
	for _, recurrence := range []string{"cron", "every ", "daily", "weekly", "monthly", "@"} {
		if strings.Contains(lower, recurrence) {
			return time.Time{}, "", fmt.Errorf("recurring schedules are not supported; provide one RFC 3339 or %s timestamp", localTimeLayout)
		}
	}
	if loc == nil {
		loc = time.Local
	}
	var due time.Time
	var err error
	if due, err = time.Parse(time.RFC3339, value); err != nil {
		due, err = time.ParseInLocation(localTimeLayout, value, loc)
		if err != nil {
			return time.Time{}, "", fmt.Errorf("timestamp must be RFC 3339 or local %s", localTimeLayout)
		}
		if due.In(loc).Format(localTimeLayout) != value {
			return time.Time{}, "", fmt.Errorf("local timestamp does not exist in the current timezone because of a daylight-saving transition; provide an RFC 3339 offset")
		}
	}
	if !due.After(now) {
		return time.Time{}, "", fmt.Errorf("scheduled time must be in the future")
	}
	zone := due.Location().String()
	if zone == "" || zone == "Local" {
		_, offset := due.Zone()
		sign := "+"
		if offset < 0 {
			sign = "-"
			offset = -offset
		}
		zone = fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
	}
	return due.UTC(), zone, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (model.Schedule, error) {
	if s.Store == nil {
		return model.Schedule{}, stateError("schedule", "the local schedule store is unavailable", "wt herd doctor", nil)
	}
	if err := validateCreateIdentity(request); err != nil {
		return model.Schedule{}, err
	}
	if request.Feature.RepositoryID == "" || request.Feature.Name == "" || request.Feature.Path == "" {
		return model.Schedule{}, stateError("schedule", "a managed feature identity is required", "run wt herd continue from a feature worktree", nil)
	}
	if request.Agent.Role == "" || request.Agent.Name == "" || request.Agent.WorkspaceID == "" {
		return model.Schedule{}, stateError("schedule", "an exact live managed agent is required", "wt herd rebind <role> --target <live-target>", nil)
	}
	if request.DueAt.IsZero() || !request.DueAt.After(s.now()) {
		return model.Schedule{}, stateError("schedule", "scheduled time must be in the future", "choose a future --at timestamp", nil)
	}
	if request.RetryWindow <= 0 {
		return model.Schedule{}, stateError("schedule", "retry window must be positive", "fix scheduler.retry_window", nil)
	}
	if strings.TrimSpace(request.Prompt) == "" || len(request.Prompt) > 16000 {
		return model.Schedule{}, stateError("schedule", "prompt must contain 1-16000 characters", "supply a shorter continuation prompt", nil)
	}

	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return model.Schedule{}, stateError("schedule lock", "could not acquire the local scheduler lock", "wait for the other bridge command, then retry", err)
	}
	defer unlock()
	state, err := s.Store.Load()
	if err != nil {
		return model.Schedule{}, stateError("schedule state", "could not load local schedules", "wt herd doctor", err)
	}
	key := featureKey(request.Feature)
	featureState, ok := state.Features[key]
	if !ok || !samePath(featureState.Feature.Path, request.Feature.Path) {
		return model.Schedule{}, stateError("schedule", "the feature is not a managed local handoff", "run wt herd retry from the feature worktree", nil)
	}
	savedAgent, ok := featureState.Agents[request.Agent.Role]
	if !ok || !sameAgent(savedAgent, request.Agent) {
		return model.Schedule{}, stateError("schedule", "the selected role no longer matches its saved agent identity", "wt herd rebind "+request.Agent.Role+" --target <live-target>", nil)
	}
	if featureState.Schedules == nil {
		featureState.Schedules = make(map[string]model.Schedule)
	}
	id, err := s.nextID()
	if err != nil {
		return model.Schedule{}, stateError("schedule", "could not allocate a schedule id", "retry the command", err)
	}
	if !scheduleIDPattern.MatchString(id) {
		return model.Schedule{}, stateError("schedule", "could not allocate a safe schedule id", "retry the command", nil)
	}
	for {
		if _, exists := featureState.Schedules[id]; !exists {
			break
		}
		id, err = s.nextID()
		if err != nil {
			return model.Schedule{}, stateError("schedule", "could not allocate a unique schedule id", "retry the command", err)
		}
		if !scheduleIDPattern.MatchString(id) {
			return model.Schedule{}, stateError("schedule", "could not allocate a safe schedule id", "retry the command", nil)
		}
	}
	now := s.now()
	schedule := model.Schedule{
		ID:            id,
		FeaturePath:   request.Feature.Path,
		Role:          request.Agent.Role,
		AgentName:     request.Agent.Name,
		AgentKind:     request.Agent.Kind,
		WorkspaceID:   request.Agent.WorkspaceID,
		PaneID:        request.Agent.PaneID,
		TerminalID:    request.Agent.TerminalID,
		NativeSession: request.Agent.NativeSession,
		DueAt:         request.DueAt.UTC(),
		RetryUntil:    request.DueAt.UTC().Add(request.RetryWindow),
		Timezone:      request.Timezone,
		Prompt:        request.Prompt,
		WakeRequired:  request.WakeRequired,
		State:         model.SchedulePending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if request.WakeRequired {
		schedule.WakeCandidateID = id
		schedule.WakeSource = "herdr-continuation"
		schedule.WakePurpose = "one_time_continuation"
		schedule.WakeRequestedAt = request.DueAt.UTC()
	}
	featureState.Schedules[id] = schedule
	featureState.UpdatedAt = now
	state.Features[key] = featureState
	if err := s.Store.Save(state); err != nil {
		return model.Schedule{}, stateError("schedule state", "could not save the one-time continuation", "check local state permissions, then retry", err)
	}
	return schedule, nil
}

// RecordWakeResult stores the single wake owner's evidence, or fails a
// wake-required schedule before it can be delivered. Registration alone is
// never treated as proof that macOS will wake the machine.
func (s *Service) RecordWakeResult(ctx context.Context, ref ScheduleRef, id string, programmedAt time.Time, failure string) (model.Schedule, error) {
	update := WakeEvidenceUpdate{
		ProgrammedAt:    programmedAt,
		VerifiedAt:      s.now(),
		ProtocolVersion: 1,
		DaemonBuild:     "legacy-evidence",
		Result:          "success",
		Code:            "ok",
		Failure:         failure,
	}
	if failure != "" {
		update.Result = "refusal"
		update.Code = "verification_failed"
		update.VerifiedAt = time.Time{}
	}
	return s.RecordWakeEvidence(ctx, ref, id, update)
}

// RecordWakeEvidence stores standalone protocol/build/read-back evidence or a
// definite/uncertain rollback result before the continuation can be delivered.
func (s *Service) RecordWakeEvidence(
	ctx context.Context,
	ref ScheduleRef,
	id string,
	update WakeEvidenceUpdate,
) (model.Schedule, error) {
	if !scheduleIDPattern.MatchString(id) {
		return model.Schedule{}, stateError("schedule wake", "schedule id is invalid", "use wt herd schedule list", nil)
	}
	if s.Store == nil {
		return model.Schedule{}, stateError("schedule wake", "the local schedule store is unavailable", "wt herd doctor", nil)
	}
	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return model.Schedule{}, stateError("schedule lock", "could not acquire the local scheduler lock", "retry", err)
	}
	defer unlock()
	state, err := s.Store.Load()
	if err != nil {
		return model.Schedule{}, stateError("schedule state", "could not load local schedules", "wt herd doctor", err)
	}
	key := ref.RepositoryID + ":" + ref.FeatureName
	featureState, ok := state.Features[key]
	if !ok {
		return model.Schedule{}, stateError("schedule wake", "managed feature was not found", "run from a managed feature worktree", nil)
	}
	schedule, ok := featureState.Schedules[id]
	if !ok {
		return model.Schedule{}, stateError("schedule wake", "schedule was not found", "wt herd schedule list", nil)
	}
	if !schedule.WakeRequired {
		return model.Schedule{}, stateError("schedule wake", "this continuation did not request a system wake", "recreate it with wt herd continue --wake", nil)
	}
	if schedule.State != model.SchedulePending {
		return model.Schedule{}, stateError("schedule wake", "only a pending continuation can record wake readiness", "wt herd schedule show "+id, nil)
	}

	now := s.now()
	update.Failure = strings.TrimSpace(update.Failure)
	schedule.WakeRegisteredAt = update.RegisteredAt.UTC()
	schedule.WakeProgrammedAt = update.ProgrammedAt.UTC()
	schedule.WakeVerifiedAt = update.VerifiedAt.UTC()
	schedule.WakeProtocol = update.ProtocolVersion
	schedule.WakeDaemonBuild = update.DaemonBuild
	schedule.WakeHelperBuild = update.HelperBuild
	schedule.WakeResult = update.Result
	schedule.WakeCode = update.Code
	schedule.WakeUncertain = update.Uncertain
	schedule.WakeRollbackAt = update.RollbackAttemptedAt.UTC()
	schedule.WakeRollbackOKAt = update.RollbackVerifiedAt.UTC()
	schedule.WakeRollbackState = update.RollbackResult
	schedule.WakeRollbackInfo = strings.TrimSpace(update.RollbackDetail)
	if update.Failure != "" {
		if update.Uncertain {
			schedule.State = model.ScheduleUncertain
		} else {
			schedule.State = model.ScheduleFailed
		}
		schedule.WakeFailureReason = update.Failure
		schedule.FailureReason = "required system wake was not confirmed"
		schedule.RecoveryCommand = "wt herd wake doctor; then wt herd schedule cancel " + id
	} else {
		if update.ProgrammedAt.IsZero() || update.ProgrammedAt.After(schedule.DueAt) ||
			update.VerifiedAt.IsZero() {
			return model.Schedule{}, stateError("schedule wake", "the confirmed wake time is missing or later than the continuation", "cancel and recreate the continuation", nil)
		}
		if update.ProtocolVersion <= 0 || strings.TrimSpace(update.DaemonBuild) == "" {
			return model.Schedule{}, stateError("schedule wake", "standalone daemon version evidence is missing", "run wt herd wake doctor, then recreate the continuation", nil)
		}
		schedule.WakeFailureReason = ""
		schedule.FailureReason = ""
		schedule.RecoveryCommand = ""
	}
	schedule.UpdatedAt = now
	featureState.Schedules[id] = schedule
	featureState.UpdatedAt = now
	state.Features[key] = featureState
	if err := s.Store.Save(state); err != nil {
		return model.Schedule{}, stateError("schedule state", "could not save wake readiness", "check local state permissions, then retry", err)
	}
	return schedule, nil
}

// RecordWakeWithdrawal persists the exact-cancel proof after a continuation
// becomes terminal. A failed withdrawal never reopens prompt delivery, but it
// remains visible to recovery and lifecycle safety checks.
func (s *Service) RecordWakeWithdrawal(
	ctx context.Context,
	ref ScheduleRef,
	id string,
	attemptedAt time.Time,
	verifiedAt time.Time,
	result string,
	detail string,
	uncertain bool,
) (model.Schedule, error) {
	if !scheduleIDPattern.MatchString(id) {
		return model.Schedule{}, stateError("schedule wake", "schedule id is invalid", "use wt herd schedule list", nil)
	}
	if s.Store == nil {
		return model.Schedule{}, stateError("schedule wake", "the local schedule store is unavailable", "wt herd doctor", nil)
	}
	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return model.Schedule{}, stateError("schedule lock", "could not acquire the local scheduler lock", "retry", err)
	}
	defer unlock()
	state, err := s.Store.Load()
	if err != nil {
		return model.Schedule{}, stateError("schedule state", "could not load local schedules", "wt herd doctor", err)
	}
	key := ref.RepositoryID + ":" + ref.FeatureName
	featureState, ok := state.Features[key]
	if !ok {
		return model.Schedule{}, stateError("schedule wake", "managed feature was not found", "run from a managed feature worktree", nil)
	}
	schedule, ok := featureState.Schedules[id]
	if !ok || !schedule.WakeRequired {
		return model.Schedule{}, stateError("schedule wake", "wake-enabled continuation was not found", "wt herd schedule list", nil)
	}
	schedule.WakeRollbackAt = attemptedAt.UTC()
	schedule.WakeRollbackOKAt = verifiedAt.UTC()
	schedule.WakeRollbackState = strings.TrimSpace(result)
	schedule.WakeRollbackInfo = strings.TrimSpace(detail)
	schedule.WakeUncertain = uncertain
	if !uncertain {
		schedule.WakeWithdrawnAt = verifiedAt.UTC()
	} else {
		schedule.WakeFailureReason = "exact standalone wake cancellation was not proven"
		schedule.RecoveryCommand = "wt herd wake doctor; then wt herd schedule show " + id
	}
	schedule.UpdatedAt = s.now()
	featureState.Schedules[id] = schedule
	featureState.UpdatedAt = schedule.UpdatedAt
	state.Features[key] = featureState
	if err := s.Store.Save(state); err != nil {
		return model.Schedule{}, stateError("schedule state", "could not persist wake withdrawal evidence", "check local state permissions, then run wt herd schedule show "+id, err)
	}
	return schedule, nil
}

// validateCreateIdentity is defensive because schedules can be created by a
// detached helper long after their feature state was first written. Every
// value that becomes a Herdr target or durable state key stays bounded and
// data-only; prompt text remains deliberately free-form user data.
func validateCreateIdentity(request CreateRequest) error {
	if !featurePattern.MatchString(request.Feature.Name) {
		return stateError("schedule", "feature name is invalid", "run wt herd continue from a feature created by wt start", nil)
	}
	if !rolePattern.MatchString(request.Agent.Role) {
		return stateError("schedule", "agent role is invalid", "use a lower-case managed role", nil)
	}
	if !config.IsSupportedAgentKind(request.Agent.Kind) {
		return stateError("schedule", "agent kind is not supported by Herdr", "rebind the role to a supported agent kind", nil)
	}
	for _, value := range []string{request.Feature.RepositoryID, request.Agent.Name, request.Agent.WorkspaceID, request.Agent.PaneID, request.Agent.TerminalID} {
		if value != "" && !liveTargetPattern.MatchString(value) {
			return stateError("schedule", "saved schedule identity contains unsafe characters", "run wt herd rebind <role> --target <live-target>", nil)
		}
	}
	if request.Timezone != "" && !timezonePattern.MatchString(request.Timezone) {
		return stateError("schedule", "schedule timezone contains unsafe characters", "use an RFC 3339 timestamp or local YYYY-MM-DD HH:MM time", nil)
	}
	for _, value := range request.Feature.Path {
		if value == 0 || value < 32 || value == 127 {
			return stateError("schedule", "feature worktree path contains a control character", "run wt herd retry from the feature worktree", nil)
		}
	}
	return nil
}

// DispatchDue evaluates every due one-time schedule. It deliberately holds
// the durable operation lock across each Herdr submission: `delivering` is
// saved before the prompt is sent, and no second dispatcher can reinterpret
// that state while the first submission is in flight. A crash after that save
// is therefore surfaced as uncertain instead of causing a duplicate prompt.
func (s *Service) DispatchDue(ctx context.Context) ([]DispatchResult, error) {
	if s.Store == nil || s.Client == nil {
		return nil, stateError("schedule dispatch", "scheduler dependencies are unavailable", "wt herd doctor", nil)
	}
	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return nil, stateError("schedule lock", "could not acquire the local scheduler lock", "wait for the other dispatcher, then retry", err)
	}
	defer unlock()
	state, err := s.Store.Load()
	if err != nil {
		return nil, stateError("schedule state", "could not load local schedules", "wt herd doctor", err)
	}

	now := s.now()
	keys := make([]string, 0, len(state.Features))
	for key := range state.Features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var results []DispatchResult
	for _, key := range keys {
		featureState := state.Features[key]
		ids := sortedScheduleIDs(featureState.Schedules)
		for _, id := range ids {
			schedule := featureState.Schedules[id]
			result, changed, shouldDeliver, dispatchErr := s.dispatchOne(ctx, featureState.Feature, &schedule, now)
			if dispatchErr != nil {
				return results, dispatchErr
			}
			if !changed {
				continue
			}
			featureState.Schedules[id] = schedule
			featureState.UpdatedAt = now
			state.Features[key] = featureState
			if err := s.Store.Save(state); err != nil {
				return results, stateError("schedule state", "could not persist dispatcher progress", "check local state permissions, then run wt herd dispatch", err)
			}
			if shouldDeliver {
				// The durable ScheduleDelivering write above is the at-most-once
				// boundary. Do not move this submission before that save.
				s.completeSchedule(ctx, &schedule, s.now())
				featureState.Schedules[id] = schedule
				featureState.UpdatedAt = schedule.UpdatedAt
				state.Features[key] = featureState
				if err := s.Store.Save(state); err != nil {
					return results, stateError("schedule state", "could not persist delivery outcome", "check local state permissions, then run wt herd dispatch", err)
				}
				result = resultFor(featureState.Feature, schedule)
			}
			if result != nil {
				results = append(results, *result)
			}
		}
	}
	return results, nil
}

func (s *Service) dispatchOne(ctx context.Context, feature model.Feature, schedule *model.Schedule, now time.Time) (*DispatchResult, bool, bool, error) {
	if schedule.State == model.ScheduleDelivering {
		schedule.State = model.ScheduleUncertain
		schedule.FailureReason = "previous dispatcher stopped after beginning prompt submission; delivery is uncertain"
		schedule.RecoveryCommand = "wt herd schedule show " + schedule.ID + " && wt herd read " + schedule.Role
		schedule.UpdatedAt = now
		return resultFor(feature, *schedule), true, false, nil
	}
	if schedule.State != model.SchedulePending && schedule.State != model.ScheduleWaiting {
		return nil, false, false, nil
	}
	if now.Before(schedule.DueAt) {
		return nil, false, false, nil
	}
	if !schedule.RetryUntil.IsZero() && now.After(schedule.RetryUntil) {
		schedule.State = model.ScheduleFailed
		schedule.FailureReason = "retry window elapsed before a safe delivery"
		schedule.RecoveryCommand = "wt herd schedule show " + schedule.ID
		schedule.UpdatedAt = now
		return resultFor(feature, *schedule), true, false, nil
	}
	if schedule.WakeRequired && schedule.WakeVerifiedAt.IsZero() {
		schedule.State = model.ScheduleFailed
		schedule.FailureReason = "required system wake was never verified"
		if schedule.WakeFailureReason == "" {
			schedule.WakeFailureReason = schedule.FailureReason
		}
		schedule.RecoveryCommand = "wt herd schedule show " + schedule.ID
		schedule.UpdatedAt = now
		return resultFor(feature, *schedule), true, false, nil
	}

	live, err := s.resolveLiveAgent(ctx, *schedule)
	if err != nil {
		schedule.State = model.ScheduleWaiting
		schedule.LastCheckedAt = now
		schedule.FailureReason = conciseError(err)
		schedule.RecoveryCommand = "wt herd rebind " + schedule.Role + " --target <live-target>"
		schedule.UpdatedAt = now
		return resultFor(feature, *schedule), true, false, nil
	}
	if !sameScheduleIdentity(*schedule, live) {
		schedule.State = model.ScheduleFailed
		schedule.LastCheckedAt = now
		schedule.FailureReason = "live agent does not match the saved feature-scoped identity"
		schedule.RecoveryCommand = "wt herd rebind " + schedule.Role + " --target <live-target>"
		schedule.UpdatedAt = now
		return resultFor(feature, *schedule), true, false, nil
	}
	// A native-session match allows a restored pane/name to be updated without
	// creating another conversation. The schedule still targets that exact
	// session, never a generic role label.
	if schedule.NativeSession.Value != "" && live.AgentSession != nil && sameNative(schedule.NativeSession, *live.AgentSession) {
		schedule.AgentName = live.Name
		schedule.WorkspaceID = live.WorkspaceID
		schedule.PaneID = live.PaneID
		schedule.TerminalID = live.TerminalID
	}
	schedule.LastCheckedAt = now
	switch live.AgentStatus {
	case model.AgentIdle, model.AgentDone:
		// Continue below.
	case model.AgentWorking, model.AgentBlocked, model.AgentUnknown, "":
		schedule.State = model.ScheduleWaiting
		schedule.FailureReason = "agent is " + normalizedStatus(live.AgentStatus) + "; waiting within the retry window"
		schedule.RecoveryCommand = "wt herd schedule show " + schedule.ID
		schedule.UpdatedAt = now
		return resultFor(feature, *schedule), true, false, nil
	default:
		schedule.State = model.ScheduleWaiting
		schedule.FailureReason = "agent is not safely eligible for a continuation"
		schedule.RecoveryCommand = "wt herd schedule show " + schedule.ID
		schedule.UpdatedAt = now
		return resultFor(feature, *schedule), true, false, nil
	}

	// The caller persists this state before AgentPromptInfo can run. A future
	// dispatcher sees `delivering` and marks it uncertain rather than retrying.
	schedule.State = model.ScheduleDelivering
	schedule.Attempts++
	schedule.LastAttemptAt = now
	schedule.FailureReason = ""
	schedule.RecoveryCommand = ""
	schedule.UpdatedAt = now
	return resultFor(feature, *schedule), true, true, nil
}

// CompleteDelivery must run after dispatchOne persisted ScheduleDelivering.
// It is split out so callers/tests can prove the exact at-most-once boundary.
func (s *Service) CompleteDelivery(ctx context.Context, ref ScheduleRef, scheduleID string) (model.Schedule, error) {
	if s.Store == nil || s.Client == nil {
		return model.Schedule{}, stateError("schedule delivery", "scheduler dependencies are unavailable", "wt herd doctor", nil)
	}
	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return model.Schedule{}, stateError("schedule lock", "could not acquire the local scheduler lock", "retry the dispatcher", err)
	}
	defer unlock()
	state, err := s.Store.Load()
	if err != nil {
		return model.Schedule{}, stateError("schedule state", "could not load local schedules", "wt herd doctor", err)
	}
	key := ref.RepositoryID + ":" + ref.FeatureName
	featureState, ok := state.Features[key]
	if !ok {
		return model.Schedule{}, stateError("schedule delivery", "managed feature was not found", "wt herd schedule show "+scheduleID, nil)
	}
	schedule, ok := featureState.Schedules[scheduleID]
	if !ok || schedule.State != model.ScheduleDelivering {
		return model.Schedule{}, stateError("schedule delivery", "schedule is not awaiting delivery", "wt herd schedule show "+scheduleID, nil)
	}
	s.completeSchedule(ctx, &schedule, s.now())
	featureState.Schedules[scheduleID] = schedule
	featureState.UpdatedAt = schedule.UpdatedAt
	state.Features[key] = featureState
	if err := s.Store.Save(state); err != nil {
		return model.Schedule{}, stateError("schedule state", "could not persist delivery outcome", "check local state permissions", err)
	}
	return schedule, nil
}

// completeSchedule finishes a schedule that has already been durably marked
// delivering. Its caller owns the state lock and must persist the result.
func (s *Service) completeSchedule(ctx context.Context, schedule *model.Schedule, now time.Time) {
	ack, promptErr := s.Client.AgentPromptInfo(ctx, schedule.AgentName, schedule.Prompt, s.promptTimeout())
	if promptErr != nil {
		if isDefiniteMissing(promptErr) {
			schedule.State = model.ScheduleWaiting
			schedule.FailureReason = conciseError(promptErr)
			schedule.RecoveryCommand = "wt herd rebind " + schedule.Role + " --target <live-target>"
		} else {
			schedule.State = model.ScheduleUncertain
			schedule.FailureReason = "Herdr did not confirm whether the continuation prompt was accepted"
			schedule.RecoveryCommand = "wt herd schedule show " + schedule.ID + " && wt herd read " + schedule.Role
		}
		schedule.UpdatedAt = now
		return
	}
	if !matchesAcknowledgement(ack, *schedule) {
		schedule.State = model.ScheduleUncertain
		schedule.FailureReason = "Herdr acknowledged a different agent identity after prompt submission"
		schedule.RecoveryCommand = "wt herd schedule show " + schedule.ID + " && wt herd read " + schedule.Role
	} else {
		schedule.State = model.ScheduleDelivered
		schedule.DeliveredAt = now
		schedule.FailureReason = ""
		schedule.RecoveryCommand = ""
	}
	schedule.UpdatedAt = now
}

// Due returns schedule records for one feature in chronological order. Prompt
// text is retained in state but callers should redact it before presentation.
func (s *Service) List(ctx context.Context, ref ScheduleRef) ([]model.Schedule, error) {
	if s.Store == nil {
		return nil, stateError("schedule list", "the local schedule store is unavailable", "wt herd doctor", nil)
	}
	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return nil, stateError("schedule lock", "could not acquire the local scheduler lock", "retry", err)
	}
	defer unlock()
	state, err := s.Store.Load()
	if err != nil {
		return nil, stateError("schedule list", "could not load local schedules", "wt herd doctor", err)
	}
	featureState, ok := state.Features[ref.RepositoryID+":"+ref.FeatureName]
	if !ok {
		return nil, stateError("schedule list", "managed feature was not found", "run from a managed feature worktree", nil)
	}
	list := make([]model.Schedule, 0, len(featureState.Schedules))
	for _, schedule := range featureState.Schedules {
		list = append(list, schedule)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].DueAt.Equal(list[j].DueAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].DueAt.Before(list[j].DueAt)
	})
	return list, nil
}

func (s *Service) Show(ctx context.Context, ref ScheduleRef, id string) (model.Schedule, error) {
	if !scheduleIDPattern.MatchString(id) {
		return model.Schedule{}, stateError("schedule show", "schedule id is invalid", "use wt herd schedule list", nil)
	}
	list, err := s.List(ctx, ref)
	if err != nil {
		return model.Schedule{}, err
	}
	for _, schedule := range list {
		if schedule.ID == id {
			return schedule, nil
		}
	}
	return model.Schedule{}, stateError("schedule show", "schedule was not found", "wt herd schedule list", nil)
}

func (s *Service) Cancel(ctx context.Context, ref ScheduleRef, id string) (model.Schedule, error) {
	if !scheduleIDPattern.MatchString(id) {
		return model.Schedule{}, stateError("schedule cancel", "schedule id is invalid", "use wt herd schedule list", nil)
	}
	if s.Store == nil {
		return model.Schedule{}, stateError("schedule cancel", "the local schedule store is unavailable", "wt herd doctor", nil)
	}
	unlock, err := s.Store.Lock(ctx)
	if err != nil {
		return model.Schedule{}, stateError("schedule lock", "could not acquire the local scheduler lock", "retry", err)
	}
	defer unlock()
	state, err := s.Store.Load()
	if err != nil {
		return model.Schedule{}, stateError("schedule cancel", "could not load local schedules", "wt herd doctor", err)
	}
	key := ref.RepositoryID + ":" + ref.FeatureName
	featureState, ok := state.Features[key]
	if !ok {
		return model.Schedule{}, stateError("schedule cancel", "managed feature was not found", "run from a managed feature worktree", nil)
	}
	schedule, ok := featureState.Schedules[id]
	if !ok {
		return model.Schedule{}, stateError("schedule cancel", "schedule was not found", "wt herd schedule list", nil)
	}
	if schedule.State != model.SchedulePending && schedule.State != model.ScheduleWaiting {
		return model.Schedule{}, stateError("schedule cancel", "only pending or waiting schedules can be canceled", "wt herd schedule show "+id, nil)
	}
	now := s.now()
	schedule.State = model.ScheduleCanceled
	schedule.CanceledAt = now
	schedule.UpdatedAt = now
	schedule.FailureReason = "canceled by user before delivery"
	schedule.RecoveryCommand = ""
	featureState.Schedules[id] = schedule
	featureState.UpdatedAt = now
	state.Features[key] = featureState
	if err := s.Store.Save(state); err != nil {
		return model.Schedule{}, stateError("schedule state", "could not save cancellation", "check local state permissions", err)
	}
	return schedule, nil
}

func (s *Service) resolveLiveAgent(ctx context.Context, schedule model.Schedule) (herdr.AgentInfo, error) {
	if schedule.NativeSession.Value != "" {
		agents, err := s.Client.AgentListInfo(ctx)
		if err != nil {
			return herdr.AgentInfo{}, err
		}
		var matches []herdr.AgentInfo
		for _, candidate := range agents {
			if candidate.AgentSession != nil && sameNative(schedule.NativeSession, *candidate.AgentSession) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return herdr.AgentInfo{}, stateError("schedule dispatch", "multiple live agents match the saved native session", "wt herd rebind "+schedule.Role+" --target <live-target>", nil)
		}
	}
	return s.Client.AgentGetInfo(ctx, schedule.AgentName)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) nextID() (string, error) {
	if s.NewID != nil {
		return s.NewID()
	}
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "sch-" + s.now().Format("20060102t150405") + "-" + hex.EncodeToString(bytes), nil
}

func (s *Service) promptTimeout() time.Duration {
	if s.PromptTimeout > 0 {
		return s.PromptTimeout
	}
	return 30 * time.Second
}

func featureKey(feature model.Feature) string { return feature.RepositoryID + ":" + feature.Name }

func sortedScheduleIDs(schedules map[string]model.Schedule) []string {
	ids := make([]string, 0, len(schedules))
	for id := range schedules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func resultFor(feature model.Feature, schedule model.Schedule) *DispatchResult {
	return &DispatchResult{FeatureID: featureKey(feature), Feature: feature, Schedule: schedule, Outcome: schedule.State}
}

func samePath(left, right string) bool {
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

func sameAgent(left, right model.RoleAgent) bool {
	if left.Name != right.Name || left.WorkspaceID != right.WorkspaceID || left.PaneID != right.PaneID || left.TerminalID != right.TerminalID || left.Kind != right.Kind {
		return false
	}
	if left.NativeSession.Value != "" || right.NativeSession.Value != "" {
		return sameNative(left.NativeSession, right.NativeSession)
	}
	return true
}

func sameNative(left, right model.NativeSession) bool {
	return left.Value != "" && left.Value == right.Value && left.Source == right.Source && left.Agent == right.Agent && left.Kind == right.Kind
}

func sameScheduleIdentity(schedule model.Schedule, live herdr.AgentInfo) bool {
	if schedule.NativeSession.Value != "" && live.AgentSession != nil && sameNative(schedule.NativeSession, *live.AgentSession) {
		return live.WorkspaceID == schedule.WorkspaceID
	}
	return live.Name == schedule.AgentName && live.WorkspaceID == schedule.WorkspaceID && live.PaneID == schedule.PaneID && live.TerminalID == schedule.TerminalID
}

func matchesAcknowledgement(ack herdr.AgentInfo, schedule model.Schedule) bool {
	if ack.Name == "" || ack.WorkspaceID == "" || ack.PaneID == "" || ack.TerminalID == "" {
		return false
	}
	if ack.Name != schedule.AgentName || ack.WorkspaceID != schedule.WorkspaceID || ack.PaneID != schedule.PaneID || ack.TerminalID != schedule.TerminalID {
		return false
	}
	if schedule.NativeSession.Value != "" && (ack.AgentSession == nil || !sameNative(schedule.NativeSession, *ack.AgentSession)) {
		return false
	}
	return true
}

func normalizedStatus(status model.AgentStatus) string {
	if status == "" {
		return "unknown"
	}
	return string(status)
}

func isDefiniteMissing(err error) bool {
	var stage *model.StageError
	return errors.As(err, &stage) && stage.Code == model.ErrAgentMissing
}

func conciseError(err error) string {
	var stage *model.StageError
	if errors.As(err, &stage) && stage.Message != "" {
		return stage.Message
	}
	return "Herdr could not resolve the saved agent"
}

func stateError(stage, message, recovery string, cause error) *model.StageError {
	return &model.StageError{Stage: stage, Code: model.ErrScheduleInvalid, Message: message, Recovery: recovery, Cause: cause}
}
