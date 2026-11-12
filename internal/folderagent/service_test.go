package folderagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeFolders struct{ path string }

func (f fakeFolders) ChooseFolder(_ context.Context, _ string, c personalassistant.FolderChoice) (string, bool, error) {
	if c.Picker && f.path == "" {
		return "", false, nil
	}
	return f.path, true, nil
}

type fakeWorkspaces struct {
	mu       sync.Mutex
	sidecars string
	created  []CreateRequest
	anchors  map[string]string
	taken    map[string]bool
	refuse   error
	goals    map[string]string
}

func (w *fakeWorkspaces) CreateAnchored(_ context.Context, req CreateRequest) (Created, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refuse != nil {
		return Created{}, w.refuse
	}
	w.created = append(w.created, req)
	id := fmt.Sprintf("ws-%d", len(w.created))
	w.anchors[id] = req.AnchorPath
	w.taken[req.Name] = true
	return Created{ID: id, Name: req.Name, Slug: strings.ToLower(req.Name), AgentName: req.AgentName}, nil
}
func (w *fakeWorkspaces) NameTaken(_ context.Context, name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.taken[name]
}
func (w *fakeWorkspaces) AnchorRoot(id string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p, ok := w.anchors[id]; ok {
		return p, nil
	}
	return "", errors.New("gone")
}
func (w *fakeWorkspaces) IDs() ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var ids []string
	for id := range w.anchors {
		ids = append(ids, id)
	}
	return ids, nil
}
func (w *fakeWorkspaces) Exists(id string) bool  { _, err := w.AnchorRoot(id); return err == nil }
func (w *fakeWorkspaces) Route(id string) string { return "/workspaces/" + id }
func (w *fakeWorkspaces) AnchorOwners(folders []string, exclude string) map[string]WorkspaceRef {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]WorkspaceRef{}
	for _, folder := range folders {
		for id, anchor := range w.anchors {
			if id != exclude && anchor == folder {
				out[folder] = WorkspaceRef{ID: id, Name: id}
			}
		}
	}
	return out
}

func (w *fakeWorkspaces) SetGoal(id, goal string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.goals == nil {
		w.goals = map[string]string{}
	}
	w.goals[id] = goal
	return nil
}

func (w *fakeWorkspaces) Goal(id string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.goals[id]
}

func (t *fakeTickets) CreateBacklog(ws, title, details, agent string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.backlog = append(t.backlog, title+" · "+agent)
	return "next-" + ws, nil
}
func (t *fakeTickets) Start(ws, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = append(t.started, id)
	return nil
}

type fakeTickets struct {
	mu       sync.Mutex
	opened   []string
	finished map[string]string
	backlog  []string
	started  []string
}

func (t *fakeTickets) Open(ws, title, agent string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.opened = append(t.opened, title+" · "+agent)
	return "ticket-" + ws, nil
}
func (t *fakeTickets) Finish(ws, id, result string, failed bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finished[id] = fmt.Sprintf("%v|%s", failed, result)
	return nil
}

type fakeSetups struct {
	mu      sync.Mutex
	runs    []Plan
	receipt *SetupReceipt
	// model is what HomeHireModel reports; "" is "no model can run".
	model string
}

func (f *fakeSetups) Readiness(context.Context, string) folderlook.Readiness {
	return folderlook.Readiness{}
}

// HomeHireModel makes the fake a HomeHireModeler.
func (f *fakeSetups) HomeHireModel() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.model
}

func (f *fakeSetups) setModel(model string) {
	f.mu.Lock()
	f.model = model
	f.mu.Unlock()
}
func (f *fakeSetups) Run(_ context.Context, _ Record, plan Plan, update func([]personalassistant.FolderPlanLine)) RunResult {
	f.mu.Lock()
	f.runs = append(f.runs, plan)
	f.mu.Unlock()
	lines := append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
	for i := range lines {
		lines[i].State = personalassistant.FolderLineDone
	}
	update(lines)
	return RunResult{Status: personalassistant.FolderSetupDone, Receipt: f.receipt}
}

type harness struct {
	svc     *Service
	ws      *fakeWorkspaces
	tickets *fakeTickets
	setups  *fakeSetups
}

func newHarness(t *testing.T, folder string) *harness {
	t.Helper()
	sidecars := t.TempDir()
	ws := &fakeWorkspaces{sidecars: sidecars, anchors: map[string]string{}, taken: map[string]bool{}}
	tickets := &fakeTickets{finished: map[string]string{}}
	setups := &fakeSetups{}
	store := NewStore(func(id string) (string, error) { return filepath.Join(sidecars, id), nil })
	downloads := filepath.Join(t.TempDir(), "Downloads")
	if err := os.Mkdir(downloads, 0o750); err != nil {
		t.Fatal(err)
	}
	svc := NewService(Deps{Store: store, Folders: fakeFolders{path: folder}, Workspaces: ws, Tickets: tickets, Setups: setups,
		KnownFolderRoot: func(string) (string, error) { return downloads, nil }})
	return &harness{svc: svc, ws: ws, tickets: tickets, setups: setups}
}

func build(t *testing.T, paths ...string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Ori Music Demo")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, rel := range paths {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			_ = os.MkdirAll(full, 0o750)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(full), 0o750)
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func demo(t *testing.T) string {
	return build(t, "Albums/Album-1/Song.rpp", "Albums/Album-2/Song.rpp", "Albums/Album-3/Song.rpp",
		"Albums/Album-4/Song.rpp", "Albums/Album-5/Song.rpp", "Albums/Album-5/Alternate.rpp",
		"Albums/Logic Sketch/Sketch.logicx/ProjectData", "Picker Songs/Track-1/Song.rpp", "Picker Songs/Track-2/Song.rpp", "Filed/")
}

func (h *harness) start(t *testing.T, brief string) View {
	t.Helper()
	chosen, err := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if err != nil || chosen == nil {
		t.Fatalf("choose: %v %v", chosen, err)
	}
	view, err := h.svc.Start(context.Background(), "u1", chosen.Token, brief, Hire{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	h.svc.Wait()
	return view
}

func TestStart_DemoFolderCreatesWorkspaceAgentTicketAndReport(t *testing.T) {
	h := newHarness(t, demo(t))
	chosen, err := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Title != "Start an agent on Ori Music Demo" || chosen.Brief != DefaultBrief || chosen.Action != "Hire & start" || chosen.BriefMax != personalassistant.BriefMaxRunes {
		t.Fatalf("card = %+v", chosen)
	}
	view, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{})
	if err != nil {
		t.Fatal(err)
	}
	if view.State != StateLooking || view.Receipt != "Looking around…" || view.AgentName != "Ori Music Demo Manager" {
		t.Fatalf("receipt view = %+v", view)
	}
	h.svc.Wait()
	req := h.ws.created[0]
	// The brief stays the agent's instruction; the workspace starts with no goal.
	if req.Name != "Ori Music Demo" || req.AgentName != "Ori Music Demo Manager" || req.Mission != "" || !filepath.IsAbs(req.AnchorPath) {
		t.Fatalf("create = %+v", req)
	}
	if rec, _ := h.svc.Record("u1", view.WorkspaceID); rec.Brief != DefaultBrief {
		t.Fatalf("brief on the record = %q", rec.Brief)
	}
	if h.tickets.opened[0] != "Look around Ori Music Demo · Ori Music Demo Manager" {
		t.Fatalf("ticket = %v", h.tickets.opened)
	}
	report, err := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != StateReported || report.Stage != foldermap.StageHome {
		t.Fatalf("report = %+v", report)
	}
	if report.FactsLine != "7 REAPER songs · 1 Logic Pro song · 2 folders with songs · 1 empty folder" {
		t.Fatalf("facts line = %q", report.FactsLine)
	}
	// No Home yet: the folder becomes a place by default; the Home is offered.
	if report.StageLine != "Set it up: Turn Ori Music Demo into a group with one workspace per song (8) · Install REAPER tools and connect them to Ori Music Demo" {
		t.Fatalf("stage line = %q", report.StageLine)
	}
	if report.ModelSentence != "Ori Music Demo Manager could not look itself: No model is set for Ori Music Demo Manager. "+
		"Fix it and press Look further. This is what Ori found." ||
		report.ToolsSentence != "Search for more tools needs a model" {
		t.Fatalf("degraded sentences = %q / %q", report.ModelSentence, report.ToolsSentence)
	}
	if strings.Join(report.Actions, ",") != "set_up,keep,look_further" {
		t.Fatalf("actions = %v", report.Actions)
	}
	if got := h.tickets.finished["ticket-"+view.WorkspaceID]; !strings.HasPrefix(got, "false|7 REAPER songs") {
		t.Fatalf("ticket result = %q", got)
	}
	// The record never holds the anchor's absolute path.
	raw, _ := os.ReadFile(filepath.Join(h.ws.sidecars, view.WorkspaceID, ".ori", recordFile))
	if strings.Contains(string(raw), req.AnchorPath) {
		t.Fatal("the record holds the anchor's absolute path")
	}
}

func TestStart_SingleSongIsTheToolsStageAndSetsUp(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp", "Audio/kick.wav"))
	view := h.start(t, "Mix this song")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if report.Stage != foldermap.StageTools || report.Plan == nil || !report.Plan.Runnable {
		t.Fatalf("report = %+v plan %+v", report, report.Plan)
	}
	if report.StageLine != "Set it up: Install REAPER tools and connect them to Ori Music Demo" {
		t.Fatalf("stage line = %q", report.StageLine)
	}
	if rec, _ := h.svc.Record("u1", view.WorkspaceID); rec.Brief != "Mix this song" || h.ws.created[0].Mission != "" {
		t.Fatalf("brief = %q, workspace goal = %q", rec.Brief, h.ws.created[0].Mission)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: "stale"}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("stale digest = %v", err)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	done, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if done.State != StateSetUp || done.Setup.Status != personalassistant.FolderSetupDone || len(h.setups.runs) != 1 {
		t.Fatalf("after set up: %+v", done)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); !errors.Is(err, ErrWrongState) {
		t.Fatalf("second set up = %v", err)
	}
}

// A plan whose installs changed between the card and Set it up is refused,
// and the fresh view carries the new install block to confirm (PRD FR21).
func TestSetUp_RefusesAPlanWhoseInstallsChanged(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp"))
	view := h.start(t, "")
	shown, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if len(shown.Plan.Installs) != 1 || shown.Plan.Installs[0].Name != "REAPER tools" || shown.Plan.Installs[0].Version != "" {
		t.Fatalf("installs shown = %+v", shown.Plan.Installs)
	}
	// The reviewed release moved on after the card was drawn.
	if _, err := h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error {
		for i := range r.Candidates {
			if r.Candidates[i].Kind == folderlook.KindIntegration {
				r.Candidates[i].Release = &folderlook.ReleaseFacts{Version: "0.9.2", Publisher: "Ori"}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: shown.Plan.Digest}); !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("set up on the old installs = %v", err)
	}
	fresh, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if fresh.Plan.Digest == shown.Plan.Digest || len(fresh.Plan.Installs) != 1 || fresh.Plan.Installs[0].Version != "0.9.2" {
		t.Fatalf("fresh plan = %+v", fresh.Plan)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: fresh.Plan.Digest}); err != nil {
		t.Fatalf("set up on the fresh plan = %v", err)
	}
	h.svc.Wait()
}

func TestStart_EveryStagePlanIsWorded(t *testing.T) {
	var dump []string
	for i, kind := range strings.Split(strings.Repeat("pdf jpg zip docx dmg ", 4), " ") {
		if kind != "" {
			dump = append(dump, fmt.Sprintf("file%02d.%s", i, kind))
		}
	}
	// A folder of loose files: File Janitor in place with File Curator.
	h := newHarness(t, build(t, dump...))
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if !report.Plan.Runnable || report.Stage != foldermap.StageTools || report.Plan.Unavailable != "" {
		t.Fatalf("File Curator plan: %+v", report.Plan)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatalf("set up = %v", err)
	}
	h.svc.Wait()
	if len(h.setups.runs) != 1 || h.setups.runs[0].Candidates[0].Kind != folderlook.KindFileCurator {
		t.Fatalf("runs = %+v", h.setups.runs)
	}
	// A portfolio without a Home yet becomes a place by default: a group of
	// its songs, with its tools. The Home is offered, not chosen.
	home := newHarness(t, demo(t))
	hv := home.start(t, "")
	hr, _ := home.svc.View(context.Background(), "u1", hv.WorkspaceID)
	var lines []string
	for _, l := range hr.Plan.Lines {
		lines = append(lines, l.Name)
	}
	want := []string{"Turns Ori Music Demo into a group",
		"Creates Album-1, linked to its own folder", "Creates Album-2, linked to its own folder", "Creates Album-3, linked to its own folder",
		"Creates Album-4, linked to its own folder", "Creates Album-5, linked to its own folder", "Creates Logic Sketch, linked to its own folder",
		"Creates Track-1, linked to its own folder", "Creates Track-2, linked to its own folder",
		"Installs REAPER tools", "Connects REAPER tools to Ori Music Demo", "Nothing in Ori Music Demo is moved, copied or read. Ori adds 1 plugin."}
	if !hr.Plan.Runnable || hr.Plan.Stage != foldermap.StageGroup || strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("default plan stage=%q runnable=%v lines=%v", hr.Plan.Stage, hr.Plan.Runnable, lines)
	}
	offeredHome := false
	for _, s := range hr.Plan.Steps {
		if s.Kind == folderlook.StepHome {
			offeredHome = !s.Chosen && !s.Default
		}
	}
	if !offeredHome {
		t.Fatalf("the Home must be offered, unchosen: %+v", hr.Plan.Steps)
	}
	empty := newHarness(t, build(t, "notes.txt"))
	ev := empty.start(t, "")
	er, _ := empty.svc.View(context.Background(), "u1", ev.WorkspaceID)
	if er.Stage != foldermap.StageFolder || er.StageLine != "Nothing to set up yet: Ori found no project it knows here." {
		t.Fatalf("folder stage = %+v", er)
	}
	if _, err := empty.svc.SetUp(context.Background(), "u1", ev.WorkspaceID, SetUpRequest{PlanDigest: er.Plan.Digest}); !errors.Is(err, ErrNothingToSet) {
		t.Fatalf("set up = %v", err)
	}
}

func TestStart_RefusalsPassThroughAndNothingIsRecorded(t *testing.T) {
	h := newHarness(t, demo(t))
	h.ws.refuse = &Refusal{Code: "anchor_already_owned", Message: `This folder already belongs to the workspace "Songs".`, Status: 409,
		Duplicate: &WorkspaceRef{ID: "ws-x", Name: "Songs"}}
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	_, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Duplicate == nil || refusal.Code != "anchor_already_owned" {
		t.Fatalf("err = %v", err)
	}
	if len(h.tickets.opened) != 0 {
		t.Fatal("a refused start opens no ticket")
	}
	// The choice survives a refusal, so the card can try again.
	h.ws.refuse = nil
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{}); err != nil {
		t.Fatalf("retry after refusal: %v", err)
	}
	h.svc.Wait()
}

func TestStart_ChoiceTokenIsOneUseAndPerUser(t *testing.T) {
	h := newHarness(t, demo(t))
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if _, err := h.svc.Start(context.Background(), "u2", chosen.Token, "", Hire{}); !errors.Is(err, ErrChoiceLost) {
		t.Fatalf("other user = %v", err)
	}
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{}); !errors.Is(err, ErrChoiceLost) {
		t.Fatalf("reused token = %v", err)
	}
	if c, err := newHarness(t, "").svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true}); c != nil || err != nil {
		t.Fatalf("cancelled dialog = %v %v", c, err)
	}
}

func TestStart_BriefWithAPathIsRefused(t *testing.T) {
	h := newHarness(t, demo(t))
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	long := strings.Repeat("a", personalassistant.BriefMaxRunes+1)
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, long, Hire{}); !errors.Is(err, personalassistant.ErrValidation) {
		t.Fatalf("long brief = %v", err)
	}
}

func TestStart_SameFolderNameGetsASuffix(t *testing.T) {
	h := newHarness(t, demo(t))
	h.ws.taken["Ori Music Demo"] = true
	h.ws.taken["Ori Music Demo 2"] = true
	h.start(t, "")
	if h.ws.created[0].Name != "Ori Music Demo 3" || h.ws.created[0].AgentName != "Ori Music Demo Manager" {
		t.Fatalf("create = %+v", h.ws.created[0])
	}
}

func TestKeepAndLookFurther(t *testing.T) {
	var paths []string
	for i := range 12 {
		for j := range 4 {
			paths = append(paths, fmt.Sprintf("Box %02d/Item %d/file.txt", i, j))
		}
	}
	paths = append(paths, "Box 11/Item 3/Deep/Song.rpp")
	h := newHarness(t, build(t, paths...))
	h.svc.deps.Walk = foldermap.Options{MaxEntries: 10}
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if len(report.NextFolders) == 0 || len(report.NextFolders) > MaxNextFolders || report.Stage != foldermap.StageFolder {
		t.Fatalf("tripped report = %+v", report)
	}
	for _, name := range report.NextFolders {
		if strings.Contains(name, "/") {
			t.Fatalf("next folders name paths: %v", report.NextFolders)
		}
	}
	kept, err := h.svc.Keep(context.Background(), "u1", view.WorkspaceID)
	if err != nil || kept.State != StateKept {
		t.Fatalf("keep = %+v %v", kept, err)
	}
	if _, err := h.svc.Keep(context.Background(), "u1", view.WorkspaceID); !errors.Is(err, ErrWrongState) {
		t.Fatalf("second keep = %v", err)
	}
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, []string{"../x"}, ""); err == nil {
		t.Fatal("an escaping folder must be refused")
	}
	h.svc.deps.Walk = foldermap.Options{}
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, []string{"Box 11"}, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	after, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if after.State != StateReported || after.Stage != foldermap.StageTools || len(after.NextFolders) != 0 {
		t.Fatalf("after look further: %+v", after)
	}
}

func TestView_OtherUsersRecordIsRefused(t *testing.T) {
	h := newHarness(t, demo(t))
	view := h.start(t, "")
	if _, err := h.svc.View(context.Background(), "u2", view.WorkspaceID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("other user = %v", err)
	}
	if got := h.svc.List(context.Background(), "u2"); len(got) != 0 {
		t.Fatalf("list for other user = %+v", got)
	}
	if got := h.svc.List(context.Background(), "u1"); len(got) != 1 {
		t.Fatalf("list = %+v", got)
	}
}

func TestLook_VanishedFolderFails(t *testing.T) {
	h := newHarness(t, demo(t))
	view := h.start(t, "")
	// The anchored folder is gone; a fresh look (no map yet) fails, says so,
	// and offers to look again.
	h.ws.mu.Lock()
	h.ws.anchors[view.WorkspaceID] = filepath.Join(t.TempDir(), "missing")
	h.ws.mu.Unlock()
	_, _ = h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error { r.Map = nil; return nil })
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	failed, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if failed.State != StateFailed || failed.Failure == "" || strings.Join(failed.Actions, ",") != ActionLookFurther {
		t.Fatalf("failed view = %+v", failed)
	}
}

func TestSweepInterrupted(t *testing.T) {
	h := newHarness(t, demo(t))
	view := h.start(t, "")
	_, _ = h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error { r.State = StateLooking; return nil })
	if n := h.svc.SweepInterrupted(); n != 1 {
		t.Fatalf("closed = %d", n)
	}
	after, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if after.State != StateReported || !strings.Contains(after.ModelSentence, "interrupted") {
		t.Fatalf("after sweep: %+v", after)
	}
}

func TestProgressFolder_BaseNameOnlyBoundedAndClean(t *testing.T) {
	long := strings.Repeat("é", 120)
	cases := []struct{ rel, fallback, want string }{
		{"Album-3", "Albums", "Album-3"},
		{"Album-3/Stems/Drums", "Albums", "Drums"},
		{"", "Albums", "Albums"},
		{".", "Albums", "Albums"},
		{"", "", ""},
		{"/Users/someone/Music/Albums/Song", "", "Song"},
		{`Old\Takes`, "", "Takes"},
		{"Bad\x00Na" + string(rune(0x202e)) + "me\n", "", "BadName"},
		{long, "", strings.Repeat("é", progressFolderMax)},
	}
	for _, c := range cases {
		got := ProgressFolder(c.rel, c.fallback)
		if got != c.want {
			t.Errorf("ProgressFolder(%q, %q) = %q, want %q", c.rel, c.fallback, got, c.want)
		}
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("ProgressFolder(%q) kept a path: %q", c.rel, got)
		}
	}
}

func TestProgress_FolderOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(Progress{Entries: 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "folder\"") || strings.Contains(string(raw), "turn") {
		t.Fatalf("empty narration leaked: %s", raw)
	}
	raw, _ = json.Marshal(Progress{Entries: 3, Opened: 2, Folder: "Album-3", Turn: true})
	if !strings.Contains(string(raw), `"folder":"Album-3"`) || !strings.Contains(string(raw), `"opened":2`) || !strings.Contains(string(raw), `"turn":true`) {
		t.Fatalf("narration missing: %s", raw)
	}
}

func TestProgressLine(t *testing.T) {
	cases := map[Progress]string{
		{}:                             "Looking around…",
		{Entries: 240, FoldersLeft: 3}: "Looking around · 240 entries · 3 folders left",
		{Entries: 1}:                   "Looking around · 1 entry",
	}
	for p, want := range cases {
		if got := ProgressLine(p); got != want {
			t.Errorf("ProgressLine(%+v) = %q, want %q", p, got, want)
		}
	}
}
