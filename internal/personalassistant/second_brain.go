package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	FollowUps   secondbrain.FollowUps
	Looker      secondbrain.Looker
	Now         func() time.Time
	NewID       func() string
}

// SecondBrainService connects the user's notes to the HQ and looks at them.
// The vault is read only here (FR42).
type SecondBrainService struct {
	store *SecondBrainStore
	deps  SecondBrainDeps

	looks   sync.WaitGroup
	mu      sync.Mutex
	running map[string]string // user id → the look this process is running

	// Hooks for the mission and the learned fact, bound at startup.
	onConnected    []func(ctx context.Context, userID string, source secondbrain.Source)
	onDisconnected []func(ctx context.Context, userID string, source secondbrain.Source)
	onDeclined     []func(ctx context.Context, userID string)
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
	return &SecondBrainService{store: store, deps: deps, running: map[string]string{}}
}

// Wait blocks until every look this service started has been written.
func (s *SecondBrainService) Wait() { s.looks.Wait() }

// SecondBrainView is GET /api/personal-assistant/second-brain.
type SecondBrainView struct {
	Status   string                  `json:"status"`
	Detected []SecondBrainDetected   `json:"detected"`
	Source   *SecondBrainSourceView  `json:"source,omitempty"`
	LastLook *SecondBrainLookSummary `json:"last_look,omitempty"`
	Ask      SecondBrainAskView      `json:"ask"`
	Version  int64                   `json:"version"`
	// LookID names the look a connect started (or the replayed one).
	LookID string `json:"look_id,omitempty"`
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
}

// SecondBrainLookSummary is the last finished look, for the slot and Home.
type SecondBrainLookSummary struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	OpenItems int       `json:"open_items"`
	Headline  string    `json:"headline"`
	Acted     bool      `json:"acted"`
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
	return s.view(doc), nil
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
		s.startLook(userID, *doc.Connected(), nil, started.ID, input.RequestID)
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
		s.startLook(userID, *connected, previous, started.ID, requestID)
	}
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
	items, err := chosenItems(connected.LastLook.Items, input)
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
	if _, err := s.mutate(ctx, userID, func(doc *SecondBrainDocument) error {
		if _, replayed, err := doc.Replay(input.RequestID, secondBrainActionFollowUps); err != nil || replayed {
			return err
		}
		if c := doc.Connected(); c != nil && c.LastLook != nil && c.LastLook.ID == lookID {
			at := s.deps.Now().UTC()
			c.LastLook.ActedAt = &at
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

func chosenItems(items []secondbrain.LookItem, input SecondBrainAcceptInput) ([]secondbrain.LookItem, error) {
	var chosen []secondbrain.LookItem
	if input.Suggested {
		for _, item := range items {
			if item.Suggested {
				chosen = append(chosen, item)
			}
		}
		return chosen, nil
	}
	byID := make(map[string]secondbrain.LookItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	seen := make(map[string]bool, len(input.ItemIDs))
	for _, id := range input.ItemIDs {
		item, ok := byID[id]
		if !ok {
			return nil, ErrValidation
		}
		if !seen[id] {
			seen[id] = true
			chosen = append(chosen, item)
		}
	}
	return chosen, nil
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
func (s *SecondBrainService) startLook(userID string, source secondbrain.Source, previous *secondbrain.Look, lookID, requestID string) {
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
		run := s.deps.Looker.Run(ctx, source, previous, lookID, requestID)
		if note := run.HeadlineNote; note != "" && note != "no model" && note != "no open items" {
			logger.Info("A look kept the rules-only headline", logger.Fields{"look_id": lookID, "reason": note})
		}
		_, err := s.store.Mutate(ctx, userID, func(doc *SecondBrainDocument) error {
			connected := doc.Connected()
			if connected == nil || connected.ID != source.ID || connected.Current == nil || connected.Current.ID != lookID {
				return errSecondBrainLookSuperseded
			}
			look := run.Look
			if look.State == secondbrain.LookFinished {
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
		return view
	}
	view.Status = SecondBrainConnected
	if info, err := os.Stat(connected.Path); err != nil || !info.IsDir() {
		view.Status = SecondBrainMissing
	}
	view.Source = &SecondBrainSourceView{
		ID: connected.ID, Kind: connected.Kind, Name: connected.Name, Path: connected.Path, ConnectedAt: connected.ConnectedAt,
	}
	if last := connected.LastLook; last != nil {
		summary := &SecondBrainLookSummary{
			ID: last.ID, OpenItems: last.Counts.OpenItems, Headline: last.Headline, Acted: last.ActedAt != nil,
		}
		if last.FinishedAt != nil {
			summary.At = *last.FinishedAt
		}
		view.LastLook = summary
	}
	return view
}
