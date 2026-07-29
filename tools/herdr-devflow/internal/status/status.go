// Package status assembles a read-only view of bridge-owned feature state and
// live Herdr facts. It keeps observed agent lifecycle separate from derived
// task and Git hints.
package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/tasklist"
)

const PluginViewSource = "plugin:ori.devflow"

type Store interface {
	Load() (model.BridgeState, error)
}

type Herdr interface {
	AgentListInfo(context.Context) ([]herdr.AgentInfo, error)
	// WorkspaceListInfo reports the workspaces Herdr currently has open. It is
	// how publication tells a live surface from a workspace that was closed
	// months ago and survives only in a saved bridge record.
	WorkspaceListInfo(context.Context) ([]herdr.WorkspaceInfo, error)
	ReportWorkspaceMetadata(context.Context, string, string, map[string]string) (json.RawMessage, error)
	ReportPaneMetadata(context.Context, string, string, map[string]string) (json.RawMessage, error)
	SetAgentView(context.Context, map[string]any) (json.RawMessage, error)
	ClearAgentView(context.Context, string) (json.RawMessage, error)
}

type EventStream interface {
	Next() (json.RawMessage, error)
	Close() error
}

type SubscribeFunc func(context.Context, []map[string]any) (EventStream, error)

type GitState struct {
	Dirty  bool   `json:"dirty"`
	Ahead  int    `json:"ahead,omitempty"`
	Stale  bool   `json:"stale,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type GitInspector interface {
	Inspect(context.Context, string) (GitState, error)
}

type GitInspectorFunc func(context.Context, string) (GitState, error)

func (f GitInspectorFunc) Inspect(ctx context.Context, path string) (GitState, error) {
	return f(ctx, path)
}

type Service struct {
	Store             Store
	Client            Herdr
	SourceID          string
	ViewSource        string
	Git               GitInspector
	Now               func() time.Time
	Subscribe         SubscribeFunc
	WatchPollInterval time.Duration
}

type Options struct {
	FeatureName string
	Worktree    string
}

type Snapshot struct {
	Version     int               `json:"version"`
	GeneratedAt time.Time         `json:"generated_at"`
	Stale       bool              `json:"stale"`
	Detail      string            `json:"detail,omitempty"`
	Features    []FeatureSnapshot `json:"features"`
	Rows        []AgentRow        `json:"rows"`
	// LiveWorkspaces are the workspace IDs Herdr reported open at collection
	// time, and LiveSurfacesKnown says whether that listing succeeded. Together
	// they let publication skip a workspace that no longer exists instead of
	// asking Herdr to label it. They are diagnostics for the write path, not
	// part of the rendered snapshot.
	LiveWorkspaces    []string `json:"-"`
	LiveSurfacesKnown bool     `json:"-"`
}

type FeatureSnapshot struct {
	Feature     model.Feature `json:"feature"`
	WorkspaceID string        `json:"workspace_id"`
	// TabID and RootPaneID identify the feature's own tab. A workspace now
	// hosts many features, so the pane — not the workspace — is the narrowest
	// surface that still belongs to exactly one feature.
	TabID           string            `json:"tab_id,omitempty"`
	RootPaneID      string            `json:"-"`
	SourceID        string            `json:"-"`
	MetadataEnabled bool              `json:"-"`
	Task            tasklist.Progress `json:"task"`
	Git             GitState          `json:"git"`
	NextSchedule    *ScheduleSummary  `json:"next_schedule,omitempty"`
}

type AgentRow struct {
	Feature        string            `json:"feature"`
	Branch         string            `json:"branch"`
	Worktree       string            `json:"worktree"`
	WorkspaceID    string            `json:"workspace_id"`
	Role           string            `json:"role"`
	AgentName      string            `json:"agent_name"`
	Kind           string            `json:"kind"`
	ObservedStatus model.AgentStatus `json:"observed_status"`
	Missing        bool              `json:"missing"`
	Stale          bool              `json:"stale"`
	StatusDetail   string            `json:"status_detail,omitempty"`
	Task           tasklist.Progress `json:"task"`
	Git            GitState          `json:"git"`
	LastActivityAt time.Time         `json:"last_activity_at,omitempty"`
	NextSchedule   *ScheduleSummary  `json:"next_schedule,omitempty"`
	Live           *herdr.AgentInfo  `json:"-"`
}

type ScheduleSummary struct {
	ID         string              `json:"id"`
	DueAt      time.Time           `json:"due_at"`
	State      model.ScheduleState `json:"state"`
	Attempts   int                 `json:"attempts"`
	RetryUntil time.Time           `json:"retry_until,omitempty"`
}

func (s *Service) Snapshot(ctx context.Context, options Options) (Snapshot, error) {
	if s.Store == nil {
		return Snapshot{}, stageError("status", model.ErrStateCorrupt, "the local bridge state store is unavailable", "wt herd doctor", nil)
	}
	bridgeState, err := s.Store.Load()
	if err != nil {
		return Snapshot{}, stageError("status", model.ErrStateCorrupt, "could not load local bridge state", "wt herd doctor", err)
	}
	snapshot := Snapshot{Version: 1, GeneratedAt: s.now()}
	liveAgents, liveErr := s.liveAgents(ctx)
	if liveErr != nil {
		snapshot.Stale = true
		snapshot.Detail = conciseLiveError(liveErr)
	} else if workspaces, err := s.Client.WorkspaceListInfo(ctx); err == nil {
		// Which workspaces are open is a separate question from which agents are
		// running: a feature's tab can be open with no agent in it, and a saved
		// record can name a workspace that was closed long ago.
		snapshot.LiveSurfacesKnown = true
		for _, workspace := range workspaces {
			if workspace.WorkspaceID != "" {
				snapshot.LiveWorkspaces = append(snapshot.LiveWorkspaces, workspace.WorkspaceID)
			}
		}
	}

	keys := make([]string, 0, len(bridgeState.Features))
	for key := range bridgeState.Features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		featureState := bridgeState.Features[key]
		if !matchesFeature(featureState.Feature, options) {
			continue
		}
		progress := tasklist.Read(filepath.Join(featureState.Feature.Path, "tasks", "tasks-"+featureState.Feature.Name+".md"))
		gitState := s.gitState(ctx, featureState.Feature.Path)
		nextSchedule := nextSchedule(featureState.Schedules)
		featureSnapshot := FeatureSnapshot{Feature: featureState.Feature, WorkspaceID: featureState.WorkspaceID, TabID: featureState.TabID, RootPaneID: featureState.Handoff.RootPaneID, SourceID: featureState.SourceID, MetadataEnabled: metadataEnabled(featureState), Task: progress, Git: gitState, NextSchedule: nextSchedule}
		snapshot.Features = append(snapshot.Features, featureSnapshot)

		roles := make([]string, 0, len(featureState.Agents))
		for role := range featureState.Agents {
			roles = append(roles, role)
		}
		sort.Strings(roles)
		for _, role := range roles {
			saved := featureState.Agents[role]
			row := AgentRow{
				Feature:        featureState.Feature.Name,
				Branch:         featureState.Feature.Branch,
				Worktree:       featureState.Feature.Path,
				WorkspaceID:    featureState.WorkspaceID,
				Role:           role,
				AgentName:      saved.Name,
				Kind:           saved.Kind,
				Task:           progress,
				Git:            gitState,
				LastActivityAt: saved.UpdatedAt,
				NextSchedule:   nextSchedule,
			}
			if liveErr != nil {
				row.ObservedStatus = model.AgentUnknown
				row.Stale = true
				row.StatusDetail = conciseLiveError(liveErr)
			} else if live, found := findLive(saved, liveAgents); found {
				copyOfLive := live
				row.Live = &copyOfLive
				row.ObservedStatus = normalizeStatus(live.AgentStatus)
				if live.Name != "" {
					row.AgentName = live.Name
				}
				if live.Agent != "" {
					row.Kind = live.Agent
				}
			} else {
				row.ObservedStatus = model.AgentMissing
				row.Missing = true
				row.StatusDetail = "saved managed agent is not live"
			}
			snapshot.Rows = append(snapshot.Rows, row)
		}
	}
	sort.Slice(snapshot.Features, func(i, j int) bool { return snapshot.Features[i].Feature.Name < snapshot.Features[j].Feature.Name })
	sort.SliceStable(snapshot.Rows, func(i, j int) bool {
		left, right := attentionRank(snapshot.Rows[i]), attentionRank(snapshot.Rows[j])
		if left != right {
			return left < right
		}
		if snapshot.Rows[i].Feature != snapshot.Rows[j].Feature {
			return snapshot.Rows[i].Feature < snapshot.Rows[j].Feature
		}
		return snapshot.Rows[i].Role < snapshot.Rows[j].Role
	})
	return snapshot, nil
}

// RehydrateMetadata reports bridge-owned display tokens only. It intentionally
// does not use pane.report_agent, so Herdr integrations retain lifecycle
// authority for semantic status.
//
// Publication is best-effort per target. A saved record can name a workspace
// Herdr deleted months ago, and asking Herdr to label it fails — that failure
// used to abort the whole refresh, so one historical record left every live
// agent on the board showing stale progress. Each target is now attempted
// independently, known-dead surfaces are skipped rather than attempted, and the
// failures are summarized in the returned error instead of hiding the rest of
// the work.
func (s *Service) RehydrateMetadata(ctx context.Context, snapshot Snapshot) error {
	if s.Client == nil {
		return stageError("status metadata", model.ErrHerdrUnavailable, "the Herdr client is unavailable", "wt herd doctor", nil)
	}
	// Rows must find their own feature. Keying features by workspace was safe
	// only while a workspace held exactly one feature; with several features
	// sharing one, a workspace-keyed lookup collapses to whichever was seen
	// last and every other row would be labelled with a stranger's branch.
	featureByName := make(map[string]FeatureSnapshot, len(snapshot.Features))
	for _, feature := range snapshot.Features {
		featureByName[feature.Feature.Name] = feature
	}

	liveWorkspaces := make(map[string]bool)
	missingWorkspaces := make(map[string]bool)
	for _, row := range snapshot.Rows {
		if row.WorkspaceID == "" {
			continue
		}
		if row.Live != nil {
			liveWorkspaces[row.WorkspaceID] = true
		} else if row.Missing {
			missingWorkspaces[row.WorkspaceID] = true
		}
	}
	// Herdr's own workspace listing is stronger evidence than the agent rows: a
	// record with no agent rows at all — the shape that broke publication — is
	// invisible to the row-derived sets above.
	openWorkspaces := make(map[string]bool, len(snapshot.LiveWorkspaces))
	for _, id := range snapshot.LiveWorkspaces {
		openWorkspaces[id] = true
	}
	closed := func(workspaceID string) bool {
		if workspaceID == "" {
			return false
		}
		if snapshot.LiveSurfacesKnown && !openWorkspaces[workspaceID] {
			return true
		}
		return missingWorkspaces[workspaceID] && !liveWorkspaces[workspaceID]
	}

	var failures []string
	record := func(target string, err error) {
		var stage *model.StageError
		detail := "Herdr rejected the update"
		if errors.As(err, &stage) && stage.Message != "" {
			detail = stage.Message
		}
		failures = append(failures, safeToken(target)+": "+safeToken(detail))
	}

	for _, feature := range snapshot.Features {
		if !feature.MetadataEnabled {
			continue
		}
		// A successful cleanup closes the feature's tab (or, for a legacy
		// record, its workspace) before Git removes the worktree. Keep the saved
		// state visible as missing, but do not push display metadata into a
		// surface that is already gone.
		if closed(feature.WorkspaceID) {
			continue
		}
		if feature.TabID != "" {
			// Tab-backed: the feature's own pane carries its tokens, so they
			// survive a workspace shared with other features and remain visible
			// even when no agent is running in the tab.
			if feature.RootPaneID == "" {
				continue
			}
			if _, err := s.Client.ReportPaneMetadata(ctx, feature.RootPaneID, s.metadataSource(feature), featureTokens(feature)); err != nil {
				record(feature.RootPaneID, err)
			}
			continue
		}
		// Legacy workspace-backed record: that workspace still belongs to this
		// one feature, so the workspace remains the honest place for its tokens.
		// Nothing writes new records in this shape, so this drains as those
		// features are retired.
		if feature.WorkspaceID == "" {
			continue
		}
		if _, err := s.Client.ReportWorkspaceMetadata(ctx, feature.WorkspaceID, s.metadataSource(feature), featureTokens(feature)); err != nil {
			record(feature.WorkspaceID, err)
		}
	}

	for _, row := range snapshot.Rows {
		if row.Live == nil || row.Live.PaneID == "" {
			continue
		}
		feature, ok := featureByName[row.Feature]
		if !ok || !feature.MetadataEnabled {
			continue
		}
		if _, err := s.Client.ReportPaneMetadata(ctx, row.Live.PaneID, s.metadataSource(feature), paneTokens(feature, row)); err != nil {
			record(row.Live.PaneID, err)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return stageError("status metadata", model.ErrHerdrUnavailable,
		"display metadata could not be published to "+strconv.Itoa(len(failures))+
			" of Herdr's surfaces; every other surface was updated ("+joinBounded(failures)+")",
		"wt herd doctor", nil)
}

// maxReportedFailures bounds the failure summary so a repository with many
// stale records cannot produce an unreadable error line.
const maxReportedFailures = 5

func joinBounded(values []string) string {
	if len(values) > maxReportedFailures {
		values = append(values[:maxReportedFailures:maxReportedFailures], "…")
	}
	return strings.Join(values, "; ")
}

func (s *Service) ApplyManagedView(ctx context.Context) error {
	if s.Client == nil {
		return stageError("status view", model.ErrHerdrUnavailable, "the Herdr client is unavailable", "wt herd doctor", nil)
	}
	if _, err := s.Client.SetAgentView(ctx, ManagedViewParams(s.viewSource())); err != nil {
		return wrapHerdr("apply Ori Devflow agent view", err)
	}
	return nil
}

func (s *Service) ClearManagedView(ctx context.Context) error {
	if s.Client == nil {
		return stageError("status view", model.ErrHerdrUnavailable, "the Herdr client is unavailable", "wt herd doctor", nil)
	}
	if _, err := s.Client.ClearAgentView(ctx, s.viewSource()); err != nil {
		return wrapHerdr("clear Ori Devflow agent view", err)
	}
	return nil
}

func ManagedViewParams(source string) map[string]any {
	if source == "" {
		source = PluginViewSource
	}
	return map[string]any{
		"source": source,
		"label":  "Ori Devflow",
		"sort": []map[string]any{
			{"field": "attention", "order": "desc"},
			{"field": "state_change_seq", "order": "desc"},
		},
	}
}

// Watch emits an initial snapshot, then uses events when available while a
// bounded ticker keeps the view fresh if the socket is quiet or reconnecting.
func (s *Service) Watch(ctx context.Context, options Options, emit func(Snapshot)) error {
	if emit == nil {
		return stageError("status watch", model.ErrConfigInvalid, "a status render callback is required", "retry wt herd status --watch", nil)
	}
	emitSnapshot := func() error {
		snapshot, err := s.Snapshot(ctx, options)
		if err != nil {
			return err
		}
		emit(snapshot)
		return nil
	}
	if err := emitSnapshot(); err != nil {
		return err
	}
	interval := s.WatchPollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var stream EventStream
	defer func() {
		if stream != nil {
			_ = stream.Close()
		}
	}()
	var eventCh <-chan streamEvent
	openStream := func() {
		if s.Subscribe == nil || stream != nil {
			return
		}
		opened, err := s.Subscribe(ctx, managedSubscriptions())
		if err != nil {
			return
		}
		stream = opened
		eventCh = nextEvent(opened)
	}
	openStream()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := emitSnapshot(); err != nil {
				return err
			}
			if stream == nil {
				openStream()
			}
		case event := <-eventCh:
			if event.err != nil {
				if stream != nil {
					_ = stream.Close()
				}
				stream, eventCh = nil, nil
				continue
			}
			if err := emitSnapshot(); err != nil {
				return err
			}
			eventCh = nextEvent(stream)
		}
	}
}

type streamEvent struct{ err error }

func nextEvent(stream EventStream) <-chan streamEvent {
	result := make(chan streamEvent, 1)
	go func() {
		_, err := stream.Next()
		result <- streamEvent{err: err}
	}()
	return result
}

func managedSubscriptions() []map[string]any {
	return []map[string]any{
		{"type": "pane.agent_status_changed"},
		{"type": "pane.created"},
		{"type": "pane.updated"},
		{"type": "pane.closed"},
		{"type": "workspace.created"},
		{"type": "workspace.updated"},
		{"type": "workspace.closed"},
		{"type": "worktree.opened"},
	}
}

func (s *Service) liveAgents(ctx context.Context) ([]herdr.AgentInfo, error) {
	if s.Client == nil {
		return nil, stageError("status", model.ErrHerdrUnavailable, "the Herdr client is unavailable", "wt herd doctor", nil)
	}
	return s.Client.AgentListInfo(ctx)
}

func (s *Service) gitState(ctx context.Context, path string) GitState {
	inspector := s.Git
	if inspector == nil {
		inspector = defaultGitInspector{}
	}
	state, err := inspector.Inspect(ctx, path)
	if err != nil {
		return GitState{Stale: true, Detail: "Git state unavailable"}
	}
	return state
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) sourceID() string {
	if s.SourceID != "" {
		return s.SourceID
	}
	return "ori.devflow"
}

func (s *Service) metadataSource(feature FeatureSnapshot) string {
	if feature.SourceID != "" {
		return feature.SourceID
	}
	return s.sourceID()
}

func metadataEnabled(feature model.FeatureState) bool {
	return feature.MetadataEnabled == nil || *feature.MetadataEnabled
}

func (s *Service) viewSource() string {
	if s.ViewSource != "" {
		return s.ViewSource
	}
	return PluginViewSource
}

func matchesFeature(feature model.Feature, options Options) bool {
	if options.FeatureName != "" && feature.Name != options.FeatureName {
		return false
	}
	if options.Worktree != "" && !samePath(feature.Path, options.Worktree) {
		return false
	}
	return true
}

func findLive(saved model.RoleAgent, liveAgents []herdr.AgentInfo) (herdr.AgentInfo, bool) {
	if saved.NativeSession.Value != "" {
		var matches []herdr.AgentInfo
		for _, candidate := range liveAgents {
			if candidate.AgentSession != nil && sameNative(saved.NativeSession, *candidate.AgentSession) && candidate.WorkspaceID == saved.WorkspaceID {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 1 {
			return matches[0], true
		}
		if len(matches) > 1 {
			return herdr.AgentInfo{}, false
		}
	}
	for _, candidate := range liveAgents {
		if candidate.Name == saved.Name && candidate.WorkspaceID == saved.WorkspaceID && candidate.PaneID == saved.PaneID && candidate.TerminalID == saved.TerminalID {
			return candidate, true
		}
	}
	return herdr.AgentInfo{}, false
}

func nextSchedule(schedules map[string]model.Schedule) *ScheduleSummary {
	if len(schedules) == 0 {
		return nil
	}
	var unresolved, terminal []model.Schedule
	for _, schedule := range schedules {
		if schedule.State.IsUnresolved() {
			unresolved = append(unresolved, schedule)
		} else {
			terminal = append(terminal, schedule)
		}
	}
	if len(unresolved) == 0 && len(terminal) == 0 {
		return nil
	}
	if len(unresolved) > 0 {
		sort.Slice(unresolved, func(i, j int) bool {
			if unresolved[i].DueAt.Equal(unresolved[j].DueAt) {
				return unresolved[i].ID < unresolved[j].ID
			}
			return unresolved[i].DueAt.Before(unresolved[j].DueAt)
		})
		chosen := unresolved[0]
		return scheduleSummary(chosen)
	}
	// Once no continuation is outstanding, keep a recent failed delivery
	// visible instead of making a problem disappear from the overview. Other
	// terminal records remain useful history but rank after failures.
	sort.Slice(terminal, func(i, j int) bool {
		left, right := scheduleAttentionRank(terminal[i]), scheduleAttentionRank(terminal[j])
		if left != right {
			return left < right
		}
		leftTime, rightTime := scheduleActivityTime(terminal[i]), scheduleActivityTime(terminal[j])
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return terminal[i].ID < terminal[j].ID
	})
	return scheduleSummary(terminal[0])
}

func scheduleSummary(chosen model.Schedule) *ScheduleSummary {
	return &ScheduleSummary{ID: chosen.ID, DueAt: chosen.DueAt, State: chosen.State, Attempts: chosen.Attempts, RetryUntil: chosen.RetryUntil}
}

func scheduleAttentionRank(schedule model.Schedule) int {
	if schedule.State == model.ScheduleFailed {
		return 0
	}
	return 1
}

func scheduleActivityTime(schedule model.Schedule) time.Time {
	if !schedule.UpdatedAt.IsZero() {
		return schedule.UpdatedAt
	}
	if !schedule.DeliveredAt.IsZero() {
		return schedule.DeliveredAt
	}
	return schedule.DueAt
}

// featureTokens are the display values that describe one feature. They are
// reported at pane scope for tab-backed features and at workspace scope only
// for legacy records whose workspace still holds a single feature.
func featureTokens(feature FeatureSnapshot) map[string]string {
	tokens := map[string]string{
		"repository":    safeToken(feature.Feature.RepositoryID),
		"feature":       safeToken(feature.Feature.Name),
		"branch":        safeToken(feature.Feature.Branch),
		"task_progress": safeToken(feature.Task.Label()),
		"next_task":     safeToken(feature.Task.Next),
		"next_schedule": "", // Explicitly clear a resolved schedule token.
	}
	if feature.NextSchedule != nil {
		tokens["next_schedule"] = safeToken(feature.NextSchedule.DueAt.Format(time.RFC3339) + " " + string(feature.NextSchedule.State))
	}
	return tokens
}

func paneTokens(feature FeatureSnapshot, row AgentRow) map[string]string {
	tokens := featureTokens(feature)
	tokens["role"] = safeToken(row.Role)
	tokens["agent_kind"] = safeToken(row.Kind)
	tokens["agent_status"] = safeToken(string(row.ObservedStatus))
	return tokens
}

func safeToken(value string) string {
	var builder strings.Builder
	for _, runeValue := range value {
		if runeValue < 32 || runeValue == 127 {
			continue
		}
		builder.WriteRune(runeValue)
		if builder.Len() >= 80 {
			break
		}
	}
	return strings.TrimSpace(builder.String())
}

func normalizeStatus(status model.AgentStatus) model.AgentStatus {
	switch status {
	case model.AgentIdle, model.AgentWorking, model.AgentBlocked, model.AgentDone, model.AgentUnknown:
		return status
	default:
		return model.AgentUnknown
	}
}

func attentionRank(row AgentRow) int {
	switch row.ObservedStatus {
	case model.AgentBlocked:
		return 0
	case model.AgentMissing:
		return 1
	case model.AgentUnknown:
		return 2
	case model.AgentWorking:
		return 3
	default:
		return 4
	}
}

func sameNative(left, right model.NativeSession) bool {
	return left.Value != "" && left.Value == right.Value && left.Source == right.Source && left.Agent == right.Agent && left.Kind == right.Kind
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

type defaultGitInspector struct{}

func (defaultGitInspector) Inspect(ctx context.Context, path string) (GitState, error) {
	// #nosec G204 -- Snapshot supplies the canonical managed worktree path; Git receives a fixed argument vector.
	status, err := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain").Output()
	if err != nil {
		return GitState{}, err
	}
	result := GitState{Dirty: strings.TrimSpace(string(status)) != ""}
	// #nosec G204 -- same canonical managed worktree path and fixed Git subcommand as above.
	ahead, err := exec.CommandContext(ctx, "git", "-C", path, "rev-list", "--count", "@{upstream}..HEAD").Output()
	if err != nil {
		return result, nil // An unset upstream is a normal local-worktree state.
	}
	value := strings.TrimSpace(string(ahead))
	if value != "" {
		_, _ = fmt.Sscanf(value, "%d", &result.Ahead)
	}
	return result, nil
}

func conciseLiveError(err error) string {
	var stage *model.StageError
	if errors.As(err, &stage) && stage.Message != "" {
		return stage.Message
	}
	return "Herdr status is unavailable"
}

func wrapHerdr(stage string, err error) error {
	var typed *model.StageError
	if errors.As(err, &typed) {
		if typed.Recovery == "" {
			typed.Recovery = "wt herd doctor"
		}
		return typed
	}
	return stageError(stage, model.ErrHerdrUnavailable, "Herdr did not complete the status operation", "wt herd doctor", err)
}

func stageError(stage string, code model.ErrorCode, message, recovery string, cause error) *model.StageError {
	return &model.StageError{Stage: stage, Code: code, Message: message, Recovery: recovery, Cause: cause}
}
