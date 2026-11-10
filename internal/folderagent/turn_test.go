package folderagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// scriptedTurns answers the turn's model calls from a script and records the
// reference material each turn was given.
type scriptedTurns struct {
	mu         sync.Mutex
	err        error
	chatErr    error
	responses  []*llm.ChatResponse
	references []string
}

func (s *scriptedTurns) Model(context.Context, string, string) (folderlook.Chat, folderlook.ModelRef, error) {
	if s.err != nil {
		return nil, folderlook.ModelRef{}, s.err
	}
	return func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.chatErr != nil {
			return nil, s.chatErr
		}
		if len(req.Messages) == 1 {
			s.references = append(s.references, req.Messages[0].Content)
		}
		if len(s.responses) == 0 {
			return &llm.ChatResponse{Content: "What it is: Nothing more to say."}, nil
		}
		next := s.responses[0]
		s.responses = s.responses[1:]
		return next, nil
	}, folderlook.ModelRef{Provider: "Anthropic", Model: "luna-test"}, nil
}

func (s *scriptedTurns) TokenBudget() int { return 0 }

type fakeMemory struct {
	mu      sync.Mutex
	entries map[string]string // provenance → text, one workspace
}

func (m *fakeMemory) Remember(_, text, provenance string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, old := range m.entries {
		if strings.HasPrefix(key, folderlook.SourceFirstLookModel) && strings.HasPrefix(old, MemoryTitle) {
			delete(m.entries, key)
		}
	}
	m.entries[provenance] = text
	return nil
}

const demoReport = `What it is: Seven songs and a sketch across two album folders.
Tools:
- integration:ori_reaper — Lets the agent work with the sessions.
- photoshop — not a candidate
Suggested goal: Keep these songs moving toward release.
Suggested first ticket: List the five songs saved most recently.
Seeing what moved last shows where to start.
Question: Which album matters most?
Next: List the five songs saved most recently and what each still needs.`

func withTurns(h *harness, turns *scriptedTurns) *fakeMemory {
	memory := &fakeMemory{entries: map[string]string{}}
	h.svc.deps.Turns, h.svc.deps.Memory = turns, memory
	return memory
}

func TestTurn_ReportCarriesTheAgentsWordsAndProvenance(t *testing.T) {
	h := newHarness(t, demo(t))
	turns := &scriptedTurns{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: folderlook.LookToolName, Arguments: `{"folder":"Albums"}`}}, Usage: llm.Usage{TotalTokens: 700}},
		{Content: demoReport, Usage: llm.Usage{TotalTokens: 900}},
	}}
	memory := withTurns(h, turns)
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if report.WhatItIs != "Seven songs and a sketch across two album folders." ||
		report.SuggestedGoal != "Keep these songs moving toward release." ||
		report.FirstTicket != "List the five songs saved most recently." ||
		report.FirstTicketWhy != "Seeing what moved last shows where to start." {
		t.Fatalf("sections = %+v", report)
	}
	if report.ModelSentence != "" || report.ToolsSentence != "" || len(report.LeftOut) != 0 {
		t.Fatalf("a finished turn shows no degraded sentence: %q %q %v", report.ModelSentence, report.ToolsSentence, report.LeftOut)
	}
	// The facts are clear (one shape), so the question is not shown.
	if report.Question != "" || len(report.AskFolders) != 0 {
		t.Fatalf("question shown with clear facts: %q", report.Question)
	}
	var ranked []string
	for _, c := range report.Candidates {
		if c.Ranked {
			ranked = append(ranked, c.ID+"="+c.Why)
		}
	}
	if strings.Join(ranked, ";") != "integration:ori_reaper=Lets the agent work with the sessions." {
		t.Fatalf("ranked = %v", ranked)
	}
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	if rec.Turn.Status != TurnFinished || rec.Turn.Model != "luna-test" || rec.Turn.Looks != 1 || rec.Turn.Tokens != 1600 {
		t.Fatalf("receipt = %+v", rec.Turn)
	}
	if rec.Sections.Source != folderlook.SourceFirstLookModel || rec.Sections.Model != "luna-test" || rec.Sections.TicketID != rec.TicketID {
		t.Fatalf("provenance = %+v", rec.Sections)
	}
	if !strings.Contains(turns.references[0], "Facts Ori found: 7 REAPER songs") || !strings.Contains(turns.references[0], "do not ask a question") {
		t.Fatalf("reference = %s", turns.references[0])
	}
	// Remember: one entry, folder names only, with the turn's provenance.
	if len(memory.entries) != 1 {
		t.Fatalf("memory = %v", memory.entries)
	}
	for provenance, text := range memory.entries {
		if provenance != fmt.Sprintf("first_look_model/luna-test/%s", rec.TicketID) {
			t.Fatalf("provenance = %q", provenance)
		}
		if !strings.HasPrefix(text, MemoryTitle+" Seven songs") || strings.Contains(text, "/") {
			t.Fatalf("memory text = %q", text)
		}
	}
	// The first ticket's result carries the agent's sentence.
	if got := h.tickets.finished[rec.TicketID]; !strings.HasPrefix(got, "false|Seven songs") {
		t.Fatalf("ticket result = %q", got)
	}

	// A later look updates the memory entry rather than adding one.
	turns.responses = []*llm.ChatResponse{{Content: "What it is: Still seven songs and a sketch."}}
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if len(memory.entries) != 1 {
		t.Fatalf("memory after a second look = %v", memory.entries)
	}
	after, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if after.LeftOut[folderlook.SectionGoal] != "Ori Music Demo Manager left this part out." {
		t.Fatalf("left out = %v", after.LeftOut)
	}
}

func TestTurn_ThePlanInTheAgentsWordsNamesTheStepsOriRuns(t *testing.T) {
	h := newHarness(t, demo(t))
	reply := "What it is: Seven songs and a sketch.\n" +
		"Plan: Put every song on the Music Production Home shelf [music-production-home] and connect REAPER tools [reaper-tools]. Keep the Logic sketch where it is.\n" +
		"Suggested goal: Finish the album.\nSuggested first ticket: List the songs.\nBecause they are the work.\nNext: List the songs."
	turns := &scriptedTurns{responses: []*llm.ChatResponse{{Content: reply}}}
	withTurns(h, turns)
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	plan := report.Plan
	if plan == nil || plan.Source != PlanByAgent || plan.DefaultReason != "" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Words != "Put every song on the Music Production Home shelf Music Production Home and connect REAPER tools REAPER tools. Keep the Logic sketch where it is." ||
		!strings.Contains(plan.WordsRaw, "[music-production-home]") {
		t.Fatalf("words=%q raw=%q", plan.Words, plan.WordsRaw)
	}
	if strings.Join(plan.Selected, ",") != "music-production-home,reaper-tools" {
		t.Fatalf("selected = %v", plan.Selected)
	}
	// The group was offered (eight projects below), not chosen.
	offered := map[string]bool{}
	for _, s := range plan.Steps {
		offered[s.Handle] = s.Chosen
	}
	if chosen, ok := offered["group"]; !ok || chosen {
		t.Fatalf("group step: offered=%v chosen=%v (%+v)", ok, chosen, plan.Steps)
	}
	if ref := turns.references[0]; !strings.Contains(ref, "Steps Ori can run here") || !strings.Contains(ref, "[music-production-home]") ||
		!strings.Contains(ref, "[group]") || !strings.Contains(ref, "Ori's default plan, used if you name no step") {
		t.Fatalf("reference = %s", ref)
	}
	// Set it up runs the agent's plan, Home and tools, as confirmed, with the
	// tools first so the Home's listing can see them; the first step is working
	// from the start.
	started, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: plan.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if started.Setup == nil || len(started.Setup.Lines) == 0 || started.Setup.Lines[0].State != personalassistant.FolderLineWorking {
		t.Fatalf("first line at start = %+v", started.Setup)
	}
	h.svc.Wait()
	run := h.setups.runs[0]
	if run.Stage != foldermap.StageHome || len(run.Candidates) != 2 || run.Candidates[0].Kind != folderlook.KindIntegration || run.Candidates[1].Kind != folderlook.KindHomeProvider {
		t.Fatalf("run = stage %q candidates %+v", run.Stage, run.Candidates)
	}
	if got := h.tickets.finished[view.TicketID]; !strings.Contains(got, "Plan: Put every song") {
		t.Fatalf("ticket result = %q", got)
	}
}

func TestTurn_TheOneQuestionWhenNothingIsRecognized(t *testing.T) {
	var paths []string
	for i := range 8 {
		for j := range 3 {
			paths = append(paths, fmt.Sprintf("Box %d/Item %d/note.txt", i, j))
		}
	}
	h := newHarness(t, build(t, paths...))
	h.svc.deps.Walk = foldermap.Options{MaxEntries: 5}
	turns := &scriptedTurns{responses: []*llm.ChatResponse{{Content: "What it is: Boxes of notes.\nQuestion: Which box should I open first?"}}}
	withTurns(h, turns)
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if report.Question != "Which box should I open first?" || len(report.AskFolders) == 0 || len(report.AskFolders) > folderlook.MaxAskFolders {
		t.Fatalf("question = %q folders = %+v", report.Question, report.AskFolders)
	}
	if !strings.Contains(turns.references[0], "ask the owner one question: which of the unopened folders") ||
		strings.Contains(turns.references[0], "do not ask a question") {
		t.Fatal("the reference must allow the question")
	}
	// The question may name only the folders the owner can pick.
	if !strings.Contains(turns.references[0], "answers by picking one of these folders, or by typing one line: Box") {
		t.Fatalf("the reference must list the pickable folders: %s", turns.references[0])
	}
	// Answering with a typed line carries it into the next turn once.
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, nil, "/Users/me"); !errors.Is(err, personalassistant.ErrValidation) {
		t.Fatalf("a path in the answer = %v", err)
	}
	pick := report.AskFolders[0]
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, []string{pick.Ref}, "Start with the clients"); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if !strings.Contains(turns.references[1], "The owner answered your question: Start with the clients") {
		t.Fatalf("second reference = %s", turns.references[1])
	}
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	if rec.Hint != "" {
		t.Fatal("a hint is used once")
	}
	if f, _ := rec.Map.Folder(pick.Ref); f.State == foldermap.StateUnopened {
		t.Fatalf("the picked folder was not opened first: %+v", f)
	}
}

func TestTurn_SkippedWhenTheModelIsMissingOrDown(t *testing.T) {
	h := newHarness(t, demo(t))
	withTurns(h, &scriptedTurns{err: folderlook.ErrNoModel})
	view := h.start(t, "")
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	if rec.Turn.Status != TurnSkipped || rec.Turn.Reason != SkipNoModel || rec.Sections != nil {
		t.Fatalf("no model = %+v", rec.Turn)
	}
	down := newHarness(t, demo(t))
	withTurns(down, &scriptedTurns{err: errors.New("provider 503")})
	dv := down.start(t, "")
	drec, _ := down.svc.Record("u1", dv.WorkspaceID)
	if drec.Turn.Reason != SkipProviderUnavailable {
		t.Fatalf("provider down = %+v", drec.Turn)
	}
	report, _ := down.svc.View(context.Background(), "u1", dv.WorkspaceID)
	if !strings.Contains(report.ModelSentence, "model was unavailable") {
		t.Fatalf("sentence = %q", report.ModelSentence)
	}
}

func TestSetUp_UsesTheGoalAndOffersTheFirstTicket(t *testing.T) {
	h := newHarness(t, build(t, "Song.rpp"))
	withTurns(h, &scriptedTurns{responses: []*llm.ChatResponse{{Content: demoReport}}})
	view := h.start(t, "")
	report, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	goal := "Finish this song by Friday"
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest, Goal: &goal}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if h.ws.goals[view.WorkspaceID] != goal {
		t.Fatalf("goal = %q", h.ws.goals[view.WorkspaceID])
	}
	done, _ := h.svc.View(context.Background(), "u1", view.WorkspaceID)
	if done.NextTicket == nil || done.NextTicket.Started || done.Actions[0] != ActionStartNext {
		t.Fatalf("receipt = %+v actions %v", done.NextTicket, done.Actions)
	}
	// The fixture folder is named after the test's own root.
	if want := "List the five songs saved most recently and what each still needs. · Ori Music Demo Manager"; h.tickets.backlog[0] != want {
		t.Fatalf("backlog = %v, want %q", h.tickets.backlog, want)
	}
	if len(h.tickets.started) != 0 {
		t.Fatal("the first ticket never starts on its own")
	}
	started, err := h.svc.StartNext(context.Background(), "u1", view.WorkspaceID)
	if err != nil || !started.NextTicket.Started || len(h.tickets.started) != 1 {
		t.Fatalf("start this = %+v %v", started.NextTicket, err)
	}
	if _, err := h.svc.StartNext(context.Background(), "u1", view.WorkspaceID); !errors.Is(err, ErrWrongState) {
		t.Fatalf("second start = %v", err)
	}
	bad := "Go to /Users/me"
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest, Goal: &bad}); !errors.Is(err, personalassistant.ErrValidation) {
		t.Fatalf("a goal with a path = %v", err)
	}
}
