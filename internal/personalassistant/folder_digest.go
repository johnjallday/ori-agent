package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Timing behind offers (FR23, FR24). Starting points, expected to be tuned.
const (
	// FolderLaterDelay is how long "Later" hides an offer (decision 3 in the
	// PRD: a week, not tomorrow).
	FolderLaterDelay = 7 * 24 * time.Hour
	// FolderNextOfferDelay is how long after a decision the next candidate
	// from the same scan may become pending. An hour is past the sitting the
	// decision was made in, which keeps the assistant to one question at a
	// time (FR24).
	FolderNextOfferDelay = time.Hour
	// folderRequestIDMax bounds the client's idempotency key.
	folderRequestIDMax = 128
	// folderPickerPrompt is the native dialog's title.
	folderPickerPrompt = "Which folder should Ori look at?"
)

var (
	ErrFolderWorkspaceNotFound  = errors.New("personal assistant: workspace not found")
	ErrFolderWorkspaceRefused   = errors.New("personal assistant: that workspace was not created for this offer")
	ErrFolderOutcomeUnavailable = errors.New("personal assistant: the folder outcome is unavailable")
	ErrFolderChipUnknown        = errors.New("personal assistant: unknown folder")
	ErrFolderChipMissing        = errors.New("personal assistant: that folder is not on this computer")
	ErrFolderScanBusy           = errors.New("personal assistant: a folder scan is already running")
	ErrFolderPickerUnavailable  = errors.New("personal assistant: the folder dialog is unavailable here")
	ErrFolderOfferNotFound      = errors.New("personal assistant: folder offer not found")
	ErrFolderOfferDecided       = errors.New("personal assistant: folder offer is no longer pending")
	ErrFolderOfferChoice        = errors.New("personal assistant: this offer needs a choice")
	ErrFolderPathLost           = errors.New("personal assistant: pick the folder again")
	errFolderReplay             = errors.New("personal assistant: replayed folder request")
)

// FolderRootError carries the message shown when a folder fails the root
// rules (FR7). The message is the same repair copy File Janitor uses.
type FolderRootError struct {
	Message string
}

func (e *FolderRootError) Error() string { return e.Message }

// FolderChip is a known folder the chooser offers by identifier (FR4).
type FolderChip struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// folderChips is the fixed chip set (decision 2 in the PRD). Each resolves
// to a folder under the user's home on the server; the browser only ever
// names the identifier.
var folderChips = []struct {
	ID, Label, Dir string
}{
	{ID: "downloads", Label: "Downloads", Dir: "Downloads"},
	{ID: "documents", Label: "Documents", Dir: "Documents"},
	{ID: "desktop", Label: "Desktop", Dir: "Desktop"},
}

// FolderPicker opens the native folder dialog on the server (FR5).
type FolderPicker interface {
	Available() bool
	Choose(ctx context.Context, prompt string) (path string, chosen bool, err error)
}

// FolderLinkRequest asks the host to attach an offer's folder to the
// workspace the user just created for it (FR28).
type FolderLinkRequest struct {
	UserID      string
	WorkspaceID string
	OfferID     string
	// Name is the folder's base name, used as the linked directory's name.
	Name string
	// Path is the folder's canonical absolute path, taken from the offer the
	// server holds, never from the browser.
	Path  string
	Shape folderdigest.Shape
}

// FolderLinkResult is what linking produced.
type FolderLinkResult struct {
	// Route is the workspace page to open.
	Route string
	// DirectoryID is the linked directory reference.
	DirectoryID string
	// FirstTaskSeeded reports whether the shape's first task was added (FR30).
	FirstTaskSeeded bool
}

// FolderWorkspaceLinker is the host seam that verifies the workspace was
// created for the offer, attaches the folder as its primary linked directory,
// and seeds the first task. It must be idempotent for a workspace already
// linked to the same folder.
type FolderWorkspaceLinker interface {
	LinkFolder(ctx context.Context, req FolderLinkRequest) (FolderLinkResult, error)
}

// FolderDigestDeps are the seams the service is built over. Zero values
// take production defaults except ValidateRoot, which the server supplies
// from File Janitor's root rules.
type FolderDigestDeps struct {
	// ValidateRoot canonicalises a selection and refuses one that is too
	// broad (FR7). It returns *FolderRootError for a user-facing refusal.
	ValidateRoot func(raw string) (string, error)
	Picker       FolderPicker
	HomeDir      func() (string, error)
	Scan         func(root string) (folderdigest.Result, error)
	Now          func() time.Time
	NewID        func() string
	// BlueprintAvailable reports whether a blueprint id is installed, so the
	// offer can fall back to the blank workspace and say so (FR28).
	BlueprintAvailable func(id string) bool
	// Linker attaches the folder to the created workspace (FR28, FR30).
	Linker FolderWorkspaceLinker
	// OnResolved runs after a project outcome completes, for the dossier
	// producer and the mission that observe it. Best-effort.
	OnResolved func(ctx context.Context, userID string, offer FolderOffer)
}

// FolderDigestService turns "show me a folder" into one explained offer and
// records what the user answered.
type FolderDigestService struct {
	store *FolderDigestStore
	deps  FolderDigestDeps

	mu       sync.Mutex
	paths    map[string]string // offer id → canonical root; memory only (FR27)
	scanning map[string]bool   // user id → a scan is in flight
}

// NewFolderDigestService builds the service.
func NewFolderDigestService(store *FolderDigestStore, deps FolderDigestDeps) *FolderDigestService {
	if deps.HomeDir == nil {
		deps.HomeDir = os.UserHomeDir
	}
	if deps.Scan == nil {
		deps.Scan = func(root string) (folderdigest.Result, error) {
			return folderdigest.Scan(root, folderdigest.Options{})
		}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.ValidateRoot == nil {
		deps.ValidateRoot = func(string) (string, error) {
			return "", &FolderRootError{Message: "Choose a different folder."}
		}
	}
	return &FolderDigestService{
		store: store, deps: deps,
		paths: map[string]string{}, scanning: map[string]bool{},
	}
}

// FolderDigestView is what the chooser and offer card render.
type FolderDigestView struct {
	Offer           *FolderOfferView `json:"offer"`
	Chips           []FolderChip     `json:"chips"`
	PickerAvailable bool             `json:"picker_available"`
	PickerNote      string           `json:"picker_note,omitempty"`
	Paused          bool             `json:"paused"`
}

// FolderOfferView is an offer as the browser sees it: names and counts,
// never a path.
type FolderOfferView struct {
	ID            string            `json:"id"`
	Status        FolderOfferStatus `json:"status"`
	Verdict       string            `json:"verdict"`
	Reason        string            `json:"reason"`
	Partial       bool              `json:"partial,omitempty"`
	Folder        string            `json:"folder"`
	Chip          string            `json:"chip,omitempty"`
	Subject       FolderSubjectView `json:"subject"`
	ProjectsCount int               `json:"projects_count,omitempty"`
	LooseFiles    int               `json:"loose_files,omitempty"`
	LooseKinds    int               `json:"loose_kinds,omitempty"`
	// Remember says whether the card may promise "I will also remember"
	// (FR22, FR39, FR52).
	Remember bool `json:"remember"`
	// NeedsPick is set when the offer's folder path is no longer known
	// (a restart after a picker-chosen folder) and a yes needs the user to
	// pick it again.
	NeedsPick bool           `json:"needs_pick,omitempty"`
	Decision  string         `json:"decision,omitempty"`
	Choice    string         `json:"choice,omitempty"`
	Outcome   *FolderOutcome `json:"outcome,omitempty"`
	// Blueprint is the id the Create Workspace modal should preselect for a
	// project outcome; empty means the blank workspace. BlueprintNote is the
	// one-line fallback explanation when the preferred blueprint is not
	// installed (FR28).
	Blueprint      string `json:"blueprint,omitempty"`
	BlueprintLabel string `json:"blueprint_label,omitempty"`
	BlueprintNote  string `json:"blueprint_note,omitempty"`
}

// FolderResolveInput reports the workspace the modal created for an offer.
type FolderResolveInput struct {
	WorkspaceID string
	RequestID   string
}

// FolderSubjectView names what the offer is about.
type FolderSubjectView struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Shape  string `json:"shape,omitempty"`
	Marker string `json:"marker,omitempty"`
	IsRoot bool   `json:"is_root,omitempty"`
}

// FolderDecisionInput is the user's answer to an offer.
type FolderDecisionInput struct {
	Decision  string
	Choice    string
	RequestID string
}

func (s *FolderDigestService) now() time.Time { return s.deps.Now() }

// Current returns the pending offer (resurfacing a due "later" or the next
// queued candidate first) with the chooser's chips.
func (s *FolderDigestService) Current(ctx context.Context, userID string) (FolderDigestView, error) {
	if s == nil || s.store == nil {
		return FolderDigestView{}, ErrRepairNeeded
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderDigestView{}, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderDigestView{}, err
	}
	pending := doc.Pending()
	if pending == nil {
		pending, err = s.promote(ctx, userID, doc)
		if err != nil {
			return FolderDigestView{}, err
		}
	}
	view := FolderDigestView{Chips: s.availableChips(), Paused: binding.Paused}
	view.PickerAvailable = s.deps.Picker != nil && s.deps.Picker.Available()
	if !view.PickerAvailable {
		view.PickerNote = "Pick a folder from the list for now."
	}
	if pending != nil {
		offer := s.view(*pending, binding.Paused)
		view.Offer = &offer
	}
	return view, nil
}

// ScanChip scans one of the known folders (FR4).
func (s *FolderDigestService) ScanChip(ctx context.Context, userID, chip string) (FolderOfferView, error) {
	if s == nil || s.store == nil {
		return FolderOfferView{}, ErrRepairNeeded
	}
	path, err := s.chipPath(chip)
	if err != nil {
		return FolderOfferView{}, err
	}
	return s.scanRoot(ctx, userID, path, chip)
}

// ScanPicked opens the native dialog and scans the chosen folder (FR5).
// A cancelled dialog returns nil with no error.
func (s *FolderDigestService) ScanPicked(ctx context.Context, userID string) (*FolderOfferView, error) {
	if s == nil || s.store == nil {
		return nil, ErrRepairNeeded
	}
	if s.deps.Picker == nil || !s.deps.Picker.Available() {
		return nil, ErrFolderPickerUnavailable
	}
	// The relationship is checked before the dialog opens, so a user who
	// is not hired never sees a Finder window appear.
	if _, err := s.store.Binding(ctx, userID); err != nil {
		return nil, err
	}
	path, chosen, err := s.deps.Picker.Choose(ctx, folderPickerPrompt)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: folder dialog: %w", err)
	}
	if !chosen {
		return nil, nil
	}
	offer, err := s.scanRoot(ctx, userID, path, "")
	if err != nil {
		return nil, err
	}
	return &offer, nil
}

func (s *FolderDigestService) scanRoot(ctx context.Context, userID, raw, chip string) (FolderOfferView, error) {
	root, err := s.deps.ValidateRoot(raw)
	if err != nil {
		return FolderOfferView{}, err
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}

	s.mu.Lock()
	if s.scanning[userID] {
		s.mu.Unlock()
		return FolderOfferView{}, ErrFolderScanBusy
	}
	s.scanning[userID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.scanning, userID)
		s.mu.Unlock()
	}()

	result, err := s.deps.Scan(root)
	if err != nil {
		switch {
		case errors.Is(err, folderdigest.ErrNotDirectory):
			return FolderOfferView{}, &FolderRootError{Message: "That is a file, not a folder. Choose a folder."}
		case os.IsPermission(err):
			return FolderOfferView{}, &FolderRootError{Message: "Ori is not allowed to look inside that folder. Choose a different folder."}
		default:
			return FolderOfferView{}, &FolderRootError{Message: "Ori could not open that folder. Choose a different folder."}
		}
	}
	// The scan itself never writes into the folder; the only write below is
	// the decision record under the HQ (FR50).
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	now := s.now()
	verdict := folderdigest.Exclude(result, now, func(c folderdigest.Candidate) bool {
		return doc.Tombstoned(FolderKey(c.Path))
	})
	offer := buildFolderOffer(verdict, result, chip, now, s.deps.NewID())

	updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		// Showing a second folder replaces the pending offer; the earlier
		// one becomes "later" (FR8).
		if pending := d.Pending(); pending != nil && offer.Status == FolderOfferPending {
			until := now.Add(FolderLaterDelay)
			pending.Status = FolderOfferLater
			pending.LaterUntil = &until
		}
		d.Offers = append(d.Offers, offer)
		pruneFolderDigest(d)
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	s.rememberPath(offer.ID, root)
	stored := updated.Offer(offer.ID)
	if stored == nil {
		stored = &offer
	}
	return s.view(*stored, binding.Paused), nil
}

// Decide records the user's answer (FR23, FR25, FR26, FR33).
func (s *FolderDigestService) Decide(ctx context.Context, userID, offerID string, input FolderDecisionInput) (FolderOfferView, error) {
	if s == nil || s.store == nil {
		return FolderOfferView{}, ErrRepairNeeded
	}
	input.Decision = strings.TrimSpace(input.Decision)
	input.Choice = strings.TrimSpace(input.Choice)
	input.RequestID = strings.TrimSpace(input.RequestID)
	switch input.Decision {
	case FolderDecisionYes, FolderDecisionNo, FolderDecisionLater:
	default:
		return FolderOfferView{}, fmt.Errorf("%w: decision", ErrValidation)
	}
	if input.RequestID == "" || len(input.RequestID) > folderRequestIDMax {
		return FolderOfferView{}, fmt.Errorf("%w: request id", ErrValidation)
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	if input.Decision == FolderDecisionYes {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return FolderOfferView{}, err
		}
		offer := doc.Offer(offerID)
		if offer == nil {
			return FolderOfferView{}, ErrFolderOfferNotFound
		}
		if receipt := doc.Receipt(input.RequestID); receipt == nil && offer.Status == FolderOfferPending {
			if _, ok := s.rootPath(*offer); !ok {
				return FolderOfferView{}, ErrFolderPathLost
			}
		}
	}

	now := s.now()
	var result FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		offer := d.Offer(offerID)
		if offer == nil {
			return ErrFolderOfferNotFound
		}
		if receipt := d.Receipt(input.RequestID); receipt != nil {
			if receipt.OfferID != offerID {
				return ErrFolderOfferDecided
			}
			result = *offer
			return errFolderReplay
		}
		if offer.Status != FolderOfferPending {
			return ErrFolderOfferDecided
		}
		choice := ""
		if input.Decision == FolderDecisionYes {
			choice, err = folderOfferChoice(*offer, input.Choice)
			if err != nil {
				return err
			}
		}
		decided := now
		offer.Decision = input.Decision
		offer.RequestID = input.RequestID
		offer.DecidedAt = &decided
		switch input.Decision {
		case FolderDecisionNo:
			offer.Status = FolderOfferDeclined
			if !d.Tombstoned(offer.Subject.Key) {
				d.Tombstones = append(d.Tombstones, FolderTombstone{Key: offer.Subject.Key, Name: offer.Subject.Name, CreatedAt: now})
			}
		case FolderDecisionLater:
			offer.Status = FolderOfferLater
			until := now.Add(FolderLaterDelay)
			offer.LaterUntil = &until
		case FolderDecisionYes:
			offer.Status = FolderOfferAwaitingOutcome
			offer.Choice = choice
			offer.Outcome = &FolderOutcome{Kind: choice}
			if err := applyFolderChoice(offer, choice); err != nil {
				return err
			}
		}
		d.Decisions = append(d.Decisions, FolderDecision{
			OfferID: offer.ID, FolderKey: offer.FolderKey, CandidateKey: offer.Subject.Key,
			Name: offer.Subject.Name, Verdict: offer.Verdict, Decision: input.Decision, Choice: choice, At: now,
		})
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: offer.ID, Action: "decide", At: now})
		pruneFolderDigest(d)
		result = *offer
		return nil
	})
	if err != nil && !errors.Is(err, errFolderReplay) {
		return FolderOfferView{}, err
	}
	return s.view(result, binding.Paused), nil
}

// folderOfferChoice resolves what a yes means for the offer's verdict.
func folderOfferChoice(offer FolderOffer, choice string) (string, error) {
	switch folderdigest.Kind(offer.Verdict) {
	case folderdigest.KindProject:
		return FolderChoiceProject, nil
	case folderdigest.KindDump:
		return FolderChoiceTidy, nil
	case folderdigest.KindMixed, folderdigest.KindAmbiguous:
		if choice == FolderChoiceProject || choice == FolderChoiceTidy {
			return choice, nil
		}
		return "", ErrFolderOfferChoice
	}
	return "", ErrFolderOfferDecided
}

// applyFolderChoice points the offer's subject at what the yes chose. In a
// mixed offer "tidy the loose files" makes the root the subject and sends the
// named project back to the queue for a later offer (FR25).
func applyFolderChoice(offer *FolderOffer, choice string) error {
	switch folderdigest.Kind(offer.Verdict) {
	case folderdigest.KindMixed:
		if choice != FolderChoiceTidy {
			return nil
		}
		var tidy *FolderCandidateRecord
		rest := make([]FolderCandidateRecord, 0, len(offer.Queue))
		for _, q := range offer.Queue {
			if q.Kind == FolderChoiceTidy && tidy == nil {
				entry := q
				tidy = &entry
				continue
			}
			rest = append(rest, q)
		}
		if tidy == nil {
			return ErrFolderOfferChoice
		}
		offer.Queue = append([]FolderCandidateRecord{offer.Subject}, rest...)
		offer.Subject = *tidy
	case folderdigest.KindAmbiguous:
		offer.Subject.Kind = choice
	}
	return nil
}

// SubjectPath returns the absolute path of what an offer is about, for the
// outcomes that need it. It is known only while the server that ran the
// scan is up, or when the offer came from a chip.
func (s *FolderDigestService) SubjectPath(ctx context.Context, userID, offerID string) (string, error) {
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return "", err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return "", ErrFolderOfferNotFound
	}
	return s.subjectPathOf(*offer)
}

func (s *FolderDigestService) subjectPathOf(offer FolderOffer) (string, error) {
	root, ok := s.rootPath(offer)
	if !ok {
		return "", ErrFolderPathLost
	}
	if offer.Subject.IsRoot || offer.Subject.RelPath == "" {
		return root, nil
	}
	return filepath.Join(root, offer.Subject.RelPath), nil
}

// Resolve completes a project outcome: the modal reported that a workspace
// was created for the offer, so the folder is attached to it as the primary
// linked directory and the offer is marked resolved (FR28, FR29, FR33). A
// replayed request id, or a second call naming the same workspace, returns
// the stored result without linking again.
func (s *FolderDigestService) Resolve(ctx context.Context, userID, offerID string, input FolderResolveInput) (FolderOfferView, error) {
	if s == nil || s.store == nil {
		return FolderOfferView{}, ErrRepairNeeded
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.WorkspaceID == "" || len(input.WorkspaceID) > 200 {
		return FolderOfferView{}, fmt.Errorf("%w: workspace id", ErrValidation)
	}
	if input.RequestID == "" || len(input.RequestID) > folderRequestIDMax {
		return FolderOfferView{}, fmt.Errorf("%w: request id", ErrValidation)
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return FolderOfferView{}, ErrFolderOfferNotFound
	}
	if receipt := doc.Receipt(input.RequestID); receipt != nil {
		if receipt.OfferID != offerID {
			return FolderOfferView{}, ErrFolderOfferDecided
		}
		return s.view(*offer, binding.Paused), nil
	}
	if offer.Status == FolderOfferResolved {
		if offer.Outcome != nil && offer.Outcome.WorkspaceID == input.WorkspaceID {
			return s.view(*offer, binding.Paused), nil
		}
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	if offer.Status != FolderOfferAwaitingOutcome || offer.Choice != FolderChoiceProject {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	if s.deps.Linker == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	path, err := s.subjectPathOf(*offer)
	if err != nil {
		return FolderOfferView{}, err
	}
	blueprint, _, _ := s.blueprintFor(offer.Subject.Shape)
	result, err := s.deps.Linker.LinkFolder(ctx, FolderLinkRequest{
		UserID: userID, WorkspaceID: input.WorkspaceID, OfferID: offerID,
		Name: offer.Subject.Name, Path: path, Shape: folderdigest.Shape(offer.Subject.Shape),
	})
	if err != nil {
		return FolderOfferView{}, err
	}

	now := s.now()
	var resolved FolderOffer
	updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		o := d.Offer(offerID)
		if o == nil {
			return ErrFolderOfferNotFound
		}
		if r := d.Receipt(input.RequestID); r != nil {
			resolved = *o
			return errFolderReplay
		}
		o.Status = FolderOfferResolved
		o.ResolvedAt = &now
		o.Outcome = &FolderOutcome{
			Kind: FolderChoiceProject, WorkspaceID: input.WorkspaceID,
			Route: result.Route, Blueprint: blueprint,
		}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: offerID, Action: "resolve", At: now})
		pruneFolderDigest(d)
		resolved = *o
		return nil
	})
	if err != nil && !errors.Is(err, errFolderReplay) {
		return FolderOfferView{}, err
	}
	if err == nil && s.deps.OnResolved != nil {
		if stored := updated.Offer(offerID); stored != nil {
			s.deps.OnResolved(ctx, userID, *stored)
		}
	}
	return s.view(resolved, binding.Paused), nil
}

// blueprintFor picks the blueprint a project outcome starts with (FR28): the
// shape's preferred blueprint when it is installed, else the blank workspace
// with a one-line note.
func (s *FolderDigestService) blueprintFor(shape string) (id, label, note string) {
	row, ok := folderdigest.BlueprintForShape(folderdigest.Shape(shape))
	if !ok {
		return "", "", ""
	}
	if s.deps.BlueprintAvailable == nil || !s.deps.BlueprintAvailable(row.BlueprintID) {
		return "", row.Label, "The " + row.Label + " blueprint is not installed, so this starts as a blank workspace."
	}
	return row.BlueprintID, row.Label, ""
}

// FolderFirstTask is the suggested first task for a workspace created from a
// folder of the given shape (FR30). It is a task the user runs; the
// assistant never starts it on its own.
func FolderFirstTask(shape folderdigest.Shape) (description, details string) {
	switch shape {
	case folderdigest.ShapeCode:
		return "List the open TODOs and the last thing changed",
			"Use workspace_directories to find the linked folder, then workspace_directory_list and workspace_directory_read to look through it. Report open TODO or FIXME notes and the most recently changed files."
	case folderdigest.ShapeAudio:
		return "Summarize the session's tracks and the most recent edits",
			"Use workspace_directories to find the linked folder, then workspace_directory_list and workspace_directory_read to read the session file. Name the tracks and what was edited most recently."
	case folderdigest.ShapeManuscript:
		return "Summarize the current draft and its open sections",
			"Use workspace_directories to find the linked folder, then workspace_directory_list and workspace_directory_read to read the manuscript. Summarize the draft as it stands and list the sections that are still open or thin."
	case folderdigest.ShapeCorpus:
		return "Build the sources index from the documents already in this folder: one line per document with a citation and a one-paragraph summary",
			"Use workspace_directories to find the linked folder, then workspace_directory_list and workspace_directory_read to read each document. Write one line per document with a citation, followed by a one-paragraph summary."
	default:
		return "Tell me what is in this folder and what looks most active",
			"Use workspace_directories to find the linked folder, then workspace_directory_list and workspace_directory_read to look through it. Describe what the folder holds and what looks most active."
	}
}

// promote surfaces the next question when nothing is pending: a "later"
// offer whose week is up, else the next queued candidate from the most
// recent decision once FolderNextOfferDelay has passed.
func (s *FolderDigestService) promote(ctx context.Context, userID string, doc FolderDigestDocument) (*FolderOffer, error) {
	now := s.now()
	var due *FolderOffer
	for i := range doc.Offers {
		o := &doc.Offers[i]
		if o.Status != FolderOfferLater || o.LaterUntil == nil || o.LaterUntil.After(now) {
			continue
		}
		if due == nil || o.LaterUntil.After(*due.LaterUntil) {
			due = o
		}
	}
	if due != nil {
		id := due.ID
		updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
			if d.Pending() != nil {
				return errFolderReplay
			}
			o := d.Offer(id)
			if o == nil || o.Status != FolderOfferLater {
				return errFolderReplay
			}
			o.Status = FolderOfferPending
			o.LaterUntil = nil
			return nil
		})
		if err != nil {
			if errors.Is(err, errFolderReplay) {
				return nil, nil
			}
			return nil, err
		}
		return updated.Offer(id), nil
	}

	var parent *FolderOffer
	for i := range doc.Offers {
		o := &doc.Offers[i]
		if len(o.Queue) == 0 || o.DecidedAt == nil || o.DecidedAt.Add(FolderNextOfferDelay).After(now) {
			continue
		}
		if o.Status != FolderOfferDeclined && o.Status != FolderOfferResolved {
			continue
		}
		if parent == nil || o.DecidedAt.After(*parent.DecidedAt) {
			parent = o
		}
	}
	if parent == nil {
		return nil, nil
	}
	if _, ok := s.rootPath(*parent); !ok {
		return nil, nil
	}
	parentID := parent.ID
	nextID := s.deps.NewID()
	updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		if d.Pending() != nil {
			return errFolderReplay
		}
		p := d.Offer(parentID)
		if p == nil {
			return errFolderReplay
		}
		var next *FolderCandidateRecord
		rest := make([]FolderCandidateRecord, 0, len(p.Queue))
		for _, q := range p.Queue {
			if next == nil && !d.Tombstoned(q.Key) {
				entry := q
				next = &entry
				continue
			}
			rest = append(rest, q)
		}
		p.Queue = nil
		if next == nil {
			return errFolderReplay
		}
		offer := FolderOffer{
			ID: nextID, Status: FolderOfferPending, Chip: p.Chip,
			FolderKey: p.FolderKey, FolderName: p.FolderName,
			Reason: next.Reason, Partial: p.Partial, ScannedAt: p.ScannedAt,
			Subject: *next, Queue: rest, CreatedAt: now,
		}
		if next.Kind == FolderChoiceTidy {
			offer.Verdict = string(folderdigest.KindDump)
			offer.LooseFiles = next.LooseFiles
			offer.LooseKinds = next.LooseKinds
		} else {
			offer.Verdict = string(folderdigest.KindProject)
			offer.ProjectsCount = 1
		}
		d.Offers = append(d.Offers, offer)
		pruneFolderDigest(d)
		return nil
	})
	if err != nil {
		if errors.Is(err, errFolderReplay) {
			return nil, nil
		}
		return nil, err
	}
	if root, ok := s.rootPath(*parent); ok {
		s.rememberPath(nextID, root)
	}
	return updated.Offer(nextID), nil
}

func (s *FolderDigestService) view(offer FolderOffer, paused bool) FolderOfferView {
	v := FolderOfferView{
		ID: offer.ID, Status: offer.Status, Verdict: offer.Verdict, Reason: offer.Reason, Partial: offer.Partial,
		Folder: offer.FolderName, Chip: offer.Chip,
		Subject: FolderSubjectView{
			Name: offer.Subject.Name, Kind: offer.Subject.Kind, Shape: offer.Subject.Shape,
			Marker: offer.Subject.Marker, IsRoot: offer.Subject.IsRoot,
		},
		ProjectsCount: offer.ProjectsCount, LooseFiles: offer.LooseFiles, LooseKinds: offer.LooseKinds,
		Decision: offer.Decision, Choice: offer.Choice, Outcome: offer.Outcome,
	}
	if offer.Status == FolderOfferPending || offer.Status == FolderOfferAwaitingOutcome {
		_, ok := s.rootPath(offer)
		v.NeedsPick = !ok
	}
	v.Remember = folderOfferRemembers(offer, paused)
	switch folderdigest.Kind(offer.Verdict) {
	case folderdigest.KindProject, folderdigest.KindMixed, folderdigest.KindAmbiguous:
		v.Blueprint, v.BlueprintLabel, v.BlueprintNote = s.blueprintFor(offer.Subject.Shape)
	}
	return v
}

// folderOfferRemembers says whether the card may promise to remember the
// project: not while paused (FR52), and not when the fact text would be
// refused by the memory validator (FR39).
func folderOfferRemembers(offer FolderOffer, paused bool) bool {
	if paused {
		return false
	}
	switch folderdigest.Kind(offer.Verdict) {
	case folderdigest.KindProject, folderdigest.KindMixed, folderdigest.KindAmbiguous:
	default:
		return false
	}
	_, err := workspace.ValidateMemoryText(FolderProjectFactText(offer.Subject.Name))
	return err == nil
}

// FolderProjectFactText is the reviewed fact a yes on a project saves
// (FR35).
func FolderProjectFactText(folderName string) string {
	return "You are working on a project in the folder " + strings.TrimSpace(folderName) + "."
}

func (s *FolderDigestService) chipPath(id string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, chip := range folderChips {
		if chip.ID != id {
			continue
		}
		home, err := s.deps.HomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", ErrFolderChipMissing
		}
		path := filepath.Join(home, chip.Dir)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return "", ErrFolderChipMissing
		}
		return path, nil
	}
	return "", ErrFolderChipUnknown
}

func (s *FolderDigestService) availableChips() []FolderChip {
	chips := make([]FolderChip, 0, len(folderChips))
	for _, chip := range folderChips {
		if _, err := s.chipPath(chip.ID); err == nil {
			chips = append(chips, FolderChip{ID: chip.ID, Label: chip.Label})
		}
	}
	return chips
}

func (s *FolderDigestService) rememberPath(offerID, root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths[offerID] = root
}

// rootPath recovers an offer's folder: from memory, or by re-resolving the
// chip it came from after a restart. A picker-chosen folder is known only to
// the process that scanned it.
func (s *FolderDigestService) rootPath(offer FolderOffer) (string, bool) {
	s.mu.Lock()
	root, ok := s.paths[offer.ID]
	s.mu.Unlock()
	if ok {
		return root, true
	}
	if offer.Chip == "" {
		return "", false
	}
	path, err := s.chipPath(offer.Chip)
	if err != nil {
		return "", false
	}
	// The stored key was taken from the canonical path, so the chip's folder
	// is canonicalised the same way before the two are compared.
	canonical, err := s.deps.ValidateRoot(path)
	if err != nil || FolderKey(canonical) != offer.FolderKey {
		return "", false
	}
	s.rememberPath(offer.ID, canonical)
	return canonical, true
}

// buildFolderOffer turns a verdict into the stored offer: the subject it
// asks about and the queue it keeps for later (FR24).
func buildFolderOffer(v folderdigest.Verdict, r folderdigest.Result, chip string, now time.Time, id string) FolderOffer {
	offer := FolderOffer{
		ID: id, Status: FolderOfferPending, Chip: chip,
		FolderKey: FolderKey(r.Root), FolderName: r.Name,
		Verdict: string(v.Kind), Reason: v.Reason, Partial: v.Partial,
		ScannedAt: now, CreatedAt: now,
		ProjectsCount: len(v.Projects), LooseFiles: v.LooseFiles, LooseKinds: v.LooseKinds,
	}
	tidy := folderCandidateRecord(v.Root, FolderChoiceTidy, v.LooseReason())
	tidy.LooseFiles, tidy.LooseKinds = v.LooseFiles, v.LooseKinds
	projects := make([]FolderCandidateRecord, 0, len(v.Projects))
	for _, c := range v.Projects {
		projects = append(projects, folderCandidateRecord(c, FolderChoiceProject, folderdigest.DescribeCandidate(c, now)))
	}
	switch v.Kind {
	case folderdigest.KindProject:
		offer.Subject = projects[0]
		offer.Queue = projects[1:]
	case folderdigest.KindMixed:
		offer.Subject = projects[0]
		offer.Queue = append(projects[1:], tidy)
	case folderdigest.KindDump:
		offer.Subject = tidy
	case folderdigest.KindAmbiguous:
		offer.Subject = folderCandidateRecord(v.Root, FolderChoiceProject, v.Reason)
	default:
		// Nothing to decide about an empty or declined folder.
		offer.Subject = tidy
		offer.Status = FolderOfferClosed
	}
	if len(offer.Queue) > folderDigestMaxQueue {
		offer.Queue = offer.Queue[:folderDigestMaxQueue]
	}
	return offer
}

func folderCandidateRecord(c folderdigest.Candidate, kind, reason string) FolderCandidateRecord {
	record := FolderCandidateRecord{
		Key: FolderKey(c.Path), Name: c.Name, Kind: kind,
		Shape: string(folderdigest.ShapeFor(c)), Reason: reason,
		IsRoot: c.IsRoot, RelPath: c.RelPath,
	}
	if c.Marker != nil {
		record.Marker = c.Marker.Label
	}
	return record
}

// pruneFolderDigest keeps the sidecar within its bounds, dropping settled
// offers oldest first and trimming receipts and decisions.
func pruneFolderDigest(d *FolderDigestDocument) {
	sort.SliceStable(d.Offers, func(i, j int) bool { return d.Offers[i].CreatedAt.Before(d.Offers[j].CreatedAt) })
	for len(d.Offers) > folderDigestMaxOffers {
		dropped := false
		for i, o := range d.Offers {
			switch o.Status {
			case FolderOfferResolved, FolderOfferDeclined, FolderOfferClosed:
				d.Offers = append(d.Offers[:i], d.Offers[i+1:]...)
				dropped = true
			}
			if dropped {
				break
			}
		}
		if !dropped {
			d.Offers = d.Offers[1:]
		}
	}
	if n := len(d.Decisions); n > folderDigestMaxDecisions {
		d.Decisions = d.Decisions[n-folderDigestMaxDecisions:]
	}
	if n := len(d.Receipts); n > folderDigestMaxReceipts {
		d.Receipts = d.Receipts[n-folderDigestMaxReceipts:]
	}
	if n := len(d.Tombstones); n > folderDigestMaxTombstones {
		d.Tombstones = d.Tombstones[n-folderDigestMaxTombstones:]
	}
}
