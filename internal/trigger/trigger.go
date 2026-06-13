// Package trigger implements event-driven mission triggers: per-workspace
// definitions that start a mission run or a task when an external event
// occurs (incoming webhook or local file change). See
// tasks/prd-event-triggers.md for the full requirements.
package trigger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
)

// Type discriminates a trigger's event source.
type Type string

const (
	TypeWebhook   Type = "webhook"
	TypeFileWatch Type = "file_watch"
)

// ActionKind discriminates what a trigger fires.
type ActionKind string

const (
	// ActionMissionRun fires the workspace's mission via the mission bridge,
	// exactly like cadence (same autonomy gate, same bookkeeping).
	ActionMissionRun ActionKind = "mission_run"
	// ActionTaskPrompt creates a workspace task from a stored prompt and
	// queues it for the task executor.
	ActionTaskPrompt ActionKind = "task_prompt"
)

// Action describes what happens when the trigger fires.
type Action struct {
	Kind ActionKind `json:"kind"`
	// Agent, Prompt, and Priority apply only to ActionTaskPrompt.
	Agent    string `json:"agent,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

// WebhookConfig is the source config for TypeWebhook.
type WebhookConfig struct {
	// Token is the unguessable URL path segment (POST /api/hooks/{token}).
	// Generated server-side; regenerable.
	Token string `json:"token"`
	// Secret, when non-empty, must be presented by callers in the
	// X-Ori-Webhook-Secret header.
	Secret string `json:"secret,omitempty"`
}

// FileWatchConfig is the source config for TypeFileWatch.
type FileWatchConfig struct {
	// Path is the absolute directory to watch (non-recursive).
	Path string `json:"path"`
	// Glob optionally filters file names (filepath.Match syntax, e.g. "*.pdf").
	// Empty means all files (subject to the watcher's built-in temp-file filter).
	Glob string `json:"glob,omitempty"`
	// Events lists which event types fire the trigger. Empty defaults to
	// ["create"].
	Events []string `json:"events,omitempty"`
}

// Event is one raw occurrence observed by a trigger source, before coalescing.
type Event struct {
	// Kind is "webhook", "file", or "test".
	Kind      string    `json:"kind"`
	Timestamp time.Time `json:"timestamp"`

	// Webhook fields.
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body,omitempty"`
	RemoteAddr  string `json:"remote_addr,omitempty"`
	// Truncated marks a Body cut at the payload cap.
	Truncated bool `json:"truncated,omitempty"`

	// File fields.
	FileEvent string `json:"file_event,omitempty"` // create | modify | remove | rename
	FilePath  string `json:"file_path,omitempty"`
	FileName  string `json:"file_name,omitempty"`
}

// PendingFire is a coalesced fire waiting to execute (queued behind an
// in-flight run). It is persisted with the trigger so a queued fire survives
// a server restart (PRD #21).
type PendingFire struct {
	FireID    string    `json:"fire_id"`
	Events    []Event   `json:"events"`
	CreatedAt time.Time `json:"created_at"`
	// DroppedEvents counts events summarized away once Events hit the
	// accumulation cap; they are represented only by this counter.
	DroppedEvents int `json:"dropped_events,omitempty"`
}

// EventCount returns the total number of raw events this fire represents,
// including ones dropped past the accumulation cap.
func (p *PendingFire) EventCount() int {
	if p == nil {
		return 0
	}
	return len(p.Events) + p.DroppedEvents
}

// FireRecord is one entry in a trigger's fire history (capped at
// maxFireHistory, mirroring ScheduledTask.ExecutionHistory).
type FireRecord struct {
	FireID     string    `json:"fire_id"`
	FiredAt    time.Time `json:"fired_at"`
	EventCount int       `json:"event_count"`
	Summary    string    `json:"summary"`
	RunID      string    `json:"run_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// maxFireHistory caps FireHistory length (same value as the scheduler's
// maxRecordedTaskExecutions).
const maxFireHistory = 20

// DefaultDebounce is the coalescing window applied when a trigger doesn't
// override DebounceSeconds.
const DefaultDebounce = 2 * time.Second

// MaxPayloadBytes caps how much raw event payload (webhook body, accumulated
// event detail) is kept and injected into prompts.
const MaxPayloadBytes = 64 * 1024

// Trigger is a per-workspace event trigger definition. Persisted in the
// workspace folder (triggers.json) — disk is truth, no SQLite column.
type Trigger struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Type        Type   `json:"type"`
	Enabled     bool   `json:"enabled"`

	Action    Action           `json:"action"`
	Webhook   *WebhookConfig   `json:"webhook,omitempty"`
	FileWatch *FileWatchConfig `json:"file_watch,omitempty"`

	// DebounceSeconds overrides the default coalescing window; 0 = default.
	DebounceSeconds int `json:"debounce_seconds,omitempty"`

	// Tracking.
	LastFiredAt  *time.Time   `json:"last_fired_at,omitempty"`
	FireCount    int          `json:"fire_count"`
	FailureCount int          `json:"failure_count"`
	LastError    string       `json:"last_error,omitempty"`
	FireHistory  []FireRecord `json:"fire_history,omitempty"`

	// PendingFire is the persisted coalesced fire queued behind an in-flight
	// run, if any.
	PendingFire *PendingFire `json:"pending_fire,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Debounce returns the trigger's effective coalescing window.
func (t *Trigger) Debounce() time.Duration {
	if t.DebounceSeconds > 0 {
		return time.Duration(t.DebounceSeconds) * time.Second
	}
	return DefaultDebounce
}

// WatchEvents returns the effective file-event set for a file-watch trigger,
// applying the "default to create" rule.
func (t *Trigger) WatchEvents() []string {
	if t.FileWatch == nil || len(t.FileWatch.Events) == 0 {
		return []string{string(filewatcher.EventCreate)}
	}
	return t.FileWatch.Events
}

// validEventTypes is the set FileWatchConfig.Events entries must come from.
var validEventTypes = map[string]bool{
	string(filewatcher.EventCreate): true,
	string(filewatcher.EventModify): true,
	string(filewatcher.EventRemove): true,
	string(filewatcher.EventRename): true,
}

// Validate checks a trigger definition for structural validity only — it
// never touches the filesystem, so tracking updates (e.g. recording that a
// watched directory disappeared) can always persist. Use CheckWatchPath for
// the live filesystem checks at create/enable time (PRD #16).
func (t *Trigger) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("trigger name is required")
	}
	if t.WorkspaceID == "" {
		return fmt.Errorf("trigger workspace_id is required")
	}

	switch t.Action.Kind {
	case ActionMissionRun:
		// No extra config; workspace mission state is checked at fire time.
	case ActionTaskPrompt:
		if strings.TrimSpace(t.Action.Prompt) == "" {
			return fmt.Errorf("task_prompt action requires a prompt")
		}
		if strings.TrimSpace(t.Action.Agent) == "" {
			return fmt.Errorf("task_prompt action requires a target agent")
		}
	default:
		return fmt.Errorf("unknown action kind %q (want %q or %q)", t.Action.Kind, ActionMissionRun, ActionTaskPrompt)
	}

	switch t.Type {
	case TypeWebhook:
		if t.Webhook == nil || strings.TrimSpace(t.Webhook.Token) == "" {
			return fmt.Errorf("webhook trigger requires a generated token")
		}
	case TypeFileWatch:
		if t.FileWatch == nil {
			return fmt.Errorf("file_watch trigger requires file watch config")
		}
		if strings.TrimSpace(t.FileWatch.Path) == "" {
			return fmt.Errorf("file_watch trigger requires a directory path")
		}
		if !filepath.IsAbs(t.FileWatch.Path) {
			return fmt.Errorf("watch path must be absolute, got %q", t.FileWatch.Path)
		}
		if t.FileWatch.Glob != "" {
			if _, err := filepath.Match(t.FileWatch.Glob, "probe.txt"); err != nil {
				return fmt.Errorf("invalid glob %q: %w", t.FileWatch.Glob, err)
			}
		}
		for _, ev := range t.FileWatch.Events {
			if !validEventTypes[ev] {
				return fmt.Errorf("invalid watch event type %q", ev)
			}
		}
	default:
		return fmt.Errorf("unknown trigger type %q (want %q or %q)", t.Type, TypeWebhook, TypeFileWatch)
	}

	return nil
}

// CheckWatchPath enforces the live file-watch path rules: exists, is a
// directory, and is readable. Called at create and enable time — moments
// where a missing directory should be a hard error — and by the watch
// manager's runtime validation sweep. Not part of Validate so that tracking
// writes still persist after a watched directory disappears.
func (t *Trigger) CheckWatchPath() error {
	if t.Type != TypeFileWatch || t.FileWatch == nil {
		return nil
	}
	path := t.FileWatch.Path
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("watch path %q does not exist", path)
		}
		return fmt.Errorf("watch path %q is not accessible: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("watch path %q is not a directory", path)
	}
	f, err := os.Open(path) // #nosec G304 -- path is the user-chosen watch directory; watching an arbitrary local folder is the feature, and it is validated as an existing dir above
	if err != nil {
		return fmt.Errorf("watch path %q is not readable: %w", path, err)
	}
	_ = f.Close()
	return nil
}

// MatchesFileEvent reports whether a raw watch event passes this trigger's
// event-type and glob filters.
func (t *Trigger) MatchesFileEvent(eventType, fileName string) bool {
	if t.FileWatch == nil {
		return false
	}
	typeOK := false
	for _, ev := range t.WatchEvents() {
		if ev == eventType {
			typeOK = true
			break
		}
	}
	if !typeOK {
		return false
	}
	if t.FileWatch.Glob == "" {
		return true
	}
	ok, err := filepath.Match(t.FileWatch.Glob, fileName)
	return err == nil && ok
}

// RecordFire appends a fire record, updates the aggregate tracking fields,
// and trims history to the cap. Failure records bump FailureCount and set
// LastError; successes reset LastError.
func (t *Trigger) RecordFire(rec FireRecord) {
	if rec.FiredAt.IsZero() {
		rec.FiredAt = time.Now()
	}
	t.LastFiredAt = &rec.FiredAt
	t.FireCount++
	if rec.Error != "" {
		t.FailureCount++
		t.LastError = rec.Error
	} else {
		t.LastError = ""
	}
	t.FireHistory = append(t.FireHistory, rec)
	if len(t.FireHistory) > maxFireHistory {
		t.FireHistory = t.FireHistory[len(t.FireHistory)-maxFireHistory:]
	}
	t.UpdatedAt = time.Now()
}

// Summary renders a one-line human-readable description of a fire's events,
// used in fire records and prompt context. Examples:
//
//	create: invoice-2026-06.pdf (+2 more)
//	POST 1.2 KB from 192.168.1.10
//	manual test fire
func Summary(events []Event, dropped int) string {
	total := len(events) + dropped
	if total == 0 {
		return "no events"
	}
	first := events[0]
	var s string
	switch first.Kind {
	case "webhook":
		s = fmt.Sprintf("POST %s from %s", humanBytes(len(first.Body)), first.RemoteAddr)
	case "file":
		s = fmt.Sprintf("%s: %s", first.FileEvent, first.FileName)
	case "test":
		s = "manual test fire"
	default:
		s = first.Kind
	}
	if total > 1 {
		s += fmt.Sprintf(" (+%d more)", total-1)
	}
	return s
}

// humanBytes formats a byte count for event summaries.
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
