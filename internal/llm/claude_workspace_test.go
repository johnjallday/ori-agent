package llm

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ori supplies its own key and workspace, so an Anthropic SDK profile on the
// machine (written by `ant auth login`) must not add its workspace_id or
// base_url to Ori's requests. A stale profile workspace made every Claude call
// fail with 404 "Workspace … not found".
func TestClaudeProvider_IgnoresTheMachineSDKProfile(t *testing.T) {
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		sent = append(sent, r.Header.Get("Anthropic-Workspace-Id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-fixture",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "configs"), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := `{"version":"1.0","authentication":{"type":"user_oauth"},"workspace_id":"wrkspc_fromTheMachineProfile"}`
	if err := os.WriteFile(filepath.Join(configDir, "configs", "default.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_CONFIG_DIR", configDir)
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	// Unset, not empty: the SDK reads ANTHROPIC_PROFILE="" as a profile named ""
	// and quietly skips the fallback profile, which would hide the leak.
	// t.Setenv first so the original value is restored after the test.
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_PROFILE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}

	provider := NewClaudeProvider(ClaudeProviderConfig("test-key", ""))
	request := ChatRequest{Model: "claude-fixture", Messages: []Message{NewUserMessage("Hello")}}
	if _, err := provider.Chat(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != "" {
		t.Fatalf("anthropic-workspace-id sent %q, want none: the machine profile leaked into Ori's request", sent)
	}
}

// Anthropic answers an unknown workspace with the same 404 not_found_error as
// an unknown model. It is a provider-settings problem, so it must not be
// reported as a missing model with "choose a different model".
func TestClaudeProvider_AWorkspace404IsNotAMissingModel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		message  string
		category ErrorCategory
		action   UserAction
	}{
		{name: "workspace", message: "Workspace `182314a5-5af1-4215-acf5-d6a8e9e036dd` not found.", category: CategoryPermissionDenied, action: ActionConfigureProvider},
		{name: "model", message: "model: claude-fixture", category: CategoryInvalidModel, action: ActionChooseModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"`+tc.message+`"}}`)
			}))
			defer server.Close()
			t.Setenv("ANTHROPIC_BASE_URL", server.URL)

			provider := NewClaudeProvider(ClaudeProviderConfig("test-key", ""))
			_, err := provider.Chat(t.Context(), ChatRequest{Model: "claude-fixture", Messages: []Message{NewUserMessage("Hello")}})
			var typed *ProviderError
			if !errors.As(err, &typed) {
				t.Fatalf("error is not typed: %v", err)
			}
			if typed.Category != tc.category || typed.Action != tc.action || typed.Retryable || typed.HTTPStatus != http.StatusNotFound {
				t.Fatalf("classified %s action=%s retryable=%v status=%d, want %s/%s", typed.Category, typed.Action, typed.Retryable, typed.HTTPStatus, tc.category, tc.action)
			}
			if tc.name == "workspace" && (!strings.Contains(typed.Message, "Workspace ID") || strings.Contains(typed.Message, "182314a5")) {
				t.Fatalf("message = %q, want Ori's own sentence naming the Workspace ID setting", typed.Message)
			}
		})
	}
}

// An API key that belongs to the whole organization is refused unless each
// request names a workspace, so a configured workspace ID goes on every
// request the provider sends, including after a key change.
func TestClaudeProvider_SendsTheWorkspaceIDOnEveryRequest(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workspaceID string
		want        string
	}{
		{name: "workspace set", workspaceID: " wrkspc_011CZkZaBF1tNoB5wlCeusgy ", want: "wrkspc_011CZkZaBF1tNoB5wlCeusgy"},
		{name: "no workspace", workspaceID: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				sent = append(sent, r.Header.Get("Anthropic-Workspace-Id"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-fixture",
					"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			}))
			defer server.Close()
			// The SDK takes its endpoint from the environment, so the provider under
			// test is the one production builds. It also reads the developer's own
			// Anthropic SDK profile (workspace_id, base_url) from ANTHROPIC_CONFIG_DIR;
			// an empty directory keeps that machine state out of the assertion.
			t.Setenv("ANTHROPIC_BASE_URL", server.URL)
			t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())

			provider := NewClaudeProvider(ClaudeProviderConfig("test-key", tc.workspaceID))
			request := ChatRequest{Model: "claude-fixture", Messages: []Message{NewUserMessage("Hello")}}
			if _, err := provider.Chat(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			provider.UpdateClient("another-key")
			if _, err := provider.Chat(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			if len(sent) != 2 || sent[0] != tc.want || sent[1] != tc.want {
				t.Fatalf("anthropic-workspace-id sent %q, want %q on both requests", sent, tc.want)
			}
		})
	}
}
