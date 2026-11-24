package filesearch

import (
	"math"
	"regexp"
	"time"
	"unicode/utf8"
)

// A tab may retain one descriptor per view, not a query history. No result
// records, document paths, question state, or answers belong in this table.
type DeskPlace struct {
	Term       string `json:"term"`
	Source     string `json:"source"`
	Selected   string `json:"selected"`
	Generation int    `json:"generation"`
	Pages      int    `json:"pages"`
}
type MapPlace struct {
	Selected string  `json:"selected"`
	CenterX  float64 `json:"centerX"`
	CenterY  float64 `json:"centerY"`
	Zoom     float64 `json:"zoom"`
}

// FilesPlace is what Home's Files view keeps in a tab for its own search: the
// term, the scope the user chose (a small JSON document the page wrote and
// reads back), which file in a searchable folder was selected, and how many
// pages of each kind of result were loaded ("notes:2,files:3"). It is its own
// slot, beside the standalone page's DeskPlace, because that one holds a single
// folder and a single page count: sharing it would put Files' term into the
// standalone page and the standalone page's into Files.
//
// Like the others it lives in server memory for this tab only, never in the
// address or in browser storage, and expires with the tab.
type FilesPlace struct {
	Term     string `json:"term"`
	Scope    string `json:"scope"`
	Selected string `json:"selected"`
	Pages    string `json:"pages"`
}

var filesPages = regexp.MustCompile(`^([a-z]{1,16}:[0-9]{1,3})(,[a-z]{1,16}:[0-9]{1,3})*$`)

func validFilesPlace(p *FilesPlace) bool {
	return len(p.Term) <= 512 && utf8.RuneCountInString(p.Term) <= 128 && utf8.ValidString(p.Term) &&
		len(p.Scope) <= 640 && utf8.ValidString(p.Scope) &&
		len(p.Selected) <= 128 && utf8.ValidString(p.Selected) &&
		(p.Pages == "" || (len(p.Pages) <= 160 && filesPages.MatchString(p.Pages)))
}

type tabKey struct{ owner, handle string }
type tabPlace struct {
	desk    *DeskPlace
	team    *MapPlace
	files   *FilesPlace
	expires *time.Timer
}

var tabHandle = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Caller holds s.mu. Never enumerate tabs. Expire even without another read.
func (s *Service) tab(owner, handle string, write bool) (*tabPlace, error) {
	if owner == "" || !tabHandle.MatchString(handle) {
		return nil, ErrRequest
	}
	if s.life.Err() != nil {
		return nil, ErrUnavailable
	}
	k := tabKey{owner, handle}
	old := s.places[k]
	if old == nil && !write {
		return nil, nil
	}
	if old != nil {
		old.expires.Stop()
	} else if len(s.places) >= 64 {
		return nil, ErrLimit
	}
	v := &tabPlace{}
	if old != nil {
		v.desk, v.team, v.files = old.desk, old.team, old.files
	}
	v.expires = time.AfterFunc(30*time.Minute, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.places[k] == v {
			delete(s.places, k)
		}
	})
	s.places[k] = v
	return v, nil
}
func (s *Service) DeskPlace(owner, key string, next *DeskPlace) (*DeskPlace, error) {
	if next != nil && (len(next.Term) > 512 || utf8.RuneCountInString(next.Term) > 128 || len(next.Source) > 64 || len(next.Selected) > 64 || next.Generation < 0 || next.Pages < 1 || next.Pages > 10000) {
		return nil, ErrRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.tab(owner, key, next != nil)
	if err != nil || v == nil {
		return nil, err
	}
	if next != nil {
		copy := *next
		v.desk = &copy
		return nil, nil
	}
	if v.desk == nil {
		return nil, nil
	}
	copy := *v.desk
	return &copy, nil
}

// FilesPlace reads (next == nil) or replaces this tab's Files search place.
func (s *Service) FilesPlace(owner, key string, next *FilesPlace) (*FilesPlace, error) {
	if next != nil && !validFilesPlace(next) {
		return nil, ErrRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.tab(owner, key, next != nil)
	if err != nil || v == nil {
		return nil, err
	}
	if next != nil {
		copy := *next
		v.files = &copy
		return nil, nil
	}
	if v.files == nil {
		return nil, nil
	}
	copy := *v.files
	return &copy, nil
}
func (s *Service) MapPlace(owner, key string, next *MapPlace) (*MapPlace, error) {
	if next != nil && (len(next.Selected) > 128 || math.Abs(next.CenterX) > 1e9 || math.Abs(next.CenterY) > 1e9 || !(next.Zoom > 0 && next.Zoom <= 32)) {
		return nil, ErrRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.tab(owner, key, next != nil)
	if err != nil || v == nil {
		return nil, err
	}
	if next != nil {
		copy := *next
		v.team = &copy
		return nil, nil
	}
	if v.team == nil {
		return nil, nil
	}
	copy := *v.team
	return &copy, nil
}
