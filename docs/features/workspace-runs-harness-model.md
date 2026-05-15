# Workspace Runs Harness Model

## Overview

Workspace Runs are Ori's generalized harness model for scoped, observable, validated agent execution inside a workspace.

Most meaningful Ori tasks happen inside a workspace. The workspace owns the durable context: notes, files, repo paths, tasks, agents, sessions, MCP tools, browser state, artifacts, and history. The harness is therefore workspace-first, not engineering-first.

```text
Workspace Run = a scoped, observable, validated agent execution inside a workspace
```

A Run is a first-class durable entity. It can be listed, inspected, stopped, validated, and re-run from the same captured request/config. Literal deterministic replay is only safe for read-only or explicitly replayable runs; mutation-heavy runs should be re-executed under current policy rather than blindly replayed.

## Core Model

```text
Workspace
  -> Workspace Run
      -> Profile        (task contract: what kind of work + definition of done)
      -> Executor       (the worker: ori_agent, native_cli, workflow, system_tool)
      -> Scope          (what the run is allowed to touch)
      -> Policy         (approval, mutation, network, fs, external-effect rules)
      -> Environment    (worktree, ports, temp dir, browser session, env vars)
      -> Context        (planned + prepared inputs available at execution start)
      -> Artifacts      (evidence: diffs, files, citations, screenshots, logs)
      -> Validation     (checks proving the work is acceptable)
      -> Report         (final user-facing result)
```

Layered concerns that sit beneath runs and are shared across them:

```text
Workspace
  -> Memory             (typed file-based memory: user, feedback, project, reference)
  -> Tool surface       (MCP bindings + skill bindings; filtered per run via Scope/Policy)
  -> Agents             (Ori agents registered to the workspace)
```

## Core Entities

- `Run` - Execution record: id, parent_run_id, workspace_id, status, prompt, executor, profile snapshot, timestamps, trace, outcome.
- `Profile` - Task contract: what kind of work this is and what "done" means. Contracts validation.
- `Executor` - The worker: kind + ref + kind-specific config.
- `Scope` - What the run is allowed to touch.
- `Policy` - Approval, mutation, network, filesystem, and external-effect rules.
- `Environment` - Runtime setup: worktree, app port, temp data dir, browser session, logs.
- `Context` - Planned and prepared task inputs: what is injected, summarized, or available on demand.
- `Artifacts` - Evidence produced by the run.
- `Validation` - Checks that prove the work is acceptable, contracted by the Profile.
- `Report` - Final user-facing result.

### Terminology

`Profile` means task contract: what kind of work this is and what is required for completion. It does not mean a runtime configuration bundle. Runtime configuration, including tool selection, memory strategy, and context strategy, lives inside `Executor.config` and Workspace settings.

## Profiles

Profiles are contracts. They do not execute work themselves. Each Profile defines:

- The kind of work it describes.
- The artifacts a run of this profile must produce.
- The validation checks that must pass for the run to be acceptable.
- Defaults for policy and validation that the caller can override.

### MVP Profiles

```text
general
engineering
```

### Reserved Profiles

```text
research
file_ops
browser
document
spreadsheet
plugin
workspace_admin
```

### Examples

These are illustrative examples, including reserved profiles that are not shippable in MVP.

- `engineering` - Requires diff capture, test output, changed files, and optional app validation.
- `research` - Requires sources, citation checks, recency checks, and a confidence summary.
- `file_ops` - Requires preview, changed paths, undo plan, and final file list.

### Profile Schema

```go
type Profile struct {
    ID          string `json:"id"`       // "engineering"
    Version     string `json:"version"`  // incremented when the contract changes
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`

    RequiredArtifacts []ArtifactKind `json:"required_artifacts,omitempty"` // ["diff", "test_output"]
    OptionalArtifacts []ArtifactKind `json:"optional_artifacts,omitempty"`

    Validation    ValidationContract `json:"validation"`
    DefaultPolicy Policy             `json:"default_policy,omitempty"`
}

type ValidationContract struct {
    RequiredChecks []string `json:"required_checks,omitempty"` // ["unit_tests_pass", "diff_present"]
    OptionalChecks []string `json:"optional_checks,omitempty"`
    AllowedToFail  []string `json:"allowed_to_fail,omitempty"` // soft checks; report but do not block
}
```

Runs should store the profile ID, version, and a profile snapshot. Profiles will evolve, and historical runs need to show the contract that was active at execution time.

Profile snapshots are captured at run creation time, before the run is queued or executed. If a profile changes between creation and execution, the run still uses its stored snapshot.

The MVP profile contracts are:

- `general` - No required artifacts and no required validation checks. It records trace, artifacts, and report when available.
- `engineering` - Requires changed-file/diff evidence when mutation is allowed, plus validation output when a validation profile other than `none` is requested.

## Executors

Executors are separate from profiles. A profile describes the work; an executor performs it.

```text
ori_agent      - An Ori agent (any provider) runs the task with workspace tools
native_cli     - A direct CLI agent (codex, claude, etc.) drives the task
workflow       - A pre-defined orchestration workflow runs the task
system_tool    - A deterministic system tool runs the task (no LLM)
```

Important distinction:

```text
ori_agent + provider codex       = normal Ori agent using the Codex provider
ori_agent + provider claude_code = normal Ori agent using the Claude Code provider

native_cli + ref codex           = direct Codex CLI engineering worker
native_cli + ref claude          = direct Claude CLI engineering worker
```

Non-CLI Ori agents can still use Codex and Claude Code models. Native CLI is a different execution mode, not the only way to use those providers.

### Executor Schema

```go
type Executor struct {
    Kind   ExecutorKind   `json:"kind"`             // "ori_agent" | "native_cli" | "workflow" | "system_tool"
    Ref    string         `json:"ref"`              // agent_id | "codex" | "claude" | workflow_id
    Config ExecutorConfig `json:"config,omitempty"` // kind-specific
}
```

### Executor Config

`ori_agent` config absorbs the runtime knobs that used to be discussed as "harness profiles":

```go
type OriAgentExecutorConfig struct {
    Model         string        `json:"model,omitempty"`
    ToolPolicy    ToolPolicy    `json:"tool_policy,omitempty"`
    MemoryPolicy  MemoryPolicy  `json:"memory_policy,omitempty"`
    ContextPolicy ContextPolicy `json:"context_policy,omitempty"`
    Subagents     SubagentPolicy `json:"subagents,omitempty"`
}

type ToolPolicy struct {
    Mode      string   `json:"mode"`                 // "all" | "allowlist" | "denylist" | "lazy"
    Allowlist []string `json:"allowlist,omitempty"`  // globs: "mcp:reaper/*", "skill:git-*"
    Denylist  []string `json:"denylist,omitempty"`
    MaxLoaded int      `json:"max_loaded,omitempty"` // lazy mode cap
}

type MemoryPolicy struct {
    Enabled   bool     `json:"enabled"`
    Types     []string `json:"types,omitempty"` // ["user","feedback","project","reference"]
    Root      string   `json:"root,omitempty"`
    IndexFile string   `json:"index_file,omitempty"`
    MaxBytes  int      `json:"max_bytes,omitempty"`
}

type ContextPolicy struct {
    Strategy        string   `json:"strategy"` // "raw" | "compact" | "summarize"
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
```

`native_cli` config:

```go
type NativeCliExecutorConfig struct {
    Model     string   `json:"model,omitempty"`
    Args      []string `json:"args,omitempty"`
    TraceMode string   `json:"trace_mode,omitempty"` // "stream_json" | "transcript_parse" | "pty"
}
```

The native CLI backend is identified by `Executor.Ref`. This avoids duplicating the backend in both `Executor.Ref` and executor config.

`Args` is an escape hatch, not an unfiltered command-line passthrough. MVP should either omit it or filter it through a per-backend allowlist. Callers must not be able to inject flags that weaken scope, sandbox, or approval policy.

Native CLI tools do not all emit structured events. The Executor abstraction is responsible for translating their output, using stream-json, transcript parse, or PTY capture, into Artifacts and typed TraceEvents when available. Capture strategy is part of the executor config, not the run.

`workflow` config:

```go
type WorkflowExecutorConfig struct {
    Inputs       map[string]interface{} `json:"inputs,omitempty"`
    ResumePolicy string                 `json:"resume_policy,omitempty"` // "fresh" | "resume"
}
```

`Executor.Ref` identifies the workflow ID. Workflow execution is reserved for post-MVP unless the existing orchestration runner can be wrapped cheaply.

`system_tool` config:

```go
type SystemToolExecutorConfig struct {
    Inputs map[string]interface{} `json:"inputs,omitempty"`
    DryRun bool                   `json:"dry_run,omitempty"`
}
```

`Executor.Ref` identifies the system tool. System-tool execution is reserved for post-MVP.

### Executor Interface

Every executor implements:

```go
type ExecutorRunner interface {
    Execute(ctx context.Context, run *Run) error
    Cancel(ctx context.Context, run *Run) error
    Artifacts(ctx context.Context, run *Run) ([]Artifact, error)
}

type TraceEmitter interface {
    TraceEvents(ctx context.Context, run *Run) ([]TraceEvent, error)
}

type ContextPreparer interface {
    PrepareContext(ctx context.Context, run *Run) (*PreparedContext, error)
}
```

`Cancel` is mandatory so `POST /runs/{id}/stop` is not a CLI-specific special case.

`TraceEmitter` is optional. Executors implement it when they can expose structured events in addition to artifacts.

`ContextPreparer` is optional. Executors implement it when they can expose a first-class prepared-context record before execution begins.

## Scope

```go
type Scope struct {
    RepoPath         string   `json:"repo_path,omitempty"`
    TargetNoteID     string   `json:"target_note_id,omitempty"`
    TargetTaskID     string   `json:"target_task_id,omitempty"`
    Folder           string   `json:"folder,omitempty"`
    BrowserTarget    string   `json:"browser_target,omitempty"`
    FilesystemRoots  []string `json:"filesystem_roots,omitempty"`  // hard boundaries
    NetworkAllowlist []string `json:"network_allowlist,omitempty"`
}
```

Scope must be canonicalized before execution. Path fields such as `RepoPath`, `Folder`, and `FilesystemRoots` must be resolved to absolute paths, symlinks must be evaluated, and every effective path must be bounded to approved workspace roots before any executor receives it. This is especially important for native CLI execution.

## Policy

```go
type Policy struct {
    Mutation        string   `json:"mutation"`         // "allowed" | "dry_run" | "denied"
    Approval        string   `json:"approval"`         // "none" | "final_only" | "per_tool"
    ToolAllow       []string `json:"tool_allow,omitempty"`
    ToolDeny        []string `json:"tool_deny,omitempty"`
    ExternalEffects string   `json:"external_effects,omitempty"` // "allowed" | "denied"
}
```

Policy is a per-run decision, not a property of an agent. The same agent can run under different policies in different runs.

Policy resolution merges profile defaults with the create request. `Profile.DefaultPolicy` fills missing fields; explicitly provided request fields override profile defaults. List fields such as `ToolAllow` and `ToolDeny` replace defaults when provided.

## Environment

```go
type Environment struct {
    Worktree       bool              `json:"worktree,omitempty"`
    WorktreePath   string            `json:"worktree_path,omitempty"` // set by Prepare
    TempDir        string            `json:"temp_dir,omitempty"`
    AppPort        int               `json:"app_port,omitempty"`
    BrowserSession string            `json:"browser_session,omitempty"`
    LogPath        string            `json:"log_path,omitempty"`
    EnvVars        map[string]string `json:"env_vars,omitempty"`
}
```

An environment manager owns setup and teardown:

```go
type EnvironmentManager interface {
    Prepare(ctx context.Context, run *Run) (*Environment, error)
    TearDown(ctx context.Context, run *Run, env *Environment) error
}
```

`Prepare` populates the Environment before `Execute` runs. `TearDown` runs after `Execute` regardless of outcome.

## Context Preparation

Environment preparation and context preparation are distinct:

- `Environment` answers: what runtime resources does this run need?
- `Context` answers: what information is available to the worker when execution begins?

```go
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
    Access string `json:"access"` // "injected" | "summarized" | "on_demand"
    Detail string `json:"detail,omitempty"`
}
```

The plan is the editable intent for how a run should be prepared. `PreparedContext` is the factual record of what the harness actually made available before execution.

For the current task-backed `ori_agent` path:

- Workspace snapshot metadata is `injected`.
- Notes, workspace files, directories, and recent sessions are `summarized` through bounded prompt previews.
- Files directly attached to the task are `injected`.
- Workspace tools remain `on_demand`; they expose full notes/files/tasks/sessions during execution without pretending that all content was preloaded.

## Artifacts

```go
type ArtifactKind string

const (
    ArtifactDiff         ArtifactKind = "diff"
    ArtifactChangedFiles ArtifactKind = "changed_files"
    ArtifactTestOutput   ArtifactKind = "test_output"
    ArtifactLog          ArtifactKind = "log"
    ArtifactScreenshot   ArtifactKind = "screenshot"
    ArtifactCitation     ArtifactKind = "citation"
    ArtifactFile         ArtifactKind = "file"
    ArtifactTrace        ArtifactKind = "trace" // structured agent trace
    ArtifactMemoryUpdate ArtifactKind = "memory_update" // reserved post-MVP
)

type Artifact struct {
    ID        string                 `json:"id"`
    RunID     string                 `json:"run_id"`
    Kind      ArtifactKind           `json:"kind"`
    Path      string                 `json:"path,omitempty"`   // on-disk reference
    Inline    []byte                 `json:"inline,omitempty"` // small artifacts
    Metadata  map[string]interface{} `json:"metadata,omitempty"`
    CreatedAt time.Time              `json:"created_at"`
}
```

## Trace

Trace is typed so `/trace` is not a free-form bag of JSON. Executors and validators map their native events into these kinds.

```go
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

type TraceEvent struct {
    ID         string                 `json:"id"`
    RunID      string                 `json:"run_id"`
    Sequence   int64                  `json:"sequence"`
    Kind       TraceEventKind         `json:"kind"`
    Source     string                 `json:"source,omitempty"` // executor, validator, lifecycle, tool name
    Message    string                 `json:"message,omitempty"`
    Status     string                 `json:"status,omitempty"`
    ToolName   string                 `json:"tool_name,omitempty"`
    ArtifactID string                 `json:"artifact_id,omitempty"`
    Data       map[string]interface{} `json:"data,omitempty"`
    CreatedAt  time.Time              `json:"created_at"`
}
```

The store assigns monotonically increasing `Sequence` values per run. The MVP trace API is polling-based and supports `?since=<sequence>` to fetch events after a known sequence. Server-Sent Events or WebSocket streaming can be added post-MVP without changing the event schema.

## Validation

Validation runs after Execute and is contracted by the Profile. The create request captures intent through `ValidationRequest`; the stored run captures results through `ValidationResult`.

```go
type ValidationRequest struct {
    Profile string   `json:"profile,omitempty"` // "unit" | "citations" | "none" | ...
    Commands []string `json:"commands,omitempty"`
}

type ValidationResult struct {
    Profile string        `json:"profile,omitempty"`
    Checks  []CheckResult `json:"checks"`
}

type CheckResult struct {
    Name     string `json:"name"`
    Status   string `json:"status"` // "passed" | "failed" | "skipped"
    Evidence string `json:"evidence,omitempty"`
    Soft     bool   `json:"soft,omitempty"` // allowed-to-fail per profile contract
}
```

`ValidationRequest.Commands` is for ad-hoc validation commands when a built-in validation profile is not enough. Commands extend the selected validation profile; they do not override or remove Profile-required checks. In MVP, command execution must be filtered through an allowlist or disabled outside trusted local development.

The Run is acceptable if every required check, per Profile contract, is `passed`. Soft checks can fail without blocking acceptance.

## Run

```go
type Run struct {
    ID           string `json:"id"`
    WorkspaceID  string `json:"workspace_id"`
    ParentRunID string `json:"parent_run_id,omitempty"` // for subagent / orchestration runs

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

    TraceTail         []TraceEvent       `json:"trace_tail,omitempty"` // recent tail only; full trace via /trace
    Artifacts         []Artifact         `json:"artifacts,omitempty"`
    PreparedContext   *PreparedContext   `json:"prepared_context,omitempty"`
    ValidationRequest *ValidationRequest `json:"validation_request,omitempty"`
    ValidationResult  *ValidationResult  `json:"validation_result,omitempty"`
    Cost              *CostSummary       `json:"cost,omitempty"`
    Report            *Report            `json:"report,omitempty"`

    Error string `json:"error,omitempty"`
}

type CostSummary struct {
    InputTokens  int     `json:"input_tokens,omitempty"`
    OutputTokens int     `json:"output_tokens,omitempty"`
    TotalTokens  int     `json:"total_tokens,omitempty"`
    USD          float64 `json:"usd,omitempty"`
}

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
```

`parent_run_id` is included in MVP so subagent and orchestration runs can be linked later without a schema migration.

Run mutation is owned by the lifecycle orchestrator. HTTP handlers create commands such as create, stop, approve, and reject, but they do not directly mutate status, trace, artifacts, validation, or report fields. Stores should serialize writes per run and return copies/snapshots for reads so polling clients cannot observe partially mutated slices or maps.

`GET /runs/{id}` should include only a recent `trace_tail` for quick status rendering. The full trace is fetched through `GET /runs/{id}/trace`, with `?since=<sequence>` for polling.

## Report

```go
type Report struct {
    Summary           string   `json:"summary"`
    ChangedFiles      []string `json:"changed_files,omitempty"`
    ValidationStatus  string   `json:"validation_status"` // "passed" | "failed" | "partial"
    FollowUps         []string `json:"follow_ups,omitempty"`
    HumanReviewNeeded bool     `json:"human_review_needed,omitempty"`
}
```

## Run Lifecycle

```text
1.  Create run                  -> status: pending
2.  Resolve workspace scope     -> status: preparing
3.  Resolve profile + policy
4.  Resolve executor + config
5.  Prepare environment         (worktree, ports, temp dir)
6.  Prepare context             -> status: preparing_context
7.  Execute task                -> status: executing
8.  Capture artifacts           (streaming during execute; finalized after)
9.  Run validation              -> status: validating
10. Produce final report
11. Apply final approval gate   -> status: awaiting_approval (if policy.approval != "none")
12. Tear down environment
13. Persist run history         -> status: succeeded | failed | cancelled | rejected
```

Each numbered step is a named lifecycle event. Hooks, post-MVP, attach to these events. Create-time validation rejects unknown profiles and unregistered executor kinds before a run record is persisted. Executors that do not need first-class context preparation may skip step 6 in MVP. For `final_only` approval, the lifecycle pauses after report generation so the user has evidence to review. If required validation checks fail, the run is marked `failed` after report generation instead of entering approval. Rejecting through `POST /reject` at this point sets status to `rejected` and proceeds to teardown. `per_tool` approval, post-MVP, can pause earlier during execution.

## Cross-Cutting Concerns

### Memory

Typed file-based memory lives in the workspace and is shared across runs:

```text
workspace_root/
  memory/
    MEMORY.md            (index)
    user_*.md            (user role, preferences)
    feedback_*.md        (guidance learned from corrections/confirmations)
    project_*.md         (current goals, deadlines, decisions)
    reference_*.md       (pointers to external systems)
```

Runs read memory through their executor's `MemoryPolicy` and may write memory entries as artifacts (`ArtifactKind` = `"memory_update"`, post-MVP). Memory is not a property of a Run; it is a property of the Workspace.

### Hooks

The 13-step lifecycle is the hook surface:

```text
pre_prepare, post_prepare,
pre_execute, post_execute,
pre_validate, post_validate,
on_artifact_captured,
on_approval_required,
on_run_finished
```

Hooks may be shell commands or Ori skills, blocking or non-blocking. Hook schema is reserved but not implemented in MVP.

Not every lifecycle step has a hook surface. The named hooks above are the initial stable surface; narrower hooks such as `pre_resolve_scope` can be added later if real use cases need them.

## API Shape

Workspace-scoped routes:

```text
POST   /api/workspaces/{workspaceID}/runs
GET    /api/workspaces/{workspaceID}/runs
GET    /api/workspaces/{workspaceID}/runs/{runID}
POST   /api/workspaces/{workspaceID}/runs/{runID}/stop
POST   /api/workspaces/{workspaceID}/runs/{runID}/approve
POST   /api/workspaces/{workspaceID}/runs/{runID}/reject
GET    /api/workspaces/{workspaceID}/runs/{runID}/artifacts
GET    /api/workspaces/{workspaceID}/runs/{runID}/trace
```

Create requests should eventually accept `client_request_id` for idempotency so UI retries do not create duplicate runs. This is post-MVP unless the first UI needs retry-safe creation.

`GET /trace` returns the current trace snapshot in MVP. It supports `?since=<sequence>` for polling long-running jobs. Streaming through SSE or WebSocket is post-MVP.

`POST /approve` accepts:

```json
{
  "comment": "Looks good"
}
```

For `final_only` approval, approve finalizes an `awaiting_approval` run as `succeeded` when validation is acceptable. For `per_tool` approval, post-MVP, approve resumes the blocked tool or lifecycle step.

`POST /reject` accepts:

```json
{
  "reason": "Diff touches files outside the requested scope"
}
```

Reject cancels any active executor work, tears down the environment, records the reason, and marks the run `rejected`. It is distinct from `failed`, which means the run or validation failed without an explicit human rejection.

### Example Engineering Request

```json
{
  "profile_id": "engineering",
  "executor": {
    "kind": "native_cli",
    "ref": "codex",
    "config": {
      "model": "gpt-5.3-codex",
      "trace_mode": "stream_json"
    }
  },
  "prompt": "Fix failing workspace task lifecycle tests",
  "scope": {
    "repo_path": "/path/to/repo"
  },
  "validation_request": {
    "profile": "unit"
  },
  "policy": {
    "mutation": "allowed",
    "approval": "final_only"
  },
  "environment": {
    "worktree": true
  }
}
```

### Example Research Request

```json
{
  "profile_id": "research",
  "executor": {
    "kind": "ori_agent",
    "ref": "Researcher",
    "config": {
      "tool_policy": {
        "mode": "allowlist",
        "allowlist": ["mcp:web/*", "skill:cite-*"]
      },
      "context_policy": {
        "strategy": "compact",
        "compact_at": 80000
      }
    }
  },
  "prompt": "Compare current competitor pricing",
  "scope": {
    "target_note_id": "note-123"
  },
  "validation_request": {
    "profile": "citations"
  },
  "policy": {
    "mutation": "allowed",
    "approval": "final_only"
  }
}
```

## MVP

Build this first:

- `Run` model.
- Durable SQLite-backed run store.
- In-memory store allowed only for prototype tests.
- Run HTTP API: create, list, get, stop, artifacts, trace.
- `general` profile.
- `engineering` profile.
- `native_cli` executor: Codex backend first; Claude backend second.
- `ori_agent` executor for Workspace Task execution, using a serialized task payload while generic non-task Ori Agent execution remains a follow-up.
- `workflow` and `system_tool` executor configs defined in schema but execution reserved for post-MVP.
- Artifact capture: status, trace, changed files, diff, logs, validation output.
- Validation profiles: `none`, `unit`.
- Final run report.
- `parent_run_id` field present, with linkage unused in MVP.
- `Cancel(ctx)` on every executor.
- Scope canonicalization and workspace-root enforcement before executor handoff.
- Cost capture for executor runs that report token or USD usage.
- Polling trace API with `?since=<sequence>`.

### Workspace Task Bridge

Workspace Tasks keep their product-facing task lifecycle, but their LLM work is delegated through a `general` Workspace Run with `executor.kind = "ori_agent"` and `scope.target_task_id` set. The task stores `current_run_id`, and each task execution-history row stores the backing `run_id` when one exists. This lets the task page keep its current UI while the harness owns durable execution evidence underneath it.

## Non-Goals for MVP

- PR creation.
- Auto-merge.
- Scheduled self-evolution.
- Multi-agent review.
- Browser validation.
- Hooks; lifecycle surface defined, implementation deferred.
- Memory writes from runs; memory reads allowed.
- Cross-workspace automation.
- Full dashboard.
- Streaming trace transport.
- Idempotent create with `client_request_id`.

## Naming

Use `Workspace Runs` as the UI and product name.

Package layout for MVP should start flatter than the final conceptual boundaries:

```text
internal/workspacerun/
  run.go
  store.go
  http.go
  lifecycle.go
  profile.go
  executor.go
  nativecli.go
  oriagent.go
  scope.go
  policy.go
  environment.go
  artifact.go
  validation.go
  report.go
```

As the code grows, split proven boundaries into subpackages:

```text
internal/workspacerun/profile/
internal/workspacerun/executor/
internal/workspacerun/validation/
internal/workspacerun/artifact/
```

Avoid naming the top-level package around engineering, since engineering is one profile of the broader harness.
