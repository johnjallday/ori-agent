package templateonboardinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/templateonboarding"
)

type memorySessionStore struct {
	mu       sync.Mutex
	sessions map[string]*templateonboarding.Session
}

func newMemorySessionStore(t *testing.T, sessions ...*templateonboarding.Session) *memorySessionStore {
	t.Helper()
	store := &memorySessionStore{sessions: make(map[string]*templateonboarding.Session, len(sessions))}
	for _, session := range sessions {
		if err := store.Save(context.Background(), session); err != nil {
			t.Fatalf("save session: %v", err)
		}
	}
	return store
}

func (s *memorySessionStore) Load(ctx context.Context, workspaceID string) (*templateonboarding.Session, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[workspaceID]
	if !ok {
		return nil, templateonboarding.ErrSessionNotFound
	}
	return session.Clone()
}

func (s *memorySessionStore) Save(ctx context.Context, session *templateonboarding.Session) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned, err := session.Clone()
	if err != nil {
		return err
	}
	s.sessions[session.WorkspaceID] = cloned
	return nil
}

type mutableEntryAgentResolver struct {
	name string
	err  error
}

func (r *mutableEntryAgentResolver) EntryAgentName(ctx context.Context, workspaceID string) (string, error) {
	_ = ctx
	_ = workspaceID
	return r.name, r.err
}

func TestHandlerGetStatusReturnsCurrentValuesMissingAndBlockers(t *testing.T) {
	session := newCollectingSession(t, "ws-status")
	if _, err := session.MergeValues(map[string]any{"genre": "rock"}); err != nil {
		t.Fatalf("merge values: %v", err)
	}
	if _, err := session.Block("skill dependency missing: reaper-session-setup"); err != nil {
		t.Fatalf("block session: %v", err)
	}

	handler := NewHandler(newMemorySessionStore(t, session), EntryAgentResolverFunc(func(ctx context.Context, workspaceID string) (string, error) {
		return "Producer", nil
	}))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-status/template-onboarding", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got StatusResponse
	decodeJSON(t, rec, &got)
	if got.Status != templateonboarding.StatusBlocked {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
	if got.EntryAgentName != "Producer" {
		t.Fatalf("entry_agent_name = %q, want Producer", got.EntryAgentName)
	}
	if len(got.Fields) != len(testSpec().Fields) {
		t.Fatalf("fields len = %d, want %d", len(got.Fields), len(testSpec().Fields))
	}
	if got.Values["genre"] != "rock" {
		t.Fatalf("genre value = %#v, want rock", got.Values["genre"])
	}
	if !containsString(got.MissingRequiredFields, "project_name") {
		t.Fatalf("missing required fields = %#v, want project_name", got.MissingRequiredFields)
	}
	if !containsString(got.DependencyBlockers, "skill dependency missing: reaper-session-setup") {
		t.Fatalf("dependency blockers = %#v, want persisted blocker", got.DependencyBlockers)
	}
	if got.ActionError == "" {
		t.Fatalf("action_error should be returned for blocked sessions")
	}
}

func TestHandlerPatchValuesValidatesMergesAndMarksReady(t *testing.T) {
	session := newCollectingSession(t, "ws-values")
	store := newMemorySessionStore(t, session)
	handler := NewHandler(store, EntryAgentResolverFunc(func(ctx context.Context, workspaceID string) (string, error) {
		return "Producer", nil
	}))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"values":{"project_name":"Night Drive","genre":"Rock","tempo":"128","explicit":"yes"}}`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-values/template-onboarding/values", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH values = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got StatusResponse
	decodeJSON(t, rec, &got)
	if got.Status != templateonboarding.StatusReadyToComplete {
		t.Fatalf("status = %q, want ready_to_complete", got.Status)
	}
	if got.Values["genre"] != "rock" {
		t.Fatalf("genre value = %#v, want canonical rock", got.Values["genre"])
	}
	if got.Values["tempo"] != float64(128) {
		t.Fatalf("tempo value = %#v, want 128", got.Values["tempo"])
	}
	if got.Values["explicit"] != true {
		t.Fatalf("explicit value = %#v, want true", got.Values["explicit"])
	}

	persisted, err := store.Load(context.Background(), "ws-values")
	if err != nil {
		t.Fatalf("load persisted session: %v", err)
	}
	if persisted.Status != templateonboarding.StatusReadyToComplete {
		t.Fatalf("persisted status = %q, want ready_to_complete", persisted.Status)
	}

	invalid := httptest.NewRecorder()
	mux.ServeHTTP(invalid, httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-values/template-onboarding/values", bytes.NewBufferString(`{"values":{"genre":"jazz"}}`)))
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid enum status = %d, want 422: %s", invalid.Code, invalid.Body.String())
	}

	blankRequired := httptest.NewRecorder()
	mux.ServeHTTP(blankRequired, httptest.NewRequest(http.MethodPatch, "/api/workspaces/ws-values/template-onboarding/values", bytes.NewBufferString(`{"values":{"project_name":"  "}}`)))
	if blankRequired.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank required status = %d, want 422: %s", blankRequired.Code, blankRequired.Body.String())
	}
}

func TestHandlerStateGatesAndPrefixIsolation(t *testing.T) {
	ready := newReadySession(t, "ws-ready")
	failed := newFailedSession(t, "ws-failed")
	store := newMemorySessionStore(t, ready, failed)
	resolver := &mutableEntryAgentResolver{}
	handler := NewHandler(store, resolver)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	prefixRec := httptest.NewRecorder()
	mux.ServeHTTP(prefixRec, httptest.NewRequest(http.MethodGet, "/api/onboarding/status", nil))
	if prefixRec.Code != http.StatusNotFound {
		t.Fatalf("global onboarding prefix status = %d, want 404", prefixRec.Code)
	}

	missingEntry := httptest.NewRecorder()
	mux.ServeHTTP(missingEntry, httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-ready/template-onboarding/complete", nil))
	if missingEntry.Code != http.StatusConflict {
		t.Fatalf("complete without entry status = %d, want 409: %s", missingEntry.Code, missingEntry.Body.String())
	}

	resolver.name = "Producer"
	complete := httptest.NewRecorder()
	mux.ServeHTTP(complete, httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-ready/template-onboarding/complete", nil))
	if complete.Code != http.StatusNotImplemented {
		t.Fatalf("complete without executor status = %d, want 501: %s", complete.Code, complete.Body.String())
	}

	retryWrongState := httptest.NewRecorder()
	mux.ServeHTTP(retryWrongState, httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-ready/template-onboarding/retry", nil))
	if retryWrongState.Code != http.StatusConflict {
		t.Fatalf("retry wrong state status = %d, want 409: %s", retryWrongState.Code, retryWrongState.Body.String())
	}

	retry := httptest.NewRecorder()
	mux.ServeHTTP(retry, httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-failed/template-onboarding/retry", nil))
	if retry.Code != http.StatusNotImplemented {
		t.Fatalf("retry without executor status = %d, want 501: %s", retry.Code, retry.Body.String())
	}
}

func TestHandlerExtractRequiresEntryAndMergesChatValues(t *testing.T) {
	pending := newPendingSession(t, "ws-extract")
	store := newMemorySessionStore(t, pending)
	resolver := &mutableEntryAgentResolver{}
	handler := NewHandler(store, resolver)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"message":"project_name: Night Drive\ngenre: rock\ntempo: 124\nexplicit: yes"}`)
	noEntry := httptest.NewRecorder()
	mux.ServeHTTP(noEntry, httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-extract/template-onboarding/extract", body))
	if noEntry.Code != http.StatusConflict {
		t.Fatalf("extract without entry status = %d, want 409: %s", noEntry.Code, noEntry.Body.String())
	}

	resolver.name = "Producer"
	body = bytes.NewBufferString(`{"message":"project_name: Night Drive\ngenre: rock\ntempo: 124\nexplicit: yes"}`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-extract/template-onboarding/extract", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("extract status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got extractResponse
	decodeJSON(t, rec, &got)
	if got.Status != templateonboarding.StatusReadyToComplete {
		t.Fatalf("status = %q, want ready_to_complete", got.Status)
	}
	if got.Values["project_name"] != "Night Drive" {
		t.Fatalf("project_name = %#v, want Night Drive", got.Values["project_name"])
	}
	if got.Values["tempo"] != float64(124) {
		t.Fatalf("tempo = %#v, want 124", got.Values["tempo"])
	}

	persisted, err := store.Load(context.Background(), "ws-extract")
	if err != nil {
		t.Fatalf("load persisted session: %v", err)
	}
	if persisted.Status != templateonboarding.StatusReadyToComplete {
		t.Fatalf("persisted status = %q, want ready_to_complete", persisted.Status)
	}
}

func TestHandlerExtractUsesStructuredOutputWhenConfigured(t *testing.T) {
	pending := newPendingSession(t, "ws-structured")
	store := newMemorySessionStore(t, pending)
	provider := &fakeStructuredProvider{
		response: `{"values":[{"field_id":"project_name","value":"Solar EP"},{"field_id":"genre","value":"pop"}],"missing_required_fields":[],"reasoning":"captured"}`,
	}
	factory := llm.NewFactory()
	factory.Register("fake", provider)

	handler := NewHandler(store, EntryAgentResolverFunc(func(ctx context.Context, workspaceID string) (string, error) {
		return "Producer", nil
	}))
	handler.SetExtractionDeps(factory, fakeSystemModelConfig{provider: "fake", model: "fake-model", reasoningEffort: "low"})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-structured/template-onboarding/extract", bytes.NewBufferString(`{"message":"make this a bright melodic release"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("extract status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if provider.structuredCalls != 1 {
		t.Fatalf("structured calls = %d, want 1", provider.structuredCalls)
	}

	var got extractResponse
	decodeJSON(t, rec, &got)
	if got.Status != templateonboarding.StatusReadyToComplete {
		t.Fatalf("status = %q, want ready_to_complete", got.Status)
	}
	if got.Values["project_name"] != "Solar EP" || got.Values["genre"] != "pop" {
		t.Fatalf("values = %#v, want structured extracted project_name and genre", got.Values)
	}
}

func newPendingSession(t *testing.T, workspaceID string) *templateonboarding.Session {
	t.Helper()
	session, err := templateonboarding.NewSession(workspaceID, testSpec(), templateonboarding.StatusPendingEntryAgent)
	if err != nil {
		t.Fatalf("new pending session: %v", err)
	}
	return session
}

func newCollectingSession(t *testing.T, workspaceID string) *templateonboarding.Session {
	t.Helper()
	session, err := templateonboarding.NewSession(workspaceID, testSpec(), templateonboarding.StatusCollecting)
	if err != nil {
		t.Fatalf("new collecting session: %v", err)
	}
	return session
}

func newReadySession(t *testing.T, workspaceID string) *templateonboarding.Session {
	t.Helper()
	session := newCollectingSession(t, workspaceID)
	if _, err := session.MergeValues(requiredValues()); err != nil {
		t.Fatalf("merge required values: %v", err)
	}
	if _, err := session.MarkReadyToComplete(); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	return session
}

func newFailedSession(t *testing.T, workspaceID string) *templateonboarding.Session {
	t.Helper()
	session := newReadySession(t, workspaceID)
	if _, err := session.StartCompletion(); err != nil {
		t.Fatalf("start completion: %v", err)
	}
	if _, err := session.MarkFailed("boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	return session
}

func testSpec() *templateonboarding.OnboardingSpec {
	minName := float64(3)
	minTempo := float64(60)
	maxTempo := float64(200)
	return &templateonboarding.OnboardingSpec{
		Version: "1",
		Fields: []templateonboarding.Field{
			{
				ID:         "project_name",
				Label:      "Project name",
				Type:       templateonboarding.FieldString,
				Required:   true,
				Validation: &templateonboarding.FieldValidation{Min: &minName},
			},
			{
				ID:       "genre",
				Label:    "Genre",
				Type:     templateonboarding.FieldEnum,
				Required: true,
				Options:  []string{"rock", "pop"},
			},
			{
				ID:         "tempo",
				Label:      "Tempo",
				Type:       templateonboarding.FieldNumber,
				Validation: &templateonboarding.FieldValidation{Min: &minTempo, Max: &maxTempo},
			},
			{
				ID:    "explicit",
				Label: "Explicit",
				Type:  templateonboarding.FieldBoolean,
			},
		},
		Completion: templateonboarding.CompletionAction{
			Type: templateonboarding.ActionTask,
			Ref:  "create_project",
		},
	}
}

func requiredValues() map[string]any {
	return map[string]any{
		"project_name": "Night Drive",
		"genre":        "rock",
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode JSON response: %v\nbody: %s", err, rec.Body.String())
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type fakeSystemModelConfig struct {
	provider        string
	model           string
	reasoningEffort string
}

func (c fakeSystemModelConfig) GetSystemModel() (string, string) {
	return c.provider, c.model
}

func (c fakeSystemModelConfig) GetSystemReasoningEffort() string {
	return c.reasoningEffort
}

type fakeStructuredProvider struct {
	response        string
	structuredCalls int
	chatCalls       int
}

func (p *fakeStructuredProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	_ = ctx
	_ = req
	p.chatCalls++
	return &llm.ChatResponse{Content: p.response}, nil
}

func (p *fakeStructuredProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	_ = ctx
	_ = req
	return nil, nil
}

func (p *fakeStructuredProvider) ChatWithStructuredOutput(ctx context.Context, req llm.StructuredOutputRequest) (*llm.ChatResponse, error) {
	_ = ctx
	_ = req
	p.structuredCalls++
	return &llm.ChatResponse{Content: p.response}, nil
}

func (p *fakeStructuredProvider) Name() string {
	return "fake"
}

func (p *fakeStructuredProvider) Type() llm.ProviderType {
	return llm.ProviderTypeLocal
}

func (p *fakeStructuredProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{}
}

func (p *fakeStructuredProvider) ValidateConfig(config llm.ProviderConfig) error {
	_ = config
	return nil
}

func (p *fakeStructuredProvider) DefaultModels() []string {
	return []string{"fake-model"}
}
