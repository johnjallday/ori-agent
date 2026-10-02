package projectlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const goodBrief = "Your library holds four songs, most between 90 and 99 BPM. Two are under two minutes. You saved one this week."

func TestBriefText_PlainShortDescriptionsOnly(t *testing.T) {
	for _, ok := range []string{goodBrief, "Three songs sit at 128.5 BPM, e.g. the newest one.", "Song #4 is the longest at 6:12."} {
		if problem := briefTextProblem(ok); problem != "" {
			t.Fatalf("%q refused: %s", ok, problem)
		}
	}
	for name, text := range map[string]string{
		"empty":           "",
		"padded":          " " + goodBrief,
		"too long":        strings.Repeat("a", 501),
		"four sentences":  "One. Two. Three. Four.",
		"a line break":    "One song.\nAnother song.",
		"markup":          "<b>Four</b> songs.",
		"markdown":        "**Four** songs.",
		"a path":          "Songs live in Music/Songs.",
		"a home path":     "Songs live in ~/Music.",
		"a windows path":  `Songs live in C:\Music.`,
		"a link":          "See https://example.com for more.",
		"a bare domain":   "See www.example.com for more.",
		"a file name":     "Take 2.wav is the newest.",
		"a project file":  "Night Drive.rpp was saved today.",
		"an email":        "Mail me@example.com.",
		"a control char":  "Four\x07 songs.",
		"a bracket list":  "[1] Four songs.",
		"a pipe table":    "Tempo | Songs",
		"a curly payload": "{\"songs\":4}",
	} {
		if briefTextProblem(text) == "" {
			t.Fatalf("%s accepted: %q", name, text)
		}
	}
}

// briefTestTools mirrors the chat adapter for a brief turn: the reads, one
// proposal tool (which a brief turn must never run), and the brief tool.
func briefTestTools(store *Store, forbidden *atomic.Bool) func(ManagerAuthority) []toolapi.Tool {
	return func(authority ManagerAuthority) []toolapi.Tool {
		tools := runTestTools(store, forbidden)(authority)
		for i, tool := range tools {
			if tool.Definition().Name == "home_library_propose_next_action" {
				tools[i] = runTestTool{"home_library_propose_next_action", func(context.Context, string) (string, error) {
					forbidden.Store(true)
					return `{"proposal_id":"x"}`, nil
				}}
			}
		}
		if authority.Run.Brief {
			tools = append(tools, runTestTool{"home_library_save_brief", func(_ context.Context, raw string) (string, error) {
				var in struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal([]byte(raw), &in); err != nil {
					return "", err
				}
				brief, replay, err := store.SaveCollectionBrief(authority, in.Text)
				if err != nil {
					return "", err
				}
				encoded, _ := json.Marshal(map[string]any{"scan_id": brief.ScanID, "replay": replay})
				return string(encoded), nil
			}})
		}
		return tools
	}
}

// newBriefFixture is newRunFixture on a Home that agreed to song details, with
// real projects, so its scan read facts before the turn.
func newBriefFixture(t *testing.T) *runFixture {
	t.Helper()
	_, scope, roots, file, tree, installed := activationFixture(t)
	bindTestManager(t, file, scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole},
		[]workspace.AssistantRoleBinding{managerBinding})
	grantSongDetails(t, file, scope)
	factsMusicTree(t, tree)
	f := &runFixture{scope: scope, file: file, tree: tree, chat: &scriptedChat{}, available: &atomic.Bool{},
		forbidden: &atomic.Bool{}, installed: installed, roots: roots}
	f.available.Store(true)
	f.library = NewStore(file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool {
		return f.available.Load()
	}).WithInstalledPlugins(installed)
	roots.library = f.library
	doc, err := f.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	f.scanID = scanRoot(t, roots, scope, doc.Roots[0].ID, "brief-fixture-scan").ID
	f.runner = f.newBriefRunner(f.library)
	return f
}

func (f *runFixture) newBriefRunner(library *Store) *ManagerRunner {
	return NewManagerRunner(library, ManagerRunHost{
		Tools: briefTestTools(library, f.forbidden),
		Model: func(context.Context, string, string) (ManagerChat, string, error) {
			return f.chat.chat, "test-model", nil
		},
	})
}

func saveBrief(text string) func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return callTools(toolCall("brief", "home_library_save_brief", map[string]any{"text": text}))
}

func TestManagerBrief_AConsentingHomeGetsOneBriefWithProvenance(t *testing.T) {
	f := newBriefFixture(t)
	if factsAt(t, f.roots, f.scope, f.doc(t).Roots[0].ID, "Single") == nil {
		t.Fatal("fixture scan read no facts")
	}
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		callTools(toolCall("1", "home_library_search", map[string]any{})),
		saveBrief(goodBrief),
	}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runFinished || run.Reason != "" || run.Mode != RunModeBrief || run.Proposals != 0 {
		t.Fatalf("run: %+v %v", run, err)
	}
	if f.chat.calls() != 2 {
		t.Fatalf("the turn went on after its brief: %d calls", f.chat.calls())
	}
	first := f.chat.requests[0]
	if first.SystemPrompt != managerBriefSystemPrompt {
		t.Fatalf("brief turn system prompt: %s", first.SystemPrompt)
	}
	offered := map[string]bool{}
	for _, tool := range first.Tools {
		offered[tool.Name] = true
		if !managerRunReadTools[tool.Name] && tool.Name != "home_library_save_brief" {
			t.Fatalf("a brief turn was offered %q", tool.Name)
		}
	}
	if !offered["home_library_save_brief"] || !offered["home_library_search"] {
		t.Fatalf("offered %v", offered)
	}
	user := first.Messages[0].Content
	for _, want := range []string{"observed 4 projects", `"songs_with_facts":2`, `"tempo_bands":[{"range":"90–99 BPM","songs":2}]`, `"most_recently_saved"`} {
		if !strings.Contains(user, want) {
			t.Fatalf("user prompt lacks %s: %s", want, user)
		}
	}
	for _, leak := range []string{f.tree.root, "Song.rpp", "Take B", "read_from"} {
		if strings.Contains(first.SystemPrompt+user, leak) {
			t.Fatalf("prompt leaks %q", leak)
		}
	}
	doc := f.doc(t)
	brief := doc.CollectionBrief
	if brief == nil || brief.ScanID != f.scanID || brief.Text != goodBrief || brief.Model != "test-model" ||
		brief.AgentName != "Manager" || brief.CreatedAt.IsZero() || len(doc.Proposals) != 0 {
		t.Fatalf("brief = %+v, proposals %d", brief, len(doc.Proposals))
	}
	page, err := f.library.ListManagerProposals(f.scope)
	if err != nil || page.Brief == nil || page.Brief.Text != goodBrief || page.Total != 0 {
		t.Fatalf("read side: %+v %v", page, err)
	}
	summary, err := f.library.Summary(f.scope)
	if err != nil || summary.ReadyProposals != 0 || summary.ProposalRun == nil || summary.ProposalRun.Mode != RunModeBrief {
		t.Fatalf("a brief is not a ready suggestion: %+v %v", summary, err)
	}
	calls := f.chat.calls()
	if again, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID); err != nil || f.chat.calls() != calls ||
		again.Status != runFinished {
		t.Fatalf("a replayed event ran a second turn: %+v %v", again, err)
	}
}

func TestManagerBrief_ProposalToolsAreRefusedUnrun(t *testing.T) {
	f := newBriefFixture(t)
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		callTools(toolCall("1", "home_library_propose_next_action", map[string]any{"entry_id": "single"}),
			toolCall("2", "workspace_note_write", map[string]any{})),
		saveBrief(goodBrief),
	}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runFinished {
		t.Fatalf("run: %+v %v", run, err)
	}
	if f.forbidden.Load() {
		t.Fatal("a proposal or other write tool ran in a brief turn")
	}
	for _, message := range f.chat.requests[1].Messages {
		if message.Role == llm.RoleTool && !strings.Contains(message.Content, "not available in a scan review") {
			t.Fatalf("a refused tool answered: %+v", message)
		}
	}
	if doc := f.doc(t); len(doc.Proposals) != 0 || doc.CollectionBrief == nil {
		t.Fatalf("proposals %d, brief %+v", len(doc.Proposals), doc.CollectionBrief)
	}
}

func TestManagerBrief_ARefusedBriefIsNotSavedAndTheTurnCanRetry(t *testing.T) {
	f := newBriefFixture(t)
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		saveBrief("Night Drive.rpp is the newest file in /Users/me/Music."),
		saveBrief(strings.Repeat("Four songs. ", 3) + "One more."),
		saveBrief(goodBrief),
	}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runFinished || f.chat.calls() != 3 {
		t.Fatalf("run: %+v %v calls %d", run, err, f.chat.calls())
	}
	if !strings.Contains(f.chat.requests[1].Messages[2].Content, "file, path or link") &&
		!strings.Contains(f.chat.requests[1].Messages[2].Content, "plain text") {
		t.Fatalf("the refusal did not say why: %s", f.chat.requests[1].Messages[2].Content)
	}
	if brief := f.doc(t).CollectionBrief; brief == nil || brief.Text != goodBrief {
		t.Fatalf("brief = %+v", brief)
	}
}

func TestManagerBrief_CapsAndSkipsMatchTodaysTurn(t *testing.T) {
	t.Run("no brief written", func(t *testing.T) {
		f := newBriefFixture(t)
		run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
		if err != nil || run.Status != runFinished || run.Reason != "" || f.doc(t).CollectionBrief != nil {
			t.Fatalf("run: %+v %v", run, err)
		}
	})
	t.Run("step limit", func(t *testing.T) {
		f := newBriefFixture(t)
		for i := 0; i < managerRunMaxSteps+1; i++ {
			f.chat.steps = append(f.chat.steps, callTools(toolCall("s", "home_library_search", map[string]any{})))
		}
		run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
		if err != nil || run.Status != runFinished || run.Reason != "step_limit" || f.chat.calls() != managerRunMaxSteps {
			t.Fatalf("run: %+v %v calls %d", run, err, f.chat.calls())
		}
	})
	t.Run("token limit", func(t *testing.T) {
		f := newBriefFixture(t)
		f.runner.host.TokenBudget = func() int { return 150 }
		f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
			callTools(toolCall("1", "home_library_search", map[string]any{})),
			callTools(toolCall("2", "home_library_search", map[string]any{})),
		}
		run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
		if err != nil || run.Reason != "token_limit" || f.doc(t).CollectionBrief != nil {
			t.Fatalf("run: %+v %v", run, err)
		}
	})
	t.Run("no model", func(t *testing.T) {
		f := newBriefFixture(t)
		f.runner.host.Model = func(context.Context, string, string) (ManagerChat, string, error) {
			return nil, "", ErrNoManagerModel
		}
		run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
		if err != nil || run.Status != runSkipped || run.Reason != "no_model" || run.Mode != RunModeBrief {
			t.Fatalf("run: %+v %v", run, err)
		}
	})
	t.Run("authority lost mid-turn", func(t *testing.T) {
		f := newBriefFixture(t)
		f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
			func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
				f.available.Store(false)
				return saveBrief(goodBrief)(ctx, request)
			},
		}
		run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
		if err != nil || run.Status == runStarted || f.doc(t).CollectionBrief != nil {
			t.Fatalf("run: %+v %v", run, err)
		}
	})
	t.Run("switched off mid-turn", func(t *testing.T) {
		f := newBriefFixture(t)
		f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
			func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
				switchSongDetailsOff(t, f.file, f.scope)
				return saveBrief(goodBrief)(ctx, request)
			},
		}
		if run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID); err != nil || f.doc(t).CollectionBrief != nil {
			t.Fatalf("run: %+v %v", run, err)
		}
	})
}

func TestManagerBrief_ANonConsentingHomeKeepsTodaysTurn(t *testing.T) {
	f := newRunFixture(t)
	f.runner = f.newBriefRunner(f.library)
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Mode != "" {
		t.Fatalf("run: %+v %v", run, err)
	}
	first := f.chat.requests[0]
	if first.SystemPrompt != managerRunSystemPrompt {
		t.Fatalf("system prompt changed: %s", first.SystemPrompt)
	}
	doc := f.doc(t)
	if want := managerRunUserPrompt(*doc.Digest); first.Messages[0].Content != want {
		t.Fatalf("user prompt changed:\n%s\nwant\n%s", first.Messages[0].Content, want)
	}
	for _, tool := range first.Tools {
		if tool.Name == "home_library_save_brief" {
			t.Fatal("a Home without the consent was offered the brief tool")
		}
	}
}

func TestManagerBrief_ASmallerFolderScanStillDescribesTheWholeLibrary(t *testing.T) {
	f := newBriefFixture(t)
	doc := f.doc(t)
	root := doc.Roots[0]
	page, err := f.roots.ListScanScopes(f.scope, root.ID, doc.Revision, 0)
	if err != nil {
		t.Fatal(err)
	}
	scopeID := ""
	for _, row := range page.Rows {
		if row.RelativeFolder == "Single" {
			scopeID = row.ID
		}
	}
	review, err := f.roots.ReviewScopedScan(f.scope, root.ID, scopeID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	scoped, _, err := f.roots.CommitScopedScan(context.Background(), f.scope, root.ID, scopeID, review.Token, "scoped-brief")
	if err != nil || scoped.Scope != "Single" {
		t.Fatalf("scoped scan: %+v %v", scoped, err)
	}
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){saveBrief(goodBrief)}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, scoped.ID)
	if err != nil || run.Status != runFinished || run.Mode != RunModeBrief {
		t.Fatalf("run: %+v %v", run, err)
	}
	if user := f.chat.requests[0].Messages[0].Content; !strings.Contains(user, `"songs":4`) {
		t.Fatalf("a smaller-folder scan's summary is not the whole library: %s", user)
	}
	if brief := f.doc(t).CollectionBrief; brief == nil || brief.ScanID != scoped.ID {
		t.Fatalf("the newer scan's brief did not replace the old: %+v", brief)
	}
}

func TestSaveCollectionBrief_OnePerScanOnlyInsideABriefTurn(t *testing.T) {
	f := newBriefFixture(t)
	authority := managerAuthorityFor(f)
	if _, _, err := f.library.SaveCollectionBrief(authority, goodBrief); !errors.Is(err, ErrConflict) {
		t.Fatalf("outside a turn (chat): %v", err)
	}
	authority.Run = ManagerRunContext{ScanID: f.scanID, Model: "test-model", Brief: true}
	if _, _, err := f.library.SaveCollectionBrief(authority, goodBrief); !errors.Is(err, ErrConflict) {
		t.Fatalf("without a started receipt: %v", err)
	}
	if _, claimed, err := f.library.claimProposalRun(f.scope, ProposalRun{ScanID: f.scanID, Status: runStarted,
		Mode: RunModeBrief, AgentName: "Manager", Model: "test-model", StartedAt: time.Now().UTC()}); err != nil || !claimed {
		t.Fatalf("claim: %v", err)
	}
	if _, replay, err := f.library.SaveCollectionBrief(authority, goodBrief); err != nil || replay {
		t.Fatalf("first save: %v %v", replay, err)
	}
	if _, replay, err := f.library.SaveCollectionBrief(authority, goodBrief); err != nil || !replay {
		t.Fatalf("the same text again is a replay: %v %v", replay, err)
	}
	if _, _, err := f.library.SaveCollectionBrief(authority, "Another brief."); !errors.Is(err, ErrBriefAlreadySaved) {
		t.Fatalf("a second brief for the scan: %v", err)
	}
	notBrief := authority
	notBrief.Run.Brief = false
	if _, _, err := f.library.SaveCollectionBrief(notBrief, goodBrief); !errors.Is(err, ErrConflict) {
		t.Fatalf("a suggestions turn saved a brief: %v", err)
	}
}

func managerAuthorityFor(f *runFixture) ManagerAuthority {
	return ManagerAuthority{HomeID: f.scope.HomeID, AgentInstanceID: managerBinding.AgentInstanceID,
		AgentName: managerBinding.AgentName}
}

func TestStore_ForgedBriefsMakeTheDocumentInvalid(t *testing.T) {
	f := newBriefFixture(t)
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){saveBrief(goodBrief)}
	if _, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID); err != nil {
		t.Fatal(err)
	}
	home, err := f.file.Get(f.scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	stored := home.GetAssistantProgramState().ProjectLibrary
	for name, edit := range map[string]func(map[string]any){
		"oversized":       func(b map[string]any) { b["text"] = strings.Repeat("a", 600) },
		"a path":          func(b map[string]any) { b["text"] = "Songs are in /Users/me." },
		"an unknown scan": func(b map[string]any) { b["scan_id"] = "not-a-scan" },
		"no model":        func(b map[string]any) { b["model"] = "" },
		"an extra field":  func(b map[string]any) { b["advice"] = "finish Song 3" },
	} {
		t.Run(name, func(t *testing.T) {
			decoder := json.NewDecoder(bytes.NewReader(stored))
			decoder.UseNumber()
			var raw map[string]any
			if err := decoder.Decode(&raw); err != nil {
				t.Fatal(err)
			}
			edit(raw["collection_brief"].(map[string]any))
			forged, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeDocument(forged, f.scope); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("a forged brief decoded: %v", err)
			}
		})
	}
	// Switching song details off removes the brief with the facts.
	if _, err := f.library.SetSongDetails(f.scope, false); err != nil {
		t.Fatal(err)
	}
	if brief := f.doc(t).CollectionBrief; brief != nil {
		t.Fatalf("switching off kept the brief: %+v", brief)
	}
	if page, err := f.library.ListManagerProposals(f.scope); err != nil || page.Brief != nil {
		t.Fatalf("read side after off: %+v %v", page, err)
	}
}
