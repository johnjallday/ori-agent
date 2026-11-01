package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/folderpath"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// Second-brain refusals. The HTTP layer turns each into one sentence.
var (
	ErrSecondBrainNotConnected = errors.New("personal assistant: no notes folder is connected")
	ErrSecondBrainUnknownVault = errors.New("personal assistant: that vault is not in Obsidian's list")
	ErrSecondBrainSelection    = errors.New("personal assistant: the chosen folder is no longer available")
	ErrSecondBrainLookNotFound = errors.New("personal assistant: that look is no longer here")
	ErrSecondBrainUnavailable  = errors.New("personal assistant: notes are unavailable")
	// ErrSecondBrainAlreadyConnected refuses setting one up while a notes
	// folder is connected; Create has no replace, since neither entry point
	// offers it with a folder connected.
	ErrSecondBrainAlreadyConnected = errors.New("personal assistant: a notes folder is already connected")
	// ErrSecondBrainItemNotFound names an item the look does not hold.
	ErrSecondBrainItemNotFound = errors.New("personal assistant: that item is no longer here")
	// ErrSecondBrainSeededItem refuses a follow-up from an item that was seeded
	// from a follow-up the user already has.
	ErrSecondBrainSeededItem = errors.New("personal assistant: that item already came from a follow-up")
	// ErrSecondBrainNoInbox says the connected folder is not one Ori set up, so
	// the panel has no Inbox for it.
	ErrSecondBrainNoInbox = errors.New("personal assistant: only a second brain Ori set up has an Inbox here")

	errSecondBrainLookSuperseded = errors.New("personal assistant: look superseded")
)

// SecondBrainFolderError refuses a chosen folder; Message is safe to show.
type SecondBrainFolderError struct {
	Message string
	// NoNotes says the folder is fine but holds no Markdown notes (FR13).
	NoNotes bool
}

func (e *SecondBrainFolderError) Error() string { return e.Message }

// SecondBrainTooSoonError refuses a Look again inside the rate limit (FR36).
type SecondBrainTooSoonError struct {
	RetryAfter time.Duration
}

func (e *SecondBrainTooSoonError) Error() string {
	return fmt.Sprintf("personal assistant: look again in %s", e.RetryAfter.Round(time.Second))
}

const (
	// SecondBrainLookAgainAfter is the rate limit on Look again (FR36).
	SecondBrainLookAgainAfter = 10 * time.Minute
	// secondBrainLookTimeout bounds one look end to end: the read's own
	// five-second budget, the headline model's 45 seconds, and the writes.
	secondBrainLookTimeout = 75 * time.Second

	secondBrainActionConnect    = "connect"
	secondBrainActionLook       = "look"
	secondBrainActionFollowUps  = "follow_ups"
	secondBrainActionAsk        = "ask"
	secondBrainActionDisconnect = "disconnect"
	secondBrainActionCreate     = "create"
	secondBrainActionCapture    = "capture"
	secondBrainActionFillIn     = "fill_in"

	// SecondBrainItemFollowUp, SecondBrainItemDone and SecondBrainItemDismiss
	// are what the Notes panel can do with one open item.
	SecondBrainItemFollowUp = "follow_up"
	SecondBrainItemDone     = "done"
	SecondBrainItemDismiss  = "dismiss"

	// maxCaptureAttempts bounds the numbering of a capture whose name is taken.
	maxCaptureAttempts = 50
	// secondBrainInboxCacheFor is how long a listing of Inbox is reused. It only
	// ever runs on request, never on a timer.
	secondBrainInboxCacheFor = 30 * time.Second
)

// Connection states for the API.
const (
	SecondBrainNotConnected = "not_connected"
	SecondBrainConnected    = "connected"
	SecondBrainMissing      = "missing"
	SecondBrainLookNone     = "none"
)

// SecondBrainVaults finds the user's Obsidian vaults (secondbrain.Detector).
type SecondBrainVaults interface {
	Vaults() []secondbrain.Vault
	Lookup(id string) (secondbrain.Vault, bool)
}

// SecondBrainDeps are the second brain's collaborators. Every folder it reads
// comes from Obsidian's own list or from a selection token the folder picker
// issued; nothing here accepts a path from a caller.
type SecondBrainDeps struct {
	Vaults SecondBrainVaults
	// ResolveSelection turns a folder-picker selection token into its path.
	ResolveSelection func(token string) (string, error)
	// CheckFolder applies the typed-path rules (folderpath.Check).
	CheckFolder func(path string) (folderpath.Checked, error)
	// DefaultParent is the folder "Set one up for me" uses when the user picks
	// none: the Workspace Directory, so the second brain sits next to the
	// workspaces and Agents and travels with the root. It is Ori's own choice,
	// never a caller's path, and it is the one parent the folder rules would
	// refuse (the Workspace Directory itself), so it bypasses CheckFolder; the
	// folder made inside it is checked like any other.
	DefaultParent func() string
	FollowUps     secondbrain.FollowUps
	Looker        secondbrain.Looker
	Now           func() time.Time
	NewID         func() string
}

// SecondBrainService connects the user's notes to the HQ and looks at them.
// The vault is read only here (FR42).
type SecondBrainService struct {
	store *SecondBrainStore
	deps  SecondBrainDeps

	looks   sync.WaitGroup
	mu      sync.Mutex
	running map[string]string // user id → the look this process is running

	// createMu serialises setups, so two requests with the same id cannot
	// both try to make the folder.
	createMu sync.Mutex
	seeds    SecondBrainSeeds
	// captureMu serialises captures, so two in one minute number their names
	// instead of racing for one.
	captureMu sync.Mutex
	// inboxCache holds the last listing of Inbox, per source.
	inboxMu    sync.Mutex
	inboxCache *secondBrainInboxEntry

	// holdings reads what HQ holds for the fill-in sync; syncMu keeps it to one
	// run at a time, and the rest coalesce repeat triggers.
	holdings  SecondBrainHoldings
	syncMu    sync.Mutex
	syncs     sync.WaitGroup
	syncState map[string]*secondBrainSyncTrigger

	// Hooks for the mission and the learned fact, bound at startup.
	onConnected    []func(ctx context.Context, userID string, source secondbrain.Source)
	onDisconnected []func(ctx context.Context, userID string, source secondbrain.Source)
	onDeclined     []func(ctx context.Context, userID string)
	onWritten      []func(ctx context.Context, userID string, kinds map[string]int)
}

// OnWritten runs fn with the kinds of note Ori just wrote in a second brain it
// set up: a capture, a Journal copy, a sync. The first-week missions observe it.
// It must not block. Startup wiring only.
func (s *SecondBrainService) OnWritten(fn func(ctx context.Context, userID string, kinds map[string]int)) {
	if s != nil && fn != nil {
		s.onWritten = append(s.onWritten, fn)
	}
}

func (s *SecondBrainService) wrote(ctx context.Context, userID string, kinds map[string]int) {
	for _, fn := range s.onWritten {
		fn(ctx, userID, kinds)
	}
}

// WrittenKinds counts the notes Ori holds in a second brain it set up, by kind,
// read from the folder. ok is false for nothing connected or a folder the user
// connected.
func (s *SecondBrainService) WrittenKinds(ctx context.Context, userID string) (map[string]int, bool) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return nil, false
	}
	source := doc.Connected()
	if source == nil || !source.CreatedByOri() {
		return nil, false
	}
	written, err := secondbrain.CountWritten(source.Path)
	if err != nil {
		return nil, false
	}
	return written.ByKind, true
}

// SecondBrainSeeds gathers what Ori already holds about the user, for the one
// setup that copies it into a new folder (FR8). It reads; it never writes.
type SecondBrainSeeds func(ctx context.Context, userID string) (secondbrain.Seeds, error)

// SetSeeds binds the seed source. The learning services it reads are built
// after this service, so this is startup wiring.
func (s *SecondBrainService) SetSeeds(seeds SecondBrainSeeds) {
	if s != nil {
		s.seeds = seeds
	}
}

// OnConnected runs fn after every connect that was not a replay, the same
// folder again included: the starter mission and the learned fact observe it
// (FR16, FR34). Startup wiring only.
func (s *SecondBrainService) OnConnected(fn func(ctx context.Context, userID string, source secondbrain.Source)) {
	if s != nil && fn != nil {
		s.onConnected = append(s.onConnected, fn)
	}
}

// OnDisconnected runs fn with the folder a disconnect removed (FR37).
func (s *SecondBrainService) OnDisconnected(fn func(ctx context.Context, userID string, source secondbrain.Source)) {
	if s != nil && fn != nil {
		s.onDisconnected = append(s.onDisconnected, fn)
	}
}

// OnDeclined runs fn when the user answers "I don't keep notes" (FR8).
func (s *SecondBrainService) OnDeclined(fn func(ctx context.Context, userID string)) {
	if s != nil && fn != nil {
		s.onDeclined = append(s.onDeclined, fn)
	}
}

// ConnectedSource reads the connected folder, for the learned fact's
// revalidation.
func (s *SecondBrainService) ConnectedSource(ctx context.Context, userID string) (secondbrain.Source, bool, error) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return secondbrain.Source{}, false, err
	}
	if connected := doc.Connected(); connected != nil {
		return *connected, true, nil
	}
	return secondbrain.Source{}, false, nil
}

// NewSecondBrainService builds the service.
func NewSecondBrainService(store *SecondBrainStore, deps SecondBrainDeps) *SecondBrainService {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = func() string { return "look-" + uuid.NewString() }
	}
	return &SecondBrainService{store: store, deps: deps, running: map[string]string{}, syncState: map[string]*secondBrainSyncTrigger{}}
}

// Wait blocks until every look and sync this service started has finished.
func (s *SecondBrainService) Wait() {
	s.looks.Wait()
	s.syncs.Wait()
}

// SecondBrainView is GET /api/personal-assistant/second-brain.
type SecondBrainView struct {
	Status   string                  `json:"status"`
	Detected []SecondBrainDetected   `json:"detected"`
	Source   *SecondBrainSourceView  `json:"source,omitempty"`
	LastLook *SecondBrainLookSummary `json:"last_look,omitempty"`
	Ask      SecondBrainAskView      `json:"ask"`
	// WrittenByOri counts what Ori has put in Inbox and Daily; only a status
	// read of a second brain Ori set up carries it (FR16).
	WrittenByOri *SecondBrainWrittenView `json:"written_by_ori,omitempty"`
	// FillIn says how keeping the notes filled in stands (FR8), for the same reads.
	FillIn *SecondBrainFillInView `json:"fill_in,omitempty"`
	// SetupDefault is where Set one up for me puts the folder without asking;
	// absent while a folder is connected or no default is available.
	SetupDefault *SecondBrainSetupDefault `json:"setup_default,omitempty"`
	// Projects lists the folders under Projects, so one can be offered as a
	// workspace when the user asks (FR18); a second brain Ori set up only.
	Projects []SecondBrainProjectView `json:"projects,omitempty"`
	// InboxCount and TodayNote feed the Notes station; a status read of a second
	// brain Ori set up carries them.
	InboxCount *int                  `json:"inbox_count,omitempty"`
	TodayNote  *SecondBrainTodayNote `json:"today_note,omitempty"`
	Version    int64                 `json:"version"`
	// LookID names the look a connect started (or the replayed one).
	LookID string `json:"look_id,omitempty"`
}

// SecondBrainTodayNote says whether today has a Daily note, and whose it is.
type SecondBrainTodayNote struct {
	Date string `json:"date"`
	// State is "ori", "user" or "none".
	State string `json:"state"`
	Link  string `json:"link,omitempty"`
}

// SecondBrainInboxView is GET …/second-brain/inbox: a bounded listing, newest
// first. Titles are read for it and never stored.
type SecondBrainInboxView struct {
	Notes   []SecondBrainInboxNote `json:"notes"`
	Total   int                    `json:"total"`
	Partial bool                   `json:"partial"`
}

// SecondBrainInboxNote is one note directly under Inbox.
type SecondBrainInboxNote struct {
	Name string `json:"name"`
	// Kind is "capture", "seed" or "user".
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Modified   time.Time `json:"modified"`
	Link       string    `json:"link,omitempty"`
	OwnedByOri bool      `json:"owned_by_ori"`
}

type secondBrainInboxEntry struct {
	sourceID string
	at       time.Time
	view     SecondBrainInboxView
}

// SecondBrainSetupDefault is the folder a one-click setup would make.
type SecondBrainSetupDefault struct {
	// ParentName is the folder it goes in, by name, for the sentence.
	ParentName string `json:"parent_name"`
	Path       string `json:"path"`
}

// SecondBrainProjectView is one folder under Projects. Path is the folder the
// Create Workspace form takes as its work folder; it is built from the
// connected folder, never read from a note.
type SecondBrainProjectView struct {
	Name string `json:"name"`
	Rel  string `json:"rel"`
	Path string `json:"path"`
}

// SecondBrainWrittenView is the "Written by Ori" count.
type SecondBrainWrittenView struct {
	Count  int            `json:"count"`
	ByKind map[string]int `json:"by_kind,omitempty"`
	LastAt time.Time      `json:"last_at,omitempty"`
}

// SecondBrainFillInView counts the notes Ori keeps by how they stand. UserOwned
// are the ones the user took over, which Ori stopped updating.
type SecondBrainFillInView struct {
	// State is "allowed", "pending" (a setup from before the fill-in, not yet
	// asked) or "later". Offer says the consent card belongs under Needs you.
	State     string     `json:"state"`
	Offer     bool       `json:"offer"`
	Written   int        `json:"written"`
	UserOwned int        `json:"user_owned"`
	Stale     int        `json:"stale"`
	LastAt    *time.Time `json:"last_at,omitempty"`
}

// SecondBrainDetected is one vault Obsidian lists. Path is shown only in the
// card's detail, to the owner (FR12, FR41).
type SecondBrainDetected struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent"`
	Path   string `json:"path"`
}

// SecondBrainSourceView is the connected folder.
type SecondBrainSourceView struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	ConnectedAt time.Time `json:"connected_at"`
	// Origin is "connected" or "created_by_ori" (FR9).
	Origin string `json:"origin"`
	// OpenLink opens a folder Ori set up as a vault in Obsidian. It is a link
	// only; nothing is launched.
	OpenLink string `json:"open_link,omitempty"`
}

// SecondBrainLookSummary is the last finished look, for the slot and Home.
type SecondBrainLookSummary struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	OpenItems int       `json:"open_items"`
	Headline  string    `json:"headline"`
	Acted     bool      `json:"acted"`
	// People, Stale and Decisions count the signals the look kept; GET …/look
	// holds the lists.
	People    int `json:"people"`
	Stale     int `json:"stale"`
	Decisions int `json:"decisions"`
	// Handled counts the items the user has triaged, so the Notes station can
	// say how many are still open without changing what the look found.
	Handled int `json:"handled"`
}

// SecondBrainAskView is the ask's state.
type SecondBrainAskView struct {
	State string `json:"state"`
	// OfferAgain says Not now's week has passed (FR8).
	OfferAgain bool `json:"offer_again,omitempty"`
}

// SecondBrainLookView is GET …/second-brain/look.
type SecondBrainLookView struct {
	State  string            `json:"state"`
	LookID string            `json:"look_id,omitempty"`
	Result *secondbrain.Look `json:"result,omitempty"`
}

// SecondBrainAcceptView is the receipt of an Add as follow-ups.
type SecondBrainAcceptView struct {
	LookID  string `json:"look_id"`
	Created int    `json:"created"`
	Skipped int    `json:"skipped"`
}

// SecondBrainConnectInput names the folder by a detected vault or a selection
// token, never by path.
type SecondBrainConnectInput struct {
	DetectedID     string
	SelectionToken string
	RequestID      string
	// Replace confirms replacing a different connected folder (FR15).
	Replace bool
}

// SecondBrainAcceptInput chooses the items to add: the suggested set or ids.
type SecondBrainAcceptInput struct {
	Suggested bool
	ItemIDs   []string
	RequestID string
}

// Status reads the record and Obsidian's vault list.
func (s *SecondBrainService) Status(ctx context.Context, userID string) (SecondBrainView, error) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainView{}, err
	}
	view := s.view(doc)
	if connected := doc.Connected(); connected != nil && connected.CreatedByOri() && view.Status == SecondBrainConnected {
		if written, err := secondbrain.CountWritten(connected.Path); err == nil {
			view.WrittenByOri = &SecondBrainWrittenView{Count: written.Count, ByKind: written.ByKind, LastAt: written.LastAt}
		}
		view.FillIn = fillInView(connected.FillIn, s.deps.Now())
		view.Projects = offerableProjects(connected)
		if inbox, err := s.inboxListing(*connected); err == nil {
			count := inbox.Total
			view.InboxCount = &count
		}
		view.TodayNote = s.todayNote(*connected)
	}
	return view, nil
}

// Inbox lists the notes directly under Inbox in a second brain Ori set up.
func (s *SecondBrainService) Inbox(ctx context.Context, userID string) (SecondBrainInboxView, error) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainInboxView{}, err
	}
	connected := doc.Connected()
	if connected == nil {
		return SecondBrainInboxView{}, ErrSecondBrainNotConnected
	}
	if !connected.CreatedByOri() {
		return SecondBrainInboxView{}, ErrSecondBrainNoInbox
	}
	// Only the Notes panel reads this, so this is the panel opening (FR6).
	s.SyncSoon(userID, "notes opened")
	return s.inboxListing(*connected)
}

// inboxListing reads Inbox, or returns a listing younger than
// secondBrainInboxCacheFor. A capture drops the cached listing.
func (s *SecondBrainService) inboxListing(source secondbrain.Source) (SecondBrainInboxView, error) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	now := s.deps.Now()
	if cached := s.inboxCache; cached != nil && cached.sourceID == source.ID && now.Sub(cached.at) < secondBrainInboxCacheFor {
		return cached.view, nil
	}
	inbox, err := secondbrain.ListInbox(source.Path)
	if err != nil {
		logger.Warn("The notes Inbox could not be listed", logger.Fields{"error": err.Error()})
		return SecondBrainInboxView{}, ErrSecondBrainUnavailable
	}
	view := SecondBrainInboxView{Notes: make([]SecondBrainInboxNote, 0, len(inbox.Notes)), Total: inbox.Total, Partial: inbox.Partial}
	for _, note := range inbox.Notes {
		view.Notes = append(view.Notes, SecondBrainInboxNote{
			Name: note.Name, Kind: note.Kind, Title: note.Title, Modified: note.Modified.UTC(), OwnedByOri: note.OwnedByOri,
			Link: secondbrain.ObsidianPathLink(filepath.Join(source.Path, secondbrain.FolderInbox, note.Name)),
		})
	}
	s.inboxCache = &secondBrainInboxEntry{sourceID: source.ID, at: now, view: view}
	return view, nil
}

func (s *SecondBrainService) dropInboxCache() {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	s.inboxCache = nil
}

// todayNote reads today's Daily note, by the server's local date: the day the
// Journal proposes and mirrors. An unreadable folder leaves the field out.
func (s *SecondBrainService) todayNote(source secondbrain.Source) *SecondBrainTodayNote {
	date := s.deps.Now().Format("2006-01-02")
	state, err := secondbrain.ReadTodayNote(source.Path, date)
	if err != nil {
		return nil
	}
	note := &SecondBrainTodayNote{Date: date, State: state}
	if state != secondbrain.TodayNoteNone {
		note.Link = secondbrain.ObsidianPathLink(filepath.Join(source.Path, secondbrain.FolderDaily, date+".md"))
	}
	return note
}

// projectViews lists the folders under Projects in a folder Ori set up.
func projectViews(root string) []SecondBrainProjectView {
	projects, err := secondbrain.ListProjects(root)
	if err != nil {
		return nil
	}
	views := make([]SecondBrainProjectView, 0, len(projects))
	for _, project := range projects {
		views = append(views, SecondBrainProjectView{Name: project.Name, Rel: project.Rel, Path: project.Path})
	}
	return views
}

// Projects lists the folders under Projects in a second brain Ori set up, for
// the assistant's reply to link when the user asks to make one a workspace.
func (s *SecondBrainService) Projects(ctx context.Context, userID string) []SecondBrainProjectView {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return nil
	}
	source := doc.Connected()
	if source == nil || !source.CreatedByOri() {
		return nil
	}
	return offerableProjects(source)
}

// offerableProjects leaves out a project note Ori keeps for a workspace: offering
// to make it a workspace would make a second one.
func offerableProjects(source *secondbrain.Source) []SecondBrainProjectView {
	views := projectViews(source.Path)
	if source.FillIn == nil {
		return views
	}
	kept := make(map[string]bool, len(source.FillIn.Mirrors))
	for _, mirror := range source.FillIn.Mirrors {
		if mirror.Ref.Kind == secondbrain.NoteKindProject && mirror.State == secondbrain.MirrorCurrent {
			kept[mirror.Rel] = true
		}
	}
	offered := make([]SecondBrainProjectView, 0, len(views))
	for _, view := range views {
		if !kept[view.Rel+"/README.md"] {
			offered = append(offered, view)
		}
	}
	return offered
}

// MarkPrompted records that the ask was shown, once per relationship (FR5).
func (s *SecondBrainService) MarkPrompted(ctx context.Context, userID, requestID string) (SecondBrainView, error) {
	if !secondbrain.ValidRequestID(requestID) {
		return SecondBrainView{}, ErrValidation
	}
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		doc.MarkPrompted(requestID, s.deps.Now())
		return nil
	})
	if err != nil {
		return SecondBrainView{}, err
	}
	return s.view(doc), nil
}

// DecideAsk records Not now or I don't keep notes (FR8).
func (s *SecondBrainService) DecideAsk(ctx context.Context, userID, decision, requestID string) (SecondBrainView, error) {
	if !secondbrain.ValidRequestID(requestID) || (decision != secondbrain.DecisionLater && decision != secondbrain.DecisionNone) {
		return SecondBrainView{}, ErrValidation
	}
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, replayed, err := doc.Replay(requestID, secondBrainActionAsk); err != nil || replayed {
			return err
		}
		if err := doc.Decide(decision, requestID, s.deps.Now()); err != nil {
			return ErrValidation
		}
		doc.Remember(secondbrain.Receipt{RequestID: requestID, Action: secondBrainActionAsk, At: s.deps.Now()})
		return nil
	})
	if err != nil {
		return SecondBrainView{}, err
	}
	if decision == secondbrain.DecisionNone {
		for _, fn := range s.onDeclined {
			fn(ctx, userID)
		}
	}
	return s.view(doc), nil
}

// Connect connects the chosen folder and starts the first look (FR15, FR22).
// A replayed request returns what the first call did.
func (s *SecondBrainService) Connect(ctx context.Context, userID string, input SecondBrainConnectInput) (SecondBrainView, error) {
	if !secondbrain.ValidRequestID(input.RequestID) || (input.DetectedID == "") == (input.SelectionToken == "") {
		return SecondBrainView{}, ErrValidation
	}
	current, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainView{}, err
	}
	if receipt, replayed, err := current.Replay(input.RequestID, secondBrainActionConnect); err != nil || replayed {
		if err != nil {
			return SecondBrainView{}, err
		}
		view := s.view(current)
		view.LookID = receipt.LookID
		return view, nil
	}
	source, err := s.chosenSource(input)
	if err != nil {
		return SecondBrainView{}, err
	}

	var started *secondbrain.Look
	var lookID string
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		started, lookID = nil, ""
		if receipt, replayed, err := doc.Replay(input.RequestID, secondBrainActionConnect); err != nil || replayed {
			lookID = receipt.LookID
			return err
		}
		connected, err := doc.Connect(source, input.Replace)
		if err != nil {
			return err
		}
		if connected.RequestID == input.RequestID {
			// A new connection: its first look starts now.
			look := s.runningLook(input.RequestID)
			connected.Current, lookID, started = &look, look.ID, &look
		} else if look := latestLook(connected); look != nil {
			lookID = look.ID
		}
		doc.Remember(secondbrain.Receipt{RequestID: input.RequestID, Action: secondBrainActionConnect, At: s.deps.Now(), LookID: lookID})
		return nil
	})
	if err != nil {
		return SecondBrainView{}, err
	}
	if started != nil {
		s.startLook(userID, *doc.Connected(), nil, started.ID, input.RequestID, nil)
	}
	if connected := doc.Connected(); connected != nil {
		for _, fn := range s.onConnected {
			fn(ctx, userID, *connected)
		}
	}
	view := s.view(doc)
	view.LookID = lookID
	return view, nil
}

// SecondBrainCreateInput names the parent by a selection token and the new
// folder by a name, never by path (FR4). No selection token means the default
// place: DefaultParent, under the fixed name secondbrain.DefaultFolderName, so
// Name is ignored.
type SecondBrainCreateInput struct {
	SelectionToken string
	Name           string
	RequestID      string
}

// Create sets up a second brain (FR4–FR11): it makes the folder inside the
// parent the user picked, seeds it, connects it as created by Ori, and starts
// the first look. A replayed request returns what the first call did. A
// failure after the folder was made removes what this call made.
func (s *SecondBrainService) Create(ctx context.Context, userID string, input SecondBrainCreateInput) (SecondBrainView, error) {
	name := strings.TrimSpace(input.Name)
	useDefault := input.SelectionToken == ""
	if useDefault {
		name = secondbrain.DefaultFolderName
	}
	if !secondbrain.ValidRequestID(input.RequestID) {
		return SecondBrainView{}, ErrValidation
	}
	if !secondbrain.ValidFolderName(name) {
		return SecondBrainView{}, &SecondBrainFolderError{Message: "Choose a simple folder name, without slashes."}
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()

	current, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainView{}, err
	}
	if receipt, replayed, err := current.Replay(input.RequestID, secondBrainActionCreate); err != nil || replayed {
		if err != nil {
			return SecondBrainView{}, err
		}
		view := s.view(current)
		view.LookID = receipt.LookID
		return view, nil
	}
	if current.Connected() != nil {
		return SecondBrainView{}, ErrSecondBrainAlreadyConnected
	}
	var parent string
	if useDefault {
		// No default (the Workspace Directory is not chosen yet) asks for a pick.
		if parent = s.defaultParent(); parent == "" {
			return SecondBrainView{}, ErrSecondBrainSelection
		}
	} else {
		checked, err := s.checkedFolder(input.SelectionToken)
		if err != nil {
			return SecondBrainView{}, err
		}
		parent = checked.Path
	}

	creation, err := s.createFolder(ctx, userID, parent, name)
	if err != nil {
		return SecondBrainView{}, err
	}
	target, err := s.deps.CheckFolder(creation.Path)
	if err != nil {
		creation.Undo()
		return SecondBrainView{}, &SecondBrainFolderError{Message: folderpath.Message(err)}
	}
	kind := secondbrain.KindMarkdown
	if secondbrain.IsObsidianVault(target.Path) {
		kind = secondbrain.KindObsidian
	}
	source := secondbrain.Source{
		ID: secondbrain.SourceID(target.Path), Kind: kind, Name: target.Name, Path: target.Path,
		ConnectedAt: s.deps.Now().UTC(), RequestID: input.RequestID, Origin: secondbrain.OriginCreatedByOri,
	}

	var started *secondbrain.Look
	var lookID string
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		started, lookID = nil, ""
		if doc.Connected() != nil {
			return ErrSecondBrainAlreadyConnected
		}
		connected, err := doc.Connect(source, false)
		if err != nil {
			return err
		}
		look := s.runningLook(input.RequestID)
		connected.Current, lookID, started = &look, look.ID, &look
		// The setup says what Ori will keep up to date before it writes, so a new
		// one needs no card (FR10).
		consented := s.deps.Now().UTC()
		connected.FillIn = &secondbrain.FillIn{Consent: secondbrain.ConsentAllowed, ConsentAt: &consented, ConsentRequestID: input.RequestID}
		doc.Remember(secondbrain.Receipt{
			RequestID: input.RequestID, Action: secondBrainActionCreate, At: s.deps.Now(), LookID: lookID,
			Created: creation.FollowUps, Skipped: creation.People,
		})
		return nil
	})
	if err != nil {
		creation.Undo()
		return SecondBrainView{}, err
	}
	setup := secondbrain.SetupCounts{FollowUps: creation.FollowUps, People: creation.People}
	s.startLook(userID, *doc.Connected(), nil, started.ID, input.RequestID, &setup)
	for _, fn := range s.onConnected {
		fn(ctx, userID, *doc.Connected())
	}
	view := s.view(doc)
	view.LookID = lookID
	return view, nil
}

// SecondBrainCaptureView is the note a capture wrote.
type SecondBrainCaptureView struct {
	// Note is the path below the notes folder, "Inbox/<stamp>-<slug>.md".
	Note string `json:"note"`
	// Name is the note's file name without its extension.
	Name string `json:"name"`
	// Link opens the note in Obsidian; a link only.
	Link string `json:"link,omitempty"`
}

// ErrSecondBrainNotOwned refuses a write to a notes folder the user connected:
// Ori only writes to one it set up (FR12).
var ErrSecondBrainNotOwned = secondbrain.ErrNotOwned

// Capture writes one note to Inbox in a second brain Ori set up (FR15). It is
// refused for any other connection. A replayed request returns the note the
// first call wrote; a name already taken gets a number, never an overwrite.
func (s *SecondBrainService) Capture(ctx context.Context, userID, text, requestID string) (SecondBrainCaptureView, error) {
	if !secondbrain.ValidRequestID(requestID) {
		return SecondBrainCaptureView{}, ErrValidation
	}
	now := s.deps.Now()
	capture, err := secondbrain.NewCapture(text, now)
	if err != nil {
		return SecondBrainCaptureView{}, err
	}
	s.captureMu.Lock()
	defer s.captureMu.Unlock()

	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainCaptureView{}, err
	}
	source := doc.Connected()
	if source == nil {
		return SecondBrainCaptureView{}, ErrSecondBrainNotConnected
	}
	if receipt, replayed, err := doc.Replay(requestID, secondBrainActionCapture); err != nil || replayed {
		return captureView(*source, receipt.Note), err
	}
	writer, err := secondbrain.NewWriter(source)
	if err != nil {
		return SecondBrainCaptureView{}, err
	}
	var rel string
	for attempt := 1; ; attempt++ {
		if attempt > maxCaptureAttempts {
			return SecondBrainCaptureView{}, ErrSecondBrainUnavailable
		}
		rel = capture.Rel(attempt)
		_, err = writer.Create(rel, secondbrain.NoteKindCapture, capture.Body)
		if !errors.Is(err, secondbrain.ErrNoteExists) {
			break
		}
	}
	if err != nil {
		logger.Warn("A capture could not be written", logger.Fields{"error": err.Error()})
		return SecondBrainCaptureView{}, err
	}
	s.dropInboxCache()
	s.wrote(ctx, userID, map[string]int{secondbrain.NoteKindCapture: 1})
	if _, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, replayed, err := doc.Replay(requestID, secondBrainActionCapture); err != nil || replayed {
			return err
		}
		doc.Remember(secondbrain.Receipt{RequestID: requestID, Action: secondBrainActionCapture, At: now, Note: rel})
		return nil
	}); err != nil {
		// The note is on disk; a retry with this id writes a second one, so say
		// so rather than hide the failure.
		logger.Warn("A capture's receipt could not be saved", logger.Fields{"error": err.Error()})
	}
	return captureView(*source, rel), nil
}

// CaptureAvailable reports whether Capture can work: a second brain Ori set up
// is connected and its folder is still there. The chat tool is offered only
// then, so a user with a connected vault never sees it.
func (s *SecondBrainService) CaptureAvailable(ctx context.Context, userID string) bool {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return false
	}
	source := doc.Connected()
	if source == nil || !source.CreatedByOri() {
		return false
	}
	info, err := os.Stat(source.Path)
	return err == nil && info.IsDir()
}

func captureView(source secondbrain.Source, rel string) SecondBrainCaptureView {
	name := strings.TrimSuffix(strings.TrimPrefix(rel, secondbrain.FolderInbox+"/"), ".md")
	view := SecondBrainCaptureView{Note: rel, Name: name}
	if source.CreatedByOri() {
		view.Link = secondbrain.ObsidianPathLink(filepath.Join(source.Path, filepath.FromSlash(rel)))
	}
	return view
}

// WriteDaily mirrors a Journal day to Daily/<date>.md in a second brain Ori
// set up (FR14). ok is false, with no error, when there is nothing to mirror to
// (nothing connected, or a folder the user connected). ErrUserModified says the
// day's note is the user's and was left alone.
func (s *SecondBrainService) WriteDaily(ctx context.Context, userID, localDate, content string) (mirrored bool, err error) {
	if !isoDay(localDate) {
		return false, ErrValidation
	}
	doc, err := s.read(ctx, userID)
	if err != nil {
		return false, err
	}
	source := doc.Connected()
	if source == nil || !source.CreatedByOri() {
		return false, nil
	}
	writer, err := secondbrain.NewWriter(source)
	if err != nil {
		return false, err
	}
	body := strings.TrimSpace(content)
	if body == "" {
		return false, ErrValidation
	}
	if _, err := writer.Write(secondbrain.FolderDaily+"/"+localDate+".md", secondbrain.NoteKindJournal, []byte(body+"\n")); err != nil {
		return false, err
	}
	s.wrote(ctx, userID, map[string]int{secondbrain.NoteKindJournal: 1})
	return true, nil
}

func isoDay(text string) bool {
	if len(text) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", text)
	return err == nil
}

// createFolder gathers the seeds and makes the folder, turning each refusal
// into one sentence the user can act on (FR5, FR11).
func (s *SecondBrainService) createFolder(ctx context.Context, userID, parent, name string) (secondbrain.Creation, error) {
	var seeds secondbrain.Seeds
	if s.seeds != nil {
		var err error
		if seeds, err = s.seeds(ctx, userID); err != nil {
			// An unreadable follow-up list must not stop the user's folder;
			// the first look then reports the seed honestly as empty.
			logger.Warn("Second-brain seeds could not be read", logger.Fields{"error": err.Error()})
			seeds = secondbrain.Seeds{}
		}
	}
	creation, err := secondbrain.Creator{}.Create(parent, name, seeds)
	switch {
	case err == nil:
		return creation, nil
	case errors.Is(err, secondbrain.ErrTargetNotEmpty):
		return secondbrain.Creation{}, &SecondBrainFolderError{Message: fmt.Sprintf("%s already has something in it. Choose another name or location.", name)}
	case errors.Is(err, secondbrain.ErrInvalidFolderName):
		return secondbrain.Creation{}, &SecondBrainFolderError{Message: "Choose a simple folder name, without slashes."}
	default:
		logger.Warn("A second brain could not be set up", logger.Fields{"error": err.Error()})
		return secondbrain.Creation{}, &SecondBrainFolderError{Message: "Ori couldn't set up the folder there. Check that you can write to that location, then try again."}
	}
}

// defaultParent is where a second brain goes when the user picks nowhere, or
// "" when there is no such place.
func (s *SecondBrainService) defaultParent() string {
	if s.deps.DefaultParent == nil {
		return ""
	}
	return strings.TrimSpace(s.deps.DefaultParent())
}

// checkedFolder resolves a folder-picker selection token and applies the
// typed-path rules (FR4).
func (s *SecondBrainService) checkedFolder(token string) (folderpath.Checked, error) {
	if s.deps.ResolveSelection == nil {
		return folderpath.Checked{}, ErrSecondBrainSelection
	}
	path, err := s.deps.ResolveSelection(token)
	if err != nil {
		return folderpath.Checked{}, ErrSecondBrainSelection
	}
	if s.deps.CheckFolder == nil {
		return folderpath.Checked{}, ErrSecondBrainUnavailable
	}
	checked, err := s.deps.CheckFolder(path)
	if err != nil {
		return folderpath.Checked{}, &SecondBrainFolderError{Message: folderpath.Message(err)}
	}
	return checked, nil
}

// chosenSource resolves the folder a connect names and applies the folder
// rules (FR2, FR13).
func (s *SecondBrainService) chosenSource(input SecondBrainConnectInput) (secondbrain.Source, error) {
	var raw string
	switch {
	case input.DetectedID != "":
		if s.deps.Vaults == nil {
			return secondbrain.Source{}, ErrSecondBrainUnknownVault
		}
		vault, ok := s.deps.Vaults.Lookup(input.DetectedID)
		if !ok {
			return secondbrain.Source{}, ErrSecondBrainUnknownVault
		}
		raw = vault.Path
	default:
		if s.deps.ResolveSelection == nil {
			return secondbrain.Source{}, ErrSecondBrainSelection
		}
		path, err := s.deps.ResolveSelection(input.SelectionToken)
		if err != nil {
			return secondbrain.Source{}, ErrSecondBrainSelection
		}
		raw = path
	}
	if s.deps.CheckFolder == nil {
		return secondbrain.Source{}, ErrSecondBrainUnavailable
	}
	checked, err := s.deps.CheckFolder(raw)
	if err != nil {
		return secondbrain.Source{}, &SecondBrainFolderError{Message: folderpath.Message(err)}
	}
	kind := secondbrain.KindMarkdown
	if secondbrain.IsObsidianVault(checked.Path) {
		kind = secondbrain.KindObsidian
	} else if input.DetectedID != "" {
		return secondbrain.Source{}, ErrSecondBrainUnknownVault
	}
	if !secondbrain.HasNotes(checked.Path) {
		return secondbrain.Source{}, &SecondBrainFolderError{Message: fmt.Sprintf("No Markdown notes found in %s.", checked.Name), NoNotes: true}
	}
	return secondbrain.Source{
		ID: secondbrain.SourceID(checked.Path), Kind: kind, Name: checked.Name, Path: checked.Path,
		ConnectedAt: s.deps.Now().UTC(), RequestID: input.RequestID,
	}, nil
}

// Look reports the current look (FR22): running, finished or failed.
func (s *SecondBrainService) Look(ctx context.Context, userID string) (SecondBrainLookView, error) {
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainLookView{}, err
	}
	connected := doc.Connected()
	if connected == nil {
		return SecondBrainLookView{}, ErrSecondBrainNotConnected
	}
	return s.lookView(userID, connected), nil
}

// LookAgain re-reads the folder without a new consent, at most once per
// SecondBrainLookAgainAfter (FR36).
func (s *SecondBrainService) LookAgain(ctx context.Context, userID, requestID string) (SecondBrainLookView, error) {
	if !secondbrain.ValidRequestID(requestID) {
		return SecondBrainLookView{}, ErrValidation
	}
	var started *secondbrain.Look
	var previous *secondbrain.Look
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		started, previous = nil, nil
		if _, replayed, err := doc.Replay(requestID, secondBrainActionLook); err != nil || replayed {
			return err
		}
		connected := doc.Connected()
		if connected == nil {
			return ErrSecondBrainNotConnected
		}
		if connected.Current != nil && connected.Current.State == secondbrain.LookRunning && s.isRunning(userID, connected.Current.ID) {
			return nil // already looking; the caller polls the running look
		}
		if last := connected.LastLook; last != nil {
			if wait := SecondBrainLookAgainAfter - s.deps.Now().Sub(last.StartedAt); wait > 0 {
				return &SecondBrainTooSoonError{RetryAfter: wait}
			}
			copied := *last
			previous = &copied
		}
		look := s.runningLook(requestID)
		connected.Current, started = &look, &look
		doc.Remember(secondbrain.Receipt{RequestID: requestID, Action: secondBrainActionLook, At: s.deps.Now(), LookID: look.ID})
		return nil
	})
	if err != nil {
		return SecondBrainLookView{}, err
	}
	connected := doc.Connected()
	if started != nil {
		s.startLook(userID, *connected, previous, started.ID, requestID, nil)
	}
	s.SyncSoon(userID, "look again")
	return s.lookView(userID, connected), nil
}

// AcceptFollowUps adds the chosen items of the last finished look as HQ
// follow-ups (FR24, FR28). Items accepted before are skipped.
func (s *SecondBrainService) AcceptFollowUps(ctx context.Context, userID, lookID string, input SecondBrainAcceptInput) (SecondBrainAcceptView, error) {
	if !secondbrain.ValidRequestID(input.RequestID) || lookID == "" || input.Suggested == (len(input.ItemIDs) > 0) {
		return SecondBrainAcceptView{}, ErrValidation
	}
	if s.deps.FollowUps == nil {
		return SecondBrainAcceptView{}, ErrSecondBrainUnavailable
	}
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainAcceptView{}, err
	}
	if receipt, replayed, err := doc.Replay(input.RequestID, secondBrainActionFollowUps); err != nil || replayed {
		return SecondBrainAcceptView{LookID: receipt.LookID, Created: receipt.Created, Skipped: receipt.Skipped}, err
	}
	connected := doc.Connected()
	if connected == nil {
		return SecondBrainAcceptView{}, ErrSecondBrainNotConnected
	}
	if connected.LastLook == nil || connected.LastLook.ID != lookID {
		return SecondBrainAcceptView{}, ErrSecondBrainLookNotFound
	}
	items, alreadyHandled, err := chosenItems(connected.LastLook.Items, input)
	if err != nil {
		return SecondBrainAcceptView{}, err
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return SecondBrainAcceptView{}, err
	}
	created, skipped, err := secondbrain.Accept(ctx, s.deps.FollowUps, secondbrain.Acceptance{
		UserID: userID, WorkspaceID: binding.HQWorkspaceID, Source: *connected, Items: items,
	})
	if err != nil {
		return SecondBrainAcceptView{}, err
	}
	skipped += alreadyHandled
	if _, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, replayed, err := doc.Replay(input.RequestID, secondBrainActionFollowUps); err != nil || replayed {
			return err
		}
		if c := doc.Connected(); c != nil && c.LastLook != nil && c.LastLook.ID == lookID {
			at := s.deps.Now().UTC()
			c.LastLook.ActedAt = &at
			// Each one now has a follow-up, so the Notes panel files it as handled.
			for _, chosen := range items {
				if item, ok := c.LastLook.Item(chosen.ID); ok && !item.Seeded {
					item.ActedAs, item.ActedAt = secondbrain.ActedFollowUp, &at
				}
			}
		}
		doc.Remember(secondbrain.Receipt{
			RequestID: input.RequestID, Action: secondBrainActionFollowUps, At: s.deps.Now(), LookID: lookID, Created: created, Skipped: skipped,
		})
		return nil
	}); err != nil {
		return SecondBrainAcceptView{}, err
	}
	return SecondBrainAcceptView{LookID: lookID, Created: created, Skipped: skipped}, nil
}

// chosenItems picks the items to add. The suggested set skips items the user has
// already triaged, and says how many, so the receipt can count them as skipped.
func chosenItems(items []secondbrain.LookItem, input SecondBrainAcceptInput) (chosen []secondbrain.LookItem, alreadyHandled int, err error) {
	if input.Suggested {
		for _, item := range items {
			switch {
			case !item.Suggested:
			case item.ActedAs != "":
				alreadyHandled++
			default:
				chosen = append(chosen, item)
			}
		}
		return chosen, alreadyHandled, nil
	}
	byID := make(map[string]secondbrain.LookItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	seen := make(map[string]bool, len(input.ItemIDs))
	for _, id := range input.ItemIDs {
		item, ok := byID[id]
		if !ok {
			return nil, 0, ErrValidation
		}
		if !seen[id] {
			seen[id] = true
			chosen = append(chosen, item)
		}
	}
	return chosen, 0, nil
}

// ActOnItem triages one open item of the last finished look: make it a
// follow-up, mark it done, or dismiss it until the next look. Nothing here
// touches the notes. Done completes the item's follow-up if it has one and is
// otherwise only remembered here; a later look that finds the line ticked or
// gone drops the mark. A replay returns the look without acting again.
func (s *SecondBrainService) ActOnItem(ctx context.Context, userID, lookID, itemID, action, requestID string) (SecondBrainLookView, error) {
	receiptAction, actedAs := "", ""
	switch action {
	case SecondBrainItemFollowUp:
		receiptAction, actedAs = "item_follow_up", secondbrain.ActedFollowUp
	case SecondBrainItemDone:
		receiptAction, actedAs = "item_done", secondbrain.ActedDone
	case SecondBrainItemDismiss:
		receiptAction, actedAs = "item_dismiss", secondbrain.ActedDismissed
	}
	if receiptAction == "" || !secondbrain.ValidRequestID(requestID) || lookID == "" || !secondbrain.ValidItemID(itemID) {
		return SecondBrainLookView{}, ErrValidation
	}
	replayed := func(doc *SecondBrainDocument) (bool, error) {
		receipt, found, err := doc.Replay(requestID, receiptAction)
		if err != nil || !found {
			return false, err
		}
		if receipt.Item != itemID {
			return false, secondbrain.ErrRequestReused
		}
		return true, nil
	}
	doc, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainLookView{}, err
	}
	connected := doc.Connected()
	if connected == nil {
		return SecondBrainLookView{}, ErrSecondBrainNotConnected
	}
	if done, err := replayed(&doc); err != nil || done {
		if err != nil {
			return SecondBrainLookView{}, err
		}
		return s.lookView(userID, connected), nil
	}
	if connected.LastLook == nil || connected.LastLook.ID != lookID {
		return SecondBrainLookView{}, ErrSecondBrainLookNotFound
	}
	item, ok := connected.LastLook.Item(itemID)
	if !ok {
		return SecondBrainLookView{}, ErrSecondBrainItemNotFound
	}
	switch action {
	case SecondBrainItemFollowUp:
		if item.Seeded {
			return SecondBrainLookView{}, ErrSecondBrainSeededItem
		}
		if s.deps.FollowUps == nil {
			return SecondBrainLookView{}, ErrSecondBrainUnavailable
		}
		binding, err := s.store.Binding(ctx, userID)
		if err != nil {
			return SecondBrainLookView{}, err
		}
		if _, _, err := secondbrain.Accept(ctx, s.deps.FollowUps, secondbrain.Acceptance{
			UserID: userID, WorkspaceID: binding.HQWorkspaceID, Source: *connected, Items: []secondbrain.LookItem{*item},
		}); err != nil {
			return SecondBrainLookView{}, err
		}
	case SecondBrainItemDone:
		if closer, ok := s.deps.FollowUps.(secondbrain.FollowUpCloser); ok {
			if _, err := secondbrain.CompleteDone(ctx, closer, userID, itemID); err != nil {
				return SecondBrainLookView{}, err
			}
		}
	}
	updated, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if done, err := replayed(doc); err != nil || done {
			return err
		}
		current := doc.Connected()
		if current == nil {
			return ErrSecondBrainNotConnected
		}
		if current.LastLook == nil || current.LastLook.ID != lookID {
			return ErrSecondBrainLookNotFound
		}
		target, ok := current.LastLook.Item(itemID)
		if !ok {
			return ErrSecondBrainItemNotFound
		}
		at := s.deps.Now().UTC()
		target.ActedAs, target.ActedAt = actedAs, &at
		current.LastLook.ActedAt = &at
		doc.Remember(secondbrain.Receipt{RequestID: requestID, Action: receiptAction, At: at, LookID: lookID, Item: itemID})
		return nil
	})
	if err != nil {
		return SecondBrainLookView{}, err
	}
	return s.lookView(userID, updated.Connected()), nil
}

// OwnsFirstPrompt says the second-brain ask is the first message after HQ for
// this relationship (FR5, FR9), so the folder hand-over is not shown. It holds
// whenever the record can be read, whatever the ask's state: a declined ask
// is never followed by the folder prompt as a second unprompted message.
func (s *SecondBrainService) OwnsFirstPrompt(ctx context.Context, userID string) bool {
	_, err := s.read(ctx, userID)
	return err == nil
}

// ResetAsk re-arms the ask after Reset Getting Started (FR39); the connection
// is kept. With no active HQ there is nothing to reset.
func (s *SecondBrainService) ResetAsk(ctx context.Context, userID string) error {
	_, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		doc.ResetAsk()
		return nil
	})
	if errors.Is(err, ErrNeedsHQ) || errors.Is(err, ErrRepairNeeded) || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// DismissLook takes a finished result off Needs you without adding anything
// (FR31, FR32). Dismissing twice is the same as once.
func (s *SecondBrainService) DismissLook(ctx context.Context, userID, lookID, requestID string) (SecondBrainLookView, error) {
	if !secondbrain.ValidRequestID(requestID) || lookID == "" {
		return SecondBrainLookView{}, ErrValidation
	}
	doc, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		connected := doc.Connected()
		if connected == nil {
			return ErrSecondBrainNotConnected
		}
		if connected.LastLook == nil || connected.LastLook.ID != lookID {
			return ErrSecondBrainLookNotFound
		}
		if connected.LastLook.ActedAt == nil {
			at := s.deps.Now().UTC()
			connected.LastLook.ActedAt = &at
		}
		return nil
	})
	if err != nil {
		return SecondBrainLookView{}, err
	}
	return s.lookView(userID, doc.Connected()), nil
}

// Disconnect removes the connection at ifVersion (FR37). The follow-ups it
// created stay.
func (s *SecondBrainService) Disconnect(ctx context.Context, userID, requestID string, ifVersion int64) (SecondBrainView, error) {
	if !secondbrain.ValidRequestID(requestID) || ifVersion < 0 {
		return SecondBrainView{}, ErrValidation
	}
	current, err := s.read(ctx, userID)
	if err != nil {
		return SecondBrainView{}, err
	}
	if _, replayed, err := current.Replay(requestID, secondBrainActionDisconnect); err != nil || replayed {
		return s.view(current), err
	}
	var removed secondbrain.Source
	doc, err := s.store.Update(ctx, userID, ifVersion, func(doc *SecondBrainDocument) error {
		if connected := doc.Connected(); connected != nil {
			removed = *connected
		}
		if err := doc.Disconnect(s.deps.Now()); err != nil {
			if errors.Is(err, secondbrain.ErrNotConnected) {
				return ErrSecondBrainNotConnected
			}
			return err
		}
		doc.Remember(secondbrain.Receipt{RequestID: requestID, Action: secondBrainActionDisconnect, At: s.deps.Now()})
		return nil
	})
	if err != nil {
		return SecondBrainView{}, err
	}
	for _, fn := range s.onDisconnected {
		fn(ctx, userID, removed)
	}
	return s.view(doc), nil
}

func (s *SecondBrainService) read(ctx context.Context, userID string) (SecondBrainDocument, error) {
	if s == nil || s.store == nil {
		return SecondBrainDocument{}, ErrSecondBrainUnavailable
	}
	return s.store.Read(ctx, userID)
}

func (s *SecondBrainService) mutate(ctx context.Context, userID string, mutate func(*SecondBrainDocument) error) (SecondBrainDocument, error) {
	if s == nil || s.store == nil {
		return SecondBrainDocument{}, ErrSecondBrainUnavailable
	}
	return s.store.Mutate(ctx, userID, mutate)
}

func (s *SecondBrainService) runningLook(requestID string) secondbrain.Look {
	return secondbrain.Look{ID: s.deps.NewID(), State: secondbrain.LookRunning, RequestID: requestID, StartedAt: s.deps.Now().UTC()}
}

func (s *SecondBrainService) isRunning(userID, lookID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[userID] == lookID
}

// startLook runs the look in the background and writes its result, unless
// the folder was disconnected or replaced, or a newer look took its place.
func (s *SecondBrainService) startLook(userID string, source secondbrain.Source, previous *secondbrain.Look, lookID, requestID string, setup *secondbrain.SetupCounts) {
	s.mu.Lock()
	s.running[userID] = lookID
	s.mu.Unlock()
	s.looks.Add(1)
	go func() {
		defer s.looks.Done()
		defer func() {
			s.mu.Lock()
			if s.running[userID] == lookID {
				delete(s.running, userID)
			}
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), secondBrainLookTimeout)
		defer cancel()
		var run secondbrain.LookRun
		if setup != nil {
			run = s.deps.Looker.RunSetup(ctx, source, lookID, requestID, *setup)
		} else {
			run = s.deps.Looker.Run(ctx, source, previous, lookID, requestID)
		}
		if note := run.HeadlineNote; note != "" && note != "no model" && note != "no open items" && note != "setup" {
			logger.Info("A look kept the rules-only headline", logger.Fields{"look_id": lookID, "reason": note})
		}
		_, err := s.store.Mutate(ctx, userID, func(doc *SecondBrainDocument) error {
			connected := doc.Connected()
			if connected == nil || connected.ID != source.ID || connected.Current == nil || connected.Current.ID != lookID {
				return errSecondBrainLookSuperseded
			}
			look := run.Look
			if look.State == secondbrain.LookFinished {
				// A triage that landed while this look ran is kept too.
				look.CarryMarks(connected.LastLook)
				connected.LastLook, connected.Current = &look, nil
			} else {
				connected.Current = &look
			}
			return nil
		})
		if err != nil {
			if !errors.Is(err, errSecondBrainLookSuperseded) {
				logger.Warn("A look at the notes could not be saved", logger.Fields{"look_id": lookID, "error": err.Error()})
			}
			return
		}
		// Items finished in the notes close their follow-ups (FR29). The notes
		// are never edited to match a follow-up closed in Ori.
		if closer, ok := s.deps.FollowUps.(secondbrain.FollowUpCloser); ok && run.Look.State == secondbrain.LookFinished && len(run.Gone) > 0 {
			if _, err := secondbrain.CompleteGone(ctx, closer, userID, run.Gone); err != nil {
				logger.Warn("Follow-ups done in the notes could not be closed", logger.Fields{"look_id": lookID, "error": err.Error()})
			}
		}
	}()
}

// lookView reports a source's look. A running look this process is not
// running was cut off by a restart; it reads as failed so the look never
// hangs (FR27).
func (s *SecondBrainService) lookView(userID string, connected *secondbrain.Source) SecondBrainLookView {
	if current := connected.Current; current != nil {
		if current.State == secondbrain.LookRunning && !s.isRunning(userID, current.ID) {
			stopped := *current
			finished := s.deps.Now().UTC()
			stopped.State, stopped.FinishedAt = secondbrain.LookFailed, &finished
			stopped.Failure = "The look stopped before it finished. Look again to try once more."
			return SecondBrainLookView{State: stopped.State, LookID: stopped.ID, Result: &stopped}
		}
		return SecondBrainLookView{State: current.State, LookID: current.ID, Result: current}
	}
	if last := connected.LastLook; last != nil {
		return SecondBrainLookView{State: last.State, LookID: last.ID, Result: last}
	}
	return SecondBrainLookView{State: SecondBrainLookNone}
}

func latestLook(source *secondbrain.Source) *secondbrain.Look {
	if source.Current != nil {
		return source.Current
	}
	return source.LastLook
}

func (s *SecondBrainService) view(doc SecondBrainDocument) SecondBrainView {
	view := SecondBrainView{
		Status:   SecondBrainNotConnected,
		Detected: []SecondBrainDetected{},
		Ask:      SecondBrainAskView{State: doc.Ask.State(), OfferAgain: doc.Ask.OfferAgain(s.deps.Now())},
		Version:  doc.Version,
	}
	if s.deps.Vaults != nil {
		for _, vault := range s.deps.Vaults.Vaults() {
			view.Detected = append(view.Detected, SecondBrainDetected{
				ID: vault.ID, Name: vault.Name, Parent: filepath.Base(filepath.Dir(vault.Path)), Path: vault.Path,
			})
		}
	}
	connected := doc.Connected()
	if connected == nil {
		if parent := s.defaultParent(); parent != "" {
			view.SetupDefault = &SecondBrainSetupDefault{
				ParentName: filepath.Base(parent), Path: filepath.Join(parent, secondbrain.DefaultFolderName),
			}
		}
		return view
	}
	view.Status = SecondBrainConnected
	if info, err := os.Stat(connected.Path); err != nil || !info.IsDir() {
		view.Status = SecondBrainMissing
	}
	view.Source = &SecondBrainSourceView{
		ID: connected.ID, Kind: connected.Kind, Name: connected.Name, Path: connected.Path, ConnectedAt: connected.ConnectedAt,
		Origin: connected.Origin,
	}
	if view.Source.Origin == "" {
		view.Source.Origin = secondbrain.OriginConnected
	}
	if connected.CreatedByOri() {
		view.Source.OpenLink = secondbrain.ObsidianPathLink(connected.Path)
	}
	if last := connected.LastLook; last != nil {
		summary := &SecondBrainLookSummary{
			ID: last.ID, OpenItems: last.Counts.OpenItems, Headline: last.Headline, Acted: last.ActedAt != nil,
			People: len(last.People), Stale: len(last.Stale), Decisions: len(last.Decisions),
		}
		for _, item := range last.Items {
			if item.ActedAs != "" {
				summary.Handled++
			}
		}
		if last.FinishedAt != nil {
			summary.At = *last.FinishedAt
		}
		view.LastLook = summary
	}
	return view
}
