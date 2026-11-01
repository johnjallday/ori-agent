package secondbrain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// The record lives at <HQ>/.ori/second-brain-v1.json (FR15). This file defines
// what it holds and the rules for changing it; the personal-assistant store
// reads and writes it under the knowledge-store discipline (no-follow open,
// lock, temp+fsync+rename, 0600, size cap, owner check).
const (
	RecordSchemaVersion = 1
	RecordFileName      = "second-brain-v1.json"
	RecordLockName      = "second-brain-v1.lock"
	RecordMaxBytes      = 512 * 1024

	// MaxSources is one in v1. The record keeps a list so a second source
	// needs no schema change.
	MaxSources = 1
	// MaxLookItems caps the open items a look keeps.
	MaxLookItems = 500
	// MaxReceipts caps the remembered request ids; the oldest is dropped.
	MaxReceipts = 32
	// MaxHeadlineRunes caps a look's headline (FR23) and its Also noticed line.
	MaxHeadlineRunes = 160
	// MaxLookSignals caps each list of signals a look keeps beside its items:
	// people, stale notes and decisions.
	MaxLookSignals = 20
	// MaxSignalTitleRunes caps the note title a kept signal carries.
	MaxSignalTitleRunes = 160
	// maxSignalNoteBytes caps a kept signal's note path. A longer one is left
	// out rather than kept, so the signals can never crowd the record.
	maxSignalNoteBytes = 300
	// AskAgainAfter is how long "Not now" waits before Needs you offers the
	// ask again (FR8).
	AskAgainAfter = 7 * 24 * time.Hour

	maxRequestIDLength = 128
	maxNameLength      = 255
	maxFailureRunes    = 240
)

// Source kinds.
const (
	KindObsidian = "obsidian"
	KindMarkdown = "markdown"
)

// Source origins. A connected folder is the user's own and is never written
// to. A folder Ori created is the user's too, but Ori may keep writing to the
// subfolders it announced (see Writer). A record from before the field means
// connected, so missing can never grant a write.
const (
	OriginConnected    = "connected"
	OriginCreatedByOri = "created_by_ori"
)

// Ask states, as the API reports them.
const (
	AskPending  = "pending"
	AskShown    = "shown"
	AskDeferred = "deferred"
	AskDeclined = "declined"
)

// Ask decisions.
const (
	DecisionLater = "later"
	DecisionNone  = "none"
)

// Look states.
const (
	LookRunning  = "running"
	LookFinished = "finished"
	LookFailed   = "failed"
)

// Who wrote a look's headline.
const (
	HeadlineByRules = "rules"
	HeadlineByModel = "model"
)

var (
	ErrInvalidRecord       = errors.New("secondbrain: invalid record")
	ErrReplaceNeedsConfirm = errors.New("secondbrain: another notes folder is connected")
	ErrNotConnected        = errors.New("secondbrain: no notes folder is connected")
	ErrRequestReused       = errors.New("secondbrain: request id was already used for another action")
)

// Record is everything Ori keeps about the user's notes: the connected
// folder and its last look, the ask's state, and the request receipts that
// make every write idempotent. Note contents are never part of it (FR20).
type Record struct {
	Sources  []Source  `json:"sources,omitempty"`
	Ask      Ask       `json:"ask"`
	Receipts []Receipt `json:"receipts,omitempty"`
}

// Source is the connected notes folder (FR15).
type Source struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	ConnectedAt time.Time `json:"connected_at"`
	RequestID   string    `json:"request_id"`
	// Origin says how the folder came to be here: OriginConnected or
	// OriginCreatedByOri. Only the second lets Ori write (FR12).
	Origin string `json:"origin,omitempty"`
	// LastLook is the latest finished look. Current is a look that is running,
	// or one that failed after LastLook; a failure never erases LastLook.
	LastLook *Look `json:"last_look,omitempty"`
	Current  *Look `json:"current,omitempty"`
}

// Ask is the state of the assistant's second-brain ask (FR5, FR8, FR37).
type Ask struct {
	// PromptedAt is the receipt that the ask was shown as a message, once per
	// relationship. Reset Getting Started clears it.
	PromptedAt      *time.Time `json:"prompted_at,omitempty"`
	PromptRequestID string     `json:"prompt_request_id,omitempty"`
	// Decision is "", DecisionLater or DecisionNone.
	Decision          string     `json:"decision,omitempty"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
	DecisionRequestID string     `json:"decision_request_id,omitempty"`
	// TombstonedAt is set by Disconnect, so the ask is not repeated until the
	// user asks again from the Notes slot or resets Getting Started.
	TombstonedAt *time.Time `json:"tombstoned_at,omitempty"`
}

// Look is one reading of the connected folder and what it found (FR23).
type Look struct {
	ID          string     `json:"id"`
	State       string     `json:"state"`
	RequestID   string     `json:"request_id,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Headline    string     `json:"headline,omitempty"`
	HeadlineBy  string     `json:"headline_by,omitempty"`
	AlsoNoticed string     `json:"also_noticed,omitempty"`
	Counts      LookCounts `json:"counts"`
	Items       []LookItem `json:"items,omitempty"`
	// People, Stale and Decisions are bounded signals for the Notes panel: names,
	// paths, counts and dates, never a note's words.
	People    []LookPerson    `json:"people,omitempty"`
	Stale     []LookStaleNote `json:"stale,omitempty"`
	Decisions []LookDecision  `json:"decisions,omitempty"`
	// NewSince and DoneSince compare this look with the one before (FR36).
	NewSince  int `json:"new_since,omitempty"`
	DoneSince int `json:"done_since,omitempty"`
	// Failure is the one sentence a failed look shows (FR27).
	Failure string `json:"failure,omitempty"`
	// ActedAt is when the user added items or dismissed the result. Until
	// then a finished result waits under Needs you (FR31, FR32).
	ActedAt *time.Time `json:"acted_at,omitempty"`
}

// LookCounts are the numbers a look found.
type LookCounts struct {
	Notes         int  `json:"notes"`
	OpenItems     int  `json:"open_items"`
	NotesWithOpen int  `json:"notes_with_open"`
	Dated         int  `json:"dated"`
	Done          int  `json:"done"`
	Partial       bool `json:"partial,omitempty"`
}

// LookItem is one open item a look kept: the line, where it is, its date, and
// its content hash (FR20, FR28). Nothing else from the note is stored.
type LookItem struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Note      string `json:"note"`
	Line      int    `json:"line"`
	Due       string `json:"due,omitempty"`
	Suggested bool   `json:"suggested,omitempty"`
	// InDecision says the item's note marks an open decision, so it becomes
	// a needs-decision follow-up.
	InDecision bool `json:"in_decision,omitempty"`
	// Seeded says the item sits in a note Ori seeded from a follow-up the user
	// already has. It is never suggested and never added again (FR10).
	Seeded bool `json:"seeded,omitempty"`
	// Link opens the note in Obsidian (FR26); "" for a plain Markdown folder.
	// It is built from the vault's name and the note's path, never read.
	Link string `json:"link,omitempty"`
}

// LookPerson is a person note and how many other notes mention it.
type LookPerson struct {
	Name     string `json:"name"`
	Note     string `json:"note"`
	Mentions int    `json:"mentions"`
	Link     string `json:"link,omitempty"`
}

// LookStaleNote is a note with open items that has sat untouched for StaleAfter.
type LookStaleNote struct {
	Note     string    `json:"note"`
	Title    string    `json:"title"`
	Open     int       `json:"open"`
	Modified time.Time `json:"modified"`
	Link     string    `json:"link,omitempty"`
}

// LookDecision is a note that marks an open decision. The decision's own line
// is not kept.
type LookDecision struct {
	Note  string `json:"note"`
	Title string `json:"title"`
	Open  int    `json:"open"`
	Link  string `json:"link,omitempty"`
}

// Receipt remembers a write by its request_id, so a replay returns what the
// first call did instead of acting twice.
type Receipt struct {
	RequestID string    `json:"request_id"`
	Action    string    `json:"action"`
	At        time.Time `json:"at"`
	LookID    string    `json:"look_id,omitempty"`
	Created   int       `json:"created,omitempty"`
	Skipped   int       `json:"skipped,omitempty"`
	// Note is the Inbox path a capture wrote, so a replay links the same note.
	Note string `json:"note,omitempty"`
}

// Connected returns the connected source, or nil.
func (r *Record) Connected() *Source {
	if r == nil || len(r.Sources) == 0 {
		return nil
	}
	return &r.Sources[0]
}

// origin is the source's origin with a missing one read as connected.
func (s Source) origin() string {
	if s.Origin == "" {
		return OriginConnected
	}
	return s.Origin
}

// CreatedByOri reports whether Ori created this folder, which is the only
// thing that lets it write there.
func (s Source) CreatedByOri() bool { return s.Origin == OriginCreatedByOri }

// DefaultOrigins fills in the origin a record from before the field left out,
// for the codec to call as it reads the record.
func (r *Record) DefaultOrigins() {
	for index := range r.Sources {
		r.Sources[index].Origin = r.Sources[index].origin()
	}
}

// Connect makes source the connected folder (FR15). Connecting the folder
// that is already connected changes nothing. Another folder replaces the
// connected one only when replace says the user confirmed it.
func (r *Record) Connect(source Source, replace bool) (*Source, error) {
	if err := validateSource(source); err != nil {
		return nil, err
	}
	source.Origin = source.origin()
	if current := r.Connected(); current != nil {
		if current.ID == source.ID {
			return current, nil
		}
		if !replace {
			return nil, ErrReplaceNeedsConfirm
		}
	}
	r.Sources = []Source{source}
	return &r.Sources[0], nil
}

// Disconnect removes the connected folder and its looks, and tombstones the
// ask so it is not repeated (FR37). Follow-ups already created are the
// user's and are not touched here.
func (r *Record) Disconnect(now time.Time) error {
	if r.Connected() == nil {
		return ErrNotConnected
	}
	r.Sources = nil
	at := now.UTC()
	r.Ask.TombstonedAt = &at
	return nil
}

// MarkPrompted records that the ask was shown, once. It reports whether this
// call recorded it.
func (r *Record) MarkPrompted(requestID string, now time.Time) bool {
	if r.Ask.PromptedAt != nil {
		return false
	}
	at := now.UTC()
	r.Ask.PromptedAt = &at
	r.Ask.PromptRequestID = requestID
	return true
}

// Decide records "Not now" (DecisionLater) or "I don't keep notes"
// (DecisionNone) (FR8). Answering also counts as the ask having been shown.
func (r *Record) Decide(decision, requestID string, now time.Time) error {
	if decision != DecisionLater && decision != DecisionNone {
		return ErrInvalidRecord
	}
	r.MarkPrompted(requestID, now)
	at := now.UTC()
	r.Ask.Decision = decision
	r.Ask.DecidedAt = &at
	r.Ask.DecisionRequestID = requestID
	return nil
}

// AskAgain lifts a decline or a tombstone, for the Notes slot's Ask again.
// The prompt receipt stays, so no second unprompted message follows.
func (r *Record) AskAgain() {
	r.Ask.Decision, r.Ask.DecidedAt, r.Ask.DecisionRequestID = "", nil, ""
	r.Ask.TombstonedAt = nil
}

// ResetAsk forgets the ask entirely, for Reset Getting Started (FR39). The
// connection is kept.
func (r *Record) ResetAsk() { r.Ask = Ask{} }

// State is the ask's state for the API.
func (a Ask) State() string {
	switch {
	case a.Decision == DecisionNone || a.TombstonedAt != nil:
		return AskDeclined
	case a.Decision == DecisionLater:
		return AskDeferred
	case a.PromptedAt != nil:
		return AskShown
	default:
		return AskPending
	}
}

// OfferAgain reports whether a deferred ask may be offered again under Needs
// you (FR8).
func (a Ask) OfferAgain(now time.Time) bool {
	return a.State() == AskDeferred && a.DecidedAt != nil && now.Sub(*a.DecidedAt) >= AskAgainAfter
}

// Receipt returns the remembered write for requestID.
func (r *Record) Receipt(requestID string) (Receipt, bool) {
	for _, receipt := range r.Receipts {
		if receipt.RequestID == requestID {
			return receipt, true
		}
	}
	return Receipt{}, false
}

// Replay returns the remembered write for requestID when it was the same
// action. A request id reused for a different action is ErrRequestReused.
func (r *Record) Replay(requestID, action string) (Receipt, bool, error) {
	receipt, ok := r.Receipt(requestID)
	if !ok {
		return Receipt{}, false, nil
	}
	if receipt.Action != action {
		return Receipt{}, false, ErrRequestReused
	}
	return receipt, true, nil
}

// Remember records a write, dropping the oldest receipt past MaxReceipts.
func (r *Record) Remember(receipt Receipt) {
	receipt.At = receipt.At.UTC()
	r.Receipts = append(r.Receipts, receipt)
	if extra := len(r.Receipts) - MaxReceipts; extra > 0 {
		r.Receipts = append([]Receipt(nil), r.Receipts[extra:]...)
	}
}

// ItemID is an open item's content hash (FR28): sha256 of the source id, the
// note's relative path and the item's normalised text. The same line in the
// same note keeps its id across looks; editing its words makes a new item.
func ItemID(sourceID, rel, text string) string {
	sum := sha256.Sum256([]byte(sourceID + "\x00" + rel + "\x00" + NormaliseItemText(text)))
	return hex.EncodeToString(sum[:])
}

// NormaliseItemText folds case and white space, so re-indenting a line or
// changing its capitals does not make it a new item.
func NormaliseItemText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

var (
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	itemIDPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	isoDayPattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ValidRequestID reports whether a request_id has the shape every write takes.
func ValidRequestID(id string) bool { return requestIDPattern.MatchString(id) }

// Validate checks every bound and shape the record must hold.
func (r Record) Validate() error {
	if len(r.Sources) > MaxSources || len(r.Receipts) > MaxReceipts {
		return ErrInvalidRecord
	}
	for _, source := range r.Sources {
		if err := validateSource(source); err != nil {
			return err
		}
		for _, look := range []*Look{source.LastLook, source.Current} {
			if look != nil {
				if err := validateLook(*look); err != nil {
					return err
				}
			}
		}
		if source.LastLook != nil && source.LastLook.State != LookFinished {
			return ErrInvalidRecord
		}
		if source.Current != nil && source.Current.State == LookFinished {
			return ErrInvalidRecord
		}
	}
	if err := validateAsk(r.Ask); err != nil {
		return err
	}
	for _, receipt := range r.Receipts {
		if !ValidRequestID(receipt.RequestID) || receipt.Action == "" || len(receipt.Action) > 32 || receipt.Created < 0 || receipt.Skipped < 0 ||
			(receipt.Note != "" && (len(receipt.Note) > 300 || !strings.HasPrefix(receipt.Note, FolderInbox+"/") || strings.Contains(receipt.Note, ".."))) {
			return ErrInvalidRecord
		}
	}
	return nil
}

func validateSource(source Source) error {
	switch {
	case source.Kind != KindObsidian && source.Kind != KindMarkdown,
		source.Name == "" || len(source.Name) > maxNameLength || !utf8.ValidString(source.Name),
		!filepath.IsAbs(source.Path) || filepath.Clean(source.Path) != source.Path || strings.ContainsRune(source.Path, 0),
		source.ID != SourceID(source.Path),
		source.ConnectedAt.IsZero(),
		!ValidRequestID(source.RequestID),
		source.origin() != OriginConnected && source.origin() != OriginCreatedByOri:
		return ErrInvalidRecord
	}
	return nil
}

func validateAsk(ask Ask) error {
	switch {
	case ask.Decision != "" && ask.Decision != DecisionLater && ask.Decision != DecisionNone,
		(ask.Decision == "") != (ask.DecidedAt == nil),
		ask.PromptRequestID != "" && !ValidRequestID(ask.PromptRequestID),
		ask.DecisionRequestID != "" && !ValidRequestID(ask.DecisionRequestID):
		return ErrInvalidRecord
	}
	return nil
}

func validateLook(look Look) error {
	switch {
	case look.ID == "" || len(look.ID) > 64,
		look.State != LookRunning && look.State != LookFinished && look.State != LookFailed,
		look.RequestID != "" && !ValidRequestID(look.RequestID),
		look.StartedAt.IsZero(),
		(look.State == LookRunning) != (look.FinishedAt == nil),
		look.HeadlineBy != "" && look.HeadlineBy != HeadlineByRules && look.HeadlineBy != HeadlineByModel,
		utf8.RuneCountInString(look.Headline) > MaxHeadlineRunes,
		utf8.RuneCountInString(look.AlsoNoticed) > MaxHeadlineRunes*2,
		utf8.RuneCountInString(look.Failure) > maxFailureRunes,
		len(look.Items) > MaxLookItems,
		len(look.People) > MaxLookSignals || len(look.Stale) > MaxLookSignals || len(look.Decisions) > MaxLookSignals,
		look.Counts.Notes < 0 || look.Counts.OpenItems < 0 || look.Counts.NotesWithOpen < 0 || look.Counts.Dated < 0 || look.Counts.Done < 0,
		look.NewSince < 0 || look.DoneSince < 0:
		return ErrInvalidRecord
	}
	for _, item := range look.Items {
		if err := validateItem(item); err != nil {
			return err
		}
	}
	for _, person := range look.People {
		if person.Name == "" || len(person.Name) > maxNameLength || strings.ContainsAny(person.Name, "\r\n") || person.Mentions < 0 ||
			!validSignalNote(person.Note) || !validLink(person.Link) {
			return ErrInvalidRecord
		}
	}
	for _, note := range look.Stale {
		if !validSignalTitle(note.Title) || note.Open < 0 || note.Modified.IsZero() || !validSignalNote(note.Note) || !validLink(note.Link) {
			return ErrInvalidRecord
		}
	}
	for _, note := range look.Decisions {
		if !validSignalTitle(note.Title) || note.Open < 0 || !validSignalNote(note.Note) || !validLink(note.Link) {
			return ErrInvalidRecord
		}
	}
	return nil
}

func validateItem(item LookItem) error {
	switch {
	case !itemIDPattern.MatchString(item.ID),
		item.Text == "" || utf8.RuneCountInString(item.Text) > MaxTaskTextRunes || strings.ContainsAny(item.Text, "\r\n"),
		!validNotePath(item.Note),
		item.Line < 1,
		item.Due != "" && (!isoDayPattern.MatchString(item.Due) || !validDate(item.Due)),
		!validLink(item.Link):
		return ErrInvalidRecord
	}
	return nil
}

// validNotePath says note is a clean path inside the folder.
func validNotePath(note string) bool {
	return note != "" && !path.IsAbs(note) && path.Clean(note) == note && note != ".." && !strings.HasPrefix(note, "../") && !strings.Contains(note, "\\")
}

func validSignalNote(note string) bool {
	return validNotePath(note) && len(note) <= maxSignalNoteBytes
}

func validSignalTitle(title string) bool {
	return utf8.RuneCountInString(title) <= MaxSignalTitleRunes && !strings.ContainsAny(title, "\r\n")
}

// validLink says link is absent or an Obsidian link safe to put in an href.
func validLink(link string) bool {
	return link == "" || (strings.HasPrefix(link, "obsidian://open?vault=") && len(link) <= 2048 && !strings.ContainsAny(link, " \r\n\"<>"))
}
