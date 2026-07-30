// Package wakecoord is the seam between the processes that want a macOS wake
// and the one process allowed to program it.
//
// Ori owns exactly one system wake event. Scheduled workspace tasks ask for one
// from inside the server; an Overnight Run asks for one from the Herdr devflow
// helper, which is a separate process started by a LaunchAgent. If both called
// `pmset` there would be two processes each believing they owned Ori's single
// wake, and whichever ran last would silently cancel the other's.
//
// So they do not both call it. Candidates are written to a shared, file-locked
// store that any Ori process may append to, and exactly one owner — the macOS
// wake service inside the server — reads them, programs the earliest, and
// writes back what it actually programmed.
//
// A local file rather than a loopback API is deliberate. The two processes are
// the same user on the same machine, so a 0600 file is already the trust
// boundary that applies to the rest of the runtime state, and this way there is
// no port, no token to distribute, and no new network surface. It also makes
// verification honest: the helper does not get to claim its own wake was
// programmed — it reads back a record written by the only process that ran
// `pmset`. If that record never appears, the wake was never programmed, and the
// Overnight Run stays awake. The failure mode is reached by doing nothing,
// which is the only failure mode worth relying on.
package wakecoord

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// FileName is the shared candidate document.
	FileName = "wake-candidates.json"
	// DocumentVersion is the schema version of that document.
	DocumentVersion = 1
	// MaxCandidates bounds the store so a runaway writer cannot grow it without
	// limit. Ori has a handful of wake sources, not thousands.
	MaxCandidates = 64
	// MaxDetailRunes bounds one candidate's human reason.
	MaxDetailRunes = 200
)

// Known sources. A candidate names the subsystem that wants the wake so
// cancellation can be scoped: withdrawing an Overnight Run's wake must never
// withdraw a scheduled workspace task's.
const (
	SourceWorkspaceTask = "workspace-task"
	SourceOvernightRun  = "herdr-overnight"
)

// Candidate is one requested wake.
type Candidate struct {
	// ID is unique within its source.
	ID string `json:"id"`
	// Source names the subsystem that wants the wake.
	Source string `json:"source"`
	// WakeAt is when the machine must be awake. The owner may program slightly
	// earlier using its own lead time; it never programs later.
	WakeAt time.Time `json:"wake_at"`
	// Detail is a bounded, operator-safe reason. It never carries prompt text,
	// terminal content, or account details.
	Detail string `json:"detail,omitempty"`
	// RegisteredAt is when the candidate was written.
	RegisteredAt time.Time `json:"registered_at"`
	// ExpiresAt bounds how long an unclaimed candidate survives, so a crashed
	// writer cannot leave the machine waking up forever.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// Valid reports whether a candidate is usable at now.
func (c Candidate) Valid(now time.Time) bool {
	if c.ID == "" || c.Source == "" || c.WakeAt.IsZero() {
		return false
	}
	if !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(now) {
		return false
	}
	return c.WakeAt.After(now)
}

// Programmed is what the single owner actually asked macOS for. Only the owner
// writes it, and it is the only evidence any other process may treat as proof
// that a wake exists.
type Programmed struct {
	// CandidateID and Source identify which candidate won.
	CandidateID string `json:"candidate_id"`
	Source      string `json:"source"`
	// WakeAt is the instant programmed, including the owner's lead time.
	WakeAt time.Time `json:"wake_at"`
	// ProgrammedAt is when the owner succeeded.
	ProgrammedAt time.Time `json:"programmed_at"`
	// Detail explains a failure to program, when there was one.
	Detail string `json:"detail,omitempty"`
}

// Owner is what the single pmset owner publishes about itself.
//
// Another process cannot work this out on its own: whether Ori may program a
// wake depends on the user's own settings, which live wherever that Ori server
// keeps them. Rather than make every other process go looking, the owner states
// it here, next to the candidates it is being asked to program.
type Owner struct {
	// Supported is true when this platform can program wake events at all.
	Supported bool `json:"supported"`
	// Enabled is the user's setting.
	Enabled bool `json:"enabled"`
	// ApprovalGranted records that the user authorized Ori to program wakes.
	ApprovalGranted bool `json:"approval_granted"`
	// ReportedAt is when the owner last published this. A stale report means
	// the owner is not running, which is itself the answer.
	ReportedAt time.Time `json:"reported_at"`
}

// Ready reports whether the owner could program a wake if asked.
func (o Owner) Ready() bool { return o.Supported && o.Enabled && o.ApprovalGranted }

// Fresh reports whether the owner reported within window of now. An owner that
// has not spoken recently is not running, and a wake it never sees is a wake
// that will not exist.
func (o Owner) Fresh(now time.Time, window time.Duration) bool {
	if o.ReportedAt.IsZero() {
		return false
	}
	return !o.ReportedAt.Add(window).Before(now)
}

// document is the whole shared file.
type document struct {
	Version    int         `json:"version"`
	Candidates []Candidate `json:"candidates"`
	Programmed *Programmed `json:"programmed,omitempty"`
	Owner      *Owner      `json:"owner,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// Store is the shared candidate file.
type Store struct {
	// Dir holds the document and its lock.
	Dir string
}

// New builds a Store over a directory.
func New(dir string) *Store { return &Store{Dir: dir} }

// Path is the shared document's location.
func (s *Store) Path() string { return filepath.Join(s.Dir, FileName) }

// identifierPattern bounds the values that reach the document. Both writers
// compose these themselves, but a store shared between processes validates
// rather than trusts.
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ErrInvalidCandidate means a candidate could not be accepted.
var ErrInvalidCandidate = errors.New("wake candidate is not valid")

// Register adds or replaces one candidate.
//
// Replacing by (source, id) is deliberate: a run that recalculates its reset
// updates its own candidate rather than accumulating a new one each time.
func (s *Store) Register(candidate Candidate, now time.Time) error {
	if !identifierPattern.MatchString(candidate.ID) || !identifierPattern.MatchString(candidate.Source) {
		return fmt.Errorf("%w: identity is not acceptable", ErrInvalidCandidate)
	}
	if candidate.WakeAt.IsZero() {
		return fmt.Errorf("%w: no wake time", ErrInvalidCandidate)
	}
	candidate.Detail = bounded(candidate.Detail, MaxDetailRunes)
	if candidate.RegisteredAt.IsZero() {
		candidate.RegisteredAt = now.UTC()
	}
	candidate.WakeAt = candidate.WakeAt.UTC()

	return s.mutate(func(doc *document) error {
		replaced := false
		for index := range doc.Candidates {
			if doc.Candidates[index].ID == candidate.ID && doc.Candidates[index].Source == candidate.Source {
				doc.Candidates[index] = candidate
				replaced = true
				break
			}
		}
		if !replaced {
			if len(doc.Candidates) >= MaxCandidates {
				return fmt.Errorf("%w: the wake candidate store is full", ErrInvalidCandidate)
			}
			doc.Candidates = append(doc.Candidates, candidate)
		}
		return nil
	}, now)
}

// hasDocument reports whether the shared file exists at all.
func (s *Store) hasDocument() bool {
	_, err := os.Stat(s.Path())
	return err == nil
}

// Cancel removes one candidate, scoped to its own source.
//
// The scoping is the whole point: an Overnight Run withdrawing its wake must
// not be able to withdraw the wake a scheduled workspace task depends on, even
// by accident and even if the identifiers collided.
func (s *Store) Cancel(source, id string, now time.Time) error {
	if !s.hasDocument() {
		// Nothing was ever registered, so there is nothing to withdraw.
		return nil
	}
	return s.mutate(func(doc *document) error {
		remaining := doc.Candidates[:0]
		for _, candidate := range doc.Candidates {
			if candidate.Source == source && candidate.ID == id {
				continue
			}
			remaining = append(remaining, candidate)
		}
		doc.Candidates = remaining
		return nil
	}, now)
}

// CancelSource removes every candidate belonging to one source.
func (s *Store) CancelSource(source string, now time.Time) error {
	if !s.hasDocument() {
		return nil
	}
	return s.mutate(func(doc *document) error {
		remaining := doc.Candidates[:0]
		for _, candidate := range doc.Candidates {
			if candidate.Source != source {
				remaining = append(remaining, candidate)
			}
		}
		doc.Candidates = remaining
		return nil
	}, now)
}

// Candidates returns every still-valid candidate, earliest first. Expired and
// past candidates are filtered rather than deleted: reading is not a write, and
// the owner prunes them when it next programs a wake.
func (s *Store) Candidates(now time.Time) ([]Candidate, error) {
	doc, err := s.read()
	if err != nil {
		return nil, err
	}
	valid := make([]Candidate, 0, len(doc.Candidates))
	for _, candidate := range doc.Candidates {
		if candidate.Valid(now) {
			valid = append(valid, candidate)
		}
	}
	sort.SliceStable(valid, func(i, j int) bool {
		if !valid[i].WakeAt.Equal(valid[j].WakeAt) {
			return valid[i].WakeAt.Before(valid[j].WakeAt)
		}
		return valid[i].ID < valid[j].ID
	})
	return valid, nil
}

// Earliest returns the candidate that should be programmed next.
func (s *Store) Earliest(now time.Time) (Candidate, bool, error) {
	candidates, err := s.Candidates(now)
	if err != nil || len(candidates) == 0 {
		return Candidate{}, false, err
	}
	return candidates[0], true, nil
}

// Programmed returns what the owner last programmed.
func (s *Store) Programmed() (Programmed, bool, error) {
	doc, err := s.read()
	if err != nil {
		return Programmed{}, false, err
	}
	if doc.Programmed == nil {
		return Programmed{}, false, nil
	}
	return *doc.Programmed, true, nil
}

// RecordProgrammed is called only by the single owner, after `pmset` succeeded.
// It also prunes candidates that can no longer fire, which is the one place
// stale entries are removed.
func (s *Store) RecordProgrammed(programmed Programmed, now time.Time) error {
	return s.mutate(func(doc *document) error {
		record := programmed
		record.ProgrammedAt = now.UTC()
		record.Detail = bounded(record.Detail, MaxDetailRunes)
		doc.Programmed = &record
		remaining := doc.Candidates[:0]
		for _, candidate := range doc.Candidates {
			if candidate.Valid(now) {
				remaining = append(remaining, candidate)
			}
		}
		doc.Candidates = remaining
		return nil
	}, now)
}

// PublishOwner records what the single owner can currently do. Only the owner
// calls it.
func (s *Store) PublishOwner(owner Owner, now time.Time) error {
	return s.mutate(func(doc *document) error {
		record := owner
		record.ReportedAt = now.UTC()
		doc.Owner = &record
		return nil
	}, now)
}

// Owner returns what the owner last published about itself.
func (s *Store) Owner() (Owner, bool, error) {
	doc, err := s.read()
	if err != nil {
		return Owner{}, false, err
	}
	if doc.Owner == nil {
		return Owner{}, false, nil
	}
	return *doc.Owner, true, nil
}

// ClearProgrammed records that no wake is programmed any more.
//
// It reads before it writes. Creating a shared file to record the absence of a
// wake nobody asked for would put state on disk for every install that has
// simply never scheduled one — and, less obviously, for every test that
// constructs a server.
func (s *Store) ClearProgrammed(now time.Time) error {
	programmed, found, err := s.Programmed()
	if err != nil || !found {
		return err
	}
	_ = programmed
	return s.mutate(func(doc *document) error {
		doc.Programmed = nil
		return nil
	}, now)
}

// mutate applies one change under the shared lock.
func (s *Store) mutate(apply func(*document) error, now time.Time) error {
	if strings.TrimSpace(s.Dir) == "" {
		return errors.New("wake coordinator directory is required")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create the wake coordinator directory: %w", err)
	}
	release, err := s.lock()
	if err != nil {
		return err
	}
	defer release()

	doc, err := s.readLocked()
	if err != nil {
		return err
	}
	if err := apply(&doc); err != nil {
		return err
	}
	doc.Version = DocumentVersion
	doc.UpdatedAt = now.UTC()
	return s.write(doc)
}

func (s *Store) read() (document, error) {
	release, err := s.lock()
	if err != nil {
		// A missing directory is an empty store, not a failure: nobody has
		// asked for a wake yet.
		if errors.Is(err, os.ErrNotExist) {
			return document{Version: DocumentVersion}, nil
		}
		return document{}, err
	}
	defer release()
	return s.readLocked()
}

func (s *Store) readLocked() (document, error) {
	// #nosec G304 -- the path is this store's own fixed filename under its
	// configured directory.
	contents, err := os.ReadFile(s.Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document{Version: DocumentVersion}, nil
		}
		return document{}, fmt.Errorf("read wake candidates: %w", err)
	}
	var doc document
	if err := json.Unmarshal(contents, &doc); err != nil {
		// A corrupt shared file must not stop the owner from programming a
		// wake: start again rather than refuse forever.
		return document{Version: DocumentVersion}, nil
	}
	if doc.Version > DocumentVersion {
		return document{}, fmt.Errorf("wake candidates were written by a newer Ori (version %d)", doc.Version)
	}
	return doc, nil
}

func (s *Store) write(doc document) error {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode wake candidates: %w", err)
	}
	temporary, err := os.CreateTemp(s.Dir, ".wake-*.tmp")
	if err != nil {
		return fmt.Errorf("create wake candidates: %w", err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect wake candidates: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write wake candidates: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("flush wake candidates: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close wake candidates: %w", err)
	}
	if err := os.Rename(name, s.Path()); err != nil {
		return fmt.Errorf("publish wake candidates: %w", err)
	}
	return nil
}

func bounded(value string, limit int) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	runes := []rune(cleaned)
	if len(runes) <= limit {
		return cleaned
	}
	return string(runes[:limit])
}

// DirOverrideEnv points both processes at an explicit shared directory. Tests
// and isolated smoke servers set it so nothing reaches the real one.
const DirOverrideEnv = "ORI_WAKE_DIR"

// DefaultDir is where both the Ori server and the Herdr devflow helper look for
// the shared document.
//
// It is derived from the user's own config directory rather than from either
// process's data directory, because neither process knows where the other keeps
// its state — and because both can compute this one independently, without a
// configuration value that could drift between them.
func DefaultDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(DirOverrideEnv)); override != "" {
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve the user config directory: %w", err)
	}
	return filepath.Join(base, "ori", "wake"), nil
}
