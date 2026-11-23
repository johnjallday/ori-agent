package filesearch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type entry struct {
	relative, name, witness, chain string
	modified                       int64
}

func (s *Service) publish(ctx context.Context, v Source, batch []entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.life.Err() != nil {
		return ErrMissing
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO filename_entries(id,source,generation,relative,basename,folded,modified,witness,ancestors) SELECT ?,id,generation,?,?,?,?,?,? FROM filename_sources WHERE id=? AND generation=? AND state='mapping'`)
	if err != nil {
		return ErrUnavailable
	}
	defer stmt.Close()
	for _, r := range batch {
		res, e := stmt.ExecContext(ctx, rand.Text(), r.relative, r.name, folded(r.name), r.modified, r.witness, r.chain, v.ID, v.Generation)
		if e != nil {
			return ErrUnavailable
		}
		if n, e := res.RowsAffected(); e != nil || n != 1 {
			return ErrMissing
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

type Hit struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Generation int       `json:"generation"`
	Name       string    `json:"name"`
	Parent     string    `json:"parent"`
	Modified   time.Time `json:"modified"`
}
type Page struct {
	Hits    []Hit    `json:"hits"`
	Next    string   `json:"next,omitempty"`
	Sources []Source `json:"sources"`
}

func (s *Service) Query(ctx context.Context, owner, term, cursor string, limit int) (Page, error) {
	var page Page
	if !utf8.ValidString(term) || len(term) > 512 || utf8.RuneCountInString(term) > 128 || len(cursor) > 128 || limit < 0 || limit > 50 {
		return page, ErrRequest
	}
	if limit == 0 {
		limit = 25
	}
	term = folded(strings.TrimSpace(term))
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.sourcesLocked(ctx, owner, true)
	if err != nil {
		return page, err
	}
	page.Sources = ss
	page.Hits = []Hit{}
	ids := []string{}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%q:%q", owner, term)
	for _, v := range ss {
		switch v.State {
		case "ready", "partial", "mapping", "interrupted":
			ids = append(ids, v.ID)
			_, _ = fmt.Fprintf(hash, ":%s:%d", v.ID, v.Generation)
		}
	}
	scope, _ := json.Marshal(ids)
	stamp := hex.EncodeToString(hash.Sum(nil))
	var after string
	if cursor != "" {
		prefix, id, ok := strings.Cut(cursor, ".")
		if !ok || prefix != stamp || len(id) != 26 {
			return Page{}, ErrMissing
		}
		after = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT e.id,e.source,e.generation,e.relative,e.basename,e.modified FROM filename_entries e JOIN filename_sources g ON g.id=e.source AND g.generation=e.generation WHERE g.owner=? AND e.source IN (SELECT value FROM json_each(?)) AND instr(e.folded,?)>0 AND (?='' OR (-e.modified,e.id)>(SELECT -modified,id FROM filename_entries WHERE id=? AND source IN (SELECT value FROM json_each(?)))) ORDER BY e.modified DESC,e.id LIMIT ?`, owner, string(scope), term, after, after, string(scope), limit+1)
	if err != nil {
		return Page{}, ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var h Hit
		var rel string
		var ts int64
		if err := rows.Scan(&h.ID, &h.Source, &h.Generation, &rel, &h.Name, &ts); err != nil {
			return Page{}, ErrUnavailable
		}
		if len(page.Hits) == limit {
			page.Next = stamp + "." + page.Hits[limit-1].ID
			break
		}
		h.Parent = filepath.Dir(rel)
		if h.Parent == "." {
			h.Parent = ""
		}
		h.Modified = time.Unix(0, ts).UTC()
		page.Hits = append(page.Hits, h)
	}
	if rows.Err() != nil {
		return Page{}, ErrUnavailable
	}
	return page, nil
}
