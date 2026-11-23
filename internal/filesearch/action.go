package filesearch

import (
	"context"
	"path/filepath"
	"strings"
)

// Act serializes the final validation/handoff against removal. Only an opaque
// saved row can reach the adapter. No workspace API or filesystem tool grant.
func (s *Service) Act(ctx context.Context, owner, id string, gen int, reveal bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.life.Err() != nil || owner == "" || id == "" || len(id) > 64 {
		return ErrMissing
	}
	var v Source
	var r entry
	err := s.DB.QueryRowContext(ctx, `SELECT g.root,g.witness,e.relative,e.basename,e.modified,e.witness,e.ancestors FROM filename_entries e JOIN filename_sources g ON g.id=e.source AND g.generation=e.generation WHERE e.id=? AND g.owner=? AND e.generation=? AND g.state IN ('ready','partial','mapping','interrupted')`, id, owner, gen).Scan(&v.root, &v.witness, &r.relative, &r.name, &r.modified, &r.witness, &r.chain)
	if err != nil || !s.current(v) || !filepath.IsLocal(r.relative) || filepath.Clean(r.relative) != r.relative || filepath.Base(r.relative) != r.name || strings.Split(r.chain, "/")[0] != v.witness {
		return ErrMissing
	}
	return dispatch(ctx, v, r, reveal, s.dispatch)
}
