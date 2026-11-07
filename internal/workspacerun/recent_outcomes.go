package workspacerun

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// RecentOutcomeReader is an optional bounded metadata projection. It never
// loads executor payloads, errors, trace, reports, tools or artifact contents.
// A full page means coverage may be partial; it is not a proof of competence.
type RecentOutcomeReader interface {
	RecentOutcomes(context.Context, string, time.Time, time.Time, int) ([]RunStatus, error)
}

func validOutcomeWindow(workspaceID string, since, until time.Time, limit int) bool {
	return workspaceID != "" && !since.IsZero() && since.Before(until) && limit > 0 && limit <= 100
}

func (s *SQLiteStore) RecentOutcomes(ctx context.Context, workspaceID string, since, until time.Time, limit int) ([]RunStatus, error) {
	if !validOutcomeWindow(workspaceID, since, until, limit) {
		return nil, fmt.Errorf("invalid recent outcome window")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT status FROM workspace_runs
 WHERE workspace_id=? AND started_at>=? AND finished_at>=started_at AND finished_at<=?
 AND status IN ('succeeded','failed','cancelled','rejected')
 ORDER BY finished_at DESC,id LIMIT ?`, workspaceID, since, until, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []RunStatus{}
	for rows.Next() {
		var status RunStatus
		if err := rows.Scan(&status); err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	return out, rows.Err()
}

func (s *MemoryStore) RecentOutcomes(ctx context.Context, workspaceID string, since, until time.Time, limit int) ([]RunStatus, error) {
	if !validOutcomeWindow(workspaceID, since, until, limit) {
		return nil, fmt.Errorf("invalid recent outcome window")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type outcome struct {
		id     string
		status RunStatus
		at     time.Time
	}
	// Keep at most limit metadata entries, without cloning any Run payload.
	page := make([]outcome, 0, limit+1)
	for _, run := range s.runs {
		if run.WorkspaceID != workspaceID || !terminalRunStatus(run.Status) || run.StartedAt == nil || run.FinishedAt == nil ||
			run.StartedAt.Before(since) || run.FinishedAt.Before(*run.StartedAt) || run.FinishedAt.After(until) {
			continue
		}
		page = append(page, outcome{id: run.ID, status: run.Status, at: *run.FinishedAt})
		sort.Slice(page, func(i, j int) bool {
			if page[i].at.Equal(page[j].at) {
				return page[i].id < page[j].id
			}
			return page[i].at.After(page[j].at)
		})
		if len(page) > limit {
			page = page[:limit]
		}
	}
	out := make([]RunStatus, len(page))
	for n := range page {
		out[n] = page[n].status
	}
	return out, nil
}
