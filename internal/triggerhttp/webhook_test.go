package triggerhttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/trigger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// --- minimal fakes (the trigger package's own tests exercise behavior; here
// we only need the service wired enough to route status codes) ---

type fakeWSSource struct{ folder string }

func (f *fakeWSSource) List() ([]string, error) { return []string{"ws1"}, nil }
func (f *fakeWSSource) GetFolderPath(string) (string, error) {
	return f.folder, nil
}

type fakeWSStore struct{ ws *workspace.Workspace }

func (s *fakeWSStore) Save(*workspace.Workspace) error { return nil }
func (s *fakeWSStore) Get(string) (*workspace.Workspace, error) {
	return s.ws, nil
}
func (s *fakeWSStore) List() ([]string, error) { return []string{"ws1"}, nil }
func (s *fakeWSStore) Delete(string) error     { return nil }
func (s *fakeWSStore) ListActive() ([]*workspace.Workspace, error) {
	return []*workspace.Workspace{s.ws}, nil
}
func (s *fakeWSStore) GetFilesPath(string) string   { return "" }
func (s *fakeWSStore) GetOutputsPath(string) string { return "" }
func (s *fakeWSStore) GetWorkspaceAgent(string, string) (*agent.Agent, bool, error) {
	return nil, false, nil
}
func (s *fakeWSStore) SaveWorkspaceAgent(string, string, *agent.Agent) error { return nil }
func (s *fakeWSStore) Lock(string) func()                                    { return func() {} }
func (s *fakeWSStore) Update(_ string, fn func(*workspace.Workspace) error) error {
	return fn(s.ws)
}

func newTestHandler(t *testing.T) (*Handler, *trigger.Service) {
	t.Helper()
	dir := t.TempDir()
	svc, err := trigger.NewService(trigger.ServiceConfig{
		WorkspaceStore: &fakeWSStore{ws: &workspace.Workspace{ID: "ws1", MissionEnabled: true}},
		Source:         &fakeWSSource{folder: dir},
		// nil mission runner: mission fires fail, but webhook routing (the
		// concern of these tests) still exercises auth/limits/202.
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(svc.Close)
	return NewHandler(svc), svc
}

func createWebhook(t *testing.T, svc *trigger.Service, secret string) trigger.Trigger {
	t.Helper()
	tr, err := svc.Create(trigger.Trigger{
		WorkspaceID: "ws1",
		Name:        "hook",
		Type:        trigger.TypeWebhook,
		Enabled:     true,
		Action:      trigger.Action{Kind: trigger.ActionTaskPrompt, Agent: "a", Prompt: "do"},
		Webhook:     &trigger.WebhookConfig{Secret: secret},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tr
}

func postHook(h *Handler, token, contentType, secret, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/"+token, strings.NewReader(body))
	req.SetPathValue("token", token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if secret != "" {
		req.Header.Set(secretHeader, secret)
	}
	rr := httptest.NewRecorder()
	h.HandleWebhook(rr, req)
	return rr
}

func TestWebhookHappyPath(t *testing.T) {
	h, svc := newTestHandler(t)
	tr := createWebhook(t, svc, "")

	rr := postHook(h, tr.Webhook.Token, "application/json", "", `{"ok":true}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "fire_id") {
		t.Errorf("202 body missing fire_id: %s", rr.Body.String())
	}
}

func TestWebhookUnknownToken404(t *testing.T) {
	h, _ := newTestHandler(t)
	rr := postHook(h, "no-such-token", "application/json", "", "{}")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestWebhookDisabledTrigger404(t *testing.T) {
	h, svc := newTestHandler(t)
	tr := createWebhook(t, svc, "")
	if _, err := svc.SetEnabled("ws1", tr.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	rr := postHook(h, tr.Webhook.Token, "application/json", "", "{}")
	if rr.Code != http.StatusNotFound {
		t.Errorf("disabled trigger status = %d, want 404 (indistinguishable from unknown)", rr.Code)
	}
}

func TestWebhookSecretEnforced(t *testing.T) {
	h, svc := newTestHandler(t)
	tr := createWebhook(t, svc, "s3cr3t")

	if rr := postHook(h, tr.Webhook.Token, "application/json", "", "{}"); rr.Code != http.StatusUnauthorized {
		t.Errorf("missing secret status = %d, want 401", rr.Code)
	}
	if rr := postHook(h, tr.Webhook.Token, "application/json", "wrong", "{}"); rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret status = %d, want 401", rr.Code)
	}
	if rr := postHook(h, tr.Webhook.Token, "application/json", "s3cr3t", "{}"); rr.Code != http.StatusAccepted {
		t.Errorf("correct secret status = %d, want 202", rr.Code)
	}
}

func TestWebhookUnsupportedContentType415(t *testing.T) {
	h, svc := newTestHandler(t)
	tr := createWebhook(t, svc, "")
	rr := postHook(h, tr.Webhook.Token, "application/octet-stream", "", "binary")
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", rr.Code)
	}
}

func TestWebhookOversizePayload413(t *testing.T) {
	h, svc := newTestHandler(t)
	tr := createWebhook(t, svc, "")
	big := strings.Repeat("x", trigger.MaxPayloadBytes+10)
	rr := postHook(h, tr.Webhook.Token, "text/plain", "", big)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
}

func TestWebhookRateLimit429(t *testing.T) {
	dir := t.TempDir()
	svc, err := trigger.NewService(trigger.ServiceConfig{
		WorkspaceStore:    &fakeWSStore{ws: &workspace.Workspace{ID: "ws1"}},
		Source:            &fakeWSSource{folder: dir},
		WebhookRatePerMin: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	h := NewHandler(svc)
	tr := createWebhook(t, svc, "")

	codes := make(map[int]int)
	for i := 0; i < 5; i++ {
		codes[postHook(h, tr.Webhook.Token, "text/plain", "", "hi").Code]++
	}
	if codes[http.StatusAccepted] != 2 {
		t.Errorf("accepted = %d, want 2 (rate cap)", codes[http.StatusAccepted])
	}
	if codes[http.StatusTooManyRequests] != 3 {
		t.Errorf("429 = %d, want 3", codes[http.StatusTooManyRequests])
	}
}

func TestWebhookWrongMethod405(t *testing.T) {
	h, svc := newTestHandler(t)
	tr := createWebhook(t, svc, "")
	req := httptest.NewRequest(http.MethodGet, "/api/hooks/"+tr.Webhook.Token, nil)
	req.SetPathValue("token", tr.Webhook.Token)
	rr := httptest.NewRecorder()
	h.HandleWebhook(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// Guard: the management view must never leak the secret.
func TestListViewHidesSecret(t *testing.T) {
	h, svc := newTestHandler(t)
	createWebhook(t, svc, "topsecret")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws1/triggers", nil)
	req.SetPathValue("workspaceID", "ws1")
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "topsecret") {
		t.Errorf("secret leaked in list response: %s", body)
	}
	if !strings.Contains(body, "has_secret") || !strings.Contains(body, "webhook_url") {
		t.Errorf("view missing has_secret/webhook_url: %s", body)
	}
}

func TestMain(m *testing.M) {
	// Keep watcher logs quiet during the package test run.
	os.Exit(m.Run())
}
