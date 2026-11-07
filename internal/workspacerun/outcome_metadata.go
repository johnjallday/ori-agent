package workspacerun

import (
	"context"
	"database/sql"
	"time"
)

// OutcomeMetadataReader checks one already-pinned identity without reading Run
// payloads, prompts, reports, errors or artifact bytes. It grants no authority.
type OutcomeMetadataReader interface {
	OutcomeMetadata(context.Context, string, string) (OutcomeMetadata, error)
}

type OutcomeMetadata struct {
	RunID, WorkspaceID, TicketID string
	Executor                     ExecutorKind
	Status                       RunStatus
	StartedAt, FinishedAt        *time.Time
	ResultCount                  int
	ResultArtifactID             string
	ResultBytes                  int64
}

func (s *SQLiteStore) OutcomeMetadata(ctx context.Context, workspace, run string) (OutcomeMetadata, error) {
	var out OutcomeMetadata
	var started, finished sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,COALESCE(json_extract(scope_json,'$.target_task_id'),''),
 COALESCE(json_extract(executor_json,'$.kind'),''),status,started_at,finished_at,
 (SELECT COUNT(*) FROM workspace_run_artifacts a WHERE a.workspace_id=r.workspace_id AND a.run_id=r.id AND a.kind='file' AND json_extract(a.metadata_json,'$.role')='task_result'),
 (SELECT COALESCE(MIN(a.id),'') FROM workspace_run_artifacts a WHERE a.workspace_id=r.workspace_id AND a.run_id=r.id AND a.kind='file' AND json_extract(a.metadata_json,'$.role')='task_result'),
 (SELECT COALESCE(SUM(length(a.inline)),0) FROM workspace_run_artifacts a WHERE a.workspace_id=r.workspace_id AND a.run_id=r.id AND a.kind='file' AND json_extract(a.metadata_json,'$.role')='task_result')
 FROM workspace_runs r WHERE workspace_id=? AND id=?`, workspace, run).Scan(&out.RunID, &out.WorkspaceID, &out.TicketID, &out.Executor, &out.Status, &started, &finished, &out.ResultCount, &out.ResultArtifactID, &out.ResultBytes)
	if err != nil {
		return OutcomeMetadata{}, err
	}
	out.StartedAt, out.FinishedAt = nullTimePtr(started), nullTimePtr(finished)
	return out, nil
}

func (s *MemoryStore) OutcomeMetadata(ctx context.Context, workspace, run string) (OutcomeMetadata, error) {
	if err := ctx.Err(); err != nil {
		return OutcomeMetadata{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := runKey(workspace, run)
	saved := s.runs[key]
	if saved == nil {
		return OutcomeMetadata{}, ErrRunNotFound
	}
	out := OutcomeMetadata{RunID: saved.ID, WorkspaceID: saved.WorkspaceID, TicketID: saved.Scope.TargetTaskID, Executor: saved.Executor.Kind, Status: saved.Status}
	if saved.StartedAt != nil {
		at := *saved.StartedAt
		out.StartedAt = &at
	}
	if saved.FinishedAt != nil {
		at := *saved.FinishedAt
		out.FinishedAt = &at
	}
	for _, artifact := range s.artifacts[key] {
		if artifact.Kind == ArtifactFile && artifact.Metadata["role"] == "task_result" {
			out.ResultCount++
			if out.ResultArtifactID == "" || artifact.ID < out.ResultArtifactID {
				out.ResultArtifactID = artifact.ID
			}
			out.ResultBytes += int64(len(artifact.Inline))
		}
	}
	return out, nil
}
