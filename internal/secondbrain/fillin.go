package secondbrain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The ledger's bounds (FR7). A full ledger and a full look together still fit
// RecordMaxBytes (TestSecondBrainCodec_EveryCapFullFitsTheRecord).
const (
	MaxMirrors         = 400
	MaxMirrorPeople    = 100
	MaxMirrorFollowUps = 200
	MaxMirrorProjects  = 50
	// MaxSyncWrites bounds the notes one sync writes (FR6).
	MaxSyncWrites = 40

	// maxMirrorRelBytes caps a ledger entry's path, as JSON encodes it. A full
	// look leaves about 180 bytes per entry, so this is what the record can hold,
	// and a name that would run past it is shortened in the file name only.
	maxMirrorRelBytes = 96
	mirrorHashLength  = 16
)

// Mirror states (FR8).
const (
	// MirrorCurrent is a note Ori wrote and keeps up to date.
	MirrorCurrent = "current"
	// MirrorStale is a note whose source is gone from HQ. It stays on disk.
	MirrorStale = "stale"
	// MirrorUserOwned is a note the user took over, by editing, moving or
	// deleting it, or by already having one at that name. Ori never retries it.
	MirrorUserOwned = "user_owned"
)

var mirrorHashPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// MirrorRef names what a note mirrors in HQ. ID is MirrorID of the source's id,
// so a ledger entry costs the same whatever the source's id looks like.
type MirrorRef struct {
	Kind string
	ID   string
}

// MirrorID shortens a source's id to its ledger form.
func MirrorID(id string) string { return shortHash(id) }

func shortHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:mirrorHashLength]
}

// mirrorHash is the ledger's form of a note's body hash. It only ever compares
// one note's body with its own last version.
func mirrorHash(body []byte) string { return bodyHash(body)[:mirrorHashLength] }

// Mirror is one ledger entry: a note Ori wrote and what it mirrors (FR7). It
// encodes as a six-element array rather than an object, since the field names
// alone, repeated 400 times, would not fit beside a full look in the record.
type Mirror struct {
	Ref       MirrorRef
	Rel       string
	Hash      string
	WrittenAt time.Time
	State     string
}

// MarshalJSON writes [kind, id, rel, hash, written_at unix seconds, state].
func (m Mirror) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{m.Ref.Kind, m.Ref.ID, m.Rel, m.Hash, m.WrittenAt.Unix(), m.State})
}

// UnmarshalJSON reads exactly the six elements MarshalJSON writes; anything else
// is refused, as an unknown field is everywhere else in the record.
func (m *Mirror) UnmarshalJSON(data []byte) error {
	var fields []json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != 6 {
		return ErrInvalidRecord
	}
	var written int64
	for index, target := range []any{&m.Ref.Kind, &m.Ref.ID, &m.Rel, &m.Hash, &written, &m.State} {
		if err := json.Unmarshal(fields[index], target); err != nil {
			return err
		}
	}
	m.WrittenAt = time.Unix(written, 0).UTC()
	return nil
}

// FillIn is what a source remembers about keeping the notes filled in: the
// ledger and where the last sync stopped. A record from before it has none,
// which decodes to an empty ledger.
type FillIn struct {
	Mirrors []Mirror `json:"mirrors,omitempty"`
	// MorePending says the last sync stopped at MaxSyncWrites; the next one
	// continues (FR6).
	MorePending bool `json:"more_pending,omitempty"`
	// LastAt is when a sync last wrote a note.
	LastAt *time.Time `json:"last_at,omitempty"`
}

func validateFillIn(fillIn FillIn) error {
	if len(fillIn.Mirrors) > MaxMirrors {
		return ErrInvalidRecord
	}
	seen := make(map[MirrorRef]bool, len(fillIn.Mirrors))
	for _, mirror := range fillIn.Mirrors {
		switch {
		case !validMirrorKind(mirror.Ref.Kind),
			!mirrorHashPattern.MatchString(mirror.Ref.ID),
			!mirrorHashPattern.MatchString(mirror.Hash),
			!mirrorRelFits(mirror.Rel),
			mirror.WrittenAt.IsZero(),
			mirror.State != MirrorCurrent && mirror.State != MirrorStale && mirror.State != MirrorUserOwned,
			seen[mirror.Ref]:
			return ErrInvalidRecord
		}
		if _, _, err := splitOwnedPath(mirror.Rel); err != nil {
			return ErrInvalidRecord
		}
		seen[mirror.Ref] = true
	}
	return nil
}

func validMirrorKind(kind string) bool {
	return kind == NoteKindFacts || kind == NoteKindPerson || kind == NoteKindProject || kind == NoteKindFollowUp
}

// mirrorRelFits measures rel as JSON writes it, since & and < take six bytes.
func mirrorRelFits(rel string) bool {
	encoded, err := json.Marshal(rel)
	return err == nil && len(encoded)-2 <= maxMirrorRelBytes
}

// PlannedNote is one note that should exist, rendered from what HQ holds.
type PlannedNote struct {
	Ref  MirrorRef
	Rel  string
	Kind string
	Body []byte
	// OnlyIfTracked plans a refresh and never a first write: the facts note is
	// emptied when the last fact is forgotten, and is not created empty.
	OnlyIfTracked bool
}

// SyncOutcome is what one reconcile did.
type SyncOutcome struct {
	Mirrors []Mirror
	// Written counts the notes written, by kind.
	Written map[string]int
	// MorePending says the write bound stopped the run.
	MorePending bool
	// Err is the first failure; the rest of the run still went ahead.
	Err error
}

// WrittenTotal is how many notes the run wrote.
func (o SyncOutcome) WrittenTotal() int {
	total := 0
	for _, count := range o.Written {
		total += count
	}
	return total
}

// Reconcile brings the notes in line with planned, given what the ledger
// remembers, and returns the new ledger. It writes only what is new or changed.
// A ledger entry is checked against the disk first, so an edit, a move or a
// deletion makes the note the user's and Ori never writes it again; nothing is
// ever deleted or renamed. Run it twice in a row and the second run writes
// nothing (FR6, FR8).
func Reconcile(writer *Writer, ledger []Mirror, planned []PlannedNote, now time.Time) SyncOutcome {
	run := reconcileRun{
		writer: writer, now: now.UTC(),
		out:     SyncOutcome{Mirrors: append([]Mirror(nil), ledger...), Written: map[string]int{}},
		tracked: make(map[MirrorRef]int, len(ledger)),
		rels:    make(map[string]bool, len(ledger)),
		counts:  map[string]int{},
		unknown: map[MirrorRef]bool{},
	}
	for index := range run.out.Mirrors {
		mirror := &run.out.Mirrors[index]
		run.tracked[mirror.Ref] = index
		run.rels[strings.ToLower(mirror.Rel)] = true
		run.counts[mirror.Ref.Kind]++
		run.checkDisk(mirror)
	}
	wanted := make(map[MirrorRef]bool, len(planned))
	for _, note := range planned {
		wanted[note.Ref] = true
		run.apply(note)
	}
	// A source gone from HQ leaves its note where it is (FR5).
	for index := range run.out.Mirrors {
		mirror := &run.out.Mirrors[index]
		if !wanted[mirror.Ref] && mirror.State == MirrorCurrent {
			mirror.State = MirrorStale
		}
	}
	return run.out
}

type reconcileRun struct {
	writer  *Writer
	now     time.Time
	out     SyncOutcome
	writes  int
	tracked map[MirrorRef]int
	rels    map[string]bool
	counts  map[string]int
	// unknown holds entries the disk check could not read this run. A transient
	// failure says nothing about who owns the note, so they are left alone.
	unknown map[MirrorRef]bool
}

func (r *reconcileRun) fail(err error) {
	if r.out.Err == nil {
		r.out.Err = err
	}
}

func (r *reconcileRun) checkDisk(mirror *Mirror) {
	if mirror.State == MirrorUserOwned {
		return
	}
	status, err := r.writer.Inspect(mirror.Rel)
	if err != nil {
		r.unknown[mirror.Ref] = true
		r.fail(err)
		return
	}
	if !status.Owned {
		mirror.State = MirrorUserOwned
	}
}

func (r *reconcileRun) apply(note PlannedNote) {
	if index, ok := r.tracked[note.Ref]; ok {
		r.refresh(&r.out.Mirrors[index], note)
		return
	}
	if note.OnlyIfTracked || !r.hasRoom(note.Ref.Kind) {
		return
	}
	r.first(note)
}

// refresh updates a note Ori already keeps, at the path it first wrote.
func (r *reconcileRun) refresh(mirror *Mirror, note PlannedNote) {
	if mirror.State == MirrorUserOwned || r.unknown[mirror.Ref] {
		return
	}
	hash := mirrorHash(note.Body)
	if mirror.Hash == hash {
		mirror.State = MirrorCurrent
		return
	}
	if r.writes == MaxSyncWrites {
		r.out.MorePending = true
		return
	}
	_, err := r.writer.Write(mirror.Rel, note.Kind, note.Body)
	switch {
	case err == nil:
		mirror.Hash, mirror.WrittenAt, mirror.State = hash, r.now, MirrorCurrent
		r.wrote(note.Kind)
	case errors.Is(err, ErrUserModified), errors.Is(err, ErrOutsideOwnedFolders):
		// The user saved the note since the disk check.
		mirror.State = MirrorUserOwned
	default:
		r.fail(fmt.Errorf("writing a note: %w", err))
	}
}

// first handles a source the ledger has not seen. A seed the setup wrote at the
// name is adopted as it stands: it already says what the note would, and copying
// it again would announce notes the user has had all along. A note left by a
// run whose ledger write failed is recognised by its hash, so that crash costs
// nothing either. Anything else at the name is the user's.
func (r *reconcileRun) first(note PlannedNote) {
	if !mirrorRelFits(note.Rel) || r.rels[strings.ToLower(note.Rel)] {
		return
	}
	status, err := r.writer.Inspect(note.Rel)
	if err != nil {
		r.fail(fmt.Errorf("reading a note: %w", err))
		return
	}
	hash := mirrorHash(note.Body)
	switch {
	case status.Exists && !status.Owned:
		r.track(note, hash, MirrorUserOwned)
		return
	case status.Owned && (status.Kind == NoteKindSeed || status.Hash[:mirrorHashLength] == hash):
		r.track(note, hash, MirrorCurrent)
		return
	}
	if r.writes == MaxSyncWrites {
		r.out.MorePending = true
		return
	}
	_, err = r.writer.Write(note.Rel, note.Kind, note.Body)
	switch {
	case err == nil:
		r.track(note, hash, MirrorCurrent)
		r.wrote(note.Kind)
	case errors.Is(err, ErrUserModified), errors.Is(err, ErrOutsideOwnedFolders):
		r.track(note, hash, MirrorUserOwned)
	default:
		r.fail(fmt.Errorf("writing a note: %w", err))
	}
}

func (r *reconcileRun) wrote(kind string) {
	r.writes++
	r.out.Written[kind]++
}

func (r *reconcileRun) track(note PlannedNote, hash, state string) {
	r.out.Mirrors = append(r.out.Mirrors, Mirror{Ref: note.Ref, Rel: note.Rel, Hash: hash, WrittenAt: r.now, State: state})
	r.tracked[note.Ref] = len(r.out.Mirrors) - 1
	r.rels[strings.ToLower(note.Rel)] = true
	r.counts[note.Ref.Kind]++
}

func (r *reconcileRun) hasRoom(kind string) bool {
	if len(r.out.Mirrors) >= MaxMirrors {
		return false
	}
	switch kind {
	case NoteKindPerson:
		return r.counts[kind] < MaxMirrorPeople
	case NoteKindFollowUp:
		return r.counts[kind] < MaxMirrorFollowUps
	case NoteKindProject:
		return r.counts[kind] < MaxMirrorProjects
	default:
		return true
	}
}
