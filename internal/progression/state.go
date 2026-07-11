package progression

import (
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// StateStore loads and persists progression state. The onboarding manager
// implements it structurally, keeping app_state.json's single serialized
// writer. Tests use an in-memory fake.
type StateStore interface {
	GetProgression() types.ProgressionState
	SetProgression(types.ProgressionState) error
}

// Snapshot is a point-in-time count of existing state, gathered once by the
// startup backfill scan and fed to each quest's Satisfied predicate. Fields
// are best-effort: a Scanner fills what it can cheaply read; a zero field just
// means that quest is not grandfathered and will complete live instead.
type Snapshot struct {
	Workspaces        int
	Notes             int
	TasksStarted      int
	AgentTasksDone    int
	Agents            int
	SkillsBound       int
	MCPServers        int
	Triggers          int
	ScheduledTasks    int
	OrchestrationRuns int
	MemoryWrites      int
	ChatMessages      int
	// Personalized is true when the user has filled out their profile
	// (interests / work style) on the Personalize page.
	Personalized bool
}

// Scanner produces a Snapshot of existing state for the backfill scan. The
// concrete implementation (reading workspace/agent/trigger stores) is wired in
// the server; the interface keeps the engine unit-testable.
type Scanner interface {
	Scan() Snapshot
}

// ScannerFunc adapts a function to the Scanner interface.
type ScannerFunc func() Snapshot

// Scan calls the wrapped function.
func (f ScannerFunc) Scan() Snapshot { return f() }

// QuestStatus is the display status of a quest for the API/UI.
type QuestStatus string

const (
	// StatusCompleted means the quest's action has been observed.
	StatusCompleted QuestStatus = "completed"
	// StatusAvailable means the quest is in the current tier and actionable.
	StatusAvailable QuestStatus = "available"
	// StatusLocked means the quest belongs to a tier above the current one
	// (shown dimmed). Note: detection still runs for locked quests, so a user
	// who jumps ahead completes them immediately regardless of this status.
	StatusLocked QuestStatus = "locked-tier"
)

// QuestView is the API representation of a quest.
type QuestView struct {
	ID          string      `json:"id"`
	Tier        int         `json:"tier"`
	Title       string      `json:"title"`
	Why         string      `json:"why"`
	Status      QuestStatus `json:"status"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
	ActionURL   string      `json:"action_url,omitempty"`
	ActionLabel string      `json:"action_label,omitempty"`
}

// TierView groups a tier's quests for the API.
type TierView struct {
	Tier     int         `json:"tier"`
	Name     string      `json:"name"`
	Complete bool        `json:"complete"`
	Quests   []QuestView `json:"quests"`
}

// Status is the full progression snapshot returned by the API.
type Status struct {
	Tiers          []TierView `json:"tiers"`
	CurrentTier    int        `json:"current_tier"`
	TotalTiers     int        `json:"total_tiers"`
	CompletedCount int        `json:"completed_count"`
	TotalCount     int        `json:"total_count"`
	AllComplete    bool       `json:"all_complete"`
	Dismissed      bool       `json:"dismissed"`
	// NextQuest is the next actionable quest (lowest tier, first incomplete),
	// or nil when everything is complete.
	NextQuest *QuestView `json:"next_quest,omitempty"`
}
