package folderagent

import (
	"context"
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
	mu   sync.Mutex
	runs []Plan
}

func (f *fakeSetups) Readiness(context.Context, string) folderlook.Readiness {
	return folderlook.Readiness{}
}
func (f *fakeSetups) Run(_ context.Context, _ Record, plan Plan, update func([]personalassistant.FolderPlanLine)) (string, string) {
	f.mu.Lock()
	f.runs = append(f.runs, plan)
	f.mu.Unlock()
	lines := append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
	for i := range lines {
		lines[i].State = personalassistant.FolderLineDone
	}
	update(lines)
	return personalassistant.FolderSetupDone, ""
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
	svc := NewService(Deps{Store: store, Folders: fakeFolders{path: folder}, Workspaces: ws, Tickets: tickets, Setups: setups})
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
	view, err := h.svc.Start(context.Background(), "u1", chosen.Token, brief)
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
	if chosen.Title != "Start an agent on Ori Music Demo" || chosen.Brief != DefaultBrief || chosen.Action != "Start" || chosen.BriefMax != personalassistant.BriefMaxRunes {
		t.Fatalf("card = %+v", chosen)
	}
	view, err := h.svc.Start(context.Background(), "u1", chosen.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.State != StateLooking || view.Receipt != "Looking around…" || view.AgentName != "Ori Music Demo Manager" {
		t.Fatalf("receipt view = %+v", view)
	}
	h.svc.Wait()
	req := h.ws.created[0]
	if req.Name != "Ori Music Demo" || req.AgentName != "Ori Music Demo Manager" || req.Mission != DefaultBrief || !filepath.IsAbs(req.AnchorPath) {
		t.Fatalf("create = %+v", req)
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
	if report.StageLine != "Set it up: creates your Music Production Home, installs REAPER tools" {
		t.Fatalf("stage line = %q", report.StageLine)
	}
	if !strings.HasPrefix(report.ModelSentence, "No model is set up, so Ori Music Demo Manager could not look itself.") ||
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
	if report.StageLine != "Set it up: installs REAPER tools" {
		t.Fatalf("stage line = %q", report.StageLine)
	}
	if h.ws.created[0].Mission != "Mix this song" {
		t.Fatalf("brief = %q", h.ws.created[0].Mission)
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

func TestStart_NotYetStagesRefuseSetUp(t *testing.T) {
	var dump []string
	for i, kind := range strings.Split(strings.Repeat("pdf jpg zip docx dmg ", 4), " ") {
		if kind != "" {
			dump = append(dump, fmt.Sprintf("file%02d.%s", i, kind))
		}
	}
	h := newHarness(t, build(t, dump...))
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if report.Plan.Runnable || report.Stage != foldermap.StageTools || report.Plan.Unavailable == "" {
		t.Fatalf("File Curator from a folder is not runnable yet: %+v", report.Plan)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); !errors.Is(err, ErrStageNotYet) {
		t.Fatalf("set up = %v", err)
	}
	// The Home stage is runnable, with its own lines.
	home := newHarness(t, demo(t))
	hv := home.start(t, "")
	hr, _ := home.svc.View(context.Background(), "u1", hv.WorkspaceID)
	var lines []string
	for _, l := range hr.Plan.Lines {
		lines = append(lines, l.Name)
	}
	want := []string{"Creates your Music Production Home", "Adds Ori Music Demo to your Music Production Home",
		"Lists 8 songs on its shelf", "Looks into 2 folders for songs", "Places Ori Music Demo inside your Music Production Home",
		"Installs REAPER tools", "Connects REAPER tools to Ori Music Demo", "Nothing is moved, copied or read"}
	if !hr.Plan.Runnable || strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("home plan runnable=%v lines=%v", hr.Plan.Runnable, lines)
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
	_, err := h.svc.Start(context.Background(), "u1", chosen.Token, "")
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Duplicate == nil || refusal.Code != "anchor_already_owned" {
		t.Fatalf("err = %v", err)
	}
	if len(h.tickets.opened) != 0 {
		t.Fatal("a refused start opens no ticket")
	}
	// The choice survives a refusal, so the card can try again.
	h.ws.refuse = nil
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, ""); err != nil {
		t.Fatalf("retry after refusal: %v", err)
	}
	h.svc.Wait()
}

func TestStart_ChoiceTokenIsOneUseAndPerUser(t *testing.T) {
	h := newHarness(t, demo(t))
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if _, err := h.svc.Start(context.Background(), "u2", chosen.Token, ""); !errors.Is(err, ErrChoiceLost) {
		t.Fatalf("other user = %v", err)
	}
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, ""); !errors.Is(err, ErrChoiceLost) {
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
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, long); !errors.Is(err, personalassistant.ErrValidation) {
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
