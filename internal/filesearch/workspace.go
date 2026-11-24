package filesearch

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"slices"
	"time"
)

// WorkspaceMatch names a persisted, currently owned external link. For older
// links we have no historical disk identity; confirmation must explicitly
// attest the current link, never silently adopt it as a prior association.
type WorkspaceMatch struct {
	ID        string `json:"id"`
	Link      string `json:"link"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Verify    bool   `json:"verify,omitempty"`
	Connected bool   `json:"connected,omitempty"`
}

type Connection struct {
	Workspace WorkspaceMatch `json:"workspace"`
	Source    string         `json:"source"`
}

func (s *Service) LockWorkspaceSetup() func() { s.setupMu.Lock(); return s.setupMu.Unlock }

func (s *Service) matchesLocked(ctx context.Context, owner string, c choice) ([]WorkspaceMatch, error) {
	if s.ValidLink == nil {
		return nil, nil
	}
	// Equality here is only a prefilter. Never walk candidate or parent paths;
	// observe exactly the selected root, verified through both stores and Files.
	query := `SELECT w.id,w.name,w.folder_slug,r.value->>'$.id',COALESCE(l.witness,''),COALESCE(l.source,''),COALESCE(r.value->>'$.created_at','')
 FROM workspaces w, json_each(w.directory_references_json) r
 LEFT JOIN filename_workspace_links l ON l.owner=w.owner_user_id AND l.workspace_id=w.id AND l.reference_id=r.value->>'$.id'
 WHERE w.owner_user_id=? AND COALESCE(w.status,'') NOT IN ('missing','trashed') AND COALESCE(r.value->>'$.purpose','')='' AND r.value->>'$.workspace_id'=w.id AND r.value->>'$.path'=?
 `
	args := []any{owner, c.root}
	if c.id == "" { // A source listing projects only its saved connection. Do not enumerate upgrade candidates on load.
		query += ` AND w.id=(SELECT workspace_id FROM filename_workspace_links WHERE owner=? AND source=?) AND l.source=?`
		args = append(args, owner, c.source, c.source)
	}
	rows, err := s.DB.QueryContext(ctx, query+` ORDER BY w.id,r.value->>'$.id' LIMIT 65`, args...)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	var out []WorkspaceMatch
	count := 0
	for rows.Next() {
		count++
		if count > 64 {
			return nil, ErrLimit
		}
		var m WorkspaceMatch
		var witness, source, created string
		if err := rows.Scan(&m.ID, &m.Name, &m.Slug, &m.Link, &witness, &source, &created); err != nil {
			return nil, ErrUnavailable
		}
		if m.Link == "" || m.Slug == "" || (witness != "" && witness != c.witness) {
			continue
		}
		m.Verify = witness == ""
		if m.Verify {
			at, e := time.Parse(time.RFC3339Nano, created)
			if e != nil || !rootExistedAt(c.root, c.witness, at) {
				continue
			}
		}
		m.Connected = source != "" && source == c.source
		out = append(out, m)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	// Release the single SQLite connection before consulting the live store.
	if err := rows.Close(); err != nil {
		return nil, ErrUnavailable
	}
	return slices.DeleteFunc(out, func(m WorkspaceMatch) bool { return !s.ValidLink(owner, c.root, m) }), nil
}

// CanCreate rechecks the owner's decision under the setup lock before the
// creator writes anything. A newly appeared match needs a new review.
func (s *Service) CanCreate(ctx context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.reviewedLocked(owner, id)
	if e != nil {
		return e
	}
	m, e := s.matchesLocked(ctx, owner, c)
	if e != nil {
		return e
	}
	if len(c.offered) != 0 || len(m) != 0 {
		return ErrChoice
	}
	return nil
}

// ReviewSource starts a new explicit workspace review from a legacy grant.
// A stored source ID is never enough: check ownership, path, witness and policy.
func (s *Service) ReviewSource(ctx context.Context, owner, id string) (*FolderReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, e := s.sourcesLocked(ctx, owner, false)
	if e != nil {
		return nil, e
	}
	for _, v := range ss {
		if v.ID != id {
			continue
		}
		if !s.current(v) {
			return nil, ErrRoot
		}
		if prev := s.intents[owner]; prev.started && prev.root == "" && time.Now().Before(prev.expires) {
			return nil, ErrChoice
		}
		c := choice{id: rand.Text(), root: v.root, witness: v.witness, kind: s.knownKind(v.root, v.witness), started: true, expires: time.Now().Add(10 * time.Minute), source: v.ID}
		c.offered, e = s.matchesLocked(ctx, owner, c)
		if e != nil {
			return nil, e
		}
		s.intents[owner] = c
		return c.review(), nil
	}
	return nil, ErrMissing
}

func (s *Service) Connect(ctx context.Context, owner, id, workspace, link string, verify bool) (*Connection, error) {
	unlock := s.LockWorkspaceSetup()
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.reviewedLocked(owner, id)
	if e != nil {
		return nil, e
	}
	matches, e := s.matchesLocked(ctx, owner, c)
	if e != nil {
		return nil, e
	}
	if !slices.Equal(matches, c.offered) {
		return nil, ErrChoice
	}
	for _, m := range matches {
		if m.Connected && (m.ID != workspace || m.Link != link) {
			return nil, ErrChoice
		}
	}
	for _, m := range matches {
		if m.ID != workspace || m.Link != link {
			continue
		}
		if m.Connected {
			existing, e := s.sourceMatchLocked(ctx, owner, c.root, c.witness)
			if e != nil || existing == nil || existing.ID != c.source {
				return nil, ErrMissing
			}
			return &Connection{Workspace: m, Source: existing.ID}, nil
		}
		if m.Verify && !verify {
			return nil, ErrChoice
		}
		v, e := s.bindLocked(ctx, owner, c, m, true)
		if e != nil {
			return nil, e
		}
		m.Verify = false
		m.Connected = true
		return &Connection{Workspace: m, Source: v.ID}, nil
	}
	return nil, ErrChoice
}

// BindCreated records the original link witness even without filename consent.
// Called only by the creator after observing the durable anchor and owner.
func (s *Service) BindCreated(ctx context.Context, owner, id, workspace, link string, grant bool) (*Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.reviewedLocked(owner, id)
	if e != nil {
		return nil, e
	}
	m := WorkspaceMatch{ID: workspace, Link: link}
	// Persist the observed identity independently of the optional grant. Even
	// a failed grant must not let a replacement folder inherit this workspace.
	if _, err := s.bindLocked(ctx, owner, c, m, false); err != nil || !grant {
		return nil, err
	}
	return s.bindLocked(ctx, owner, c, m, true)
}

func (s *Service) bindLocked(ctx context.Context, owner string, c choice, m WorkspaceMatch, grant bool) (*Source, error) {
	existing, e := s.sourceMatchLocked(ctx, owner, c.root, c.witness)
	if e != nil {
		return nil, e
	}
	if (existing == nil && c.source != "") || (existing != nil && existing.ID != c.source) {
		return nil, ErrChoice
	}
	release, e := s.Gate.Enter()
	if e != nil {
		return nil, ErrUnavailable
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	var source any
	v := existing
	fresh := grant && v == nil
	if grant {
		if fresh {
			v = &Source{ID: rand.Text(), Label: filepath.Base(c.root), Generation: 1, State: "queued", root: c.root, witness: c.witness}
			if _, e = tx.ExecContext(ctx, `INSERT INTO filename_sources(id,owner,root,witness,generation,state) VALUES(?,?,?,?,1,'queued')`, v.ID, owner, c.root, c.witness); e != nil {
				return nil, ErrUnavailable
			}
		}
		source = v.ID
		// An obsolete link (removed from workspace metadata) is not a connection.
		if _, e = tx.ExecContext(ctx, `UPDATE filename_workspace_links SET source=NULL WHERE owner=? AND source=? AND (workspace_id<>? OR reference_id<>?)`, owner, v.ID, m.ID, m.Link); e != nil {
			return nil, ErrUnavailable
		}
	}
	result, e := tx.ExecContext(ctx, `INSERT INTO filename_workspace_links(owner,workspace_id,reference_id,witness,source)
 SELECT ?,?,?,?,? WHERE EXISTS (SELECT 1 FROM workspaces w,json_each(w.directory_references_json) r WHERE w.id=? AND w.owner_user_id=? AND COALESCE(w.status,'') NOT IN ('missing','trashed') AND r.value->>'$.id'=? AND r.value->>'$.workspace_id'=w.id AND r.value->>'$.path'=? AND COALESCE(r.value->>'$.purpose','')='')
 ON CONFLICT(owner,workspace_id,reference_id) DO UPDATE SET source=COALESCE(excluded.source,filename_workspace_links.source) WHERE filename_workspace_links.witness=excluded.witness`, owner, m.ID, m.Link, c.witness, source, m.ID, owner, m.Link, c.root)
	if e != nil {
		return nil, ErrUnavailable
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 || !s.current(Source{root: c.root, witness: c.witness}) {
		return nil, ErrChoice
	}
	if e = tx.Commit(); e != nil {
		return nil, ErrUnavailable
	}
	if fresh {
		s.startLocked(*v, release)
		release = nil
	}
	if grant {
		c.source = v.ID
		for i := range c.offered {
			if c.offered[i].ID == m.ID && c.offered[i].Link == m.Link {
				c.offered[i].Verify = false
				c.offered[i].Connected = true
			}
		}
		s.intents[owner] = c
	}
	return v, nil
}
