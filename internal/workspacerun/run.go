package workspacerun

import (
	"encoding/json"
	"time"
)

type RawConfig = json.RawMessage

type RunStatus string

const (
	RunStatusPending          RunStatus = "pending"
	RunStatusPreparing        RunStatus = "preparing"
	RunStatusPreparingContext RunStatus = "preparing_context"
	RunStatusExecuting        RunStatus = "executing"
	RunStatusValidating       RunStatus = "validating"
	RunStatusAwaitingApproval RunStatus = "awaiting_approval"
	RunStatusSucceeded        RunStatus = "succeeded"
	RunStatusFailed           RunStatus = "failed"
	RunStatusCancelled        RunStatus = "cancelled"
	RunStatusRejected         RunStatus = "rejected"
)

type ExecutorKind string

const (
	ExecutorKindOriAgent   ExecutorKind = "ori_agent"
	ExecutorKindNativeCLI  ExecutorKind = "native_cli"
	ExecutorKindWorkflow   ExecutorKind = "workflow"
	ExecutorKindSystemTool ExecutorKind = "system_tool"
)

type ArtifactKind string

const (
	ArtifactDiff                 ArtifactKind = "diff"
	ArtifactChangedFiles         ArtifactKind = "changed_files"
	ArtifactTestOutput           ArtifactKind = "test_output"
	ArtifactLog                  ArtifactKind = "log"
	ArtifactScreenshot           ArtifactKind = "screenshot"
	ArtifactCitation             ArtifactKind = "citation"
	ArtifactFile                 ArtifactKind = "file"
	ArtifactTrace                ArtifactKind = "trace"
	ArtifactMemoryUpdate         ArtifactKind = "memory_update"
	ArtifactTaskRawResult        ArtifactKind = "task_raw_result"
	ArtifactTaskNormalizedRow    ArtifactKind = "task_normalized_row"
	ArtifactTaskOutputValidation ArtifactKind = "task_output_validation"
	ArtifactTaskOutputRepair     ArtifactKind = "task_output_repair"
	ArtifactTaskStorageReceipt   ArtifactKind = "task_storage_receipt"
)

type TraceEventKind string

const (
	TraceStatusChange     TraceEventKind = "status_change"
	TraceMessage          TraceEventKind = "message"
	TraceToolCall         TraceEventKind = "tool_call"
	TraceToolResult       TraceEventKind = "tool_result"
	TraceArtifactCaptured TraceEventKind = "artifact_captured"
	TraceValidationCheck  TraceEventKind = "validation_check"
	TraceError            TraceEventKind = "error"
)

type Run struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	ParentRunID string `json:"parent_run_id,omitempty"`

	ProfileID       string  `json:"profile_id"`
	ProfileVersion  string  `json:"profile_version"`
	ProfileSnapshot Profile `json:"profile_snapshot"`

	Executor    Executor    `json:"executor"`
	Scope       Scope       `json:"scope"`
	Policy      Policy      `json:"policy"`
	Environment Environment `json:"environment"`
	ContextPlan ContextPlan `json:"context_plan,omitempty"`

	Prompt string `json:"prompt"`

	Status     RunStatus  `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	TraceTail         []TraceEvent       `json:"trace_tail,omitempty"`
	Artifacts         []Artifact         `json:"artifacts,omitempty"`
	PreparedContext   *PreparedContext   `json:"prepared_context,omitempty"`
	ValidationRequest *ValidationRequest `json:"validation_request,omitempty"`
	ValidationResult  *ValidationResult  `json:"validation_result,omitempty"`
	TaskOutput        *TaskOutputSummary `json:"task_output,omitempty"`
	Cost              *CostSummary       `json:"cost,omitempty"`
	Report            *Report            `json:"report,omitempty"`

	Error string `json:"error,omitempty"`
}

type Executor struct {
	Kind   ExecutorKind `json:"kind"`
	Ref    string       `json:"ref"`
	Config *RawConfig   `json:"config,omitempty"`
}

type OriAgentExecutorConfig struct {
	Model         string         `json:"model,omitempty"`
	ToolPolicy    ToolPolicy     `json:"tool_policy,omitempty"`
	MemoryPolicy  MemoryPolicy   `json:"memory_policy,omitempty"`
	ContextPolicy ContextPolicy  `json:"context_policy,omitempty"`
	Subagents     SubagentPolicy `json:"subagents,omitempty"`
	TaskPayload   *RawConfig     `json:"task_payload,omitempty"`
}

type ToolPolicy struct {
	Mode      string   `json:"mode"`
	Allowlist []string `json:"allowlist,omitempty"`
	Denylist  []string `json:"denylist,omitempty"`
	MaxLoaded int      `json:"max_loaded,omitempty"`
}

type MemoryPolicy struct {
	Enabled   bool     `json:"enabled"`
	Types     []string `json:"types,omitempty"`
	Root      string   `json:"root,omitempty"`
	IndexFile string   `json:"index_file,omitempty"`
	MaxBytes  int      `json:"max_bytes,omitempty"`
}

type ContextPolicy struct {
	Strategy        string   `json:"strategy"`
	CompactAt       int      `json:"compact_at,omitempty"`
	KeepRecentTurns int      `json:"keep_recent_turns,omitempty"`
	InjectFiles     []string `json:"inject_files,omitempty"`
	PlanModeDefault bool     `json:"plan_mode_default,omitempty"`
}

type SubagentPolicy struct {
	MaxConcurrent  int  `json:"max_concurrent,omitempty"`
	Worktree       bool `json:"worktree,omitempty"`
	InheritProfile bool `json:"inherit_profile,omitempty"`
}

type NativeCLIExecutorConfig struct {
	Model     string   `json:"model,omitempty"`
	Args      []string `json:"args,omitempty"`
	TraceMode string   `json:"trace_mode,omitempty"`
}

type WorkflowExecutorConfig struct {
	Inputs       map[string]any `json:"inputs,omitempty"`
	ResumePolicy string         `json:"resume_policy,omitempty"`
}

type SystemToolExecutorConfig struct {
	Inputs map[string]any `json:"inputs,omitempty"`
	DryRun bool           `json:"dry_run,omitempty"`
}

type Scope struct {
	RepoPath         string   `json:"repo_path,omitempty"`
	TargetNoteID     string   `json:"target_note_id,omitempty"`
	TargetTaskID     string   `json:"target_task_id,omitempty"`
	Folder           string   `json:"folder,omitempty"`
	BrowserTarget    string   `json:"browser_target,omitempty"`
	FilesystemRoots  []string `json:"filesystem_roots,omitempty"`
	NetworkAllowlist []string `json:"network_allowlist,omitempty"`
}

type Policy struct {
	Mutation        string   `json:"mutation"`
	Approval        string   `json:"approval"`
	ToolAllow       []string `json:"tool_allow,omitempty"`
	ToolDeny        []string `json:"tool_deny,omitempty"`
	ExternalEffects string   `json:"external_effects,omitempty"`
}

const (
	PolicyMutationAllowed = "allowed"
	PolicyMutationDryRun  = "dry_run"
	PolicyMutationDenied  = "denied"

	PolicyApprovalNone      = "none"
	PolicyApprovalFinalOnly = "final_only"
	PolicyApprovalPerTool   = "per_tool"

	PolicyExternalEffectsAllowed = "allowed"
	PolicyExternalEffectsDenied  = "denied"
)

type Environment struct {
	Worktree       bool              `json:"worktree,omitempty"`
	WorktreePath   string            `json:"worktree_path,omitempty"`
	TempDir        string            `json:"temp_dir,omitempty"`
	AppPort        int               `json:"app_port,omitempty"`
	BrowserSession string            `json:"browser_session,omitempty"`
	LogPath        string            `json:"log_path,omitempty"`
	EnvVars        map[string]string `json:"env_vars,omitempty"`
}

type ContextPlan struct {
	Strategy                 string `json:"strategy,omitempty"`
	IncludeWorkspaceSnapshot bool   `json:"include_workspace_snapshot,omitempty"`
	IncludeAttachedFiles     bool   `json:"include_attached_files,omitempty"`
	ExposeWorkspaceTools     bool   `json:"expose_workspace_tools,omitempty"`
}

type PreparedContext struct {
	Strategy       string                `json:"strategy,omitempty"`
	Summary        string                `json:"summary,omitempty"`
	Items          []PreparedContextItem `json:"items,omitempty"`
	AvailableTools []string              `json:"available_tools,omitempty"`
	PreparedAt     time.Time             `json:"prepared_at"`
}

type PreparedContextItem struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref,omitempty"`
	Name   string `json:"name,omitempty"`
	Access string `json:"access"`
	Detail string `json:"detail,omitempty"`
}

const (
	PreparedContextAccessInjected   = "injected"
	PreparedContextAccessSummarized = "summarized"
	PreparedContextAccessOnDemand   = "on_demand"
)

type Artifact struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Kind      ArtifactKind   `json:"kind"`
	Path      string         `json:"path,omitempty"`
	Inline    []byte         `json:"inline,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type TraceEvent struct {
	ID         string         `json:"id"`
	RunID      string         `json:"run_id"`
	Sequence   int64          `json:"sequence"`
	Kind       TraceEventKind `json:"kind"`
	Source     string         `json:"source,omitempty"`
	Message    string         `json:"message,omitempty"`
	Status     string         `json:"status,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ArtifactID string         `json:"artifact_id,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type ValidationRequest struct {
	Profile  string   `json:"profile,omitempty"`
	Commands []string `json:"commands,omitempty"`
}

type ValidationResult struct {
	Profile string        `json:"profile,omitempty"`
	Checks  []CheckResult `json:"checks"`
}

type TaskOutputSummary struct {
	TaskID           string                      `json:"task_id,omitempty"`
	ValidationStatus string                      `json:"validation_status,omitempty"`
	StorageStatus    string                      `json:"storage_status,omitempty"`
	ContractVersion  string                      `json:"contract_version,omitempty"`
	ValidatedAt      *time.Time                  `json:"validated_at,omitempty"`
	ErrorCount       int                         `json:"error_count,omitempty"`
	Errors           []TaskOutputValidationError `json:"errors,omitempty"`
	RawOutputRef     string                      `json:"raw_output_ref,omitempty"`
	NormalizedRowRef string                      `json:"normalized_row_ref,omitempty"`
	RepairStatus     string                      `json:"repair_status,omitempty"`
	ManualApproval   bool                        `json:"manual_approval,omitempty"`
}

type TaskOutputValidationError struct {
	Code     string   `json:"code,omitempty"`
	Column   string   `json:"column,omitempty"`
	Message  string   `json:"message,omitempty"`
	Expected []string `json:"expected,omitempty"`
	Actual   []string `json:"actual,omitempty"`
}

type CheckResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
	Soft     bool   `json:"soft,omitempty"`
}

const (
	CheckStatusPassed  = "passed"
	CheckStatusFailed  = "failed"
	CheckStatusSkipped = "skipped"
)

type CostSummary struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	TotalTokens  int     `json:"total_tokens,omitempty"`
	USD          float64 `json:"usd,omitempty"`
}

type Report struct {
	Summary           string   `json:"summary"`
	ChangedFiles      []string `json:"changed_files,omitempty"`
	ValidationStatus  string   `json:"validation_status"`
	FollowUps         []string `json:"follow_ups,omitempty"`
	HumanReviewNeeded bool     `json:"human_review_needed,omitempty"`
}

const (
	ValidationStatusPassed  = "passed"
	ValidationStatusFailed  = "failed"
	ValidationStatusPartial = "partial"
)
