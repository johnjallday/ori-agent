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

// The consent answers a ledger holds, and the decisions that make them (FR10).
const (
	ConsentAllowed = "allowed"
	ConsentLater   = "later"

	FillInAllow = "allow"
	FillInLater = "later"

	// How the drawer's project question ended.
	ProjectAnswered = "answered"
	ProjectSkipped  = "skipped"

	// FillInAskAgainAfter is how long Not now waits before the card comes back,
	// as the ask's does.
	FillInAskAgainAfter = AskAgainAfter
)

// PlainFolder says rel is directly in Inbox or Daily: the two places Ori wrote
// before the fill-in, which need no further answer.
func PlainFolder(rel string) bool {
	return strings.HasPrefix(rel, FolderInbox+"/") || strings.HasPrefix(rel, FolderDaily+"/")
}

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
	// Consent is the user's answer to "keep People, a note per project and what
	// I know about you up to date": ConsentAllowed or ConsentLater. A setup made
	// after the fill-in shipped is allowed from the start. One from before has no
	// answer, which reads as pending, and until it is allowed the sync writes
	// only Inbox and Daily (FR10).
	Consent          string     `json:"consent,omitempty"`
	ConsentAt        *time.Time `json:"consent_at,omitempty"`
	ConsentRequestID string     `json:"consent_request_id,omitempty"`
	// Withheld says the last sync had notes outside Inbox and Daily to write and
	// did not, for want of the answer. It is what puts the card under Needs you:
	// a setup with nothing to write is never asked.
	Withheld bool `json:"withheld,omitempty"`
	// ProjectAsk is how the drawer's one project question ended: ProjectAnswered
	// or ProjectSkipped. Either way it is never asked again in the drawer; the
	// Profile page's interview is separate (first-week FR23).
	ProjectAsk   string     `json:"project_ask,omitempty"`
	ProjectAskAt *time.Time `json:"project_ask_at,omitempty"`
	// WeekShownAt is when Today first showed the first-week recap row, and
	// WeekDismissedAt when the user dismissed it. The row is shown once: neither
	// a later look nor a later sync brings it back (first-week FR25).
	WeekShownAt     *time.Time `json:"week_shown_at,omitempty"`
	WeekDismissedAt *time.Time `json:"week_dismissed_at,omitempty"`
	// Report collects what the Today row says, until the row has been seen (FR9).
	Report *FillInReport `json:"report,omitempty"`
}

// FillInReport is what was added to the notes since the Today row was last seen.
type FillInReport struct {
	// Counts holds the notes added, by kind; for the facts note, its updates.
	Counts map[string]int `json:"counts"`
	// At is the newest addition.
	At time.Time `json:"at"`
	// ShownAt is when the row was first shown. The next addition starts over.
	ShownAt *time.Time `json:"shown_at,omitempty"`
}

// Allowed says the user, or the setup, let Ori keep every kind of note.
func (f *FillIn) Allowed() bool { return f != nil && f.Consent == ConsentAllowed }

// Decide records the user's answer. Answering twice with the same decision is
// the same as once, whatever the request id.
func (f *FillIn) Decide(decision, requestID string, now time.Time) error {
	consent := ""
	switch decision {
	case FillInAllow:
		consent = ConsentAllowed
	case FillInLater:
		consent = ConsentLater
	default:
		return ErrInvalidRecord
	}
	if f.Consent == ConsentAllowed {
		return nil
	}
	at := now.UTC()
	f.Consent, f.ConsentAt, f.ConsentRequestID = consent, &at, requestID
	return nil
}

// State is the consent as the API reports it.
func (f *FillIn) State() string {
	switch {
	case f.Allowed():
		return ConsentAllowed
	case f != nil && f.Consent == ConsentLater:
		return ConsentLater
	default:
		return "pending"
	}
}

// Offer says the card should be under Needs you: there is something to write
// that the answer would let through, and the user has not said yes. Not now
// holds for a week (FR10).
func (f *FillIn) Offer(now time.Time) bool {
	switch {
	case f.Allowed():
		return false
	case f == nil || !f.Withheld:
		return false
	case f.Consent == ConsentLater:
		return f.ConsentAt != nil && now.Sub(*f.ConsentAt) >= FillInAskAgainAfter
	default:
		return true
	}
}

// AddToReport folds a sync's additions into the report. A report already shown is
// finished with, so what comes next is new.
func (f *FillIn) AddToReport(added map[string]int, now time.Time) {
	total := 0
	for _, count := range added {
		total += count
	}
	if total == 0 {
		return
	}
	if f.Report == nil || f.Report.ShownAt != nil {
		f.Report = &FillInReport{Counts: map[string]int{}}
	}
	for kind, count := range added {
		f.Report.Counts[kind] += count
	}
	f.Report.At = now.UTC()
}

// ReportSentence is the Today row: "Ori added 2 people and a project to your
// notes". The facts note has one copy that changes, so it says updated.
func ReportSentence(counts map[string]int) string {
	var parts []string
	for _, kind := range []struct{ key, one, many string }{
		{NoteKindPerson, "a person", "people"},
		{NoteKindProject, "a project", "projects"},
		{NoteKindFollowUp, "a follow-up", "follow-ups"},
	} {
		switch count := counts[kind.key]; {
		case count == 1:
			parts = append(parts, kind.one)
		case count > 1:
			parts = append(parts, fmt.Sprintf("%d %s", count, kind.many))
		}
	}
	added := ""
	switch len(parts) {
	case 0:
	case 1:
		added = parts[0]
	default:
		added = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	switch {
	case added != "" && counts[NoteKindFacts] > 0:
		return "Ori added " + added + " to your notes and updated what it knows about you"
	case added != "":
		return "Ori added " + added + " to your notes"
	case counts[NoteKindFacts] > 0:
		return "Ori updated what it knows about you in your notes"
	default:
		return ""
	}
}

func validateFillIn(fillIn FillIn) error {
	if len(fillIn.Mirrors) > MaxMirrors {
		return ErrInvalidRecord
	}
	if fillIn.Consent != "" && fillIn.Consent != ConsentAllowed && fillIn.Consent != ConsentLater ||
		(fillIn.Consent == "") != (fillIn.ConsentAt == nil) ||
		fillIn.ConsentRequestID != "" && !ValidRequestID(fillIn.ConsentRequestID) {
		return ErrInvalidRecord
	}
	if fillIn.ProjectAsk != "" && fillIn.ProjectAsk != ProjectAnswered && fillIn.ProjectAsk != ProjectSkipped ||
		(fillIn.ProjectAsk == "") != (fillIn.ProjectAskAt == nil) {
		return ErrInvalidRecord
	}
	if fillIn.WeekDismissedAt != nil && fillIn.WeekShownAt == nil {
		return ErrInvalidRecord
	}
	if report := fillIn.Report; report != nil {
		if len(report.Counts) > len(noteKinds) || report.At.IsZero() {
			return ErrInvalidRecord
		}
		for kind, count := range report.Counts {
			if !validMirrorKind(kind) || count < 0 {
				return ErrInvalidRecord
			}
		}
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
	// Numbered takes the next free name when Rel is taken, rather than treating
	// what is there as this note. Follow-ups use it: two can share a title, and a
	// note of the same name in Inbox is not about the same thing.
	Numbered bool
	// Tick says the source is done: tick the note's open line if Ori still keeps
	// it, and never write it for the first time.
	Tick bool
}

// maxNumberedAttempts bounds the numbering of a note whose name is taken.
const maxNumberedAttempts = 50

// SyncOutcome is what one reconcile did.
type SyncOutcome struct {
	Mirrors []Mirror
	// Written counts the notes written, by kind. A tick is one of them.
	Written map[string]int
	// Added is what the user is told about: new notes by kind, and each update
	// of the facts note. A tick or a refreshed person is not news.
	Added map[string]int
	// MorePending says the write bound stopped the run.
	MorePending bool
	// Withheld is set by the caller: notes were left out of the plan for want of
	// consent.
	Withheld bool
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
		out:     SyncOutcome{Mirrors: append([]Mirror(nil), ledger...), Written: map[string]int{}, Added: map[string]int{}},
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
	index, ok := r.tracked[note.Ref]
	switch {
	case ok && note.Tick:
		r.tick(&r.out.Mirrors[index], note)
	case ok:
		r.refresh(&r.out.Mirrors[index], note)
	case note.Tick || note.OnlyIfTracked:
	case r.hasRoom(note.Ref.Kind) || r.evictStale(note.Ref.Kind):
		r.first(note)
	}
}

// tick closes a note whose source is done. An entry already stale was ticked
// before, or closed some other way, and stays as it is.
func (r *reconcileRun) tick(mirror *Mirror, note PlannedNote) {
	if mirror.State != MirrorCurrent || r.unknown[mirror.Ref] {
		return
	}
	if r.writes == MaxSyncWrites {
		r.out.MorePending = true
		return
	}
	hash, ticked, err := r.writer.Tick(mirror.Rel)
	switch {
	case err == nil:
		mirror.Hash, mirror.State = hash, MirrorStale
		if ticked {
			mirror.WrittenAt = r.now
			r.writes++
			r.out.Written[note.Kind]++
		}
	case errors.Is(err, ErrUserModified), errors.Is(err, ErrOutsideOwnedFolders):
		mirror.State = MirrorUserOwned
	default:
		r.fail(fmt.Errorf("ticking a note: %w", err))
	}
}

// evictStale frees a follow-up's ledger entry for a new one when the cap is
// full. A closed follow-up's note stays on disk and is simply no longer tracked,
// so years of follow-ups never stop new ones reaching the notes. Other kinds
// keep their entries: their notes can come back.
func (r *reconcileRun) evictStale(kind string) bool {
	if kind != NoteKindFollowUp {
		return false
	}
	for index, mirror := range r.out.Mirrors {
		if mirror.Ref.Kind != kind || mirror.State != MirrorStale {
			continue
		}
		r.out.Mirrors = append(r.out.Mirrors[:index], r.out.Mirrors[index+1:]...)
		delete(r.rels, strings.ToLower(mirror.Rel))
		delete(r.tracked, mirror.Ref)
		r.counts[kind]--
		for ref, at := range r.tracked {
			if at > index {
				r.tracked[ref] = at - 1
			}
		}
		return true
	}
	return false
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
		r.wrote(note.Kind, note.Kind == NoteKindFacts)
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
// nothing either. Anything else at the name is the user's, unless the note is
// Numbered and can go next to it.
func (r *reconcileRun) first(note PlannedNote) {
	for attempt := 1; attempt <= maxNumberedAttempts; attempt++ {
		rel := note.Rel
		if attempt > 1 {
			if !note.Numbered {
				return
			}
			rel = numberedRel(note.Rel, attempt)
		}
		if r.place(note, rel) {
			return
		}
	}
}

// numberedRel is Inbox/<stem>-<n>.md, the setup's own numbering.
func numberedRel(rel string, attempt int) string {
	return fmt.Sprintf("%s-%d.md", strings.TrimSuffix(rel, ".md"), attempt)
}

// place tries one name. false says the name is taken and the note may try another.
func (r *reconcileRun) place(note PlannedNote, rel string) bool {
	if !mirrorRelFits(rel) || r.rels[strings.ToLower(rel)] {
		return false
	}
	status, err := r.writer.Inspect(rel)
	if err != nil {
		r.fail(fmt.Errorf("reading a note: %w", err))
		return true
	}
	hash := mirrorHash(note.Body)
	switch {
	case status.Owned && (status.Kind == NoteKindSeed || status.Hash[:mirrorHashLength] == hash):
		r.track(note, rel, hash, MirrorCurrent)
		return true
	case status.Exists && note.Numbered:
		return false
	case status.Exists && !status.Owned:
		r.track(note, rel, hash, MirrorUserOwned)
		return true
	}
	if r.writes == MaxSyncWrites {
		r.out.MorePending = true
		return true
	}
	_, err = r.writer.Write(rel, note.Kind, note.Body)
	switch {
	case err == nil:
		r.track(note, rel, hash, MirrorCurrent)
		r.wrote(note.Kind, true)
	case errors.Is(err, ErrUserModified), errors.Is(err, ErrOutsideOwnedFolders):
		if note.Numbered {
			return false
		}
		r.track(note, rel, hash, MirrorUserOwned)
	default:
		r.fail(fmt.Errorf("writing a note: %w", err))
	}
	return true
}

func (r *reconcileRun) wrote(kind string, news bool) {
	r.writes++
	r.out.Written[kind]++
	if news {
		r.out.Added[kind]++
	}
}

func (r *reconcileRun) track(note PlannedNote, rel, hash, state string) {
	r.out.Mirrors = append(r.out.Mirrors, Mirror{Ref: note.Ref, Rel: rel, Hash: hash, WrittenAt: r.now, State: state})
	r.tracked[note.Ref] = len(r.out.Mirrors) - 1
	r.rels[strings.ToLower(rel)] = true
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
