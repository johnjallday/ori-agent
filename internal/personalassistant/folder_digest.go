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
	filePickerPrompt   = "Which file should Ori look at?"
)

var (
	ErrFolderWorkspaceNotFound  = errors.New("personal assistant: workspace not found")
	ErrFolderWorkspaceRefused   = errors.New("personal assistant: that workspace was not created for this offer")
	ErrFolderOutcomeUnavailable = errors.New("personal assistant: the folder outcome is unavailable")
	ErrFolderCreateFailed       = errors.New("personal assistant: the workspace could not be created")
	ErrFolderTidyFailed         = errors.New("personal assistant: File Janitor could not be set up for the folder")
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
	// UnavailableReason names why Available is false: platform.
	// FolderDialogUnavailablePlatform or FolderDialogUnavailableDesktopOff.
	// "" while the dialog is available.
	UnavailableReason() string
	Choose(ctx context.Context, prompt string) (path string, chosen bool, err error)
}

// FolderFilePicker is the optional file mode of the server-side chooser.
// The browser never supplies a path or file extension.
type FolderFilePicker interface {
	ChooseFile(ctx context.Context, prompt string) (path string, chosen bool, err error)
}

// The reasons a FolderPicker gives. They mirror the platform package's
// constants (a server test pins the two together) so this package stays free
// of platform code.
const (
	FolderDialogUnavailablePlatform   = "platform"
	FolderDialogUnavailableDesktopOff = "desktop_off"
)

// Chooser notes: the chooser must always say what can be done, never offer a
// list that may be empty (a sandboxed home has no Downloads, Documents, or
// Desktop, and its server has the folder dialog switched off).
const (
	folderChipsMissingNote    = "Downloads, Documents and Desktop are not under this home"
	folderDialogOffPhrase     = "the folder dialog is switched off in this session"
	folderDialogMacOnlyPhrase = "the folder dialog is only available on macOS"
	folderDialogAwayPhrase    = "the folder dialog is unavailable here"
)

// folderChooserNote is the line under the chips. Empty only when there is
// nothing to explain: chips to choose from and a dialog for anything else.
func folderChooserNote(chips int, pickerAvailable bool, reason string) string {
	if pickerAvailable {
		if chips > 0 {
			return ""
		}
		return folderChipsMissingNote + "; pick another folder."
	}
	phrase := folderDialogAwayPhrase
	switch reason {
	case FolderDialogUnavailableDesktopOff:
		phrase = folderDialogOffPhrase
	case FolderDialogUnavailablePlatform:
		phrase = folderDialogMacOnlyPhrase
	}
	if chips > 0 {
		return "Pick a folder from the list; " + phrase + "."
	}
	return folderChipsMissingNote + ", and " + phrase + "."
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

// FolderCreateRequest asks the host to set up a project workspace for an
// offer the way the assistant would: the folder's name, the shape's
// blueprint (or blank), the folder linked, the first task seeded. The card's
// confirm line is the user's yes; nothing here comes from the browser but
// that yes.
type FolderCreateRequest struct {
	UserID  string
	OfferID string
	// Name is the folder's base name, the workspace's name too.
	Name string
	// Path is the folder's canonical absolute path from the offer the server
	// holds.
	Path  string
	Shape folderdigest.Shape
	// Blueprint is the installed blueprint to create from; "" is the blank
	// workspace.
	Blueprint string
	// RequestID is the click's idempotency key.
	RequestID string
}

// FolderCreateResult is the workspace the host set up.
type FolderCreateResult struct {
	WorkspaceID string
	// Route is the workspace page to open.
	Route string
	// Created is false when a workspace made for this offer already existed
	// (a retried click) and was reused.
	Created bool
	Receipt []FolderReceiptRow
}

// FolderWorkspaceCreator is the host seam that creates the workspace for a
// project offer and links the folder to it. It must reuse a workspace already
// created for the same offer rather than make a second one.
type FolderWorkspaceCreator interface {
	CreateProjectWorkspace(ctx context.Context, req FolderCreateRequest) (FolderCreateResult, error)
}

// FolderTidyRequest asks the host to tidy a shown folder through the File
// Janitor engine (FR31).
type FolderTidyRequest struct {
	UserID  string
	OfferID string
	// Name is the folder's base name; Path its canonical absolute path from
	// the offer the server holds.
	Name string
	Path string
	// RequestID is the click's idempotency key, carried into the setup run.
	RequestID string
}

// FolderTidyResult is where the tidy landed.
type FolderTidyResult struct {
	WorkspaceID string
	// Route is the page to open: the first review batch, or the workspace
	// that already manages the folder.
	Route string
	// Existing reports that another File Janitor workspace already owned the
	// folder (or an ancestor), so nothing new was created (FR32).
	Existing bool
	// BatchID is the first review batch when a scan ran.
	BatchID string
	// Note is a one-line explanation shown on the offer when the outcome was
	// not a fresh first review.
	Note string
}

// FolderTidier runs the tidy outcome: the assistant-led File Janitor setup
// with the folder already chosen, ending at the first review batch.
type FolderTidier interface {
	TidyFolder(ctx context.Context, req FolderTidyRequest) (FolderTidyResult, error)
}

// FolderJourneyVerifier checks a canonical plugin-quest receipt against the
// offered folder, owner and blueprint. It never trusts a browser workspace ID.
type FolderJourneyVerifier interface {
	VerifiedProject(ctx context.Context, userID, runID, folderPath, blueprintID, integrationKey string, acceptedAfter time.Time) (FolderCreateResult, error)
}

// FolderHomeVerifier re-reads the canonical Group Template Home, not a
// browser assertion that an arbitrary workspace has been created.
type FolderHomeVerifier interface {
	VerifiedHome(ctx context.Context, userID, homeID, providerKey string, acceptedAfter time.Time) (FolderCreateResult, error)
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
	// AppInstalled is a silent installed-application lookup, not an offer
	// trigger. An unknown or unavailable detector reports false.
	AppInstalled func(ctx context.Context, appName string) bool
	// ProjectQuest returns a single installed, reviewed project-setup quest.
	// Nil or ambiguous means use the host's generated install quest first.
	ProjectQuest func(ctx context.Context, integrationKey, blueprintID string) (pluginID, questID string, ok bool)
	// HomeExists is an owner-scoped canonical station read. Unknown/unreadable
	// state fails closed rather than promising a duplicate Home.
	HomeExists func(ctx context.Context, userID, providerKey string) (exists bool, err error)
	// LegacyDeclined reads the pre-folder app-card answer once per domain.
	LegacyDeclined func(ctx context.Context, userID, domain string) (bool, error)
	// BlueprintAvailable reports whether a blueprint id is installed, so the
	// offer can fall back to the blank workspace and say so (FR28).
	BlueprintAvailable func(id string) bool
	// Linker attaches the folder to the created workspace (FR28, FR30).
	Linker FolderWorkspaceLinker
	// Creator sets a project workspace up on the assistant's behalf when the
	// user confirms the card's plan; the modal (Linker) stays the adjust path.
	Creator FolderWorkspaceCreator
	// Tidier runs the tidy outcome through the File Janitor engine (FR31).
	Tidier FolderTidier
	// Journey verifies the plugin's created project before resolving a card.
	Journey     FolderJourneyVerifier
	HomeJourney FolderHomeVerifier
	// OnResolved runs after a project outcome completes: the dossier
	// producer learns from the offer and reports whether the fact was saved
	// (FR35–FR39). Best-effort; its answer is recorded on the outcome.
	OnResolved func(ctx context.Context, userID string, offer FolderOffer) FolderLearning
	// OnOutcome runs after any outcome completes, for the mission that
	// observes it (FR42). Best-effort.
	OnOutcome func(ctx context.Context, userID string, offer FolderOffer)
	// MissionUnresolved is bound after progression and the assistant exist.
	// Nil fails closed: no automatic prompt without trustworthy mission state.
	MissionUnresolved func(id string) bool
}

// FolderLearning is what the dossier producer did with a resolved project
// offer.
type FolderLearning struct {
	// Remembered reports that the project fact was approved into the
	// dossier.
	Remembered bool
	// Note explains a fact that was not saved, in the assistant's voice.
	Note string
}

// SetOnResolved installs the dossier producer after the service exists;
// the producer is built later than the service in the host.
func (s *FolderDigestService) SetOnResolved(fn func(ctx context.Context, userID string, offer FolderOffer) FolderLearning) {
	if s != nil {
		s.deps.OnResolved = fn
	}
}

// SetOnOutcome installs the outcome observer (the mission hook).
func (s *FolderDigestService) SetOnOutcome(fn func(ctx context.Context, userID string, offer FolderOffer)) {
	if s != nil {
		s.deps.OnOutcome = fn
	}
}

// SetMissionUnresolved binds the progression view at Phase 22.7.
func (s *FolderDigestService) SetMissionUnresolved(fn func(id string) bool) {
	if s != nil {
		s.deps.MissionUnresolved = fn
	}
}

// RecentReceipts reads only settled offers with outcomes in the requested
// window. It returns the stored receipt, never a reconstructed claim about
// what a workspace may contain today. Tidy outcomes are included too.
func (s *FolderDigestService) RecentReceipts(ctx context.Context, userID string, since time.Time) ([]FolderOffer, error) {
	if s == nil || s.store == nil {
		return nil, ErrRepairNeeded
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := []FolderOffer{}
	for _, offer := range doc.Offers {
		if offer.Status == FolderOfferResolved && offer.Outcome != nil && offer.ResolvedAt != nil && !offer.ResolvedAt.Before(since) {
			result = append(result, offer)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].ResolvedAt.After(*result[j].ResolvedAt) })
	return result, nil
}

// ResolvedProjectOfferForKey finds the resolved project offer whose subject
// is the folder with the given key, for revalidating a fact learned from it
// (FR38). The folder's path is then read from the workspace the offer
// created, never from this record.
func (s *FolderDigestService) ResolvedProjectOfferForKey(ctx context.Context, userID, key string) (FolderOffer, bool, error) {
	if s == nil || s.store == nil {
		return FolderOffer{}, false, ErrRepairNeeded
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return FolderOffer{}, false, err
	}
	var found *FolderOffer
	for i := range doc.Offers {
		o := &doc.Offers[i]
		if o.Status != FolderOfferResolved || o.Subject.Key != key || o.Outcome == nil ||
			o.Outcome.Kind != FolderChoiceProject || strings.TrimSpace(o.Outcome.WorkspaceID) == "" {
			continue
		}
		if found == nil || o.CreatedAt.After(found.CreatedAt) {
			found = o
		}
	}
	if found == nil {
		return FolderOffer{}, false, nil
	}
	return *found, true, nil
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
	Offer               *FolderOfferView `json:"offer"`
	Chips               []FolderChip     `json:"chips"`
	PickerAvailable     bool             `json:"picker_available"`
	FilePickerAvailable bool             `json:"file_picker_available"`
	PickerNote          string           `json:"picker_note,omitempty"`
	Paused              bool             `json:"paused"`
	PromptFirstFolder   bool             `json:"prompt_first_folder"`
}

// FolderOfferView is an offer as the browser sees it: names and counts,
// never a path.
type FolderOfferView struct {
	ID            string                   `json:"id"`
	Status        FolderOfferStatus        `json:"status"`
	Verdict       string                   `json:"verdict"`
	Reason        string                   `json:"reason"`
	Partial       bool                     `json:"partial,omitempty"`
	Folder        string                   `json:"folder"`
	Chip          string                   `json:"chip,omitempty"`
	Subject       FolderSubjectView        `json:"subject"`
	ProjectsCount int                      `json:"projects_count,omitempty"`
	Portfolio     *FolderPortfolioEvidence `json:"portfolio,omitempty"`
	LooseFiles    int                      `json:"loose_files,omitempty"`
	LooseKinds    int                      `json:"loose_kinds,omitempty"`
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
	// CreateAvailable says the assistant can set the workspace up itself on
	// a yes (decide with create): name, blueprint, folder, first task. When
	// false the card's only project path is the Create Workspace modal.
	CreateAvailable bool `json:"create_available,omitempty"`
	// Capability is derived from the host table and the saved digest evidence;
	// it is never accepted as a client-supplied action or plugin declaration.
	Capability *FolderCapabilityView `json:"capability,omitempty"`
}

// FolderCapabilityView is the optional explanation on the existing digest
// card. The setup journey, not the scan, owns every consequence.
type FolderCapabilityView struct {
	Domain        string `json:"domain"`
	Recognized    string `json:"recognized"`
	Workspace     string `json:"workspace"`
	Integration   string `json:"integration"`
	Evidence      string `json:"evidence"`
	AppInstalled  bool   `json:"app_installed"`
	Question      string `json:"question"`
	AcceptLabel   string `json:"accept_label"`
	DeclineLabel  string `json:"decline_label"`
	SetupQuestID  string `json:"setup_quest_id"`
	SetupSource   string `json:"setup_source"`
	SetupPluginID string `json:"setup_plugin_id,omitempty"`
	Revived       bool   `json:"revived,omitempty"`
}

// FolderResolveInput reports the workspace the modal created for an offer.
type FolderResolveInput struct {
	WorkspaceID string
	HomeID      string
	RequestID   string
}

// FolderJourneyInput identifies a quest run, never a claimed workspace.
type FolderJourneyInput struct {
	RunID     string
	RequestID string
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
	// Create asks the assistant to set the project workspace up itself (the
	// card's confirmed plan) rather than wait for the Create Workspace modal.
	// Ignored for anything but a project yes.
	Create bool
}

func (s *FolderDigestService) now() time.Time { return s.deps.Now() }

// Current returns the pending or confirmed-in-progress offer with the chooser's
// chips. A confirmed journey survives page/server restarts; only when neither
// exists do we resurface a due "later" or the next queued candidate.
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
		pending = doc.Awaiting()
	}
	if pending == nil {
		pending, err = s.promote(ctx, userID, doc)
		if err != nil {
			return FolderDigestView{}, err
		}
	}
	view := FolderDigestView{Chips: s.availableChips(), Paused: binding.Paused}
	view.PromptFirstFolder = !binding.Paused && doc.FirstPromptShownAt == nil &&
		s.deps.MissionUnresolved != nil && s.deps.MissionUnresolved("pa-show-folder")
	reason := ""
	if s.deps.Picker != nil {
		view.PickerAvailable = s.deps.Picker.Available()
		_, supportsFiles := s.deps.Picker.(FolderFilePicker)
		view.FilePickerAvailable = view.PickerAvailable && supportsFiles
		reason = s.deps.Picker.UnavailableReason()
	}
	view.PickerNote = folderChooserNote(len(view.Chips), view.PickerAvailable, reason)
	if pending != nil {
		offer := s.view(ctx, *pending, binding.Paused)
		view.Offer = &offer
	}
	return view, nil
}

// MarkFirstPromptShown consumes the one-time hand-over on the server, not in
// browser storage. A retry is safe and returns the current chooser state.
func (s *FolderDigestService) MarkFirstPromptShown(ctx context.Context, userID string) (FolderDigestView, error) {
	if s == nil || s.store == nil {
		return FolderDigestView{}, ErrRepairNeeded
	}
	before, err := s.Current(ctx, userID)
	if err != nil || !before.PromptFirstFolder {
		return before, err
	}
	_, err = s.store.Mutate(ctx, userID, func(doc *FolderDigestDocument) error {
		if doc.FirstPromptShownAt == nil {
			now := s.now()
			doc.FirstPromptShownAt = &now
		}
		return nil
	})
	if err != nil {
		return FolderDigestView{}, err
	}
	return s.Current(ctx, userID)
}

// ClearFirstPrompt re-arms the hand-over after Reset Getting Started, without
// altering any offers, receipts, or workspaces. Pre-HQ resets have no sidecar.
func (s *FolderDigestService) ClearFirstPrompt(ctx context.Context, userID string) error {
	if s == nil || s.store == nil {
		return ErrRepairNeeded
	}
	_, err := s.store.Mutate(ctx, userID, func(doc *FolderDigestDocument) error {
		doc.FirstPromptShownAt = nil
		return nil
	})
	if errors.Is(err, ErrNeedsHQ) || errors.Is(err, ErrRepairNeeded) {
		return nil // No valid active HQ exists to hold the prompt yet.
	}
	return err
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

// ScanPickedFile digests the parent of the file selected on the server. The
// file is never opened, and its path is never sent to the browser or sidecar.
func (s *FolderDigestService) ScanPickedFile(ctx context.Context, userID string) (*FolderOfferView, error) {
	if s == nil || s.store == nil {
		return nil, ErrRepairNeeded
	}
	picker, ok := s.deps.Picker.(FolderFilePicker)
	if !ok || !s.deps.Picker.Available() {
		return nil, ErrFolderPickerUnavailable
	}
	if _, err := s.store.Binding(ctx, userID); err != nil {
		return nil, err
	}
	path, chosen, err := picker.ChooseFile(ctx, filePickerPrompt)
	if err != nil {
		return nil, fmt.Errorf("personal assistant: file dialog: %w", err)
	}
	if !chosen {
		return nil, nil
	}
	info, err := os.Lstat(path) // #nosec G304 G703 -- path comes from the local server-side native file dialog, not HTTP
	if err != nil || !info.Mode().IsRegular() {
		return nil, &FolderRootError{Message: "That file is unavailable. Choose a regular file."}
	}
	offer, err := s.scanSelectedRoot(ctx, userID, filepath.Dir(path), "", filepath.Base(path))
	if err != nil {
		return nil, err
	}
	return &offer, nil
}

func (s *FolderDigestService) scanRoot(ctx context.Context, userID, raw, chip string) (FolderOfferView, error) {
	return s.scanSelectedRoot(ctx, userID, raw, chip, "")
}

func (s *FolderDigestService) scanSelectedRoot(ctx context.Context, userID, raw, chip, pickedFile string) (FolderOfferView, error) {
	root, err := s.deps.ValidateRoot(raw)
	if err != nil {
		return FolderOfferView{}, err
	}
	// Recheck inside the canonical parent after root validation: a symlink
	// swap between dialog and scan must not redirect the chosen file.
	if pickedFile != "" {
		info, statErr := os.Lstat(filepath.Join(root, pickedFile)) // #nosec G304 G703 -- root is canonical and picker supplies only a basename
		if statErr != nil || !info.Mode().IsRegular() {
			return FolderOfferView{}, &FolderRootError{Message: "That file is unavailable. Choose a regular file."}
		}
	}
	binding, err := s.store.Binding(ctx, userID)
	if err != nil {
		return FolderOfferView{}, err
	}
	// Native picker paths intentionally are not persisted. After a restart the
	// user can re-pick the same canonical root to resume a confirmed journey;
	// do not replace that pending offer or silently adopt a different folder.
	if chip == "" {
		doc, readErr := s.store.Read(ctx, userID)
		if readErr != nil {
			return FolderOfferView{}, readErr
		}
		for _, prior := range doc.Offers {
			if prior.FolderKey == FolderKey(root) && prior.Status == FolderOfferAwaitingOutcome {
				s.rememberPath(prior.ID, root)
				view := s.view(ctx, prior, binding.Paused)
				return view, nil
			}
		}
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
	fileShape := folderdigest.Shape("")
	if pickedFile != "" {
		verdict, fileShape = fileVerdict(verdict, pickedFile, now)
	}
	offer := buildFolderOffer(verdict, result, chip, now, s.deps.NewID())
	// A whole collection is one Home question, not the first of seven
	// per-project questions. An existing or unreadable Home suppresses it.
	if pickedFile == "" && verdict.Portfolio != nil {
		row, found := folderdigest.CapabilityForShape(verdict.Portfolio.Shape)
		if found && row.Offer != nil && row.Offer.HomeProviderKey != "" && s.deps.HomeExists != nil {
			exists, homeErr := s.deps.HomeExists(ctx, userID, row.Offer.HomeProviderKey)
			if homeErr == nil {
				offer = buildPortfolioOffer(offer, verdict, row.Offer.HomeProviderKey)
				if exists {
					// Home already serves this shape: leave one plain folder
					// suggestion, never a Home or per-project capability offer.
					offer.Portfolio = nil
				}
			}
		}
	}
	if fileShape != "" && offer.Status == FolderOfferPending {
		offer.Subject.Shape = string(fileShape)
		offer.Subject.DominantExtension = strings.ToLower(filepath.Ext(pickedFile))
	}
	domain, evidence := folderOfferDomainClass(offer)
	legacyDeclined := false
	if domain != "" && doc.DeclineFor(domain) == nil && s.deps.LegacyDeclined != nil {
		legacyDeclined, err = s.deps.LegacyDeclined(ctx, userID, domain)
		if err != nil {
			return FolderOfferView{}, err // do not offer on an unreadable prior no
		}
	}

	updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		if legacyDeclined && d.DeclineFor(domain) == nil {
			d.DomainDeclines = append(d.DomainDeclines, FolderDomainDecline{
				Domain: domain, FirstEvidence: "single_project", Count: 1, At: now,
			})
		}
		if decline := d.DeclineFor(domain); decline != nil {
			offer.CapabilityRevived = decline.Count == 1 && decline.FirstEvidence == "single_project" && evidence == "portfolio"
			offer.CapabilitySuppressed = !offer.CapabilityRevived
		}
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
	return s.view(ctx, *stored, binding.Paused), nil
}

// fileVerdict makes a recognized picked file's parent the project instead
// of suggesting an unrelated subfolder. An unrecognized pick still uses an
// already-recognized parent; otherwise it follows the existing empty path.
func fileVerdict(v folderdigest.Verdict, pickedFile string, now time.Time) (folderdigest.Verdict, folderdigest.Shape) {
	extension := strings.ToLower(filepath.Ext(pickedFile))
	shape := folderdigest.ShapeFor(folderdigest.Candidate{DominantExtension: extension})
	if shape == "" {
		shape = folderdigest.ShapeForExtension(extension)
	}
	if shape != "" {
		root := v.Root
		v.Kind = folderdigest.KindProject
		v.Project = &root
		v.Projects = []folderdigest.Candidate{root}
		v.Reason = "Picked a " + strings.TrimPrefix(extension, ".") + " file in " + root.Name + "; " + folderdigest.DescribeCandidate(root, now)
		return v, shape
	}
	if v.Project != nil && v.Project.IsRoot {
		return v, ""
	}
	v.Kind = folderdigest.KindEmpty
	v.Project = nil
	v.Projects = nil
	v.Reason = "No recognized project in " + v.Root.Name
	return v, ""
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
	// A tidy, or a workspace the assistant sets up itself, runs its outcome
	// before anything is recorded, so a failed setup leaves the offer pending
	// and a retried click can try again (FR33).
	var tidy *FolderTidyResult
	var created *FolderCreateResult
	if input.Decision == FolderDecisionYes {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return FolderOfferView{}, err
		}
		offer := doc.Offer(offerID)
		if offer == nil {
			return FolderOfferView{}, ErrFolderOfferNotFound
		}
		if input.Create && isProjectCapabilityOffer(*offer) {
			return FolderOfferView{}, ErrFolderOutcomeUnavailable
		}
		if receipt := doc.Receipt(input.RequestID); receipt == nil && offer.Status == FolderOfferPending {
			if _, ok := s.rootPath(*offer); !ok {
				return FolderOfferView{}, ErrFolderPathLost
			}
			choice, err := folderOfferChoice(*offer, input.Choice)
			if err != nil {
				return FolderOfferView{}, err
			}
			switch {
			case choice == FolderChoiceTidy:
				result, err := s.runTidy(ctx, userID, *offer, input.RequestID)
				if err != nil {
					return FolderOfferView{}, err
				}
				tidy = &result
			case choice == FolderChoiceProject && input.Create:
				result, err := s.runCreate(ctx, userID, *offer, input.RequestID)
				if err != nil {
					return FolderOfferView{}, err
				}
				created = &result
			}
		}
	}

	now := s.now()
	var result FolderOffer
	resolvedNow := false
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
			if !offer.CapabilitySuppressed {
				if domain, evidence := folderOfferDomainClass(*offer); domain != "" {
					if decline := d.DeclineFor(domain); decline != nil {
						if decline.Count < 2 {
							decline.Count++
						}
						decline.At = now
					} else {
						d.DomainDeclines = append(d.DomainDeclines, FolderDomainDecline{Domain: domain, FirstEvidence: evidence, Count: 1, At: now})
					}
				}
			}
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
			if tidy != nil {
				// The tidy already ran: the offer is resolved in the same
				// request, with the File Janitor route to open.
				offer.Status = FolderOfferResolved
				offer.ResolvedAt = &decided
				offer.Outcome.WorkspaceID = tidy.WorkspaceID
				offer.Outcome.Route = tidy.Route
				offer.Outcome.Note = tidy.Note
				offer.Outcome.Existing = tidy.Existing
				resolvedNow = true
			}
			if created != nil {
				// The assistant set the workspace up: resolved here, as a
				// modal-created workspace is on resolve.
				offer.Status = FolderOfferResolved
				offer.ResolvedAt = &decided
				offer.Outcome.WorkspaceID = created.WorkspaceID
				offer.Outcome.Route = created.Route
				offer.Outcome.Blueprint, _, _ = s.blueprintForOffer(*offer)
				offer.Outcome.Receipt = append([]FolderReceiptRow(nil), created.Receipt...)
				resolvedNow = true
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
	if err == nil && resolvedNow {
		result = s.afterOutcome(ctx, userID, result)
	}
	return s.view(ctx, result, binding.Paused), nil
}

// runCreate has the host set the project workspace up for the offer's
// subject: the folder's name, the shape's installed blueprint, the folder
// linked, the first task seeded.
func (s *FolderDigestService) runCreate(ctx context.Context, userID string, offer FolderOffer, requestID string) (FolderCreateResult, error) {
	if s.deps.Creator == nil {
		return FolderCreateResult{}, ErrFolderOutcomeUnavailable
	}
	path, err := s.subjectPathOf(offer)
	if err != nil {
		return FolderCreateResult{}, err
	}
	blueprint, _, _ := s.blueprintForOffer(offer)
	result, err := s.deps.Creator.CreateProjectWorkspace(ctx, FolderCreateRequest{
		UserID: userID, OfferID: offer.ID, Name: offer.Subject.Name, Path: path,
		Shape: folderdigest.Shape(offer.Subject.Shape), Blueprint: blueprint, RequestID: requestID,
	})
	if err != nil {
		return FolderCreateResult{}, err
	}
	if strings.TrimSpace(result.WorkspaceID) == "" {
		return FolderCreateResult{}, ErrFolderCreateFailed
	}
	return result, nil
}

// runTidy hands the offer's folder to the File Janitor engine (FR31, FR32).
// The tidy's own subject is the root even for a mixed offer, whose named
// project is the queue's business.
func (s *FolderDigestService) runTidy(ctx context.Context, userID string, offer FolderOffer, requestID string) (FolderTidyResult, error) {
	if s.deps.Tidier == nil {
		return FolderTidyResult{}, ErrFolderOutcomeUnavailable
	}
	root, ok := s.rootPath(offer)
	if !ok {
		return FolderTidyResult{}, ErrFolderPathLost
	}
	result, err := s.deps.Tidier.TidyFolder(ctx, FolderTidyRequest{
		UserID: userID, OfferID: offer.ID, Name: offer.FolderName, Path: root, RequestID: requestID,
	})
	if err != nil {
		return FolderTidyResult{}, err
	}
	if strings.TrimSpace(result.WorkspaceID) == "" {
		return FolderTidyResult{}, ErrFolderOutcomeUnavailable
	}
	return result, nil
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

// ProjectSelectionPath issues a trusted project-picker selection only for a
// confirmed capability card, never for a browser-selected arbitrary path.
func (s *FolderDigestService) ProjectSelectionPath(ctx context.Context, userID, offerID string) (string, error) {
	if s == nil || s.store == nil {
		return "", ErrRepairNeeded
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return "", err
	}
	offer := doc.Offer(offerID)
	if offer == nil {
		return "", ErrFolderOfferNotFound
	}
	if offer.Status != FolderOfferAwaitingOutcome || offer.Choice != FolderChoiceProject || offer.DecidedAt == nil || offer.Portfolio != nil || offer.CapabilitySuppressed {
		return "", ErrFolderOfferDecided
	}
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if !ok || row.Offer == nil {
		return "", ErrFolderOfferDecided
	}
	return s.subjectPathOf(*offer)
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
		return s.view(ctx, *offer, binding.Paused), nil
	}
	if offer.Status == FolderOfferResolved {
		if offer.Outcome != nil && offer.Outcome.WorkspaceID == input.WorkspaceID {
			return s.view(ctx, *offer, binding.Paused), nil
		}
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	if offer.Status != FolderOfferAwaitingOutcome || offer.Choice != FolderChoiceProject {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	// An arbitrary workspace ID from the browser cannot prove that the
	// reviewed journey created it. The journey completion path will provide
	// a canonical receipt before the digest can resolve this offer.
	if isProjectCapabilityOffer(*offer) {
		return FolderOfferView{}, ErrFolderWorkspaceRefused
	}
	if s.deps.Linker == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	path, err := s.subjectPathOf(*offer)
	if err != nil {
		return FolderOfferView{}, err
	}
	blueprint, _, _ := s.blueprintForOffer(*offer)
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
	if err == nil {
		if stored := updated.Offer(offerID); stored != nil {
			resolved = s.afterOutcome(ctx, userID, *stored)
		}
	}
	return s.view(ctx, resolved, binding.Paused), nil
}

// ResolvePortfolio verifies a just-created independent Home against its
// reviewed provider and Group Template creation provenance before recording
// an outcome. No project integration or linked directory is inferred.
func (s *FolderDigestService) ResolvePortfolio(ctx context.Context, userID, offerID string, input FolderResolveInput) (FolderOfferView, error) {
	if s == nil || s.store == nil || s.deps.HomeJourney == nil || strings.TrimSpace(input.HomeID) == "" ||
		strings.TrimSpace(input.RequestID) == "" || len(input.RequestID) > folderRequestIDMax {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
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
		if receipt.OfferID != offerID || receipt.Action != "portfolio" {
			return FolderOfferView{}, ErrFolderOfferDecided
		}
		return s.view(ctx, *offer, binding.Paused), nil
	}
	if offer.Status != FolderOfferAwaitingOutcome || offer.Portfolio == nil || offer.CapabilitySuppressed || offer.DecidedAt == nil {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	verified, err := s.deps.HomeJourney.VerifiedHome(ctx, userID, input.HomeID, offer.Portfolio.ProviderKey, *offer.DecidedAt)
	if err != nil || verified.WorkspaceID != input.HomeID || !strings.HasPrefix(verified.Route, "/workspaces/") {
		return FolderOfferView{}, ErrFolderWorkspaceRefused
	}
	now := s.now()
	var resolved FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil || item.Status != FolderOfferAwaitingOutcome || item.Portfolio == nil || item.CapabilitySuppressed {
			return ErrFolderOfferDecided
		}
		item.Status, item.ResolvedAt = FolderOfferResolved, &now
		item.Outcome = &FolderOutcome{Kind: FolderChoiceHome, WorkspaceID: verified.WorkspaceID, Route: verified.Route}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: offerID, Action: "portfolio", At: now})
		pruneFolderDigest(d)
		resolved = *item
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	// A portfolio Home is not a project workspace and must not enter the
	// dossier's folder→project fact or first-project mission observers.
	return s.view(ctx, resolved, binding.Paused), nil
}

// PortfolioProvider returns the Home provider for a confirmed collection
// offer. A browser never gets to choose the plugin or create a Home on scan.
func (s *FolderDigestService) PortfolioProvider(ctx context.Context, userID, offerID string) (string, error) {
	if s == nil || s.store == nil {
		return "", ErrFolderOutcomeUnavailable
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return "", err
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Status != FolderOfferAwaitingOutcome || offer.Choice != FolderChoiceProject ||
		offer.DecidedAt == nil || offer.CapabilitySuppressed || offer.Portfolio == nil {
		return "", ErrFolderOfferDecided
	}
	return offer.Portfolio.ProviderKey, nil
}

// folderOfferDomainClass identifies only an actually eligible capability.
// A blank Ableton/Logic workspace has no music-domain decline to record.
func folderOfferDomainClass(offer FolderOffer) (domain, evidence string) {
	if offer.Portfolio != nil {
		row, ok := folderdigest.CapabilityForShape(folderdigest.Shape(offer.Portfolio.Shape))
		if ok && row.Offer != nil && row.Offer.HomeProviderKey == offer.Portfolio.ProviderKey {
			return row.Offer.Slug, "portfolio"
		}
		return "", ""
	}
	if offer.Subject.Kind != FolderChoiceProject || offer.Verdict != string(folderdigest.KindProject) {
		return "", ""
	}
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if ok {
		return row.Offer.Slug, "single_project"
	}
	return "", ""
}

// ResolveJourney completes a confirmed capability offer only when the host
// can re-read a ready plugin quest and prove it created a workspace connected
// to this exact shown folder. A replay or mismatched run cannot adopt a
// foreign or historical project, and failure leaves the offer resumable.
func (s *FolderDigestService) ResolveJourney(ctx context.Context, userID, offerID string, input FolderJourneyInput) (FolderOfferView, error) {
	if s == nil || s.store == nil || s.deps.Journey == nil {
		return FolderOfferView{}, ErrFolderOutcomeUnavailable
	}
	input.RunID = strings.TrimSpace(input.RunID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.RunID == "" || len(input.RunID) > 200 || input.RequestID == "" || len(input.RequestID) > folderRequestIDMax {
		return FolderOfferView{}, fmt.Errorf("%w: journey request", ErrValidation)
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
		if receipt.OfferID != offerID || receipt.Action != "journey" {
			return FolderOfferView{}, ErrFolderOfferDecided
		}
		return s.view(ctx, *offer, binding.Paused), nil
	}
	if offer.Status != FolderOfferAwaitingOutcome || offer.Choice != FolderChoiceProject || offer.DecidedAt == nil || offer.Portfolio != nil {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	if !ok {
		return FolderOfferView{}, ErrFolderOfferDecided
	}
	path, err := s.subjectPathOf(*offer)
	if err != nil {
		return FolderOfferView{}, err
	}
	verified, err := s.deps.Journey.VerifiedProject(ctx, userID, input.RunID, path, row.Blueprint.BlueprintID, row.Offer.IntegrationKey, *offer.DecidedAt)
	if err != nil || verified.WorkspaceID == "" || !strings.HasPrefix(verified.Route, "/workspaces/") {
		return FolderOfferView{}, ErrFolderWorkspaceRefused
	}
	now := s.now()
	var resolved FolderOffer
	_, err = s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
		item := d.Offer(offerID)
		if item == nil || item.Status != FolderOfferAwaitingOutcome || item.Choice != FolderChoiceProject {
			return ErrFolderOfferDecided
		}
		item.Status = FolderOfferResolved
		item.ResolvedAt = &now
		item.Outcome = &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: verified.WorkspaceID, Route: verified.Route, Blueprint: row.Blueprint.BlueprintID}
		d.Receipts = append(d.Receipts, FolderReceipt{RequestID: input.RequestID, OfferID: offerID, Action: "journey", At: now})
		pruneFolderDigest(d)
		resolved = *item
		return nil
	})
	if err != nil {
		return FolderOfferView{}, err
	}
	resolved = s.afterOutcome(ctx, userID, resolved)
	return s.view(ctx, resolved, binding.Paused), nil
}

// afterOutcome runs the best-effort observers of a completed outcome: the
// dossier producer for a project (whose answer is recorded on the offer)
// and the mission hook for any outcome.
func (s *FolderDigestService) afterOutcome(ctx context.Context, userID string, offer FolderOffer) FolderOffer {
	if s.deps.OnResolved != nil && offer.Outcome != nil && offer.Outcome.Kind == FolderChoiceProject {
		learning := s.deps.OnResolved(ctx, userID, offer)
		updated, err := s.store.Mutate(ctx, userID, func(d *FolderDigestDocument) error {
			o := d.Offer(offer.ID)
			if o == nil || o.Outcome == nil {
				return errFolderReplay
			}
			o.Outcome.Remembered = learning.Remembered
			if learning.Note != "" {
				o.Outcome.Note = learning.Note
			}
			return nil
		})
		if err == nil {
			if stored := updated.Offer(offer.ID); stored != nil {
				offer = *stored
			}
		}
	}
	if s.deps.OnOutcome != nil {
		s.deps.OnOutcome(ctx, userID, offer)
	}
	return offer
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

func (s *FolderDigestService) blueprintForOffer(offer FolderOffer) (id, label, note string) {
	if offer.CapabilitySuppressed {
		return "", "", ""
	}
	return s.blueprintForSubject(offer.Subject)
}

// blueprintForSubject prevents an unrelated audio project from silently
// acquiring the wrong tool-specific blueprint. Non-audio shapes keep the existing
// installed-blueprint/blank fallback.
func (s *FolderDigestService) blueprintForSubject(subject FolderCandidateRecord) (id, label, note string) {
	if subject.Shape == string(folderdigest.ShapeAudio) {
		if _, ok := folderdigest.ProjectCapabilityFor(folderdigest.ShapeAudio, subject.MarkerName, subject.DominantExtension); !ok {
			return "", "", ""
		}
	}
	return s.blueprintFor(subject.Shape)
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

func isProjectCapabilityOffer(offer FolderOffer) bool {
	if offer.CapabilitySuppressed {
		return false
	}
	if offer.Portfolio != nil {
		return true
	}
	if offer.Verdict != string(folderdigest.KindProject) || offer.Subject.Kind != FolderChoiceProject {
		return false
	}
	_, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension)
	return ok
}

func (s *FolderDigestService) view(ctx context.Context, offer FolderOffer, paused bool) FolderOfferView {
	v := FolderOfferView{
		ID: offer.ID, Status: offer.Status, Verdict: offer.Verdict, Reason: offer.Reason, Partial: offer.Partial,
		Folder: offer.FolderName, Chip: offer.Chip,
		Subject: FolderSubjectView{
			Name: offer.Subject.Name, Kind: offer.Subject.Kind, Shape: offer.Subject.Shape,
			Marker: offer.Subject.Marker, IsRoot: offer.Subject.IsRoot,
		},
		ProjectsCount: offer.ProjectsCount, Portfolio: offer.Portfolio, LooseFiles: offer.LooseFiles, LooseKinds: offer.LooseKinds,
		Decision: offer.Decision, Choice: offer.Choice, Outcome: offer.Outcome,
	}
	if offer.Status == FolderOfferPending || offer.Status == FolderOfferAwaitingOutcome {
		_, ok := s.rootPath(offer)
		v.NeedsPick = !ok
	}
	v.Remember = folderOfferRemembers(offer, paused)
	switch folderdigest.Kind(offer.Verdict) {
	case folderdigest.KindProject, folderdigest.KindMixed, folderdigest.KindAmbiguous:
		v.Blueprint, v.BlueprintLabel, v.BlueprintNote = s.blueprintForOffer(offer)
		v.CreateAvailable = s.deps.Creator != nil
	}
	if offer.Portfolio != nil && !offer.CapabilitySuppressed {
		if row, ok := folderdigest.CapabilityForShape(folderdigest.Shape(offer.Portfolio.Shape)); ok && row.Offer != nil {
			v.CreateAvailable = false
			v.Blueprint, v.BlueprintLabel, v.BlueprintNote = "", "", ""
			v.Capability = &FolderCapabilityView{
				Revived: offer.CapabilityRevived,
				Domain:  row.Offer.Slug, Recognized: fmt.Sprintf("%d music projects", offer.Portfolio.Projects),
				Workspace: "Music Production Home", Integration: "Setup will install the reviewed " + row.Offer.HomeProviderName + " provider if needed; no project integration is installed for these folders.",
				Evidence: offer.Reason, Question: "Set up one Music Production Home for these projects?",
				AcceptLabel: "Yes, set up Music Production Home", DeclineLabel: row.Offer.OfferCopy.DeclineLabel,
				SetupSource: "portfolio", SetupQuestID: "home_" + row.Offer.HomeProviderKey,
			}
		}
	} else if offer.CapabilitySuppressed {
		v.Blueprint, v.BlueprintLabel, v.BlueprintNote = "", "", ""
	} else if offer.Verdict == string(folderdigest.KindProject) && offer.Subject.Kind == FolderChoiceProject {
		if row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(offer.Subject.Shape), offer.Subject.MarkerName, offer.Subject.DominantExtension); ok {
			tool, found := folderdigest.ToolForExtension(offer.Subject.DominantExtension)
			if offer.Subject.MarkerName != "" {
				if marker, matched := folderdigest.MatchMarker(offer.Subject.MarkerName, false); matched {
					tool, found = folderdigest.ToolForMarker(marker)
				}
			}
			recognized := row.Offer.DisplayName
			if found {
				recognized = tool.ToolName
			}
			installed := s.deps.AppInstalled != nil && s.deps.AppInstalled(ctx, row.Offer.IntegrationName)
			integration := row.Offer.IntegrationName + " is not installed here. Setup can install the Ori integration plugin; install " + row.Offer.IntegrationName + " separately before live connection."
			if installed {
				integration = "Setup will connect to " + row.Offer.IntegrationName + " through the Ori integration plugin, installing the plugin if needed."
			}
			v.CreateAvailable = false // only the reviewed journey may create this workspace
			v.Blueprint = row.Blueprint.BlueprintID
			v.BlueprintLabel = row.Blueprint.Label
			v.BlueprintNote = "" // no silent blank-workspace fallback on an offer
			v.Capability = &FolderCapabilityView{
				Revived: offer.CapabilityRevived,
				Domain:  row.Offer.Slug, Recognized: recognized,
				Workspace: row.Blueprint.Label + " workspace", Integration: integration,
				Evidence: offer.Subject.Reason, AppInstalled: installed,
				Question:     row.Offer.OfferCopy.Question,
				AcceptLabel:  row.Offer.OfferCopy.AcceptLabel,
				DeclineLabel: row.Offer.OfferCopy.DeclineLabel,
				SetupQuestID: "install_" + row.Offer.IntegrationKey,
				SetupSource:  "host",
			}
			if s.deps.ProjectQuest != nil {
				pluginID, questID, found := s.deps.ProjectQuest(ctx, row.Offer.IntegrationKey, row.Blueprint.BlueprintID)
				if found && pluginID != "" && questID != "" {
					v.Capability.SetupSource = "plugin"
					v.Capability.SetupPluginID = pluginID
					v.Capability.SetupQuestID = questID
				}
			}
		}
	}
	return v
}

// folderOfferRemembers says whether the card may promise to remember the
// project: not while paused (FR52), and not when the fact text would be
// refused by the memory validator (FR39).
func folderOfferRemembers(offer FolderOffer, paused bool) bool {
	if paused || offer.Portfolio != nil {
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

// buildPortfolioOffer makes the root the subject even when the scan would
// otherwise prioritize one subproject. No queue of per-project offers follows.
func buildPortfolioOffer(offer FolderOffer, verdict folderdigest.Verdict, providerKey string) FolderOffer {
	signal := verdict.Portfolio
	offer.Subject = folderCandidateRecord(verdict.Root, FolderChoiceProject,
		fmt.Sprintf("%d %s project folders", signal.Projects, signal.Shape))
	offer.Subject.Shape = string(signal.Shape)
	// The collection has no one project tool; don't let a dominant extension
	// subfolder turn an already-owned Home into another project offer.
	offer.Subject.MarkerName, offer.Subject.Marker, offer.Subject.DominantExtension = "", "", ""
	offer.Subject.IsRoot = true
	offer.Verdict = string(folderdigest.KindProject)
	offer.Reason = offer.Subject.Reason
	offer.ProjectsCount = signal.Projects
	offer.Portfolio = &FolderPortfolioEvidence{Shape: string(signal.Shape), Projects: signal.Projects, ProviderKey: providerKey}
	offer.Queue = nil
	return offer
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
		DominantExtension: c.DominantExtension,
		IsRoot:            c.IsRoot, RelPath: c.RelPath,
	}
	if c.Marker != nil {
		record.Marker = c.Marker.Label
		record.MarkerName = c.Marker.Name
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
