package foldersetup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const portfolioFolder = "/Users/me/Music/Songs"

// portfolioWorld is the synthetic state behind every portfolio seam, with one
// call log so a test can prove what ran, in which order, and what did not.
type portfolioWorld struct {
	log []string

	// The Home.
	homeID        string
	homeCreatedAt time.Time
	reviewReuse   bool
	reviewTmpl    string
	commitHomeErr error

	// Its required roles.
	missing    []HomeRole
	modelReady bool
	staffErr   error

	// The library.
	initialized  bool
	linked       int
	rootID       string
	scanned      bool
	listed       int
	partial      bool
	reviewFolder string
	includesScan bool
	notMetadata  bool
	needsPick    bool
	scanErr      error

	// The standing consent.
	sharing  string
	grantErr error

	// The song-details consent, and whether the listing review reads them.
	songDetails     bool
	detailsErr      error
	reviewReadsSong bool

	receiptFacts *PortfolioReceiptFacts
}

func newPortfolioWorld() *portfolioWorld {
	return &portfolioWorld{
		reviewTmpl: "plugin-home:music-project-management:music-producer-assistant", reviewFolder: portfolioFolder,
		missing: []HomeRole{{RoleID: "portfolio_manager", Name: "Portfolio Manager"}}, modelReady: true, listed: 200,
	}
}

func (w *portfolioWorld) called(entry string) bool {
	for _, call := range w.log {
		if call == entry {
			return true
		}
	}
	return false
}

// Homes.
func (w *portfolioWorld) Find(context.Context) (HomeFound, bool, error) {
	w.log = append(w.log, "home:find")
	if w.homeID == "" {
		return HomeFound{}, false, nil
	}
	return HomeFound{ID: w.homeID, CreatedAt: w.homeCreatedAt}, true, nil
}

func (w *portfolioWorld) Review(context.Context) (HomeReview, error) {
	w.log = append(w.log, "home:review")
	return HomeReview{Token: "t-home", TemplateID: w.reviewTmpl, Name: "Music Production Home", Reuse: w.reviewReuse}, nil
}

func (w *portfolioWorld) Commit(_ context.Context, review HomeReview, _ string) (string, error) {
	w.log = append(w.log, "home:commit")
	if w.commitHomeErr != nil {
		return "", w.commitHomeErr
	}
	w.homeID, w.homeCreatedAt = "home-1", time.Now()
	return w.homeID, nil
}

// HomeStaffing (its Missing/ModelReady/Staff live on a wrapper: Review/Commit
// names are taken by Homes).
type worldStaffing struct{ *portfolioWorld }

func (w worldStaffing) Missing(context.Context, string) ([]HomeRole, error) {
	return append([]HomeRole(nil), w.missing...), nil
}

func (w worldStaffing) ModelReady(context.Context) bool { return w.modelReady }

func (w worldStaffing) Staff(_ context.Context, _ string, role HomeRole) error {
	w.log = append(w.log, "staff:"+role.Name)
	if w.staffErr != nil {
		return w.staffErr
	}
	w.missing = nil
	return nil
}

// Library.
type worldLibrary struct{ *portfolioWorld }

func (w worldLibrary) State(context.Context, string) (LibraryState, error) {
	return LibraryState{Initialized: w.initialized, Linked: w.linked, RootID: w.rootID, Scanned: w.scanned, Listed: w.listed, Partial: w.partial}, nil
}

func (w worldLibrary) ReviewInitialize(context.Context, string) (InitReview, error) {
	w.log = append(w.log, "library:review")
	return InitReview{Token: "t-init", Linked: w.linked}, nil
}

func (w worldLibrary) CommitInitialize(context.Context, string, InitReview, string) error {
	w.log = append(w.log, "library:on")
	w.initialized = true
	return nil
}

func (w worldLibrary) ReviewConnect(context.Context, string) (ConnectReview, error) {
	w.log = append(w.log, "root:review")
	if w.needsPick {
		return ConnectReview{}, ErrNeedsPick
	}
	return ConnectReview{Token: "t-root", Folder: w.reviewFolder, IncludesScan: w.includesScan}, nil
}

func (w worldLibrary) CommitConnect(context.Context, string, ConnectReview, string) (string, error) {
	w.log = append(w.log, "root:connect")
	w.rootID = "root-1"
	return w.rootID, nil
}

func (w worldLibrary) ReviewScan(_ context.Context, _, rootID string) (LibraryScanReview, error) {
	w.log = append(w.log, "scan:review")
	return LibraryScanReview{Token: "t-scan", RootID: rootID, MetadataOnly: !w.notMetadata,
		ReadsSongDetails: w.songDetails || w.reviewReadsSong}, nil
}

func (w worldLibrary) CommitScan(context.Context, string, string, LibraryScanReview, string) (ScanOutcome, error) {
	w.log = append(w.log, "scan:commit")
	if w.scanErr != nil {
		return ScanOutcome{}, w.scanErr
	}
	w.scanned = true
	return ScanOutcome{Listed: w.listed, Partial: w.partial}, nil
}

// Sharing.
type worldSharing struct{ *portfolioWorld }

func (w worldSharing) State(context.Context, string) (string, error) { return w.sharing, nil }

func (w worldSharing) Grant(context.Context, string) error {
	w.log = append(w.log, "consent:grant")
	if w.grantErr != nil {
		return w.grantErr
	}
	w.sharing = SharingOn
	return nil
}

// SongDetails.
type worldSongDetails struct{ *portfolioWorld }

func (w worldSongDetails) Grant(context.Context, string) error {
	w.log = append(w.log, "details:grant")
	if w.detailsErr != nil {
		return w.detailsErr
	}
	w.songDetails = true
	return nil
}

// Receipts.
type worldReceipts struct{ *portfolioWorld }

func (w worldReceipts) Receipt(_ context.Context, homeID string, facts PortfolioReceiptFacts) ([]personalassistant.FolderReceiptRow, error) {
	w.receiptFacts = &facts
	return []personalassistant.FolderReceiptRow{{Kind: "home", Name: "Music Production Home", Route: "/workspaces/home/assistant#projectLibraryPanel"}}, nil
}

// portfolioPlanFor is the plan a fresh collection with a shared assistant shows.
func portfolioPlanFor(facts PortfolioFacts) personalassistant.FolderSetupPlan {
	return BuildPortfolioPlan(facts)
}

func freshFacts() PortfolioFacts {
	return PortfolioFacts{
		FolderName: "Songs", Projects: 200, CollectionNoun: "music projects", HomeName: "Music Production Home",
		HomeTemplate:        "plugin-home:music-project-management:music-producer-assistant",
		Provider:            Plugin{Name: "Music Project Management", PluginID: "music-project-management", State: personalassistant.FolderInstallReady, Version: "0.1.1"},
		Integration:         &Plugin{Name: "REAPER", PluginID: "reaper-plugin", State: personalassistant.FolderInstallReady, Version: "0.9.0"},
		IntegrationProjects: 200, AppName: "REAPER", ProjectLabel: "REAPER song",
	}
}

func runPortfolio(t *testing.T, w *portfolioWorld, plan personalassistant.FolderSetupPlan, mutate func(*PortfolioRunner, *PortfolioConfig)) (Result, *recorder) {
	t.Helper()
	progress := &recorder{}
	runner := &PortfolioRunner{
		Homes: w, Staffing: worldStaffing{w}, Library: worldLibrary{w}, Sharing: worldSharing{w}, Receipts: worldReceipts{w},
		SongDetails: worldSongDetails{w}, Folder: portfolioFolder, Progress: progress,
	}
	cfg := PortfolioConfig{Plan: plan, AcceptedAfter: time.Now().Add(-time.Minute)}
	if mutate != nil {
		mutate(runner, &cfg)
	}
	result, err := runner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result, progress
}

func TestPortfolioRunSetsUpAFreshCollectionInOrder(t *testing.T) {
	w := newPortfolioWorld()
	result, progress := runPortfolio(t, w, portfolioPlanFor(freshFacts()), nil)
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	// The song-details consent is recorded before anything is listed, so the
	// setup's own scan reads the facts its library line disclosed.
	want := "home:find,home:review,home:commit,staff:Portfolio Manager,details:grant,library:review,library:on,root:review,root:connect,scan:review,scan:commit,consent:grant"
	if got := strings.Join(w.log, ","); got != want {
		t.Fatalf("calls =\n %s\nwant\n %s", got, want)
	}
	last := progress.last()
	if last.HomeID != "home-1" || len(last.Receipt) != 1 {
		t.Fatalf("the run must record its Home and receipt: %+v", last)
	}
	for kind, state := range lineStates(last) {
		if state != personalassistant.FolderLineDone {
			t.Errorf("line %s = %s, want done", kind, state)
		}
	}
	if w.receiptFacts == nil || !w.receiptFacts.HomeCreated || w.receiptFacts.Listed.Listed != 200 || !w.receiptFacts.Shared ||
		len(w.receiptFacts.AddedAgents) != 1 {
		t.Fatalf("receipt facts = %+v", w.receiptFacts)
	}
}

// Every pass re-reads where things stand: a resume after any step repeats nothing.
func TestPortfolioRunResumesAfterEachStep(t *testing.T) {
	steps := []struct {
		name   string
		mutate func(*portfolioWorld, *PortfolioConfig)
		never  []string
	}{
		{"after the Home", func(w *portfolioWorld, cfg *PortfolioConfig) { w.homeID = "home-1"; cfg.HomeID = "home-1" },
			[]string{"home:review", "home:commit"}},
		{"after the agents", func(w *portfolioWorld, cfg *PortfolioConfig) {
			w.homeID, cfg.HomeID, w.missing = "home-1", "home-1", nil
		}, []string{"home:commit", "staff:Portfolio Manager"}},
		{"after the library", func(w *portfolioWorld, cfg *PortfolioConfig) {
			w.homeID, cfg.HomeID, w.missing, w.initialized = "home-1", "home-1", nil, true
		}, []string{"staff:Portfolio Manager", "library:on"}},
		{"after the folder", func(w *portfolioWorld, cfg *PortfolioConfig) {
			w.homeID, cfg.HomeID, w.missing, w.initialized, w.rootID = "home-1", "home-1", nil, true, "root-1"
		}, []string{"library:on", "root:connect"}},
		{"after the listing", func(w *portfolioWorld, cfg *PortfolioConfig) {
			w.homeID, cfg.HomeID, w.missing, w.initialized, w.rootID, w.scanned = "home-1", "home-1", nil, true, "root-1", true
		}, []string{"root:connect", "scan:commit"}},
		{"after the consent", func(w *portfolioWorld, cfg *PortfolioConfig) {
			w.homeID, cfg.HomeID, w.missing, w.initialized, w.rootID, w.scanned, w.sharing = "home-1", "home-1", nil, true, "root-1", true, SharingOn
		}, []string{"scan:commit", "consent:grant"}},
		// A pass that stopped right after creating the Home, before recording it:
		// the Home made since Set up was pressed is this run's own.
		{"after an unrecorded Home", func(w *portfolioWorld, cfg *PortfolioConfig) { w.homeID, w.homeCreatedAt = "home-1", time.Now() },
			[]string{"home:review", "home:commit"}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			w := newPortfolioWorld()
			result, _ := runPortfolio(t, w, portfolioPlanFor(freshFacts()), func(_ *PortfolioRunner, cfg *PortfolioConfig) { step.mutate(w, cfg) })
			if result.Status != personalassistant.FolderSetupDone {
				t.Fatalf("result = %+v cause=%v", result, result.Cause)
			}
			for _, forbidden := range step.never {
				if w.called(forbidden) {
					t.Fatalf("%s was repeated: %v", forbidden, w.log)
				}
			}
		})
	}
}

func TestPortfolioRunStopsWhenAReviewDiffersFromThePlan(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*portfolioWorld)
		never  string
	}{
		{"a Home appeared that the run did not make", func(w *portfolioWorld) { w.homeID = "someone-elses"; w.homeCreatedAt = time.Now().Add(-time.Hour) }, "staff:Portfolio Manager"},
		{"the review would reuse a Home", func(w *portfolioWorld) { w.reviewReuse = true }, "home:commit"},
		{"another Home template", func(w *portfolioWorld) { w.reviewTmpl = "plugin-home:other:program" }, "home:commit"},
		{"the folder review names another folder", func(w *portfolioWorld) { w.reviewFolder = "/Users/me/Elsewhere" }, "root:connect"},
		{"connecting would also scan", func(w *portfolioWorld) { w.includesScan = true }, "root:connect"},
		{"the listing is not names only", func(w *portfolioWorld) { w.notMetadata = true }, "scan:commit"},
		{"the library would carry in projects", func(w *portfolioWorld) { w.linked = 3 }, "library:on"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newPortfolioWorld()
			tc.mutate(w)
			result, _ := runPortfolio(t, w, portfolioPlanFor(freshFacts()), nil)
			if result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopPlanChanged {
				t.Fatalf("result = %+v", result)
			}
			if w.called(tc.never) || w.called("consent:grant") {
				t.Fatalf("committed something the plan did not describe: %v", w.log)
			}
		})
	}
}

// No agent is created without a model, and a retry once there is one finishes.
func TestPortfolioRunNeedsAModelBeforeAnyAgent(t *testing.T) {
	w := newPortfolioWorld()
	w.modelReady = false
	result, progress := runPortfolio(t, w, portfolioPlanFor(freshFacts()), nil)
	if result.StopReason != personalassistant.FolderStopNeedsModel {
		t.Fatalf("result = %+v", result)
	}
	if w.called("staff:Portfolio Manager") || w.called("library:on") || w.called("consent:grant") {
		t.Fatalf("ran past the missing model: %v", w.log)
	}
	if lineStates(progress.last())[personalassistant.FolderPlanAgents] != personalassistant.FolderLineWaiting {
		t.Fatalf("a model question is not a failure: %v", lineStates(progress.last()))
	}
	w.modelReady = true
	before := len(w.log)
	result, _ = runPortfolio(t, w, portfolioPlanFor(freshFacts()), func(_ *PortfolioRunner, cfg *PortfolioConfig) { cfg.HomeID = w.homeID })
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("retry = %+v cause=%v", result, result.Cause)
	}
	for _, call := range w.log[before:] {
		if call == "home:commit" {
			t.Fatalf("the Home was made twice: %v", w.log)
		}
	}
}

func TestPortfolioRunStopsWhenTheProviderInstallFails(t *testing.T) {
	w := newPortfolioWorld()
	facts := freshFacts()
	facts.Provider.State = personalassistant.FolderInstallInstall
	var log []string
	result, _ := runPortfolio(t, w, portfolioPlanFor(facts), func(r *PortfolioRunner, _ *PortfolioConfig) {
		r.Providers = &fakeProvider{pluginID: "music-project-management", version: "0.1.1", installErr: errors.New("offline"), calls: &log}
	})
	if result.StopReason != personalassistant.FolderStopInstallFailed || len(w.log) != 0 {
		t.Fatalf("result = %+v calls = %v", result, w.log)
	}
}

func TestPortfolioRunInstallsTheIntegrationThePlanPromised(t *testing.T) {
	w := newPortfolioWorld()
	facts := freshFacts()
	facts.Integration.State = personalassistant.FolderInstallInstall
	var log []string
	install := newFakeInstall(&log)
	result, _ := runPortfolio(t, w, portfolioPlanFor(facts), func(r *PortfolioRunner, _ *PortfolioConfig) { r.Install = install })
	if result.Status != personalassistant.FolderSetupDone || !install.installed || !install.enabled {
		t.Fatalf("result = %+v install = %+v", result, install)
	}
	if !strings.HasPrefix(strings.Join(log, ","), "install:review_install,install:install") {
		t.Fatalf("install calls = %v", log)
	}
}

func TestPortfolioRunInterruptedRecordsAStop(t *testing.T) {
	w := newPortfolioWorld()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	progress := &recorder{}
	runner := &PortfolioRunner{Homes: w, Staffing: worldStaffing{w}, Library: worldLibrary{w}, Sharing: worldSharing{w}, Receipts: worldReceipts{w},
		SongDetails: worldSongDetails{w}, Folder: portfolioFolder, Progress: progress,
		Providers: &fakeProvider{pluginID: "music-project-management", version: "0.1.1", calls: &[]string{}}}
	facts := freshFacts()
	facts.Provider.State = personalassistant.FolderInstallInstall
	runner.Providers = cancelledProvider{}
	result, err := runner.Run(ctx, PortfolioConfig{Plan: portfolioPlanFor(facts)})
	if err != nil || result.Status != personalassistant.FolderSetupStopped || result.StopReason != personalassistant.FolderStopInterrupted {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	if progress.last().Status != personalassistant.FolderSetupStopped {
		t.Fatalf("the stop was not recorded: %+v", progress.last())
	}
}

type cancelledProvider struct{}

func (cancelledProvider) Preview(ctx context.Context) (ProviderPreview, error) {
	return ProviderPreview{}, ctx.Err()
}
func (cancelledProvider) Install(ctx context.Context, _ string) error { return ctx.Err() }

func TestPortfolioRunNeedsTheFolderAgainWhenItWasLost(t *testing.T) {
	w := newPortfolioWorld()
	w.needsPick = true
	result, progress := runPortfolio(t, w, portfolioPlanFor(freshFacts()), nil)
	if result.StopReason != personalassistant.FolderStopNeedsPick || w.called("root:connect") {
		t.Fatalf("result = %+v calls = %v", result, w.log)
	}
	if lineStates(progress.last())[personalassistant.FolderPlanLibrary] != personalassistant.FolderLineWaiting {
		t.Fatalf("a lost folder is a question: %v", lineStates(progress.last()))
	}
}

// 2.8: an existing Home goes straight to the folder; its roles and an active
// consent are left as they are.
func TestPortfolioRunIntoAnExistingHome(t *testing.T) {
	w := newPortfolioWorld()
	w.homeID, w.missing, w.sharing = "home-1", nil, SharingOn
	facts := freshFacts()
	facts.HomeExists, facts.HomeStaffed, facts.Sharing = true, true, SharingOn
	plan := portfolioPlanFor(facts)
	for _, line := range plan.Lines {
		if line.Kind == personalassistant.FolderPlanHome || line.Kind == personalassistant.FolderPlanAgents || line.Kind == personalassistant.FolderPlanAssistant {
			t.Fatalf("an existing, staffed, consenting Home's plan lists %s", line.Kind)
		}
	}
	result, _ := runPortfolio(t, w, plan, nil)
	if result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("result = %+v cause=%v", result, result.Cause)
	}
	if got := strings.Join(w.log, ","); got != "home:find,library:review,library:on,root:review,root:connect,scan:review,scan:commit" {
		t.Fatalf("calls = %s", got)
	}
	if w.receiptFacts.HomeCreated || !w.receiptFacts.Shared {
		t.Fatalf("receipt facts = %+v", w.receiptFacts)
	}
	// Without a consent yet, the same card asks for it and the run records it.
	w2 := newPortfolioWorld()
	w2.homeID, w2.missing = "home-1", nil
	facts.Sharing = ""
	if result, _ := runPortfolio(t, w2, portfolioPlanFor(facts), nil); result.Status != personalassistant.FolderSetupDone || !w2.called("consent:grant") {
		t.Fatalf("result = %+v calls = %v", result, w2.log)
	}
}

// 2.9: a collection with no project the integration can set up is listed and
// that is all: no integration, no assistant line, no consent.
func TestPortfolioRunWithoutSharedProjectsOnlyLists(t *testing.T) {
	facts := freshFacts()
	facts.Integration, facts.IntegrationProjects = nil, 0
	plan := portfolioPlanFor(facts)
	if plan.Intent.GrantsConsent || plan.Intent.SharedProjects || plan.Intent.Integration != "" {
		t.Fatalf("intent = %+v", plan.Intent)
	}
	w := newPortfolioWorld()
	result, _ := runPortfolio(t, w, plan, func(r *PortfolioRunner, _ *PortfolioConfig) { r.Sharing = nil })
	if result.Status != personalassistant.FolderSetupDone || w.called("consent:grant") || w.receiptFacts.Shared {
		t.Fatalf("result = %+v calls = %v facts = %+v", result, w.log, w.receiptFacts)
	}
}

// 2.7: a partial listing reaches the receipt as partial; it is never claimed whole.
func TestPortfolioRunPassesAPartialListingOn(t *testing.T) {
	w := newPortfolioWorld()
	w.listed, w.partial = 143, true
	result, _ := runPortfolio(t, w, portfolioPlanFor(freshFacts()), nil)
	if result.Status != personalassistant.FolderSetupDone || !w.receiptFacts.Listed.Partial || w.receiptFacts.Listed.Listed != 143 {
		t.Fatalf("result = %+v facts = %+v", result, w.receiptFacts)
	}
}

func TestPortfolioRunRefusesToRunUnwired(t *testing.T) {
	plan := portfolioPlanFor(freshFacts())
	if _, err := (&PortfolioRunner{}).Run(context.Background(), PortfolioConfig{Plan: plan}); !errors.Is(err, ErrPortfolioNotWired) {
		t.Fatalf("unwired = %v", err)
	}
	w := newPortfolioWorld()
	runner := &PortfolioRunner{Homes: w, Staffing: worldStaffing{w}, Library: worldLibrary{w}, Receipts: worldReceipts{w}, Progress: &recorder{}}
	if _, err := runner.Run(context.Background(), PortfolioConfig{Plan: plan}); !errors.Is(err, ErrPortfolioNotWired) {
		t.Fatalf("a consent plan without the consent seam = %v", err)
	}
}
