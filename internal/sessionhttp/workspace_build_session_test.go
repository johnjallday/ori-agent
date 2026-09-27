package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// memoryBuildStore is an in-memory WorkspaceBuildStore with the same rules as
// the sidecar: one open build, versioned writes.
type memoryBuildStore struct {
	mu  sync.Mutex
	doc personalassistant.WorkspaceBuildDocument
	now time.Time
}

func (s *memoryBuildStore) Read(context.Context, string) (personalassistant.WorkspaceBuildDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneBuildDocument(s.doc), nil
}

func (s *memoryBuildStore) Mutate(_ context.Context, _ string, mutate func(*personalassistant.WorkspaceBuildDocument) error) (personalassistant.WorkspaceBuildDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := cloneBuildDocument(s.doc)
	doc.ExpireStale(s.now)
	if err := mutate(&doc); err != nil {
		return personalassistant.WorkspaceBuildDocument{}, err
	}
	open := 0
	for _, session := range doc.Sessions {
		if session.Status == personalassistant.WorkspaceBuildOpen {
			open++
		}
	}
	if open > 1 {
		return personalassistant.WorkspaceBuildDocument{}, personalassistant.ErrWorkspaceBuildOpen
	}
	doc.Version++
	s.doc = doc
	return cloneBuildDocument(doc), nil
}

func (s *memoryBuildStore) Now() time.Time { return s.now }

func cloneBuildDocument(doc personalassistant.WorkspaceBuildDocument) personalassistant.WorkspaceBuildDocument {
	data, _ := json.Marshal(doc)
	var out personalassistant.WorkspaceBuildDocument
	_ = json.Unmarshal(data, &out)
	out.Version = doc.Version
	return out
}

// scriptedProvider answers each model call with the next scripted reply.
type scriptedProvider struct {
	mu         sync.Mutex
	replies    []string
	errs       []error
	calls      int
	structured bool
	systems    []string
	messages   [][]llm.Message
}

func (p *scriptedProvider) next(system string, messages []llm.Message) (*llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.calls
	p.calls++
	p.systems = append(p.systems, system)
	p.messages = append(p.messages, messages)
	if index < len(p.errs) && p.errs[index] != nil {
		return nil, p.errs[index]
	}
	if index >= len(p.replies) {
		return nil, errors.New("no scripted reply")
	}
	return &llm.ChatResponse{Content: p.replies[index]}, nil
}

func (p *scriptedProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return p.next(req.SystemPrompt, req.Messages)
}
func (p *scriptedProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not streamed")
}
func (p *scriptedProvider) Name() string                            { return "scripted" }
func (p *scriptedProvider) Type() llm.ProviderType                  { return llm.ProviderTypeCloud }
func (p *scriptedProvider) Capabilities() llm.ProviderCapabilities  { return llm.ProviderCapabilities{} }
func (p *scriptedProvider) ValidateConfig(llm.ProviderConfig) error { return nil }
func (p *scriptedProvider) DefaultModels() []string                 { return []string{"scripted-1"} }

// structuredProvider adds the structured-output path.
type structuredProvider struct{ *scriptedProvider }

func (p structuredProvider) ChatWithStructuredOutput(_ context.Context, req llm.StructuredOutputRequest) (*llm.ChatResponse, error) {
	return p.next(req.SystemPrompt, req.Messages)
}

type buildFixture struct {
	handler   *Handler
	store     *memoryBuildStore
	provider  *scriptedProvider
	assistant *personalassistant.Projection
	modelErr  error
	catalog   []WorkspaceBuildCatalogEntry
}

func readyEntry(template projecttemplates.Template) WorkspaceBuildCatalogEntry {
	return WorkspaceBuildCatalogEntry{Template: template, Readiness: blueprintreadiness.Readiness{State: blueprintreadiness.StateReady}}
}

func buildTestCatalog() []WorkspaceBuildCatalogEntry {
	return []WorkspaceBuildCatalogEntry{
		readyEntry(projecttemplates.Template{
			ID: "content-production", Name: "Content Production", Tags: []string{"content", "writing"},
			Tagline: "Brand voice, drafts, scheduled posts.",
			Agents: []projecttemplates.AgentSpec{
				{Name: "Content Lead"}, {Name: "Brand Copywriter"}, {Name: "Content Editor"},
			},
		}),
		readyEntry(projecttemplates.Template{
			ID: "code-project", Name: "Code Project", Tags: []string{"code"},
			Agents:            []projecttemplates.AgentSpec{{Name: "Engineer"}},
			ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{SupportedModes: []projecttemplates.ProjectConnectionMode{"new_project", "existing_project"}},
		}),
		readyEntry(projecttemplates.Template{
			ID: "research-project", Name: "Research Project", Tags: []string{"research", "notes"},
			Agents: []projecttemplates.AgentSpec{{Name: "Researcher"}},
			Inputs: &projecttemplates.InputsDeclaration{SchemaVersion: 1, Title: "Settings", Fields: []projecttemplates.InputField{
				{ID: "sources", Label: "Sources", Type: projecttemplates.InputFieldNumber, Min: 1, Max: 20, Step: 1, Default: 5.0},
				{ID: "cadence", Label: "Cadence", Type: projecttemplates.InputFieldSelect, Default: "weekly",
					Options: []projecttemplates.InputOption{{Value: "daily", Label: "Daily"}, {Value: "weekly", Label: "Weekly"}}},
			}},
		}),
		{
			Template:  projecttemplates.Template{ID: "blocked-blueprint", Name: "Blocked Blueprint"},
			Readiness: blueprintreadiness.Readiness{State: blueprintreadiness.State("unavailable")},
		},
		readyEntry(projecttemplates.Template{ID: "personal-ops", Name: "Personal HQ", Builtin: true}),
		readyEntry(projecttemplates.Template{
			ID: "studio-song", Name: "Studio Song", Tags: []string{"music"},
			Agents:           []projecttemplates.AgentSpec{{Name: "Producer"}},
			GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyRequired, DefaultHomeName: "Studio Home"},
		}),
	}
}

func newBuildFixture(t *testing.T, structured bool, replies ...string) *buildFixture {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	f := &buildFixture{
		handler:  handler,
		store:    &memoryBuildStore{now: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC), doc: personalassistant.WorkspaceBuildDocument{SchemaVersion: 1}},
		provider: &scriptedProvider{replies: replies, structured: structured},
		assistant: &personalassistant.Projection{
			State: personalassistant.APIStateActive, DisplayName: "Luna", GlobalAgentProfile: "Luna",
		},
		catalog: buildTestCatalog(),
	}
	for _, name := range []string{"Luna", "Scout"} {
		if err := handler.agentStore.CreateAgent(name, &agentstore.CreateAgentConfig{}); err != nil {
			t.Fatal(err)
		}
	}
	handler.SetWorkspaceBuild(WorkspaceBuildDeps{
		Store: f.store,
		Assistant: func(context.Context, string) (*personalassistant.Projection, error) {
			return f.assistant, nil
		},
		Catalog: func(context.Context, string) ([]WorkspaceBuildCatalogEntry, error) {
			return f.catalog, nil
		},
		ResolveModel: func(context.Context, string) (WorkspaceBuildModel, error) {
			if f.modelErr != nil {
				return WorkspaceBuildModel{}, f.modelErr
			}
			var provider llm.Provider = f.provider
			if f.provider.structured {
				provider = structuredProvider{f.provider}
			}
			return WorkspaceBuildModel{Provider: provider, ProviderName: "scripted", Model: "scripted-1"}, nil
		},
		ModelAvailable: func(provider, model string) bool { return provider == "scripted" && model == "scripted-1" },
	})
	return f
}

func (f *buildFixture) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	f.handler.HandleWorkspaces(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func sessionOf(t *testing.T, body map[string]any) personalassistant.WorkspaceBuildSession {
	t.Helper()
	data, _ := json.Marshal(body["session"])
	var session personalassistant.WorkspaceBuildSession
	if err := json.Unmarshal(data, &session); err != nil || session.ID == "" {
		t.Fatalf("no session in %v", body)
	}
	return session
}

func (f *buildFixture) start(t *testing.T) personalassistant.WorkspaceBuildSession {
	t.Helper()
	status, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions", map[string]any{"entry_point": "home_cockpit_create"})
	if status != http.StatusCreated {
		t.Fatalf("create status %d: %v", status, body)
	}
	return sessionOf(t, body)
}

func (f *buildFixture) turn(t *testing.T, session personalassistant.WorkspaceBuildSession, body map[string]any) (int, map[string]any) {
	t.Helper()
	body["version"] = session.Version
	return f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+session.ID+"/turns", body)
}

const describeReply = `{"say":"Content Production fits — it already has a drafting flow. I named it Newsletter Desk.",
"ask":{"question":"Assign Luna as the lead, or make a new one?","choices":[{"id":"x","label":"Assign Luna"},{"id":"y","label":"Make a new one"}],"allow_free_text":true},
"patch":{"set":["blueprint_id","name","description"],"blueprint_id":"content-production","name":"Newsletter Desk","description":"Drafts the Monday newsletter from research notes.",
"inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[{"section":"blueprint","text":"It already has a drafting flow."}],"alternatives":[{"blueprint_id":"research-project","reason":"If the notes matter more than the posts."}],
"ready":false,"create_now":false}`

const talkOnlyReply = `{"say":"Sounds lovely.","ask":{"question":"","choices":[],"allow_free_text":true},
"patch":{"set":[],"blueprint_id":"","name":"","description":"","inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":false,"create_now":false}`

func TestWorkspaceBuildAvailability_ReportsWhyBuildModeIsOff(t *testing.T) {
	f := newBuildFixture(t, true)
	if status, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/availability", nil); status != 200 || body["available"] != true {
		t.Fatalf("ready: %d %v", status, body)
	}
	f.modelErr = ErrWorkspaceBuildNoModel
	if _, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/availability", nil); body["available"] != false || body["reason"] != "no_model" {
		t.Fatalf("no model: %v", body)
	}
	f.modelErr = nil
	f.assistant.State = personalassistant.APIStateNeedsHire
	if _, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/availability", nil); body["reason"] != "assistant_not_ready" {
		t.Fatalf("not hired: %v", body)
	}
	f.assistant.State = personalassistant.APIStatePaused
	if _, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/availability", nil); body["available"] != true {
		t.Fatalf("paused assistant builds too: %v", body)
	}

	unwired, cleanup := createTestHandler(t)
	defer cleanup()
	rec := httptest.NewRecorder()
	unwired.HandleWorkspaces(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/build-sessions/availability", nil))
	if !strings.Contains(rec.Body.String(), "assistant_not_ready") {
		t.Fatalf("unwired handler: %s", rec.Body.String())
	}
}

func TestWorkspaceBuild_CreateThenResumeTheOneOpenBuild(t *testing.T) {
	f := newBuildFixture(t, true)
	first := f.start(t)
	if first.Status != personalassistant.WorkspaceBuildOpen || first.Assistant.DisplayName != "Luna" || first.FurthestStep != 1 {
		t.Fatalf("fresh session %+v", first)
	}
	status, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions", map[string]any{"entry_point": "workspace_hub_create"})
	if status != http.StatusOK || body["resumed"] != true || sessionOf(t, body).ID != first.ID {
		t.Fatalf("second create must resume: %d %v", status, body)
	}
	if status, _ := f.do(t, http.MethodPost, "/api/workspaces/build-sessions", map[string]any{"entry_point": "folder_digest"}); status != http.StatusBadRequest {
		t.Fatalf("a manual opener must not build: %d", status)
	}
	if status, _ := f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+first.ID+"/abandon", map[string]any{}); status != http.StatusOK {
		t.Fatalf("abandon: %d", status)
	}
	if second := f.start(t); second.ID == first.ID {
		t.Fatal("after abandoning, a new build starts")
	}
}

func TestWorkspaceBuild_ATurnFillsBlueprintNameAndDescription(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	session := f.start(t)
	status, body := f.turn(t, session, map[string]any{"text": "a newsletter from my research notes every monday"})
	if status != http.StatusOK {
		t.Fatalf("turn %d %v", status, body)
	}
	got := sessionOf(t, body)
	if got.Draft.TemplateID != "content-production" || got.Draft.Name != "Newsletter Desk" || got.Draft.Description == "" {
		t.Fatalf("draft %+v", got.Draft)
	}
	if strings.Join(got.Applied, ",") != "blueprint,name,description" {
		t.Fatalf("applied %v", got.Applied)
	}
	if len(got.Transcript) != 2 || got.Transcript[0].Role != "user" || got.Transcript[1].Role != "assistant" {
		t.Fatalf("transcript %+v", got.Transcript)
	}
	choices := got.PendingQuestion.Choices
	if len(choices) != 2 || choices[0].ID == "x" || choices[0].Label != "Assign Luna" {
		t.Fatalf("chips must carry server ids: %+v", choices)
	}
	if got.TurnCount != 1 || got.FirstRequest != "a newsletter from my research notes every monday" || got.FurthestStep != 3 {
		t.Fatalf("counters %d %q %d", got.TurnCount, got.FirstRequest, got.FurthestStep)
	}
	if len(got.Why) != 1 || len(got.Alternatives) != 1 || got.Alternatives[0].Label != "Research Project" {
		t.Fatalf("why/alternatives %+v %+v", got.Why, got.Alternatives)
	}
	// The model saw the catalog and the user's words, never a path, and never
	// a readiness-blocked blueprint or the Personal HQ the user already has.
	system := f.provider.systems[0]
	if !strings.Contains(system, `"id":"content-production"`) || strings.Contains(system, "blocked-blueprint") ||
		strings.Contains(system, `"id":"personal-ops"`) {
		t.Fatalf("catalog in prompt: %s", system)
	}
	if last := f.provider.messages[0]; last[len(last)-1].Content != "a newsletter from my research notes every monday" {
		t.Fatalf("messages %+v", last)
	}
}

func TestWorkspaceBuild_AChipAnswersWithItsLabel(t *testing.T) {
	assign := strings.Replace(describeReply, `"set":["blueprint_id","name","description"]`, `"set":["team"]`, 1)
	assign = strings.Replace(assign, `"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]}`,
		`"team":{"mode":"staffed","agents":[{"name":"Content Lead","action":"reuse","rename":"Luna","model":"","provider":"","system_prompt":""}],"roles":[],"saved_agents":["Scout"]}`, 1)
	f := newBuildFixture(t, true, describeReply, assign)
	session := f.start(t)
	_, body := f.turn(t, session, map[string]any{"text": "a newsletter"})
	session = sessionOf(t, body)
	chip := session.PendingQuestion.Choices[0]
	status, body := f.turn(t, session, map[string]any{"choice_id": chip.ID})
	if status != http.StatusOK {
		t.Fatalf("chip turn %d %v", status, body)
	}
	got := sessionOf(t, body)
	if got.Transcript[1].Chosen != chip.ID || got.Transcript[2].Text != "Assign Luna" {
		t.Fatalf("chosen chip %+v", got.Transcript)
	}
	team := got.TeamPatch
	if team == nil || len(team.Roles) != 1 || team.Roles[0].RoleID != "content-lead" || team.Roles[0].Mode != "assign" ||
		team.Roles[0].AgentName != "Luna" || strings.Join(team.SavedAgents, ",") != "Scout" {
		t.Fatalf("team %+v", team)
	}
	// The team is stated as the form holds it, whatever the model said.
	if last := got.Transcript[len(got.Transcript)-1]; !strings.Contains(last.Text, "On the form: Content Lead — Luna; Scout added.") {
		t.Fatalf("team receipt missing: %q", last.Text)
	}
	if status, _ := f.turn(t, got, map[string]any{"choice_id": "c9-9"}); status != http.StatusConflict {
		t.Fatalf("a chip that is not offered: %d", status)
	}
}

func TestWorkspaceBuild_TalkOnlyRepliesAreRetriedThenReplaced(t *testing.T) {
	f := newBuildFixture(t, false, talkOnlyReply, talkOnlyReply)
	session := f.start(t)
	status, body := f.turn(t, session, map[string]any{"text": "something with my research notes"})
	if status != http.StatusOK {
		t.Fatalf("turn %d", status)
	}
	got := sessionOf(t, body)
	if f.provider.calls != 2 || !strings.Contains(f.provider.systems[1], "neither set a field nor asked") {
		t.Fatalf("expected one nudged retry, calls=%d", f.provider.calls)
	}
	last := got.Transcript[len(got.Transcript)-1]
	if last.Text != "Which of these is closest?" || !last.Fixed || len(last.Choices) != 3 {
		t.Fatalf("substitute %+v", last)
	}
	if last.Choices[0].Label != "Research Project" {
		t.Fatalf("closest first: %+v", last.Choices)
	}
	for _, entry := range got.Transcript {
		if entry.Text == "Sounds lovely." {
			t.Fatal("a talk-only reply must never be shown")
		}
	}
}

func TestWorkspaceBuild_AFirstReplyWithoutTheEssentialsIsNudged(t *testing.T) {
	nameOnly := strings.Replace(describeReply, `"set":["blueprint_id","name","description"]`, `"set":["name"]`, 1)
	f := newBuildFixture(t, true, nameOnly, describeReply)
	session := f.start(t)
	_, body := f.turn(t, session, map[string]any{"text": "a newsletter"})
	if got := sessionOf(t, body); f.provider.calls != 2 || got.Draft.TemplateID != "content-production" {
		t.Fatalf("calls %d draft %+v", f.provider.calls, got.Draft)
	}
}

func TestWorkspaceBuild_ATakenNameIsRefusedForTheAssistantToFix(t *testing.T) {
	rename := `{"say":"Renamed it.","ask":{"question":"Anything else to change?","choices":[],"allow_free_text":true},
"patch":{"set":["name"],"blueprint_id":"","name":"Field Notes","description":"","inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":false,"create_now":false}`
	f := newBuildFixture(t, true, describeReply, rename)
	if err := f.handler.store.CreateWorkspace(context.Background(), &session.Workspace{ID: "ws-taken", Name: "Field Notes", FolderSlug: "field-notes"}); err != nil {
		t.Fatal(err)
	}
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "a newsletter"})
	_, body = f.turn(t, sessionOf(t, body), map[string]any{"text": "call it Field Notes"})
	got := sessionOf(t, body)
	if got.Draft.Name != "Newsletter Desk" {
		t.Fatalf("a taken name must not reach the form: %q", got.Draft.Name)
	}
	if len(got.Rejections) != 1 || got.Rejections[0].Field != "name" || got.Rejections[0].Reason != "name_taken" {
		t.Fatalf("rejections %+v", got.Rejections)
	}
	// "Renamed it." is not left standing as a claim.
	last := got.Transcript[len(got.Transcript)-1]
	if !strings.Contains(last.Text, "The form didn’t take the name yet") {
		t.Fatalf("refusal note missing: %q", last.Text)
	}
}

func TestWorkspaceBuild_TheRefusalNoteNamesEachPartOnce(t *testing.T) {
	note := buildRefusalNote([]personalassistant.BuildRejection{
		{Field: "team.agents.Journal"}, {Field: "team.roles.x"}, {Field: "inputs.sources"}, {Field: "color"},
	})
	if note != "(The form didn’t take the team, a setting or the color yet — I’ll adjust them.)" {
		t.Fatalf("note %q", note)
	}
	if buildRefusalNote(nil) != "" {
		t.Fatal("no refusals, no note")
	}
}

func TestWorkspaceBuild_ARetryHearsWhatTheHostRefused(t *testing.T) {
	taken := strings.Replace(describeReply, `"name":"Newsletter Desk"`, `"name":"Field Notes"`, 1)
	f := newBuildFixture(t, true, taken, describeReply)
	if err := f.handler.store.CreateWorkspace(context.Background(), &session.Workspace{ID: "ws-taken", Name: "Field Notes", FolderSlug: "field-notes"}); err != nil {
		t.Fatal(err)
	}
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "a newsletter"})
	got := sessionOf(t, body)
	if f.provider.calls != 2 || !strings.Contains(f.provider.systems[1], `"reason":"name_taken"`) {
		t.Fatalf("the retry must name the refusal; calls=%d", f.provider.calls)
	}
	if got.Draft.Name != "Newsletter Desk" {
		t.Fatalf("draft %+v", got.Draft)
	}
}

func TestWorkspaceBuild_AStaleVersionIsAConflict(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	session := f.start(t)
	session.Version = 99
	status, body := f.turn(t, session, map[string]any{"text": "a newsletter"})
	if status != http.StatusConflict || body["code"] != "version" {
		t.Fatalf("stale version: %d %v", status, body)
	}
}

func TestWorkspaceBuild_AModelFailureKeepsTheTurnAndOffersTryAgain(t *testing.T) {
	f := newBuildFixture(t, true, "", describeReply)
	f.provider.errs = []error{context.DeadlineExceeded}
	session := f.start(t)
	status, body := f.turn(t, session, map[string]any{"text": "a newsletter"})
	if status != http.StatusOK {
		t.Fatalf("failure turn %d", status)
	}
	failed := sessionOf(t, body)
	last := failed.Transcript[len(failed.Transcript)-1]
	if !last.Fixed || !strings.HasPrefix(last.Text, "I couldn’t reach my model") || len(last.Choices) != 1 || last.Choices[0].ID != "try_again" {
		t.Fatalf("failure line %+v", last)
	}
	if failed.Transcript[0].Text != "a newsletter" || failed.Retry == nil {
		t.Fatalf("the user's turn is kept: %+v", failed)
	}
	status, body = f.turn(t, failed, map[string]any{"choice_id": "try_again"})
	if status != http.StatusOK {
		t.Fatalf("try again %d %v", status, body)
	}
	got := sessionOf(t, body)
	users := 0
	for _, entry := range got.Transcript {
		if entry.Role == "user" {
			users++
		}
	}
	if users != 1 || got.Draft.TemplateID != "content-production" || got.Retry != nil {
		t.Fatalf("retry replays without repeating the user: users=%d %+v", users, got.Draft)
	}
	// The failure line is not something the model is told it said.
	for _, message := range f.provider.messages[1] {
		if strings.Contains(message.Content, "couldn’t reach") {
			t.Fatal("fixed lines must not be sent to the model")
		}
	}
}

func TestWorkspaceBuild_PlainChatRepliesMayBeFenced(t *testing.T) {
	f := newBuildFixture(t, false, "```json\n"+describeReply+"\n```")
	session := f.start(t)
	_, body := f.turn(t, session, map[string]any{"text": "a newsletter"})
	if got := sessionOf(t, body); got.Draft.TemplateID != "content-production" {
		t.Fatalf("draft %+v", got.Draft)
	}
	if !strings.Contains(f.provider.systems[0], "Reply with one JSON object only") {
		t.Fatal("the plain path asks for JSON")
	}
}

func TestWorkspaceBuild_AFirstMessageIsTheFirstTurn(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	status, body := f.do(t, http.MethodPost, "/api/workspaces/build-sessions", map[string]any{
		"entry_point": "personal_assistant_ask", "first_message": "create a newsletter workspace",
	})
	got := sessionOf(t, body)
	if status != http.StatusCreated || got.Transcript[0].Text != "create a newsletter workspace" || got.Draft.Name != "Newsletter Desk" {
		t.Fatalf("first message: %d %+v", status, got)
	}
}

func TestWorkspaceBuild_FormEditsAreSaidInPlainWords(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	session := f.start(t)
	_, body := f.turn(t, session, map[string]any{"text": "a newsletter"})
	session = sessionOf(t, body)
	draft := session.Draft
	draft.TemplateID = "code-project"
	draft.Name = "Field Notes"
	status, body := f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+session.ID+"/draft", map[string]any{
		"draft": draft, "version": session.Version, "step": 2,
	})
	if status != http.StatusOK || body["blueprint_changed"] != true {
		t.Fatalf("patch %d %v", status, body)
	}
	got := sessionOf(t, body)
	last := got.Transcript[len(got.Transcript)-1]
	if last.Role != "form" || last.Text != "You switched the blueprint to Code Project and renamed it to Field Notes." {
		t.Fatalf("form line %+v", last)
	}
	if got.Version != session.Version+1 || got.Draft.Name != "Field Notes" {
		t.Fatalf("version %d draft %+v", got.Version, got.Draft)
	}
	status, _ = f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+session.ID+"/draft", map[string]any{
		"draft": draft, "version": session.Version,
	})
	if status != http.StatusConflict {
		t.Fatalf("stale draft patch: %d", status)
	}
	status, _ = f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+session.ID+"/draft", map[string]any{
		"draft": map[string]any{"path": "/Users/me"}, "version": got.Version,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a draft key outside the create request is refused: %d", status)
	}
}

func TestWorkspaceBuild_ASyncRecordsTheFormWithoutCallingItTheUsers(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "a newsletter"})
	build = sessionOf(t, body)
	draft := build.Draft
	draft.RoleStaffing = json.RawMessage(`[{"role_id":"content-lead","mode":"assign","name":"Luna"}]`)
	draft.ExistingAgentNames = []string{"Luna"}
	status, body := f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+build.ID+"/draft", map[string]any{
		"draft": draft, "version": build.Version, "sync": true, "step": 3,
		"team_state": map[string]any{"version": 1, "blueprint_key": "template:content-production"},
	})
	if status != http.StatusOK || body["blueprint_changed"] != false {
		t.Fatalf("sync %d %v", status, body)
	}
	got := sessionOf(t, body)
	if last := got.Transcript[len(got.Transcript)-1]; last.Role == "form" {
		t.Fatalf("a sync must not be said as the user's edit: %+v", last)
	}
	if len(got.Draft.RoleStaffing) == 0 || got.FurthestStep != 3 || len(got.TeamState) == 0 {
		t.Fatalf("sync not recorded: %+v", got)
	}
	// The same change sent as the user's names the person added.
	draft.ExistingAgentNames = []string{"Luna", "Scout"}
	_, body = f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+build.ID+"/draft", map[string]any{
		"draft": draft, "version": got.Version,
	})
	if last := sessionOf(t, body).Transcript; last[len(last)-1].Text != "You added Scout." {
		t.Fatalf("user edit line: %+v", last[len(last)-1])
	}
}

func TestWorkspaceBuild_TheModelHearsTheUsersOwnEdit(t *testing.T) {
	f := newBuildFixture(t, true, describeReply, describeReply)
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "a newsletter"})
	build = sessionOf(t, body)
	draft := build.Draft
	draft.TemplateID = "code-project"
	_, body = f.do(t, http.MethodPatch, "/api/workspaces/build-sessions/"+build.ID+"/draft", map[string]any{
		"draft": draft, "version": build.Version,
	})
	build = sessionOf(t, body)
	if build.TeamPatch != nil {
		t.Fatal("the old blueprint's team goes with it")
	}
	f.turn(t, build, map[string]any{"text": "(I changed the blueprint)"})
	messages := f.provider.messages[1]
	found := false
	for _, message := range messages {
		if message.Content == "[I edited the form myself] You switched the blueprint to Code Project." {
			found = true
		}
	}
	if !found || messages[len(messages)-1].Content != "(I changed the blueprint)" {
		t.Fatalf("the model must see the user's edit: %+v", messages)
	}
	if !strings.Contains(f.provider.systems[1], `"blueprint_id":"code-project"`) {
		t.Fatal("the prompt's form must show the user's blueprint")
	}
}

func TestWorkspaceBuild_TheFolderChipOnlyForBlueprintsThatLinkOne(t *testing.T) {
	folder := `{"say":"Code Project fits.","ask":{"question":"Link the repository you already have?","choices":[],"allow_free_text":true},
"patch":{"set":["blueprint_id","name","description","ask_folder"],"blueprint_id":"%s","name":"Field Notes","description":"Tracks the code.","inputs":[],"parent_id":"","ask_folder":true,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":false,"create_now":false}`
	f := newBuildFixture(t, true, strings.ReplaceAll(folder, "%s", "code-project"))
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "my code"})
	got := sessionOf(t, body)
	if !got.AskFolder || len(got.PendingQuestion.Choices) != 2 || got.PendingQuestion.Choices[0].ID != "folder_choose" || got.PendingQuestion.Choices[1].ID != "folder_none" {
		t.Fatalf("folder chips %+v", got.PendingQuestion)
	}
	if status, _ := f.turn(t, got, map[string]any{"choice_id": "folder_choose"}); status != http.StatusBadRequest {
		t.Fatalf("choosing a folder is never a turn: %d", status)
	}

	f = newBuildFixture(t, true, strings.ReplaceAll(folder, "%s", "content-production"))
	build = f.start(t)
	_, body = f.turn(t, build, map[string]any{"text": "my notes"})
	got = sessionOf(t, body)
	last := got.Transcript[len(got.Transcript)-1]
	if got.AskFolder || !strings.Contains(last.Text, "use “Explore a folder” on Home") {
		t.Fatalf("refused folder: ask=%v %q", got.AskFolder, last.Text)
	}
}

func TestWorkspaceBuild_CreateItIsOnlyAFlag(t *testing.T) {
	createIt := strings.Replace(describeReply, `"create_now":false`, `"create_now":true`, 1)
	f := newBuildFixture(t, true, createIt)
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "create it"})
	if got := sessionOf(t, body); !got.CreateNow || got.Status != personalassistant.WorkspaceBuildOpen {
		t.Fatalf("create_now %+v", got)
	}
}

func TestWorkspaceBuild_ACreateClosesTheBuildAndRecordsHowItWasSetUp(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	workspaces := agentworkspace.NewInMemoryStore()
	f.handler.SetWorkspaceTaskStore(workspaces)
	ws := agentworkspace.NewWorkspace(agentworkspace.CreateWorkspaceParams{Name: "Newsletter Desk"})
	if err := workspaces.Save(ws); err != nil {
		t.Fatal(err)
	}
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "a newsletter from my research notes every monday"})
	build = sessionOf(t, body)

	f.handler.finishWorkspaceBuild(context.Background(), build.ID, ws.ID)
	doc, _ := f.store.Read(context.Background(), "local")
	closed := doc.Session(build.ID)
	if closed.Status != personalassistant.WorkspaceBuildCreated || closed.CreatedWorkspaceID != ws.ID {
		t.Fatalf("session %+v", closed)
	}
	stored, _ := workspaces.Get(ws.ID)
	summary := stored.GetTemplateProvenance().BuildSummary
	if summary == nil || summary.AssistantName != "Luna" || summary.TurnCount != 1 ||
		summary.UserRequest != "a newsletter from my research notes every monday" ||
		len(summary.Decisions) != 1 || summary.Decisions[0].Section != "blueprint" {
		t.Fatalf("summary %+v", summary)
	}
	// The team line comes from what was created, not from the model's words.
	decided := buildSummaryFor(personalassistant.WorkspaceBuildSession{
		Why: []personalassistant.BuildWhy{{Section: "team", Text: "Assigned Luna, as you asked."}},
		Draft: personalassistant.BuildDraft{
			RoleStaffing:       json.RawMessage(`[{"role_id":"research-lead","mode":"create","name":"Research Lead"}]`),
			ExistingAgentNames: []string{"Scout"},
		},
	})
	if len(decided.Decisions) != 1 || decided.Decisions[0].Text != "Research lead: a new agent, “Research Lead”; Scout joins the team" {
		t.Fatalf("team decision %+v", decided.Decisions)
	}
	// Idempotent for the same workspace; ignored for an unknown or closed build.
	f.handler.finishWorkspaceBuild(context.Background(), build.ID, ws.ID)
	f.handler.finishWorkspaceBuild(context.Background(), "unknown", ws.ID)
	if again := f.start(t); again.ID == build.ID {
		t.Fatal("a created build is never resumed")
	}
}

func applyTestPatch(t *testing.T, draft personalassistant.BuildDraft, patch buildReplyPatch) (personalassistant.WorkspaceBuildSession, buildApplyResult) {
	t.Helper()
	session := personalassistant.WorkspaceBuildSession{Draft: draft}
	validation := buildValidation{
		catalog:   buildTestCatalog(),
		agents:    []buildSavedAgent{{Name: "Luna"}, {Name: "Scout"}},
		groups:    []buildGroup{{ID: "group-1", Name: "Writing"}},
		nameTaken: func(name string) bool { return strings.EqualFold(name, "Taken") },
		modelOK:   func(provider, model string) bool { return provider == "openai" && model == "gpt-5" },
	}
	result := applyBuildReply(&session, patch, validation)
	return session, result
}

func TestWorkspaceBuildValidation_RefusesFieldByField(t *testing.T) {
	content := personalassistant.BuildDraft{TemplateID: "content-production", Name: "Desk"}
	research := personalassistant.BuildDraft{TemplateID: "research-project"}
	code := personalassistant.BuildDraft{TemplateID: "code-project"}
	song := personalassistant.BuildDraft{TemplateID: "studio-song"}
	tests := []struct {
		name     string
		draft    personalassistant.BuildDraft
		patch    buildReplyPatch
		applied  string
		rejected string
	}{
		{"unknown blueprint", content, buildReplyPatch{Set: []string{"blueprint_id"}, BlueprintID: "made-up"}, "", "blueprint_id:not_available"},
		{"blocked blueprint", content, buildReplyPatch{Set: []string{"blueprint_id"}, BlueprintID: "blocked-blueprint"}, "", "blueprint_id:not_available"},
		{"blank", content, buildReplyPatch{Set: []string{"blueprint_id"}, BlueprintID: ""}, "blueprint", ""},
		{"empty name", content, buildReplyPatch{Set: []string{"name"}, Name: " "}, "", "name:empty"},
		{"long name", content, buildReplyPatch{Set: []string{"name"}, Name: strings.Repeat("n", 81)}, "", "name:too_long"},
		{"taken name", content, buildReplyPatch{Set: []string{"name"}, Name: "Taken"}, "", "name:name_taken"},
		{"inputs on a blueprint without any", content, buildReplyPatch{Set: []string{"inputs"}, Inputs: []buildReplyInput{{ID: "x", Value: "1"}}}, "", "inputs:" + buildReasonNoInputs},
		{"input in range", research, buildReplyPatch{Set: []string{"inputs"}, Inputs: []buildReplyInput{{ID: "sources", Value: "7"}, {ID: "cadence", Value: "daily"}}}, "inputs", ""},
		{"input out of range", research, buildReplyPatch{Set: []string{"inputs"}, Inputs: []buildReplyInput{{ID: "sources", Value: "99"}}}, "", "inputs.sources:Sources must be between 1 and 20"},
		{"unknown option", research, buildReplyPatch{Set: []string{"inputs"}, Inputs: []buildReplyInput{{ID: "cadence", Value: "hourly"}}}, "", "inputs.cadence:"},
		{"a group", content, buildReplyPatch{Set: []string{"parent_id"}, ParentID: "group-1"}, "parent", ""},
		{"not a group", content, buildReplyPatch{Set: []string{"parent_id"}, ParentID: "group-9"}, "", "parent_id:" + buildReasonNotAGroup},
		{"placement owned by the blueprint", song, buildReplyPatch{Set: []string{"parent_id"}, ParentID: "group-1"}, "", "parent_id:" + buildReasonPlacementFixed},
		{"folder on a blueprint that links one", code, buildReplyPatch{Set: []string{"ask_folder"}, AskFolder: true}, "", ""},
		{"folder on one that cannot", content, buildReplyPatch{Set: []string{"ask_folder"}, AskFolder: true}, "", "ask_folder:" + buildReasonNoFolder},
		{"reuse an unknown agent", content, buildReplyPatch{Set: []string{"team"}, Team: buildReplyTeam{Agents: []buildReplyAgent{{Name: "Content Lead", Action: "reuse", Rename: "Ghost"}}}}, "", "team.agents.Content Lead:" + buildReasonUnknownAgent},
		{"an agent the blueprint lacks", content, buildReplyPatch{Set: []string{"team"}, Team: buildReplyTeam{Agents: []buildReplyAgent{{Name: "Pilot", Action: "create"}}}}, "", "team.agents.Pilot:" + buildReasonUnknownRole},
		{"a role by id", content, buildReplyPatch{Set: []string{"team"}, Team: buildReplyTeam{Roles: []buildReplyRole{{RoleID: "content-editor", Mode: "assign", AgentName: "scout"}}}}, "team", ""},
		{"an unresolvable model", content, buildReplyPatch{Set: []string{"team"}, Team: buildReplyTeam{Agents: []buildReplyAgent{{Name: "Content Lead", Action: "create", Provider: "openai", Model: "nope"}}}}, "team", "team.agents.Content Lead.model:" + buildReasonModel},
		{"agentless is Blank only", content, buildReplyPatch{Set: []string{"team"}, Team: buildReplyTeam{Mode: "agentless"}}, "", "team.mode:" + buildReasonAgentless},
		{"team before a blueprint", personalassistant.BuildDraft{}, buildReplyPatch{Set: []string{"team"}, Team: buildReplyTeam{SavedAgents: []string{"Luna"}}}, "", "team:" + buildReasonNoBlueprint},
		{"a color not offered", content, buildReplyPatch{Set: []string{"color"}, Color: "#123456"}, "", "color:" + buildReasonColor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, result := applyTestPatch(t, test.draft, test.patch)
			if got := strings.Join(result.applied, ","); got != test.applied {
				t.Fatalf("applied %q, want %q", got, test.applied)
			}
			rejected := ""
			for _, rejection := range result.rejections {
				rejected = rejection.Field + ":" + rejection.Reason
			}
			if !strings.HasPrefix(rejected, test.rejected) || (test.rejected == "" && rejected != "") {
				t.Fatalf("rejected %q, want %q", rejected, test.rejected)
			}
		})
	}
}

func TestWorkspaceBuildValidation_ANewBlueprintClearsWhatBelongedToTheOld(t *testing.T) {
	draft := personalassistant.BuildDraft{
		TemplateID: "code-project", Name: "Field Notes",
		BlueprintInputs:   map[string]json.RawMessage{"sources": json.RawMessage("5")},
		ProjectConnection: json.RawMessage(`{"mode":"existing_project","selection_token":"t"}`),
		RoleStaffing:      json.RawMessage(`[{"role_id":"engineer","mode":"assign","name":"Luna"}]`),
	}
	session, result := applyTestPatch(t, draft, buildReplyPatch{Set: []string{"blueprint_id"}, BlueprintID: "studio-song"})
	if strings.Join(result.applied, ",") != "blueprint" {
		t.Fatalf("applied %v", result.applied)
	}
	if session.Draft.BlueprintInputs != nil || session.Draft.ProjectConnection != nil || session.Draft.RoleStaffing != nil {
		t.Fatalf("stale draft kept: %+v", session.Draft)
	}
	if session.Draft.Name != "Field Notes" {
		t.Fatal("the name is the user's, not the blueprint's")
	}
	if session.NeedsHome == nil || session.NeedsHome.Label != "Studio Home" {
		t.Fatalf("needs home %+v", session.NeedsHome)
	}
}
