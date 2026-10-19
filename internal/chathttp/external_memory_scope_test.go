package chathttp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// externalProfileSentinel is a profile-grade line of the Codex fixture.
const externalProfileSentinel = "self-contained trading research tools"

// assistantGate stands in for KnowledgeContextReader.IsPersonalAssistant: the
// user's assistant is instance "entry" in workspace "hq".
type assistantGate struct{ asked []string }

func (g *assistantGate) IsPersonalAssistant(_ context.Context, userID, workspaceID, instanceID string) bool {
	g.asked = append(g.asked, workspaceID+"/"+instanceID)
	return userID == userprofile.LocalUserID && workspaceID == "hq" && instanceID == "entry"
}

// syncedExternalMemory is a real mirror with every source enabled, synced from
// the Codex fixture.
func syncedExternalMemory(t *testing.T) *externalmemory.Reader {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "externalmemory", "testdata", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	store := externalmemory.NewStore(t.TempDir())
	if _, err := store.UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		for _, id := range externalmemory.SourceIDs {
			doc.Sources[id] = externalmemory.SourceState{Enabled: true}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := externalmemory.NewRunner(store, externalmemory.NewRegistry(externalmemory.NewCodexSource(root)))
	if _, err := runner.Run(context.Background(), externalmemory.ReasonManual); err != nil {
		t.Fatal(err)
	}
	reader := runner.Reader()
	if !strings.Contains(reader.ContextBlock(8000), externalProfileSentinel) {
		t.Fatal("fixture mirror has no profile-grade block")
	}
	return reader
}

type scopeFixture struct {
	h       *Handler
	folders *workspace.FileStore
	gate    *assistantGate
}

// newScopeFixture is a Personal HQ (Atlas = the assistant's entry instance,
// Helper = another HQ agent) and a project workspace where an agent is also
// named Atlas, with external memory wired.
func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Home"})
	hq.ID = "hq"
	hq.OwnerUserID = userprofile.LocalUserID
	hq.AgentInstances = []workspace.AgentInstance{{ID: "entry", Name: "Atlas", EntryPoint: true}, {ID: "other", Name: "Helper"}}
	album := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Album 3"})
	album.ID = "album"
	album.OwnerUserID = userprofile.LocalUserID
	album.AgentInstances = []workspace.AgentInstance{{ID: "scout", Name: "Scout", EntryPoint: true}, {ID: "atlas-copy", Name: "Atlas"}}
	for _, ws := range []*workspace.Workspace{hq, album} {
		if err := folders.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	gate := &assistantGate{}
	h := &Handler{fileStore: folders, workspaceStore: folders, userProvider: userprofile.LocalUserProvider{}}
	h.SetExternalMemory(syncedExternalMemory(t), gate)
	return &scopeFixture{h: h, folders: folders, gate: gate}
}

func route(workspaceID, agentName string) normalizedChatRouteContext {
	return normalizedChatRouteContext{Surface: "workspace_detail", WorkspaceID: workspaceID, AgentName: agentName, PagePath: "/workspaces/" + workspaceID}
}

// chatPrompts returns the structured memory section and a {{workspace.memory}}
// base prompt for one route.
func (f *scopeFixture) chatPrompts(r normalizedChatRouteContext) []string {
	structured := f.h.buildWorkspaceMemoryPrompt(context.Background(), r, true)
	prompt := &resolvedChatAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Context {{workspace.memory}}"}}}
	f.h.resolveAgentBasePromptVars(context.Background(), r, prompt)
	return []string{structured, prompt.Settings.SystemPrompt}
}

// chatTools returns the tool names a chat turn for this route is offered.
func (f *scopeFixture) chatTools(r normalizedChatRouteContext, instanceID string) []string {
	wtp := NewWorkspaceToolProvider(nil, f.folders, r.WorkspaceID)
	wtp.SetFileStore(f.folders)
	f.h.attachExternalMemoryRecall(wtp, r.AgentName, r, instanceID)
	var names []string
	for _, tool := range wtp.Tools() {
		names = append(names, tool.Definition().Name)
	}
	return names
}

func TestExternalMemory_ThePersonalAssistantInItsHQGetsTheBlockAndTheTool(t *testing.T) {
	f := newScopeFixture(t)
	r := route("hq", "Atlas")
	for _, text := range f.chatPrompts(r) {
		if strings.Count(text, externalProfileSentinel) != 1 || !strings.Contains(text, externalmemory.DataNotice) {
			t.Fatalf("assistant prompt should carry the external block once, as data:\n%s", text)
		}
	}
	if names := f.chatTools(r, ""); !slices.Contains(names, workspace.MemoryRecallToolName) {
		t.Fatalf("assistant was not offered memory_recall: %v", names)
	}
	// The resolver's own instance wins over the name.
	if names := f.chatTools(r, "entry"); !slices.Contains(names, workspace.MemoryRecallToolName) {
		t.Fatalf("resolved entry instance lost memory_recall: %v", names)
	}

	wtp := NewWorkspaceToolProvider(nil, f.folders, "hq")
	f.h.attachExternalMemoryRecall(wtp, "Atlas", r, "")
	var recall interface {
		Call(context.Context, string) (string, error)
	}
	for _, tool := range wtp.Tools() {
		if tool.Definition().Name == workspace.MemoryRecallToolName {
			recall = tool
		}
	}
	out, err := recall.Call(context.Background(), `{"query":"EMA20 close below EMA50","limit":3}`)
	if err != nil || !strings.Contains(out, "[Codex]") || !strings.Contains(out, externalmemory.DataNotice) || !strings.Contains(out, "id: ") {
		t.Fatalf("recall result: %v\n%s", err, out)
	}
	if _, err := recall.Call(context.Background(), `{"query":""}`); err == nil {
		t.Fatal("an empty query should be refused")
	}
}

// FR19: nobody but the personal assistant answering in its own Personal HQ
// gets the block or the tool, with every source enabled and entries present.
func TestExternalMemory_NoOtherChatTurnGetsTheBlockOrTheTool(t *testing.T) {
	f := newScopeFixture(t)
	cases := []struct {
		name     string
		route    normalizedChatRouteContext
		instance string
	}{
		{"another HQ agent", route("hq", "Helper"), ""},
		{"no responding agent", route("hq", ""), ""},
		{"project workspace entry agent", route("album", "Scout"), ""},
		{"project agent sharing the assistant's name", route("album", "Atlas"), ""},
		{"resolver instance is not the entry", route("hq", "Atlas"), "other"},
		{"not a workspace surface", normalizedChatRouteContext{Surface: "home", WorkspaceID: "hq", AgentName: "Atlas"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.instance == "" {
				for _, text := range f.chatPrompts(tc.route) {
					if strings.Contains(text, externalProfileSentinel) || strings.Contains(text, externalmemory.ContextHeading) {
						t.Fatalf("received the external block:\n%s", text)
					}
				}
			}
			if names := f.chatTools(tc.route, tc.instance); slices.Contains(names, workspace.MemoryRecallToolName) {
				t.Fatalf("offered memory_recall: %v", names)
			}
		})
	}
}

func TestExternalMemory_AmbiguousOrUnwiredGivesNothing(t *testing.T) {
	f := newScopeFixture(t)
	// The file store keeps one instance per name; a workspace from anywhere
	// else could still carry two, and then neither is a principal.
	twins := &workspace.Workspace{AgentInstances: []workspace.AgentInstance{{ID: "entry", Name: "Atlas"}, {ID: "twin", Name: "atlas"}}}
	if got := respondingInstance(twins, "Atlas"); got != "" {
		t.Fatalf("a duplicated name resolved to %q", got)
	}
	if got := respondingInstance(&workspace.Workspace{AgentInstances: []workspace.AgentInstance{{Name: "Atlas"}}}, "Atlas"); got != "" {
		t.Fatalf("an instance without an id resolved to %q", got)
	}
	unwired := &Handler{fileStore: f.folders, workspaceStore: f.folders, userProvider: userprofile.LocalUserProvider{}}
	if section := unwired.externalMemorySection(context.Background(), route("hq", "Atlas")); section != "" {
		t.Fatalf("an unwired handler rendered: %q", section)
	}
}

// Task runs build their tools from the same provider without the chat
// handler (internal/server/builder_workflow.go). A provider built that way has
// no memory_recall, and nothing outside the chat handler's gate turns it on.
func TestExternalMemory_TaskToolsNeverIncludeRecall(t *testing.T) {
	f := newScopeFixture(t)
	provider := NewWorkspaceToolProvider(nil, f.folders, "hq")
	provider.SetExecutingAgent("Atlas")
	provider.SetRunTaskID("task-1")
	provider.SetFileStore(f.folders)
	for _, tool := range provider.Tools() {
		if tool.Definition().Name == workspace.MemoryRecallToolName {
			t.Fatal("a task-run provider offered memory_recall")
		}
	}

	internalDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		filepath.Join(internalDir, "chathttp", "external_memory.go"):        true, // the gated call
		filepath.Join(internalDir, "chathttp", "workspace_memory_tools.go"): true, // the definition
	}
	err = filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path) // #nosec G304 -- the repository's own sources
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "SetExternalMemoryRecall(") && !allowed[path] {
			t.Errorf("%s turns on memory_recall outside the personal-assistant gate", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
