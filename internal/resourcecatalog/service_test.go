package resourcecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeProvider answers like a project plugin's two catalog operations.
type fakeProvider struct {
	mu           sync.Mutex
	status       ProviderStatus
	installation Installation
	sources      []Source
	// entries per source; pages are cut from them by the request's cursor.
	entries map[string][]InventoryEntry
	states  map[string]string
	// hook runs before a page is answered; it may block or fail.
	hook  func(ctx context.Context, request InventoryRequest) error
	calls []InventoryRequest
	// sourceInputs records every sources input; each folder it names is
	// answered as one ready file source, unless dropFolders leaves them out.
	sourceInputs []string
	dropFolders  bool
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	declaration := validDeclaration(t)
	installed := true
	return &fakeProvider{
		status:       ProviderStatus{Available: true, Catalog: CatalogRef{PluginID: "reaper-plugin", ID: declaration.ID, Version: declaration.Version}, Declaration: declaration},
		installation: Installation{ID: "inst_machine_a1", Label: "REAPER 7.28", Layout: "per_user", AppInstalled: &installed},
		sources: []Source{
			{ID: "project_templates", Category: "templates", Label: "Project templates", Location: "~/Library/Application Support/REAPER/ProjectTemplates", Access: AccessFile, State: SourceReady},
			{ID: "reaper_scripts", Category: "scripts", Label: "REAPER Scripts", Location: "~/Library/Application Support/REAPER/Scripts", Access: AccessFile, State: SourceReady},
		},
		entries: map[string][]InventoryEntry{
			"project_templates": {
				{NativeID: "Vocals.RPP", Kind: "project_template", Name: "Vocals", Facts: []Fact{}},
				{NativeID: "Band/Vocals.RPP", Kind: "project_template", Name: "Vocals", Facts: []Fact{}},
			},
			"reaper_scripts": {
				{NativeID: "Cockos/Lyrics.lua", Kind: "script", Name: "Lyrics", Facts: []Fact{{ID: "language", Value: "Lua"}}},
			},
		},
		states: map[string]string{},
	}
}

func (p *fakeProvider) Status(*workspace.Workspace) ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *fakeProvider) Sources(_ context.Context, _ *workspace.Workspace, input []byte) ([]byte, error) {
	var request struct {
		SchemaVersion int           `json:"schema_version"`
		Folders       []folderInput `json:"folders"`
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.SchemaVersion != 1 {
		return nil, errors.New("the sources input is not the contract's")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sourceInputs = append(p.sourceInputs, string(input))
	sources := slices.Clone(p.sources)
	if !p.dropFolders {
		for _, folder := range request.Folders {
			sources = append(sources, Source{ID: folder.ID, Category: folder.Category, Label: "Folder " + filepath.Base(folder.Path),
				Location: folder.Path, Access: AccessFile, State: SourceReady})
		}
	}
	return json.Marshal(SourcesAnswer{SchemaVersion: 1, Installation: p.installation, Sources: sources})
}

// Inventory pages one entry at a time so cursors and continuation are real.
func (p *fakeProvider) Inventory(ctx context.Context, _ *workspace.Workspace, input []byte) ([]byte, error) {
	var request InventoryRequest
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.calls = append(p.calls, request)
	hook := p.hook
	p.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, request); err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	empty, none := []InventoryEntry{}, ""
	answer := InventoryAnswer{SchemaVersion: 1, InstallationID: request.InstallationID, SourceID: request.SourceID,
		State: InventoryOK, Entries: empty, NextCursor: &none, Coverage: &Coverage{Skipped: []Skip{}}}
	if state := p.states[request.SourceID]; state != "" {
		answer.State = state
		return json.Marshal(answer)
	}
	all := p.entries[request.SourceID]
	position := 0
	if request.Cursor != "" {
		if _, err := fmtSscan(request.Cursor, &position); err != nil {
			return nil, err
		}
	}
	if position < len(all) {
		answer.Entries = all[position : position+1]
		answer.Coverage.Visited = 1
		if position+1 < len(all) {
			next := "at-" + itoa(position+1)
			answer.NextCursor = &next
		}
	}
	return json.Marshal(answer)
}

func fmtSscan(cursor string, position *int) (int, error) {
	value := strings.TrimPrefix(cursor, "at-")
	parsed := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, errors.New("bad cursor")
		}
		parsed = parsed*10 + int(r-'0')
	}
	*position = parsed
	return 1, nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func (p *fakeProvider) set(fn func(p *fakeProvider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

type catalogFixture struct {
	store    *workspace.FileStore
	provider *fakeProvider
	service  *Service
	studio   *workspace.Workspace
	home     *workspace.Workspace
	dataDir  string
	// mu guards clock and homeAvailable for every service the fixture builds:
	// a restart test runs two, and the first one's inspection goroutine can
	// still be ticking the clock while the second reads it.
	mu    sync.Mutex
	clock time.Time
	// homeAvailable stands for the Home's own package being installed and
	// pinned; lookups read it at call time.
	homeAvailable bool
}

// setHomeAvailable changes whether the Home's package counts as installed.
func (f *catalogFixture) setHomeAvailable(available bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.homeAvailable = available
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	store, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	homeOwner := workspace.AssistantProgramHomeOwner{
		PluginID: "music-project-management", PluginVersion: "0.4.0", ProgramID: "music-producer-assistant", HomeSchemaVersion: 1, HomeVersion: 1,
		DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 3, ComponentFingerprint: strings.Repeat("b", 64),
	}
	declaration := &workspace.AssistantProgramDeclaration{
		SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: "music-producer-assistant", StationName: "Music Production Home",
		Roles: []workspace.AssistantProgramRoleSpec{{ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate."}},
	}
	programs := workspace.NewAssistantProgramStore(store)
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: homeOwner.PluginID, ProgramID: homeOwner.ProgramID}
	home, _, err := programs.EnsureNamedIndependentStation(key, declaration, "Music Production Home", homeOwner)
	if err != nil {
		t.Fatal(err)
	}
	studio := linkTestStudio(t, store, home, "studio-one")
	f := &catalogFixture{store: store, provider: newFakeProvider(t), home: home, studio: studio, dataDir: t.TempDir(),
		clock: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	f.service = f.newService(f.dataDir)
	return f
}

func linkTestStudio(t *testing.T, store *workspace.FileStore, home *workspace.Workspace, id string) *workspace.Workspace {
	t.Helper()
	state := home.GetAssistantProgramState()
	key := state.Key.Normalize()
	owner := &workspace.AssistantProjectProviderOwner{
		PluginID: "reaper-plugin", PluginVersion: "0.11.0", BlueprintID: "reaper-studio", BlueprintVersion: 1,
		ProjectTeamID: "reaper-studio-team", ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: strings.Repeat("c", 64),
		PluginGeneration: 5, ComponentFingerprint: strings.Repeat("d", 64), WorkspaceKind: workspace.AssistantWorkspaceKindResource,
	}
	studio := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "REAPER Studio"})
	studio.ID, studio.OwnerUserID = id, "local"
	studio.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "plugin:reaper-plugin:reaper-studio", AssistantProgram: state.Declaration,
		AssistantProjectRoles: []workspace.AssistantProgramRoleSpec{{ID: "reaper-studio-assistant", Label: "REAPER Studio Assistant",
			Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Explain the catalog."}},
		GroupRequirement: &workspace.GroupRequirementSnapshot{
			SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: "required", SelectedComposition: workspace.GroupRequirementCompositionGrouped,
			TemplateID: "plugin:reaper-plugin:reaper-studio", DefinitionDigest: strings.Repeat("e", 64), ReviewDigest: strings.Repeat("f", 64),
			OperationDigest: strings.Repeat("1", 64), AppliedAt: time.Now().UTC(), ProgramKey: &key,
			HomeWorkspaceID: home.ID, ProjectLinkID: workspace.AssistantProjectLinkID(home.ID, studio.ID),
			HomeProvider: state.HomeProvider, ProjectProvider: owner,
		},
	})
	if err := store.Save(studio); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.NewAssistantProgramStore(store).EnsureProjectStation(studio.ID); err != nil {
		t.Fatalf("link the Studio: %v", err)
	}
	linked, err := store.Get(studio.ID)
	if err != nil {
		t.Fatal(err)
	}
	return linked
}

func (f *catalogFixture) newService(dataDir string) *Service {
	ids := 0
	return NewService(Deps{
		Workspaces: f.store, Folders: f.store, DataDir: dataDir, Provider: f.provider,
		HomeProviderAvailable: func(*workspace.Workspace) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.homeAvailable
		},
		Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.clock = f.clock.Add(time.Second)
			return f.clock
		},
		NewID: func() string {
			f.mu.Lock()
			defer f.mu.Unlock()
			ids++
			return "id" + itoa(ids)
		},
	})
}

// approveAll reviews and approves every source, then waits for the read.
func (f *catalogFixture) approveAll(t *testing.T) Operation {
	t.Helper()
	review, err := f.service.ReviewSources(context.Background(), "local", f.studio.ID)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	ids := []string{}
	for _, source := range review.Sources {
		ids = append(ids, source.SourceID)
	}
	op, err := f.service.CommitSources(context.Background(), "local", f.studio.ID, review.Token, "approve-"+itoa(len(ids)), ids)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	f.service.wait(f.studio.ID)
	return f.operation(t, op.ID)
}

func (f *catalogFixture) refresh(t *testing.T, key string) Operation {
	t.Helper()
	op, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, key, ModeRefresh, nil)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	f.service.wait(f.studio.ID)
	return f.operation(t, op.ID)
}

func (f *catalogFixture) operation(t *testing.T, id string) Operation {
	t.Helper()
	op, err := f.service.Operation("local", f.studio.ID, id)
	if err != nil {
		t.Fatalf("operation %s: %v", id, err)
	}
	return op
}

func (f *catalogFixture) overview(t *testing.T) OwnerView {
	t.Helper()
	view, err := f.service.Overview("local", f.studio.ID)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	return view
}

func categoryNamed(view OwnerView, id string) CategoryView {
	for _, category := range view.Categories {
		if category.ID == id {
			return category
		}
	}
	return CategoryView{}
}

func (f *catalogFixture) rows(t *testing.T, query Query) []EntryRow {
	t.Helper()
	page, err := f.service.Entries("local", f.studio.ID, query)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	return page.Rows
}

func TestInspection_ReviewApproveAndPublishOneRevision(t *testing.T) {
	f := newCatalogFixture(t)
	if view := f.overview(t); view.Revision != 0 || categoryNamed(view, "templates").State != CategoryNotInspected || !view.CanInspect || view.Approved {
		t.Fatalf("new Studio = %+v", view)
	}
	if len(f.provider.calls) != 0 {
		t.Fatal("reading the overview called the inventory")
	}
	op := f.approveAll(t)
	if op.State != OperationPublished || op.PublishedRevision != 1 {
		t.Fatalf("operation = %+v", op)
	}
	view := f.overview(t)
	templates := categoryNamed(view, "templates")
	if view.Revision != 1 || templates.State != CategoryInspected || templates.Counts.Present != 2 || templates.InspectedAt == nil {
		t.Fatalf("after inspection = %+v / %+v", view, templates)
	}
	// Two templates with the same name stay two entries with their source.
	rows := f.rows(t, Query{Category: "templates"})
	if len(rows) != 2 || rows[0].Name != "Vocals" || rows[1].Name != "Vocals" || rows[0].EntryID == rows[1].EntryID || rows[0].SourceLabel != "Project templates" {
		t.Fatalf("template rows = %+v", rows)
	}
	// Same source, kind and name: each row carries its own identity so the
	// owner can tell them apart. A name of its own carries none.
	if hints := []string{rows[0].IdentityHint, rows[1].IdentityHint}; !slices.Contains(hints, "Vocals.RPP") || !slices.Contains(hints, "Band/Vocals.RPP") {
		t.Fatalf("same-name rows are not told apart: %+v", rows)
	}
	scripts := f.rows(t, Query{Category: "scripts"})
	if len(scripts) != 1 || scripts[0].Facts[0].Value != "Lua" || scripts[0].Facts[1].Value != "Unknown" || scripts[0].Facts[1].Known || scripts[0].IdentityHint != "" {
		t.Fatalf("script facts = %+v", scripts)
	}
	// The Studio's own files hold no location; this computer's grants do.
	folder, _ := f.store.GetFolderPath(f.studio.ID)
	observations, err := os.ReadFile(filepath.Join(folder, CatalogDir, observationsFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(observations, []byte("Application Support")) || bytes.Contains(observations, []byte("/Users/")) {
		t.Fatalf("observations carry a location: %s", observations)
	}
	grants, err := os.ReadFile(filepath.Join(f.dataDir, dataDirName, f.studio.ID, grantsFile))
	if err != nil || !bytes.Contains(grants, []byte("ProjectTemplates")) {
		t.Fatalf("grants = %s, %v", grants, err)
	}
}

func TestInspection_OnlyACompleteReadMarksEntriesMissing(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	byName := func(rows []EntryRow, native string) EntryRow {
		for _, row := range rows {
			if row.EntryID == EntryID("inst_machine_a1", "project_templates", native) {
				return row
			}
		}
		return EntryRow{}
	}
	// A failed read leaves everything as it was.
	f.provider.set(func(p *fakeProvider) {
		p.entries["project_templates"] = p.entries["project_templates"][:1]
		p.hook = func(_ context.Context, request InventoryRequest) error {
			if request.SourceID == "project_templates" {
				return errors.New("read failed")
			}
			return nil
		}
	})
	f.refresh(t, "refresh-failed")
	rows := f.rows(t, Query{Category: "templates"})
	if byName(rows, "Band/Vocals.RPP").State != EntryPresent {
		t.Fatalf("a failed read marked an entry missing: %+v", rows)
	}
	if state := categoryNamed(f.overview(t), "templates").State; state != CategoryRefreshFailed {
		t.Fatalf("templates state after a failed refresh = %s", state)
	}
	// A complete read marks the unseen one no longer found and keeps it.
	f.provider.set(func(p *fakeProvider) { p.hook = nil })
	f.refresh(t, "refresh-complete")
	rows = f.rows(t, Query{Category: "templates"})
	if byName(rows, "Band/Vocals.RPP").State != EntryNoLongerFound || byName(rows, "Vocals.RPP").State != EntryPresent {
		t.Fatalf("after a complete read = %+v", rows)
	}
	if counts := categoryNamed(f.overview(t), "templates").Counts; counts.Present != 1 || counts.NoLongerFound != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestInspection_CancelKeepsFinishedSourcesAndSaysCancelled(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	f.provider.set(func(p *fakeProvider) {
		p.entries["project_templates"] = append(p.entries["project_templates"], InventoryEntry{NativeID: "New.RPP", Kind: "project_template", Name: "New", Facts: []Fact{}})
		p.entries["reaper_scripts"] = append(p.entries["reaper_scripts"], InventoryEntry{NativeID: "New.lua", Kind: "script", Name: "New", Facts: []Fact{}})
	})
	blocked := make(chan struct{})
	f.provider.set(func(p *fakeProvider) {
		p.hook = func(ctx context.Context, request InventoryRequest) error {
			if request.SourceID == "reaper_scripts" {
				close(blocked)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
	})
	op, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "refresh-cancel", ModeRefresh, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-blocked
	if _, err := f.service.Cancel("local", f.studio.ID, op.ID); err != nil {
		t.Fatal(err)
	}
	f.service.wait(f.studio.ID)
	op = f.operation(t, op.ID)
	if op.State != OperationCancelled || op.PublishedRevision != 2 {
		t.Fatalf("cancelled operation = %+v", op)
	}
	templates := f.rows(t, Query{Category: "templates", Q: "new"})
	scripts := f.rows(t, Query{Category: "scripts", Q: "new"})
	if len(templates) != 1 || len(scripts) != 0 {
		t.Fatalf("templates %d (finished source published), scripts %d (cancelled source kept)", len(templates), len(scripts))
	}
	if state := categoryNamed(f.overview(t), "scripts").State; state != CategoryPartial {
		t.Fatalf("scripts after cancel = %s", state)
	}
}

func TestWithdrawal_DuringAReadStopsItAndPublishesNothingOfThatSource(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	f.provider.set(func(p *fakeProvider) {
		p.entries["reaper_scripts"] = append(p.entries["reaper_scripts"],
			InventoryEntry{NativeID: "Two.lua", Kind: "script", Name: "Two", Facts: []Fact{}},
			InventoryEntry{NativeID: "Three.lua", Kind: "script", Name: "Three", Facts: []Fact{}})
	})
	scriptPages := 0
	f.provider.set(func(p *fakeProvider) {
		p.hook = func(_ context.Context, request InventoryRequest) error {
			if request.SourceID != "reaper_scripts" {
				return nil
			}
			scriptPages++
			if scriptPages == 1 {
				review, err := f.service.ReviewWithdraw("local", f.studio.ID, "reaper_scripts")
				if err != nil {
					return err
				}
				if _, err := f.service.CommitWithdraw("local", f.studio.ID, review.Token); err != nil {
					return err
				}
			}
			return nil
		}
	})
	op := f.refresh(t, "refresh-withdraw")
	if scriptPages != 1 {
		t.Fatalf("pages requested after withdrawal: %d", scriptPages)
	}
	for _, source := range op.Sources {
		if source.SourceID == "reaper_scripts" && source.State != OutcomeWithdrawn {
			t.Fatalf("withdrawn source state = %+v", source)
		}
	}
	rows := f.rows(t, Query{Category: "scripts"})
	if len(rows) != 1 || rows[0].Name != "Lyrics" || rows[0].State != AccessWithdrawn {
		t.Fatalf("scripts after withdrawal = %+v", rows)
	}
	view := f.overview(t)
	if scripts := categoryNamed(view, "scripts"); scripts.State != CategoryAccessWithdrawn || scripts.Counts.Withdrawn != 1 || scripts.Counts.Present != 0 {
		t.Fatalf("scripts category = %+v", scripts)
	}
	// Reading it again needs a new review: refresh leaves it out.
	f.provider.set(func(p *fakeProvider) { p.hook = nil; p.calls = nil })
	f.refresh(t, "refresh-after-withdraw")
	for _, call := range f.provider.calls {
		if call.SourceID == "reaper_scripts" {
			t.Fatal("a refresh read a withdrawn source")
		}
	}
}

func TestInspection_IsSingleFlightAndIdempotent(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	release := make(chan struct{})
	f.provider.set(func(p *fakeProvider) {
		p.hook = func(context.Context, InventoryRequest) error { <-release; return nil }
	})
	first, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "same-key", ModeRefresh, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "same-key", ModeRefresh, nil)
	if err != nil || again.ID != first.ID {
		t.Fatalf("same key = %+v, %v", again, err)
	}
	if _, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "other-key", ModeRefresh, nil); !errors.Is(err, ErrInspectionActive) {
		t.Fatalf("second inspection err = %v", err)
	}
	close(release)
	f.service.wait(f.studio.ID)
	if replay, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "same-key", ModeRefresh, nil); err != nil || replay.ID != first.ID {
		t.Fatalf("replay after finish = %+v, %v", replay, err)
	}
}

func TestScope_AChangedSourceOrInstallationNeedsANewReview(t *testing.T) {
	f := newCatalogFixture(t)
	review, err := f.service.ReviewSources(context.Background(), "local", f.studio.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The source list changes between review and commit: the commit is stale.
	f.provider.set(func(p *fakeProvider) { p.sources[0].Location = "~/Elsewhere/ProjectTemplates" })
	if _, err := f.service.CommitSources(context.Background(), "local", f.studio.ID, review.Token, "approve", []string{"project_templates"}); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("stale commit err = %v", err)
	}
	f.approveAll(t)
	// A moved source needs review before a refresh reads it.
	f.provider.set(func(p *fakeProvider) { p.sources[1].Location = "~/Moved/Scripts" })
	if _, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "refresh-moved", ModeRefresh, nil); !errors.Is(err, ErrScopeReviewRequired) {
		t.Fatalf("moved source err = %v", err)
	}
	// Another installation needs a full review, and its approval leaves the
	// old installation's entries as history.
	f.provider.set(func(p *fakeProvider) {
		p.sources[1].Location = "~/Library/Application Support/REAPER/Scripts"
		p.installation.ID = "inst_machine_b2"
	})
	if _, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "refresh-other", ModeRefresh, nil); !errors.Is(err, ErrScopeReviewRequired) {
		t.Fatalf("other installation err = %v", err)
	}
	review, err = f.service.ReviewSources(context.Background(), "local", f.studio.ID)
	if err != nil || !review.ReplacesInstallation {
		t.Fatalf("review for another installation = %+v, %v", review, err)
	}
	if _, err := f.service.CommitSources(context.Background(), "local", f.studio.ID, review.Token, "approve-b", []string{"reaper_scripts"}); err != nil {
		t.Fatal(err)
	}
	f.service.wait(f.studio.ID)
	rows := f.rows(t, Query{Category: "templates"})
	if len(rows) != 2 || rows[0].State != AccessHistory {
		t.Fatalf("old installation's templates = %+v", rows)
	}
}

func TestRestart_InterruptsARunningInspectionAndPublishesNothing(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	release := make(chan struct{})
	f.provider.set(func(p *fakeProvider) {
		p.hook = func(context.Context, InventoryRequest) error { <-release; return errors.New("process ended") }
	})
	op, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "refresh-restart", ModeRefresh, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted := f.newService(f.dataDir)
	interrupted, err := restarted.Operation("local", f.studio.ID, op.ID)
	if err != nil || interrupted.State != OperationInterrupted {
		t.Fatalf("after restart = %+v, %v", interrupted, err)
	}
	close(release)
	f.service.wait(f.studio.ID)
	if view := f.overview(t); view.Revision < 1 {
		t.Fatalf("revision = %d", view.Revision)
	}
}

func TestOwnerView_WorksWithoutTheProviderAndOnAnotherComputer(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	f.provider.set(func(p *fakeProvider) { p.status = ProviderStatus{Reason: ReasonProviderDisabled} })
	view := f.overview(t)
	if view.CanInspect || view.Provider.Available || view.Provider.Reason != ReasonProviderDisabled ||
		categoryNamed(view, "templates").Label != "Templates" || categoryNamed(view, "templates").Counts.Present != 2 {
		t.Fatalf("offline overview = %+v", view)
	}
	if _, err := f.service.ReviewSources(context.Background(), "local", f.studio.ID); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("review without the provider err = %v", err)
	}
	// Another computer: same workspace folder, its own data directory.
	other := f.newService(t.TempDir())
	otherView, err := other.Overview("local", f.studio.ID)
	if err != nil {
		t.Fatal(err)
	}
	if otherView.Approved || categoryNamed(otherView, "templates").State != CategoryHistory || categoryNamed(otherView, "templates").Counts.History != 2 {
		t.Fatalf("another computer = %+v", otherView)
	}
	if _, err := other.StartInspection(context.Background(), "local", f.studio.ID, "refresh-there", ModeRefresh, nil); err == nil {
		t.Fatal("another computer reused this computer's approval")
	}
}

func TestOwnerView_RefusesAnotherOwnerAndANonResourceWorkspace(t *testing.T) {
	f := newCatalogFixture(t)
	if _, err := f.service.Overview("someone-else", f.studio.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner err = %v", err)
	}
	if _, err := f.service.Overview("local", f.home.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the Home err = %v", err)
	}
	if _, err := f.service.Overview("local", "../etc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a path err = %v", err)
	}
}

func TestEntries_SearchesTagsAndNotesAndStalesCursors(t *testing.T) {
	f := newCatalogFixture(t)
	f.approveAll(t)
	folder, _ := f.store.GetFolderPath(f.studio.ID)
	p, _ := studioPaths(folder, f.dataDir, f.studio.ID)
	lyrics := EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua")
	if err := writeDocument(p.annotations(), Annotations{SchemaVersion: 1, Revision: 1, Annotations: map[string]Annotation{
		lyrics: {Favorite: true, Tags: []string{"live"}, Note: "Use on the vocal bus.", Revision: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	if rows := f.rows(t, Query{Q: "LIVE"}); len(rows) != 1 || rows[0].EntryID != lyrics || !rows[0].Favorite {
		t.Fatalf("tag search = %+v", rows)
	}
	if rows := f.rows(t, Query{Q: "vocal bus"}); len(rows) != 1 {
		t.Fatalf("note search = %+v", rows)
	}
	if rows := f.rows(t, Query{FavoritesOnly: true}); len(rows) != 1 {
		t.Fatalf("favorites = %+v", rows)
	}
	page, err := f.service.Entries("local", f.studio.ID, Query{Limit: 1})
	if err != nil || page.NextCursor == "" || page.Total != 3 {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	f.refresh(t, "refresh-cursor")
	if _, err := f.service.Entries("local", f.studio.ID, Query{Limit: 1, Cursor: page.NextCursor}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale cursor err = %v", err)
	}
	detail, err := f.service.Entry("local", f.studio.ID, lyrics)
	if err != nil || detail.Note != "Use on the vocal bus." || detail.SourceLocation == "" || detail.Identity != "Cockos/Lyrics.lua" {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
}
