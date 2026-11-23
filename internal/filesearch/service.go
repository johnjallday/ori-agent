package filesearch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/knownfolder"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

const maxSources = 8

var errUnreadable = errors.New("unreadable search entry")

type Source struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Generation    int    `json:"generation"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	Count         int    `json:"count"`
	MappedAt      int64  `json:"mapped_at,omitempty"`
	root, witness string
}

type Options struct {
	DB     *sql.DB
	Policy func() Policy
	Pick   func(context.Context) (string, bool, error)
	Gate   *resetstate.WorkGate
}
type choice struct {
	id, kind string
	expires  time.Time
	started  bool
}
type Service struct {
	Options
	mu           sync.Mutex // serializes grant changes, publication, and snapshots
	intents      map[string]choice
	jobs         map[string]context.CancelFunc
	serial       chan struct{}
	life         context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	dispatch     func(context.Context, string, string) error // nil uses the fixed native document adapter
	afterPublish func(context.Context)                       // deterministic test barrier, set before starting work
}

func New(ctx context.Context, o Options) (*Service, error) {
	if o.DB == nil || o.Policy == nil || o.Pick == nil {
		return nil, ErrUnavailable
	}
	// Nothing enqueues on reopen. Retain any committed rows as interrupted coverage.
	if _, err := o.DB.ExecContext(ctx, `UPDATE filename_sources SET state='interrupted',reason='Refresh to finish mapping.' WHERE state IN ('queued','mapping')`); err != nil {
		return nil, ErrUnavailable
	}
	life, cancel := context.WithCancel(context.Background())
	return &Service{Options: o, intents: map[string]choice{}, jobs: map[string]context.CancelFunc{}, serial: make(chan struct{}, 1), life: life, cancel: cancel}, nil
}
func (s *Service) Close() { s.mu.Lock(); s.cancel(); clear(s.intents); s.mu.Unlock(); s.wg.Wait() }

// Begin is a mutation, not a status read. At most one short-lived choice per local owner.
func (s *Service) Begin(owner, kind string) (string, error) {
	if owner == "" || (kind != "documents" && kind != "downloads" && kind != "custom") {
		return "", ErrRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.life.Err() != nil {
		return "", ErrUnavailable
	}
	if prev := s.intents[owner]; prev.started && time.Now().Before(prev.expires) {
		return "", ErrChoice
	}
	id := rand.Text()
	s.intents[owner] = choice{id: id, kind: kind, expires: time.Now().Add(2 * time.Minute)}
	return id, nil
}

// Select accepts only this owner's single-use intent. Even if the dialog is still
// open, removal/expiry invalidates it before it can commit or start work.
func (s *Service) Select(ctx context.Context, owner, id string) (*Source, error) {
	s.mu.Lock()
	c := s.intents[owner]
	if id == "" || c.id != id || c.started || time.Now().After(c.expires) || s.life.Err() != nil {
		s.mu.Unlock()
		return nil, ErrChoice
	}
	c.started = true
	s.intents[owner] = c
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	ctx, cancel := context.WithDeadline(ctx, c.expires)
	stop := context.AfterFunc(s.life, cancel)
	defer stop()
	defer cancel()
	defer func() {
		s.mu.Lock()
		if s.intents[owner].id == id {
			delete(s.intents, owner)
		}
		s.mu.Unlock()
	}()
	var path string
	var err error
	if c.kind == "custom" {
		var chosen bool
		path, chosen, err = s.Pick(ctx)
		if err != nil {
			return nil, ErrUnavailable
		}
		if !chosen {
			return nil, nil
		}
	} else {
		home, e := physicalGuard(s.Policy().Home)
		if e != nil {
			return nil, ErrUnavailable
		}
		name := map[string]string{"documents": "Documents", "downloads": "Downloads"}[c.kind]
		path, err = knownfolder.Resolve(home, c.kind, nil)
		if err != nil {
			return nil, ErrUnavailable
		}
		if path != filepath.Join(home, name) {
			return nil, ErrRoot
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intents[owner].id != id || ctx.Err() != nil || s.life.Err() != nil {
		return nil, ErrChoice
	}
	if !s.Policy().allows(path) {
		return nil, ErrRoot
	}
	w, err := rootWitness(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	sources, err := s.sourcesLocked(ctx, owner, false)
	if err != nil {
		return nil, err
	}
	for _, v := range sources {
		if v.witness == w && v.root == path {
			return &v, nil
		}
		if overlap(path, v.root) || v.witness == w {
			return nil, ErrOverlap
		}
	}
	if len(sources) >= maxSources {
		return nil, ErrLimit
	}
	v := Source{ID: rand.Text(), Label: filepath.Base(path), Generation: 1, State: "queued", root: path, witness: w}
	release, err := s.Gate.Enter()
	if err != nil {
		return nil, ErrUnavailable
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO filename_sources(id,owner,root,witness,generation,state) VALUES(?,?,?,?,1,'queued')`, v.ID, owner, path, w)
	if err != nil {
		release()
		return nil, ErrUnavailable
	}
	s.startLocked(v, release)
	return &v, nil
}
func (s *Service) Sources(ctx context.Context, owner string) ([]Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sourcesLocked(ctx, owner, true)
}
func (s *Service) sourcesLocked(ctx context.Context, owner string, verify bool) ([]Source, error) {
	if owner == "" || s.life.Err() != nil {
		return nil, ErrUnavailable
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,root,witness,generation,state,reason,mapped_at,(SELECT COUNT(*) FROM filename_entries WHERE source=g.id AND generation=g.generation) FROM filename_sources g WHERE owner=? ORDER BY rowid LIMIT 9`, owner)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	out := []Source{}
	for rows.Next() {
		var v Source
		if err = rows.Scan(&v.ID, &v.root, &v.witness, &v.Generation, &v.State, &v.Reason, &v.MappedAt, &v.Count); err != nil {
			return nil, ErrUnavailable
		}
		v.Label = filepath.Base(v.root)
		if verify && !s.current(v) {
			v.State = "unavailable"
			v.Reason = "Reconnect the original folder and refresh, or remove it and choose another."
			v.Count = 0
		}
		out = append(out, v)
	}
	if rows.Err() != nil || len(out) > maxSources {
		return nil, ErrUnavailable
	}
	return out, nil
}
func (s *Service) current(v Source) bool {
	if !s.Policy().allows(v.root) {
		return false
	}
	w, err := rootWitness(v.root)
	return err == nil && w == v.witness
}
func (s *Service) Remove(ctx context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == "" || s.life.Err() != nil {
		return ErrUnavailable
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM filename_sources WHERE id=? AND owner=?`, id, owner)
	if err != nil {
		return ErrUnavailable
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return ErrMissing
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM filename_entries WHERE source=?`, id); err != nil {
		return ErrUnavailable
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	delete(s.intents, owner)
	if cancel := s.jobs[id]; cancel != nil {
		cancel()
	}
	return nil
}
func (s *Service) Refresh(ctx context.Context, owner, id string, gen int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.sourcesLocked(ctx, owner, false)
	if err != nil {
		return err
	}
	for _, v := range ss {
		if v.ID != id || v.Generation != gen {
			continue
		}
		// A prior failed read is retryable only on the same physical root.
		if !s.current(v) || s.jobs[id] != nil {
			return ErrUnavailable
		}
		release, e := s.Gate.Enter()
		if e != nil {
			return ErrUnavailable
		}
		tx, e := s.DB.BeginTx(ctx, nil)
		if e == nil {
			defer func() { _ = tx.Rollback() }()
			_, e = tx.ExecContext(ctx, `DELETE FROM filename_entries WHERE source=?`, v.ID)
			if e == nil {
				_, e = tx.ExecContext(ctx, `UPDATE filename_sources SET generation=generation+1,state='queued',reason='' WHERE id=? AND owner=?`, id, owner)
			}
			if e == nil {
				e = tx.Commit()
			}
		}
		if e != nil {
			release()
			return ErrUnavailable
		}
		v.Generation++
		s.startLocked(v, release)
		return nil
	}
	return ErrMissing
}

func (s *Service) startLocked(v Source, release func()) {
	ctx, cancel := context.WithCancel(s.life)
	s.jobs[v.ID] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer release()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.jobs, v.ID); s.mu.Unlock() }()
		select {
		case s.serial <- struct{}{}:
			defer func() { <-s.serial }()
		case <-ctx.Done():
			return
		}
		s.scan(ctx, v)
	}()
}
func (s *Service) scan(ctx context.Context, v Source) {
	update := func(state, reason string, done bool) error {
		t := int64(0)
		if done {
			t = time.Now().Unix()
		}
		_, err := s.DB.ExecContext(ctx, `UPDATE filename_sources SET state=?,reason=?,mapped_at=CASE WHEN ?>0 THEN ? ELSE mapped_at END WHERE id=? AND generation=?`, state, reason, t, t, v.ID, v.Generation)
		return err
	}
	if update("mapping", "", false) != nil {
		return
	}
	scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := walk(scanCtx, v, func(rows []entry) error {
		if !s.current(v) {
			return ErrUnavailable
		}
		if err := s.publish(scanCtx, v, rows); err != nil {
			return err
		}
		if s.afterPublish != nil {
			s.afterPublish(scanCtx)
		}
		return nil
	})
	if ctx.Err() != nil {
		return
	}
	state, reason := "ready", ""
	switch {
	case errors.Is(err, ErrUnavailable) || !s.current(v):
		state, reason = "unavailable", "Cannot read this folder."
	case errors.Is(err, errUnreadable):
		state, reason = "partial", "Some folders or filenames could not be read."
	case errors.Is(err, ErrMissing):
		state, reason = "partial", "A folder changed or could not be checked during mapping."
	case err != nil:
		state, reason = "partial", "Not every entry fit within the search limits."
	}
	_ = update(state, reason, state == "ready" || state == "partial")
}
