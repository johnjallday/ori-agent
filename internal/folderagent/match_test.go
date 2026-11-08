package folderagent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeMatches struct {
	mu    sync.Mutex
	words [][]string
	hits  []folderlook.SearchHit
}

func (f *fakeMatches) Search(_ context.Context, words []string) []folderlook.SearchHit {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.words = append(f.words, append([]string(nil), words...))
	return f.hits
}

func (f *fakeMatches) Installed(context.Context) folderlook.Installed {
	return func(kind, id string) bool { return kind == folderlook.KindMCP && id == "notes-mcp" }
}

type fakePins struct {
	mu       sync.Mutex
	pins     []string
	released []string
}

func (p *fakePins) Pin(ws, agent string, read bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pins = append(p.pins, agent+map[bool]string{true: "+read", false: ""}[read])
	return nil
}

func (p *fakePins) Release(ws, agent string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.released = append(p.released, agent)
	return nil
}

var songHits = []folderlook.SearchHit{
	{Kind: folderlook.KindSkill, ID: "acme/music@mix-notes", Name: "mix-notes", Description: "acme/music · 120 installs", Marketplace: "skills.sh"},
	{Kind: folderlook.KindPlugin, ID: "community/release-planner", Name: "release-planner", Marketplace: "community"},
	{Kind: folderlook.KindMCP, ID: "notes-mcp", Name: "notes-mcp", Marketplace: "MCP Registry"},
}

const rankingReport = `What it is: One REAPER song.
Tools:
- skill:acme/music@mix-notes — Keeps the mix notes for this song.
- mcp:notes-mcp — Already here, keeps notes.
- plugin:invented — not a candidate
Suggested goal: Finish the mix.
Suggested first ticket: List what the mix still needs.
Why.
Next: List what the mix still needs.`

func TestMatch_SearchRunsOnlyWithAModelAndWithTheTablesWords(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp"))
	matches := &fakeMatches{hits: songHits}
	h.svc.deps.Matches = matches
	view := h.start(t, "")
	if len(matches.words) != 0 {
		t.Fatalf("searched without a model: %v", matches.words)
	}
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if report.ToolsSentence != "Search for more tools needs a model" {
		t.Fatalf("tools sentence = %q", report.ToolsSentence)
	}

	h2 := newHarness(t, build(t, "Song.rpp"))
	h2.svc.deps.Matches = matches
	withTurns(h2, &scriptedTurns{responses: []*llm.ChatResponse{{Content: rankingReport}}})
	h2.start(t, "")
	if len(matches.words) != 1 || strings.Join(matches.words[0], ",") != "REAPER,audio" {
		t.Fatalf("search words = %v", matches.words)
	}
}

func TestMatch_RankedCandidatesArePreselectedAndInThePlan(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp"))
	h.svc.deps.Matches = &fakeMatches{hits: songHits}
	pins := &fakePins{}
	h.svc.deps.Pins = pins
	turns := &scriptedTurns{responses: []*llm.ChatResponse{{Content: rankingReport}}}
	withTurns(h, turns)
	view := h.start(t, "")
	if !strings.Contains(turns.references[0], "skill:acme/music@mix-notes · mix-notes") {
		t.Fatalf("the reference must list the search candidates: %s", turns.references[0])
	}
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	var shown []string
	for _, c := range report.Candidates {
		shown = append(shown, c.ID+"|"+c.Action+"|"+map[bool]string{true: "ranked"}[c.Ranked])
	}
	// Required first, then the agent's ranked ones; the unranked plugin is
	// left out, and the invented id never appears.
	want := "integration:ori_reaper|install|,skill:acme/music@mix-notes|add|ranked,mcp:notes-mcp|already_here|ranked"
	if strings.Join(shown, ",") != want {
		t.Fatalf("candidates = %v", shown)
	}
	var lines []string
	for _, l := range report.Plan.Lines {
		lines = append(lines, l.Name)
	}
	if !contains(lines, "Adds skill mix-notes · found by search") || contains(lines, "Adds notes-mcp · found by search") {
		t.Fatalf("plan lines = %v", lines)
	}
	// The plan's steps by handle: the tools and the ranked skill.
	if strings.Join(report.Plan.Selected, ",") != "mix-notes,reaper-tools" {
		t.Fatalf("default selection = %v", report.Plan.Selected)
	}

	// Unticking the ranked skill: the owner's ticks name the tools only, the
	// plan has no add-on, and Set it up records the decline so a later look
	// does not propose it again.
	plain, err := h.svc.Plan("u1", view.WorkspaceID, []string{"reaper-tools"})
	if err != nil || strings.Join(plain.Selected, ",") != "reaper-tools" || plain.Digest == report.Plan.Digest {
		t.Fatalf("plan without the skill = %+v %v", plain, err)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: plain.Digest, Selected: []string{"reaper-tools"}}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	if strings.Join(rec.Declined, ",") != "skill:acme/music@mix-notes" {
		t.Fatalf("declined = %v", rec.Declined)
	}
	if len(h.setups.runs) != 1 || len(h.setups.runs[0].Candidates) != 1 {
		t.Fatalf("the run carried %d candidates", len(h.setups.runs[0].Candidates))
	}
	if len(pins.released) != 1 || pins.pins[0] != "Ori Music Demo Manager" || pins.pins[len(pins.pins)-1] != "Ori Music Demo Manager+read" {
		t.Fatalf("pins = %v released = %v", pins.pins, pins.released)
	}

	turns.responses = []*llm.ChatResponse{{Content: rankingReport}}
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if strings.Contains(turns.references[1], "skill:acme/music@mix-notes") {
		t.Fatal("a declined candidate was proposed again")
	}
}

func TestMatch_SelectedAddOnRunsThroughSetUp(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp"))
	h.svc.deps.Matches = &fakeMatches{hits: songHits}
	withTurns(h, &scriptedTurns{responses: []*llm.ChatResponse{{Content: rankingReport}}})
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	run := h.setups.runs[0]
	var kinds []string
	for _, c := range run.Candidates {
		kinds = append(kinds, c.Kind)
	}
	if strings.Join(kinds, ",") != "integration,skill" {
		t.Fatalf("run candidates = %v", kinds)
	}
	done, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if done.Setup == nil || done.Setup.Status != personalassistant.FolderSetupDone {
		t.Fatalf("setup = %+v", done.Setup)
	}
}

func TestStageLine_ToolsStageWithoutARequiredTool(t *testing.T) {
	facts := foldermap.Facts{RootProject: &foldermap.Project{Shape: "manuscript", Marker: "main.tex"}}
	optional := []folderlook.Candidate{{ID: "skill:a@b", Kind: folderlook.KindSkill, Name: "Cite", Action: folderlook.ActionAdd}}
	if got := StageLine("Thesis", facts, optional); got != "Set it up: adds the tools you leave selected below" {
		t.Fatalf("with an optional tool = %q", got)
	}
	// Ranked by the agent, the add-on is in Ori's default plan.
	optional[0].Ranked = true
	if got := StageLine("Thesis", facts, optional); got != "Set it up: Add the skill Cite" {
		t.Fatalf("with a ranked tool = %q", got)
	}
	if got := StageLine("Thesis", facts, nil); got != "Nothing to set up for this project yet." {
		t.Fatalf("without = %q", got)
	}
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}
