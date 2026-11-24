package filesearch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/johnjallday/ori-agent/internal/platform"
	"path/filepath"
	"strconv"
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
	defer func() { _ = stmt.Close() }()
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

// The saved mode only informs the affordance; dispatch always checks the current object.
func indexedReader(name, witness string) string {
	parts := strings.Split(witness, ":")
	if len(parts) != 6 {
		return ""
	}
	mode, err := strconv.ParseUint(parts[4], 10, 16)
	if err != nil || mode&0111 != 0 {
		return ""
	}
	return platform.ReviewDocumentViewer(name)
}

type Hit struct {
	Reader     string    `json:"reader,omitempty"`
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
	return s.QueryLocation(ctx, owner, term, "", cursor, limit)
}

// QueryLocation narrows the authorized generation set before keyset pagination.
// Sources still describes the owner's grants, including those with no coverage.
func (s *Service) QueryLocation(ctx context.Context, owner, term, location, cursor string, limit int) (Page, error) {
	var page Page
	if len(location) > 64 || !utf8.ValidString(term) || len(term) > 512 || utf8.RuneCountInString(term) > 128 || len(cursor) > 128 || limit < 0 || limit > 50 {
		return page, ErrRequest
	}
	if limit == 0 {
		limit = 25
	}
	term = folded(strings.TrimSpace(term))
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.visibleSourcesLocked(ctx, owner)
	if err != nil {
		return page, err
	}
	page.Sources = ss
	page.Hits = []Hit{}
	ids := []string{}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%q:%q:%q", owner, term, location)
	found := location == ""
	for _, v := range ss {
		if location != "" && location != v.ID {
			continue
		}
		found = true
		switch v.State {
		case "ready", "partial", "mapping", "interrupted":
			ids = append(ids, v.ID)
			_, _ = fmt.Fprintf(hash, ":%s:%d", v.ID, v.Generation)
		}
	}
	if !found {
		return Page{}, ErrMissing
	}
	scope, _ := json.Marshal(ids)
	stamp := hex.EncodeToString(hash.Sum(nil))
	var after string
	if cursor != "" {
		prefix, id, ok := strings.Cut(cursor, ".")
		if !ok || prefix != stamp || id == "" || len(id) > 64 {
			return Page{}, ErrMissing
		}
		var valid bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM filename_entries e JOIN filename_sources g ON g.id=e.source AND g.generation=e.generation WHERE e.id=? AND g.owner=? AND e.source IN (SELECT value FROM json_each(?)) AND instr(e.folded,?)>0)`, id, owner, string(scope), term).Scan(&valid); err != nil {
			return Page{}, ErrUnavailable
		}
		if !valid {
			return Page{}, ErrMissing
		}
		after = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT e.id,e.source,e.generation,e.relative,e.basename,e.modified,e.witness FROM filename_entries e JOIN filename_sources g ON g.id=e.source AND g.generation=e.generation WHERE g.owner=? AND e.source IN (SELECT value FROM json_each(?)) AND instr(e.folded,?)>0 AND (?='' OR (-e.modified,e.id)>(SELECT -modified,id FROM filename_entries WHERE id=? AND source IN (SELECT value FROM json_each(?)))) ORDER BY e.modified DESC,e.id LIMIT ?`, owner, string(scope), term, after, after, string(scope), limit+1)
	if err != nil {
		return Page{}, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h Hit
		var rel, witness string
		var ts int64
		if err := rows.Scan(&h.ID, &h.Source, &h.Generation, &rel, &h.Name, &ts, &witness); err != nil {
			return Page{}, ErrUnavailable
		}
		if len(page.Hits) == limit {
			page.Next = stamp + "." + page.Hits[limit-1].ID
			break
		}
		h.Reader = indexedReader(h.Name, witness)
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
