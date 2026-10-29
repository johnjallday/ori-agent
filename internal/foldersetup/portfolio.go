package foldersetup

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// The portfolio run sets up a whole collection of projects from one consent:
// the Home provider, the integration its projects need, the Home, the Home's
// required agents, the Home's library connected to the folder and listing its
// projects, and the standing consent for the projects' shared assistant. No
// project workspace is made: a project gets one when it is opened (D4).
//
// Like the single-project run it holds no state between calls. Every pass reads
// where things stand and performs only what is missing; every consequence is a
// review whose disclosure is compared with the confirmed plan before its commit.

// HomeFound is the owner's existing Home for the provider.
type HomeFound struct {
	ID string
	// CreatedAt is when the reviewed Home template created it (zero when the
	// Home was not made from one).
	CreatedAt time.Time
}

// HomeReview is what the Home review disclosed.
type HomeReview struct {
	Token      string
	TemplateID string
	Name       string
	// Reuse means the review would reuse an existing Home rather than create one.
	Reuse bool
}

// Homes finds, reviews and creates the collection's Home from the reviewed Home
// template, the same review and commit the Create Group modal uses.
type Homes interface {
	Find(ctx context.Context) (HomeFound, bool, error)
	Review(ctx context.Context) (HomeReview, error)
	Commit(ctx context.Context, review HomeReview, key string) (string, error)
}

// HomeRole is one required Home role still empty, and the name of the agent the
// run would create for it.
type HomeRole struct {
	RoleID string
	Name   string
}

// HomeStaffing fills the Home's required roles with new agents.
type HomeStaffing interface {
	Missing(ctx context.Context, homeID string) ([]HomeRole, error)
	// ModelReady says a new agent would have a model to chat with.
	ModelReady(ctx context.Context) bool
	Staff(ctx context.Context, homeID string, role HomeRole) error
}

// LibraryState is where the Home's library stands for the collection folder.
type LibraryState struct {
	Initialized bool
	// Linked is how many already-linked projects initializing would carry in.
	Linked int
	// RootID is the active library root that already covers the folder.
	RootID string
	// Scanned says that root has a finished listing; Listed and Partial describe it.
	Scanned bool
	Listed  int
	Partial bool
}

// InitReview, ConnectReview and LibraryScanReview are what the library's own
// reviews disclose. Folder is the server-held path of the reviewed root; it is
// compared with the collection folder and never shown.
type InitReview struct {
	Token  string
	Linked int
}

type ConnectReview struct {
	Token        string
	Folder       string
	IncludesScan bool
}

type LibraryScanReview struct {
	Token        string
	RootID       string
	MetadataOnly bool
	// ReadsSongDetails says the listing also reads each project's tempo, length
	// and track count (the Home's song-details consent is on).
	ReadsSongDetails bool
}

// ScanOutcome is what a listing found.
type ScanOutcome struct {
	Listed  int
	Partial bool
}

// Library is the Home's project library: initialize, connect the folder, list it.
type Library interface {
	State(ctx context.Context, homeID string) (LibraryState, error)
	ReviewInitialize(ctx context.Context, homeID string) (InitReview, error)
	CommitInitialize(ctx context.Context, homeID string, review InitReview, key string) error
	ReviewConnect(ctx context.Context, homeID string) (ConnectReview, error)
	CommitConnect(ctx context.Context, homeID string, review ConnectReview, key string) (string, error)
	ReviewScan(ctx context.Context, homeID, rootID string) (LibraryScanReview, error)
	CommitScan(ctx context.Context, homeID, rootID string, review LibraryScanReview, key string) (ScanOutcome, error)
}

// SongDetails records the Home's song-details consent: each scan may read
// each project's tempo, length and track count.
type SongDetails interface {
	Grant(ctx context.Context, homeID string) error
}

// Profile fills the Home's profile once the Home exists: it looks for the
// applications installed on this computer and, when grantTemplates, records
// the templates consent this card gave and lists them once. A plugin that
// cannot list templates is not an error; only a profile that could not be
// saved is.
type Profile interface {
	Setup(ctx context.Context, homeID string, grantTemplates bool) error
}

// Sharingis the Home's standing consent for the projects' shared assistant.
type Sharing interface {
	// State is "" (none), SharingOn or SharingOff.
	State(ctx context.Context, homeID string) (string, error)
	Grant(ctx context.Context, homeID string) error
}

// PortfolioReceiptFacts is what the run itself knows it did; everything else on
// the receipt is read back from canonical state.
type PortfolioReceiptFacts struct {
	HomeCreated bool
	AddedAgents []string
	Listed      ScanOutcome
	// Shared says the collection has projects the shared assistant can join.
	Shared bool
	// Opened is the song the Home's manager opened, for a single song's plan.
	Opened *OpenOutcome
}

// OpenInput is one listed project the run opens: the Home's catalog entry and,
// when its folder holds several project files, the one the user chose. The
// host's opener adds the rest (the key, the goal, the first task).
type OpenInput struct {
	HomeID       string
	EntryID      string
	SelectedFile string
}

// OpenOutcome is what opening one project did, as the library's own open
// service reports it. Created is false when an earlier pass already made the
// workspace (connected): this pass only finished what was missing.
type OpenOutcome struct {
	EntryID     string
	WorkspaceID string
	LinkID      string
	Route       string
	Created     bool
	// Staffing is how the project role was filled: added, joined, already, or
	// why not (none, off, not_shared, consent_stale, assistant_missing).
	Staffing  string
	AgentName string
	FirstTask bool
}

// OpenStop is an open that asks the user first (needs_choice with its
// candidates, consent_stale, needs_model) or that the row's own state refuses.
// Nothing was made.
type OpenStop struct {
	Reason     string
	Candidates []string
	Detail     string
}

func (s *OpenStop) Error() string { return "foldersetup: open stopped: " + s.Reason + ": " + s.Detail }

// ErrOpenNotListed means the Home's catalog holds no single entry for the
// project the plan promised to open.
var ErrOpenNotListed = errors.New("foldersetup: the project to open is not listed exactly once")

// Opener is the Home's manager opening a project its library lists, through
// the same reviewed open the library's Open row uses. It never creates a
// second workspace for a project that is already open.
type Opener interface {
	// Find is the catalog entry the root the run connected for folderName lists
	// as itself; ErrOpenNotListed when there is not exactly one.
	Find(ctx context.Context, homeID, folderName string) (string, error)
	Open(ctx context.Context, in OpenInput) (OpenOutcome, error)
}

// Receipts reads back, from canonical state, what the run made.
type Receipts interface {
	Receipt(ctx context.Context, homeID string, facts PortfolioReceiptFacts) ([]personalassistant.FolderReceiptRow, error)
}

// PortfolioRunner is the resumable portfolio run.
type PortfolioRunner struct {
	Providers Provider
	// Install is the host's install quest for the integration; nil when the plan
	// installs none.
	Install  Journey
	Homes    Homes
	Staffing HomeStaffing
	Library  Library
	Sharing  Sharing
	// SongDetails is required when the plan grants the song-details consent.
	SongDetails SongDetails
	// Profile is required when the plan showed the Home's profile line.
	Profile Profile
	// Opener is required when the plan opens projects (a single song's plan).
	Opener   Opener
	Receipts Receipts
	// Folder is the collection folder's server-held path.
	Folder   string
	Progress Progress
	NewKey   func() string
	// Fresh material fencing before every owner read and commit.
	ValidateDestination func(context.Context, string) error
}

// PortfolioConfig is what the user confirmed and what an earlier pass recorded.
type PortfolioConfig struct {
	Plan personalassistant.FolderSetupPlan
	// HomeID is the Home an earlier pass built or joined.
	HomeID string
	// AcceptedAfter is when the user pressed Set up: a Home made since then by
	// the reviewed template is this run's own (a pass that stopped right after
	// creating it), never a stranger's.
	AcceptedAfter time.Time
	// EntryName is the project file the user chose when an earlier pass stopped
	// at needs_choice.
	EntryName string
	// Open says the user pressed the song's Open. Without it a single song's
	// run stops at handoff_ready once its Home is ready and lists the song.
	Open bool
}

// ErrPortfolioNotWired means the host left out a seam the plan needs.
var ErrPortfolioNotWired = errors.New("foldersetup: the portfolio run is not wired")

// Run drives the collection's setup to the end or to the first stop.
func (p *PortfolioRunner) Run(ctx context.Context, cfg PortfolioConfig) (Result, error) {
	if p == nil || p.Homes == nil || p.Staffing == nil || p.Library == nil || p.Receipts == nil || p.Progress == nil ||
		(cfg.Plan.Intent.GrantsConsent && p.Sharing == nil) || (cfg.Plan.Intent.GrantsSongDetails && p.SongDetails == nil) ||
		(cfg.Plan.Intent.SetsProfile && p.Profile == nil) || (len(cfg.Plan.Intent.OpensProjects) > 0 && p.Opener == nil) {
		return Result{}, ErrPortfolioNotWired
	}
	state := &run{
		Runner:    &Runner{Install: p.Install, Providers: p.Providers, Progress: p.Progress, NewKey: p.NewKey},
		cfg:       Config{Plan: cfg.Plan},
		entryName: cfg.EntryName,
	}
	state.lines = append([]personalassistant.FolderPlanLine(nil), cfg.Plan.Lines...)
	for i := range state.lines {
		state.lines[i].State = personalassistant.FolderLineWaiting
	}
	portfolio := &portfolioRun{run: state, runner: p, cfg: cfg, homeID: cfg.HomeID}
	err := portfolio.drive(ctx)
	var halt *stop
	switch {
	case err == nil:
		portfolio.setKind(personalassistant.FolderPlanSongs, personalassistant.FolderLineDone)
		portfolio.setKind(personalassistant.FolderPlanAssistant, personalassistant.FolderLineDone)
		return portfolio.finish(ctx, personalassistant.FolderSetupDone, "", nil, nil)
	case ctx.Err() != nil:
		// Whichever step noticed, a cancelled run was interrupted, not refused.
		return portfolio.finish(ctx, personalassistant.FolderSetupStopped, personalassistant.FolderStopInterrupted, nil, err)
	case errors.As(err, &halt):
		var cause error
		if halt.detail != "" {
			cause = halt
		}
		return portfolio.finish(ctx, personalassistant.FolderSetupStopped, halt.reason, halt.candidates, cause)
	case errors.Is(err, ErrNeedsPick):
		return portfolio.finish(ctx, personalassistant.FolderSetupStopped, personalassistant.FolderStopNeedsPick, nil, nil)
	default:
		return portfolio.finish(ctx, personalassistant.FolderSetupStopped, personalassistant.FolderStopFailed, nil, err)
	}
}

type portfolioRun struct {
	*run
	runner  *PortfolioRunner
	cfg     PortfolioConfig
	homeID  string
	receipt []personalassistant.FolderReceiptRow
	// homeCreated and added are what this run made, for the receipt.
	homeCreated bool
	added       []string
	// opened is the song this pass opened (or finished opening).
	opened *OpenOutcome
}

// record writes the run's state with its Home and (once known) its receipt.
func (s *portfolioRun) record(ctx context.Context, status, reason string) error {
	return s.recordAsking(ctx, status, reason, nil)
}

// recordAsking also carries the files a needs_choice stop asks between, and
// the one chosen on an earlier pass.
func (s *portfolioRun) recordAsking(ctx context.Context, status, reason string, candidates []string) error {
	return s.Progress.Update(ctx, Update{
		Lines: append([]personalassistant.FolderPlanLine(nil), s.lines...), Status: status, StopReason: reason,
		HomeID: s.homeID, Receipt: append([]personalassistant.FolderReceiptRow(nil), s.receipt...),
		EntryName: s.entryName, EntryCandidates: candidates,
	})
}

func (s *portfolioRun) finish(ctx context.Context, status, reason string, candidates []string, cause error) (Result, error) {
	if status == personalassistant.FolderSetupStopped {
		// A stop that asks the user something leaves the line it was on waiting
		// for the answer; any other leaves it failed.
		next := personalassistant.FolderLineFailed
		switch reason {
		case personalassistant.FolderStopNeedsPick, personalassistant.FolderStopNeedsModel, personalassistant.FolderStopNeedsChoice,
			personalassistant.FolderStopConsentStale, personalassistant.FolderStopAssistantMissing, personalassistant.FolderStopHandoffReady:
			next = personalassistant.FolderLineWaiting
		}
		for i := range s.lines {
			if s.lines[i].State == personalassistant.FolderLineWorking {
				s.lines[i].State = next
			}
		}
	}
	err := s.recordAsking(context.WithoutCancel(ctx), status, reason, candidates)
	return Result{Status: status, StopReason: reason, Cause: cause}, err
}

// handoffAt is the index of a single song's handoff line, or len(lines): the
// lines before it are the Home's, the lines after it the opened song's.
func (s *portfolioRun) handoffAt() int {
	for i := range s.lines {
		if s.lines[i].Kind == personalassistant.FolderPlanHandoff {
			return i
		}
	}
	return len(s.lines)
}

// setKind moves the Home's lines of a kind; a single song's plan has a second
// agents line after its handoff, which belongs to the open.
func (s *portfolioRun) setKind(kind, state string) {
	for i := 0; i < s.handoffAt(); i++ {
		if s.lines[i].Kind == kind {
			s.lines[i].State = state
		}
	}
}

// setOpen moves the handoff line itself or the opened song's line of a kind.
func (s *portfolioRun) setOpen(kind, state string) {
	at := s.handoffAt()
	for i := at; i < len(s.lines); i++ {
		if s.lines[i].Kind == kind {
			s.lines[i].State = state
		}
	}
}

func (s *portfolioRun) working(ctx context.Context, kind string) error {
	s.setKind(kind, personalassistant.FolderLineWorking)
	return s.record(ctx, personalassistant.FolderSetupRunning, "")
}

func (s *portfolioRun) done(kind string) { s.setKind(kind, personalassistant.FolderLineDone) }

func (s *portfolioRun) drive(ctx context.Context) error {
	intent := s.cfg.Plan.Intent
	if err := s.record(ctx, personalassistant.FolderSetupRunning, ""); err != nil {
		return err
	}
	if s.Providers != nil && intent.Provider != "" {
		if err := s.installProvider(ctx, intent); err != nil {
			return err
		}
	} else {
		s.done(personalassistant.FolderPlanProvider)
	}
	if s.Install != nil && intent.Integration != "" {
		if err := s.installIntegration(ctx, intent); err != nil {
			return err
		}
	} else {
		s.done(personalassistant.FolderPlanIntegration)
	}
	if err := s.home(ctx); err != nil {
		return err
	}
	if err := s.staffHome(ctx); err != nil {
		return err
	}
	if err := s.profile(ctx); err != nil {
		return err
	}
	listed, err := s.library(ctx)
	if err != nil {
		return err
	}
	if len(s.cfg.Plan.Intent.OpensProjects) > 0 && !s.cfg.Open {
		// The Home is ready and lists the song. Opening it, and the shared
		// assistant's standing consent that comes with it, is the user's own
		// next press, on the Portfolio Manager's suggestion in the Home.
		if err := s.guard(ctx); err != nil {
			return err
		}
		return &stop{reason: personalassistant.FolderStopHandoffReady}
	}
	if err := s.share(ctx); err != nil {
		return err
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	if err := s.open(ctx); err != nil {
		return err
	}
	rows, err := s.runner.Receipts.Receipt(ctx, s.homeID, PortfolioReceiptFacts{
		HomeCreated: s.homeCreated || (s.cfg.Plan.Intent.CreatesHome && s.cfg.HomeID != ""), AddedAgents: s.added, Listed: listed,
		Shared: s.cfg.Plan.Intent.SharedProjects, Opened: s.opened,
	})
	if err != nil {
		return failed("could not read back what was set up: " + err.Error())
	}
	s.receipt = rows
	return nil
}

func (s *portfolioRun) guard(ctx context.Context) error {
	if expected := s.cfg.Plan.Destination; expected != nil && expected.Status == "existing" && s.homeID != "" && s.homeID != expected.WorkspaceID {
		return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the Home ID differs from the review"}
	}
	if s.runner.ValidateDestination != nil {
		if err := s.runner.ValidateDestination(ctx, s.homeID); err != nil {
			return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the Home destination is no longer available"}
		}
	}
	return nil
}

// profile fills the Home's profile after the Home exists and before the
// listing, when the plan showed that line. The templates consent is recorded
// only for a Home this plan created (in this pass or an earlier one). The
// step never stops the run: a profile that could not be saved leaves its line
// failed, and the owner fills it on the Home instead. Only a cancelled run, or
// a Home that is no longer the reviewed destination, ends here.
func (s *portfolioRun) profile(ctx context.Context) error {
	intent := s.cfg.Plan.Intent
	if !intent.SetsProfile {
		return nil
	}
	// The profile is written to the Home, so the reviewed destination is
	// checked first, as before every other write of this run.
	if err := s.guard(ctx); err != nil {
		return err
	}
	if err := s.working(ctx, personalassistant.FolderPlanProfile); err != nil {
		return err
	}
	created := s.homeCreated || (intent.CreatesHome && s.cfg.HomeID != "")
	if err := s.runner.Profile.Setup(ctx, s.homeID, intent.GrantsTemplates && created); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.setKind(personalassistant.FolderPlanProfile, personalassistant.FolderLineFailed)
		return nil
	}
	s.done(personalassistant.FolderPlanProfile)
	return nil
}

// home finds the Home the run built earlier, or builds it through the reviewed
// Home template exactly as the plan said: a Home is created only when the plan
// promised one, and an existing Home is used only when the plan said so (or it
// is this run's own, made after Set up was pressed).
func (s *portfolioRun) home(ctx context.Context) error {
	if err := s.guard(ctx); err != nil {
		return err
	}
	intent := s.cfg.Plan.Intent
	if s.homeID != "" {
		s.done(personalassistant.FolderPlanHome)
		return nil
	}
	if err := s.working(ctx, personalassistant.FolderPlanHome); err != nil {
		return err
	}
	found, exists, err := s.runner.Homes.Find(ctx)
	if err != nil {
		return failed("could not read the Home: " + err.Error())
	}
	if exists {
		if expected := s.cfg.Plan.Destination; expected != nil && (expected.Status == "new" || found.ID != expected.WorkspaceID) {
			return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "an unreceipted or different Home exists"}
		}
		ours := !found.CreatedAt.IsZero() && !s.cfg.AcceptedAfter.IsZero() && !found.CreatedAt.Before(s.cfg.AcceptedAfter)
		if intent.CreatesHome && !ours {
			return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "a Home already exists; the plan said one would be created"}
		}
		s.homeID, s.homeCreated = found.ID, intent.CreatesHome
		s.done(personalassistant.FolderPlanHome)
		return s.record(ctx, personalassistant.FolderSetupRunning, "")
	}
	if !intent.CreatesHome {
		return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the Home the plan joins is gone"}
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	review, err := s.runner.Homes.Review(ctx)
	if err != nil {
		return failed("the Home review failed: " + err.Error())
	}
	if review.Token == "" || review.Reuse || (intent.HomeTemplate != "" && review.TemplateID != intent.HomeTemplate) || (s.cfg.Plan.Destination != nil && review.Name != s.cfg.Plan.Destination.Name) {
		return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the Home review differs from the plan"}
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	homeID, err := s.runner.Homes.Commit(ctx, review, s.key())
	if err != nil || homeID == "" {
		return failed("the Home was not created")
	}
	s.homeID, s.homeCreated = homeID, true
	s.done(personalassistant.FolderPlanHome)
	return s.record(ctx, personalassistant.FolderSetupRunning, "")
}

// staffHome adds the Home's required agents the plan promised. A Home that has
// them already, or a plan that did not promise any, adds nobody.
func (s *portfolioRun) staffHome(ctx context.Context) error {
	if err := s.guard(ctx); err != nil {
		return err
	}
	if !s.cfg.Plan.Intent.StaffsHome {
		s.done(personalassistant.FolderPlanAgents)
		return nil
	}
	missing, err := s.runner.Staffing.Missing(ctx, s.homeID)
	if err != nil {
		return failed("could not read the Home's roles: " + err.Error())
	}
	if len(missing) == 0 {
		s.done(personalassistant.FolderPlanAgents)
		return nil
	}
	if err := s.working(ctx, personalassistant.FolderPlanAgents); err != nil {
		return err
	}
	// No agent is created without a model (needs_model leaves the line waiting).
	if !s.runner.Staffing.ModelReady(ctx) {
		return &stop{reason: personalassistant.FolderStopNeedsModel}
	}
	for _, role := range missing {
		if role.RoleID == "" || role.Name == "" {
			return failed("a required Home role has no agent name")
		}
		if err := s.guard(ctx); err != nil {
			return err
		}
		if err := s.runner.Staffing.Staff(ctx, s.homeID, role); err != nil {
			return failed("could not add " + role.Name + ": " + err.Error())
		}
		s.added = append(s.added, role.Name)
	}
	s.done(personalassistant.FolderPlanAgents)
	return s.record(ctx, personalassistant.FolderSetupRunning, "")
}

// library turns the Home's library on, connects the collection folder and lists
// it, each through the library's own review. Folder and scope are compared with
// the plan: only this folder, names and project files only, plus the three song
// facts when the plan's library line said so.
func (s *portfolioRun) library(ctx context.Context) (ScanOutcome, error) {
	lib := s.runner.Library
	// The song-details consent the library line disclosed is recorded on the
	// Home this run created before anything is listed, so the setup's own scan
	// reads the facts. A Home the run did not create never gets it.
	if s.cfg.Plan.Intent.GrantsSongDetails {
		if !s.cfg.Plan.Intent.CreatesHome {
			return ScanOutcome{}, &stop{reason: personalassistant.FolderStopPlanChanged, detail: "song details are granted only on a Home the plan creates"}
		}
		if err := s.guard(ctx); err != nil {
			return ScanOutcome{}, err
		}
		if err := s.runner.SongDetails.Grant(ctx, s.homeID); err != nil {
			return ScanOutcome{}, failed("the song-details consent was not recorded: " + err.Error())
		}
	}
	if err := s.guard(ctx); err != nil {
		return ScanOutcome{}, err
	}
	current, err := lib.State(ctx, s.homeID)
	if err != nil {
		return ScanOutcome{}, failed("could not read the Home's library: " + err.Error())
	}
	if current.RootID != "" && !s.cfg.Plan.Intent.CreatesHome && len(s.cfg.Plan.Intent.OpensProjects) > 0 {
		// OQ4: a root of this Home already covers the song; no second root is
		// connected and the song's own entry is opened from it.
		s.markAlreadyListed()
	}
	if current.Scanned && current.RootID != "" {
		s.done(personalassistant.FolderPlanLibrary)
		return ScanOutcome{Listed: current.Listed, Partial: current.Partial}, nil
	}
	if err := s.working(ctx, personalassistant.FolderPlanLibrary); err != nil {
		return ScanOutcome{}, err
	}
	if !current.Initialized {
		if err := s.guard(ctx); err != nil {
			return ScanOutcome{}, err
		}
		review, err := lib.ReviewInitialize(ctx, s.homeID)
		if err != nil {
			return ScanOutcome{}, failed("the library review failed: " + err.Error())
		}
		// A Home this run created has nothing to carry in.
		if review.Token == "" || (s.cfg.Plan.Intent.CreatesHome && review.Linked != 0) {
			return ScanOutcome{}, &stop{reason: personalassistant.FolderStopPlanChanged, detail: "turning the library on would carry in projects the plan did not mention"}
		}
		if err := s.guard(ctx); err != nil {
			return ScanOutcome{}, err
		}
		if err := lib.CommitInitialize(ctx, s.homeID, review, s.key()); err != nil {
			return ScanOutcome{}, failed("the library was not turned on: " + err.Error())
		}
	}
	rootID := current.RootID
	if rootID == "" {
		if err := s.guard(ctx); err != nil {
			return ScanOutcome{}, err
		}
		review, err := lib.ReviewConnect(ctx, s.homeID)
		if errors.Is(err, ErrNeedsPick) {
			return ScanOutcome{}, err
		}
		if err != nil {
			return ScanOutcome{}, failed("the folder review failed: " + err.Error())
		}
		if review.Token == "" || review.IncludesScan || s.runner.Folder == "" ||
			filepath.Clean(review.Folder) != filepath.Clean(s.runner.Folder) {
			return ScanOutcome{}, &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the folder review names another folder"}
		}
		if err := s.guard(ctx); err != nil {
			return ScanOutcome{}, err
		}
		rootID, err = lib.CommitConnect(ctx, s.homeID, review, s.key())
		if err != nil || rootID == "" {
			return ScanOutcome{}, failed("the folder was not connected")
		}
	}
	if err := s.guard(ctx); err != nil {
		return ScanOutcome{}, err
	}
	review, err := lib.ReviewScan(ctx, s.homeID, rootID)
	if err != nil {
		return ScanOutcome{}, failed("the listing review failed: " + err.Error())
	}
	if review.Token == "" || review.RootID != rootID || !review.MetadataOnly {
		return ScanOutcome{}, &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the listing review is not names and project files only"}
	}
	if review.ReadsSongDetails && !s.cfg.Plan.Intent.ReadsSongDetails {
		return ScanOutcome{}, &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the listing review reads song details the plan did not mention"}
	}
	if err := s.guard(ctx); err != nil {
		return ScanOutcome{}, err
	}
	listed, err := lib.CommitScan(ctx, s.homeID, rootID, review, s.key())
	if err != nil {
		return ScanOutcome{}, failed("the folder was not listed: " + err.Error())
	}
	s.done(personalassistant.FolderPlanLibrary)
	return listed, s.record(ctx, personalassistant.FolderSetupRunning, "")
}

// AlreadyListedDetail is the library line's detail when a root of the Home
// already lists the song.
const AlreadyListedDetail = "Already listed."

func (s *portfolioRun) markAlreadyListed() {
	for i := 0; i < s.handoffAt(); i++ {
		if s.lines[i].Kind == personalassistant.FolderPlanLibrary {
			s.lines[i].Detail = AlreadyListedDetail
		}
	}
}

// open is the Home's manager opening the song the plan promised, once the
// Home is ready, listed and fenced, through the library's own reviewed open.
// A pass after an earlier one made the workspace finishes what is missing and
// never makes a second one. A stop that asks the user leaves its lines waiting.
func (s *portfolioRun) open(ctx context.Context) error {
	opens := s.cfg.Plan.Intent.OpensProjects
	if len(opens) == 0 {
		return nil
	}
	if len(opens) != 1 {
		return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "a song's plan opens exactly one project"}
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	s.setOpen(personalassistant.FolderPlanHandoff, personalassistant.FolderLineWorking)
	s.setOpen(personalassistant.FolderPlanWorkspace, personalassistant.FolderLineWorking)
	if err := s.record(ctx, personalassistant.FolderSetupRunning, ""); err != nil {
		return err
	}
	entryID, err := s.runner.Opener.Find(ctx, s.homeID, opens[0])
	if errors.Is(err, ErrOpenNotListed) {
		return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the library does not list the song exactly once"}
	}
	if err != nil || entryID == "" {
		return failed("could not find the listed song: " + errText(err))
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	outcome, err := s.runner.Opener.Open(ctx, OpenInput{HomeID: s.homeID, EntryID: entryID, SelectedFile: s.entryName})
	var asked *OpenStop
	switch {
	case errors.As(err, &asked):
		switch asked.Reason {
		case personalassistant.FolderStopNeedsChoice, personalassistant.FolderStopNeedsModel,
			personalassistant.FolderStopConsentStale, personalassistant.FolderStopPlanChanged:
			return &stop{reason: asked.Reason, candidates: asked.Candidates, detail: asked.Detail}
		}
		return failed("the song could not be opened: " + asked.Reason + ": " + asked.Detail)
	case err != nil:
		return failed("the song could not be opened: " + err.Error())
	case outcome.WorkspaceID == "" || outcome.EntryID != entryID:
		return failed("the open did not name the song's workspace")
	}
	for _, kind := range []string{personalassistant.FolderPlanWorkspace, personalassistant.FolderPlanFolder, personalassistant.FolderPlanMode} {
		s.setOpen(kind, personalassistant.FolderLineDone)
	}
	switch outcome.Staffing {
	case "added", "joined", "already":
		s.setOpen(personalassistant.FolderPlanAgents, personalassistant.FolderLineDone)
	case personalassistant.FolderStopConsentStale, personalassistant.FolderStopAssistantMissing:
		s.setOpen(personalassistant.FolderPlanAgents, personalassistant.FolderLineWorking)
		return &stop{reason: outcome.Staffing}
	default:
		s.setOpen(personalassistant.FolderPlanAgents, personalassistant.FolderLineWorking)
		return failed("the song's shared assistant was not added: " + outcome.Staffing)
	}
	if !outcome.FirstTask {
		s.setOpen(personalassistant.FolderPlanTask, personalassistant.FolderLineWorking)
		return failed("the song's first task was not seeded")
	}
	s.setOpen(personalassistant.FolderPlanTask, personalassistant.FolderLineDone)
	s.setOpen(personalassistant.FolderPlanHandoff, personalassistant.FolderLineDone)
	s.opened = &outcome
	return s.record(ctx, personalassistant.FolderSetupRunning, "")
}

func errText(err error) string {
	if err == nil {
		return "no entry"
	}
	return err.Error()
}

// share records the standing consent the plan's assistant line asked for. A Home
// whose consent is already on records nothing.
func (s *portfolioRun) share(ctx context.Context) error {
	if err := s.guard(ctx); err != nil {
		return err
	}
	if !s.cfg.Plan.Intent.GrantsConsent {
		return nil
	}
	current, err := s.runner.Sharing.State(ctx, s.homeID)
	if err != nil {
		return failed("could not read the Home's consent: " + err.Error())
	}
	if current == SharingOn {
		return nil
	}
	if err := s.working(ctx, personalassistant.FolderPlanAssistant); err != nil {
		return err
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	if err := s.runner.Sharing.Grant(ctx, s.homeID); err != nil {
		return failed("the consent was not recorded: " + err.Error())
	}
	return nil
}
