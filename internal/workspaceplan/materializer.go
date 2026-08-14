package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Materialization: turning an approved version into real work.
//
// This is the only place an approval is spent, and the ordering below is the
// whole design. It has to survive a crash at any step and a second caller
// racing it, without ever producing two Task trees or reporting success over a
// failed write.
//
//	1. Replay check   — if this version already materialized, return that result
//	2. Revalidate     — assignees and capabilities, as they are right now
//	3. Compile        — deterministic Task IDs, no side effects
//	4. Stage artifacts— render and path-check, write nothing
//	5. Commit tasks   — through the workspace's validated batch add
//	6. Link           — Plan-to-Task provenance, both directions
//	7. Write artifacts— only after tasks committed
//	8. Consume        — atomically spend the approval and record the result
//	9. Approve        — move the Plan, only now that everything is durable
//
// Deterministic Task IDs are what make a retry safe: step 3 computes the same
// IDs every time, so step 5 recognizes existing work rather than duplicating it
// (FR-91). A crash between 5 and 8 leaves an unconsumed approval and Tasks that
// already exist; the retry finds them, links idempotently, and consumes. No
// duplicates, no orphans (FR-89, FR-90).

// TaskWriter is the workspace persistence the materializer needs.
//
// Update rather than Get+Save is deliberate, and it is the difference between
// correct and nearly-correct here. Materialization has to check whether its
// deterministic Tasks already exist and add the missing ones as ONE atomic
// step: with a separate read and write, two concurrent materializations both
// see "not present" and both add the same IDs. The workspace store's Update
// holds its lock across the whole read-modify-write (FR-89, FR-178).
type TaskWriter interface {
	Get(id string) (*workspace.Workspace, error)
	Update(id string, fn func(*workspace.Workspace) error) error
}

// ArtifactWriter writes a rendered planning artifact into the workspace.
//
// It is an interface because the filesystem is the one part of materialization
// that cannot join the same transaction as the Tasks, so it is staged, written
// last, and compensated on failure (FR-90).
type ArtifactWriter interface {
	// WriteArtifact writes content to a workspace-relative path. The path has
	// already been normalized and checked for escapes; an implementation must
	// still refuse anything it considers unsafe (FR-97, FR-169).
	WriteArtifact(ctx context.Context, workspaceID, relativePath string, content []byte) error
	// RemoveArtifact deletes a previously written artifact, used to
	// compensate a failed materialization.
	RemoveArtifact(ctx context.Context, workspaceID, relativePath string) error
}

// Materializer converts approved Plan versions into Workspace Tasks and
// planning artifacts.
type Materializer struct {
	service   *Service
	tasks     TaskWriter
	artifacts ArtifactWriter
	// renderer produces artifact content from typed Plan content. Rendering is
	// deterministic and application-owned rather than model-written, so an
	// approved plan always produces the same document (FR-96).
	renderer ArtifactRenderer
}

// NewMaterializer returns a materializer over the given services.
func NewMaterializer(service *Service, tasks TaskWriter, opts ...MaterializerOption) *Materializer {
	materializer := &Materializer{
		service:  service,
		tasks:    tasks,
		renderer: DefaultArtifactRenderer{},
	}
	for _, opt := range opts {
		opt(materializer)
	}
	return materializer
}

// MaterializerOption configures a Materializer.
type MaterializerOption func(*Materializer)

// WithArtifactWriter attaches artifact persistence. Without one, a Plan with
// enabled artifacts is refused rather than silently materializing only its
// Tasks — approving "write the PRD" and getting no PRD is not a success.
func WithArtifactWriter(writer ArtifactWriter) MaterializerOption {
	return func(m *Materializer) { m.artifacts = writer }
}

// WithArtifactRenderer replaces the deterministic artifact renderer.
func WithArtifactRenderer(renderer ArtifactRenderer) MaterializerOption {
	return func(m *Materializer) { m.renderer = renderer }
}

// MaterializeInput identifies the approval to spend.
type MaterializeInput struct {
	ApprovalID string
	// Validation carries the agents and capabilities that exist right now.
	// They are checked again here, immediately before writes, because
	// availability at review time is not availability at materialization time
	// (FR-85).
	Validation ValidationContext
}

// MaterializeResult reports what an approval produced.
type MaterializeResult struct {
	PlanID  string   `json:"plan_id"`
	Version int      `json:"plan_version"`
	TaskIDs []string `json:"task_ids"`
	// ArtifactPaths are the workspace-relative files written.
	ArtifactPaths []string `json:"artifact_paths,omitempty"`
	// Replayed is true when this call found the work already done and returned
	// the original result rather than doing it again (FR-73).
	Replayed bool `json:"replayed"`
	// StartExecution mirrors the approval's declared effect, so the caller
	// knows whether to dispatch without re-deriving it (FR-103).
	StartExecution bool `json:"start_execution"`
	// Actor is the approving user. Automatic execution is attributed to them
	// rather than to whoever happened to send the materialize request: the
	// approval is what authorized the work, so it is whose name belongs on it
	// (FR-79, FR-87).
	Actor string `json:"actor,omitempty"`
	// Launched and LaunchReason report what automatic execution did. A false
	// Launched with a reason is the honest answer for an automatic Plan that
	// materialized but could not begin — it never looks like it started.
	Launched     bool   `json:"launched"`
	LaunchReason string `json:"launch_reason,omitempty"`
}

// Materialize spends an approval and creates the work it authorized.
func (m *Materializer) Materialize(ctx context.Context, workspaceID, planID string, input MaterializeInput) (*MaterializeResult, error) {
	store := m.service.Store()

	plan, err := store.GetPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	approval, err := store.GetApproval(ctx, workspaceID, planID, input.ApprovalID)
	if err != nil {
		return nil, err
	}

	// 1. Replay. An approval already spent returns what it produced, so a
	//    retried request is answered rather than repeated (FR-72, FR-73).
	if approval.Consumed() {
		return replayResult(plan, approval), nil
	}
	if approval.Invalidated() {
		return nil, fmt.Errorf("%w: this approval was invalidated (%s)",
			ErrApprovalMismatch, approval.InvalidatedReason)
	}

	version, err := store.GetVersion(ctx, workspaceID, planID, approval.Version)
	if err != nil {
		return nil, err
	}
	// The approval binds to a content hash; if the version it names no longer
	// hashes the same, something is deeply wrong and no work should follow.
	if approval.ContentHash != version.ContentHash {
		return nil, fmt.Errorf("%w: the approved version no longer matches its approval",
			ErrApprovalMismatch)
	}

	// 2. Revalidate against the world as it is now, not as it was at review.
	if result := ValidatePlanContent(version.Objective, version.Content, input.Validation); !result.OK() {
		return nil, result.Error()
	}

	now := m.service.Now()

	// 3. Compile. Pure: nothing is written yet.
	compiled, err := CompileTaskTree(CompileInput{
		Plan: plan, Version: version,
		ApprovalID: approval.ID, ApprovedBy: approval.UserName,
		Now: now,
	})
	if err != nil {
		return nil, err
	}

	// 4. Stage artifacts: render and path-check before anything is committed,
	//    so an unsafe path fails the whole materialization rather than leaving
	//    Tasks behind (FR-97, FR-98).
	staged, err := m.stageArtifacts(plan, version)
	if err != nil {
		return nil, err
	}

	// 5. Commit tasks through the workspace's own validated batch add, which
	//    checks the whole graph and rolls the batch back on failure (FR-92).
	taskIDs, err := m.commitTasks(plan.WorkspaceID, compiled)
	if err != nil {
		return nil, fmt.Errorf("materialize plan tasks: %w", err)
	}

	// 6. Link. Idempotent by the store's unique index, so a racing retry
	//    writes nothing new (FR-91).
	links := make([]TaskLink, 0, len(compiled))
	for _, entry := range compiled {
		links = append(links, entry.Link)
	}
	if err := store.LinkTasks(ctx, workspaceID, planID, links); err != nil {
		return nil, fmt.Errorf("link plan tasks: %w", err)
	}

	// 7. Artifacts last, because they are the only effect that cannot be
	//    rolled back by the Task path.
	written, err := m.writeArtifacts(ctx, plan.WorkspaceID, staged)
	if err != nil {
		// Compensate what was written before failing, so a partial document
		// set does not survive a failed materialization (FR-90).
		m.removeArtifacts(ctx, plan.WorkspaceID, written)
		return nil, err
	}

	// 8. Consume. This is the mutual exclusion: two callers reaching here both
	//    issue the conditional update, exactly one wins, and the loser replays
	//    the winner's result (FR-72, FR-178).
	result := ApprovalResult{
		TaskIDs:       taskIDs,
		ArtifactPaths: pathsOf(staged),
		Started:       approval.Effect.StartsExecution(),
		CompletedAt:   now,
	}
	if err := store.ConsumeApproval(ctx, workspaceID, planID, approval.ID, result, now); err != nil {
		if errors.Is(err, ErrApprovalConsumed) {
			// Someone else materialized this approval while we worked. Their
			// Tasks are ours — the IDs are deterministic — so nothing was
			// duplicated and nothing needs cleaning up.
			winner, getErr := store.GetApproval(ctx, workspaceID, planID, approval.ID)
			if getErr != nil {
				return nil, getErr
			}
			return replayResult(plan, winner), nil
		}
		return nil, err
	}

	// 9. Only now is the Plan approved. Reaching this status means the work
	//    exists and is durable (FR-94).
	if err := m.markApproved(ctx, plan, version, approval, now); err != nil {
		return nil, err
	}

	return &MaterializeResult{
		PlanID:         plan.ID,
		Version:        version.Number,
		TaskIDs:        taskIDs,
		ArtifactPaths:  pathsOf(staged),
		StartExecution: approval.Effect.StartsExecution(),
		Actor:          approval.UserName,
	}, nil
}

func replayResult(plan *Plan, approval *Approval) *MaterializeResult {
	result := &MaterializeResult{
		PlanID:         plan.ID,
		Version:        approval.Version,
		Replayed:       true,
		StartExecution: approval.Effect.StartsExecution(),
		Actor:          approval.UserName,
	}
	if approval.ConsumedResult != nil {
		result.TaskIDs = approval.ConsumedResult.TaskIDs
		result.ArtifactPaths = approval.ConsumedResult.ArtifactPaths
	}
	return result
}

// commitTasks writes the compiled tree through workspace.AddTasks.
//
// Tasks that already exist are skipped rather than re-added: after a crash
// between commit and consume, a retry recomputes the same deterministic IDs and
// must recognize its own earlier work instead of failing or duplicating it.
func (m *Materializer) commitTasks(workspaceID string, compiled []CompiledTask) ([]string, error) {
	if m.tasks == nil {
		return nil, fmt.Errorf("%w: no task store is configured", ErrValidation)
	}

	taskIDs := make([]string, 0, len(compiled))
	for _, entry := range compiled {
		taskIDs = append(taskIDs, entry.Task.ID)
	}

	// The whole check-and-add happens inside the store's lock. Doing the check
	// outside it lets two concurrent materializations both conclude the Tasks
	// are missing and both add them — which the graph validator catches as
	// duplicate IDs, failing an operation that should have simply been a
	// no-op for the second caller (FR-89, FR-178).
	err := m.tasks.Update(workspaceID, func(ws *workspace.Workspace) error {
		if ws == nil {
			return fmt.Errorf("%w: %s", ErrWorkspaceNotFound, workspaceID)
		}

		existing := make(map[string]struct{}, len(ws.Tasks))
		for _, task := range ws.Tasks {
			existing[task.ID] = struct{}{}
		}

		pending := make([]workspace.Task, 0, len(compiled))
		for _, entry := range compiled {
			if _, already := existing[entry.Task.ID]; already {
				continue
			}
			pending = append(pending, entry.Task)
		}
		if len(pending) == 0 {
			// Everything already exists: either a retry after a crash between
			// commit and consume, or a concurrent caller that got here first.
			// Both are fine, and the IDs are still the answer.
			return nil
		}

		// AddTasks validates the batch as a whole and restores the workspace
		// if the graph is invalid, so a partial tree cannot survive (FR-89).
		return ws.AddTasks(pending)
	})
	if err != nil {
		// Never claim tasks were created when the write did not land (FR-99).
		return nil, err
	}
	return taskIDs, nil
}

// stagedArtifact is one rendered document waiting to be written.
type stagedArtifact struct {
	Path    string
	Content []byte
}

// stageArtifacts renders every enabled artifact and validates its path, before
// anything is committed. An unsafe path fails here rather than after Tasks
// exist (FR-95, FR-97, FR-98).
func (m *Materializer) stageArtifacts(plan *Plan, version *Version) ([]stagedArtifact, error) {
	enabled := version.Content.EnabledArtifacts()
	if len(enabled) == 0 {
		return nil, nil
	}
	if m.artifacts == nil {
		// Approving "write the PRD" and getting no PRD is not a success.
		return nil, fmt.Errorf(
			"%w: this plan approves %d artifact write(s) but no artifact writer is configured",
			ErrValidation, len(enabled))
	}

	staged := make([]stagedArtifact, 0, len(enabled))
	for _, artifact := range enabled {
		path, err := NormalizeArtifactPath(artifact.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnsafePath, err)
		}
		content, err := m.renderer.Render(artifact, plan, version)
		if err != nil {
			return nil, err
		}
		staged = append(staged, stagedArtifact{Path: path, Content: content})
	}
	return staged, nil
}

func (m *Materializer) writeArtifacts(ctx context.Context, workspaceID string, staged []stagedArtifact) ([]string, error) {
	written := make([]string, 0, len(staged))
	for _, artifact := range staged {
		if err := m.artifacts.WriteArtifact(ctx, workspaceID, artifact.Path, artifact.Content); err != nil {
			return written, fmt.Errorf("write plan artifact %s: %w", artifact.Path, err)
		}
		written = append(written, artifact.Path)
	}
	return written, nil
}

func (m *Materializer) removeArtifacts(ctx context.Context, workspaceID string, paths []string) {
	if m.artifacts == nil {
		return
	}
	for _, path := range paths {
		// Compensation is best-effort by nature: the failure that brought us
		// here may also prevent cleanup. The materialization still fails.
		_ = m.artifacts.RemoveArtifact(ctx, workspaceID, path)
	}
}

// markApproved records the version decision, the activity, and the Plan's move
// to approved — in that order, so a Plan is never reported approved before the
// record explaining why exists.
func (m *Materializer) markApproved(ctx context.Context, plan *Plan, version *Version, approval *Approval, now time.Time) error {
	store := m.service.Store()

	if err := store.SetVersionDecision(ctx, plan.WorkspaceID, plan.ID, version.Number,
		VersionApproved, approval.UserName, "", now); err != nil {
		return err
	}

	consumed := NewActivity(plan, ActivityApprovalConsumed, SourceService, approval.UserName, "")
	consumed.Version = version.Number
	consumed.ApprovalID = approval.ID
	consumed.CreatedAt = now
	if _, err := store.AppendActivity(ctx, consumed); err != nil {
		return err
	}

	materialized := NewActivity(plan, ActivityMaterialized, SourceService, approval.UserName, "")
	materialized.Version = version.Number
	materialized.ApprovalID = approval.ID
	materialized.CreatedAt = now
	if _, err := store.AppendActivity(ctx, materialized); err != nil {
		return err
	}

	// The move to approved is authorized by the consumed approval itself, not
	// by this code claiming to be a user. The approval was re-read after
	// consumption so the check sees it spent (FR-59, FR-94).
	spent, err := store.GetApproval(ctx, plan.WorkspaceID, plan.ID, approval.ID)
	if err != nil {
		return err
	}
	if err := ValidateApprovalTransition(plan.Status, StatusApproved, spent, plan.ID, version.Number); err != nil {
		// The plan moved under us — cancelled mid-materialization, most
		// likely. The work exists and the approval is spent, so this is
		// reported rather than forced or swallowed: a caller that believes a
		// plan is approved when it is not would act on a false premise.
		return fmt.Errorf("materialized version %d but could not move the plan to approved: %w",
			version.Number, err)
	}

	change := NewStatusChange(plan, StatusApproved, SourceService, approval.UserName,
		fmt.Sprintf("materialized version %d", version.Number))
	change.Version = version.Number
	change.ApprovalID = approval.ID
	change.CreatedAt = now
	return store.SetPlanStatus(ctx, plan.WorkspaceID, plan.ID, StatusApproved, change)
}

func pathsOf(staged []stagedArtifact) []string {
	if len(staged) == 0 {
		return nil
	}
	paths := make([]string, 0, len(staged))
	for _, artifact := range staged {
		paths = append(paths, artifact.Path)
	}
	return paths
}

// NormalizeArtifactPath cleans a workspace-relative artifact path and refuses
// anything that escapes the workspace root.
//
// It is deliberately separate from ValidateArtifactPath's authoring-time check:
// this is the boundary that actually writes a file, and a path is untrusted
// input wherever it arrived from (FR-97, FR-169).
func NormalizeArtifactPath(path string) (string, error) {
	if err := ValidateArtifactPath(path); err != nil {
		return "", err
	}
	cleaned := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")))
	// Clean can still produce an escape from input like "a/../../b"; the check
	// after cleaning is the one that counts.
	if cleaned == "." || cleaned == "/" || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("artifact path %q must not escape the workspace root", path)
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("artifact path %q must be relative to the workspace", path)
	}
	return cleaned, nil
}
