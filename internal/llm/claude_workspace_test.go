package llm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
			// test is the one production builds.
			t.Setenv("ANTHROPIC_BASE_URL", server.URL)

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
