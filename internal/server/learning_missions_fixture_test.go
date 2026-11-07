package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Full production wiring, but never discover installed credentials or contact
// a developer's local model server. Tests register controlled providers only.
func newLearningTestServer(t *testing.T) (*ServerBuilder, http.Handler) {
	t.Helper()
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "AGENT_STORE_PATH", "WORKSPACE_DIR", "WORKFLOW_TEMPLATES_DIR", "ORI_TEMPLATES_DIR", "ORI_VAULT_DIR", "ORI_CLAUDE_CREDENTIALS_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"} {
		t.Setenv(key, "")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	bin := t.TempDir()
	for _, name := range []string{"security", "codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 44\n"), 0750); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	offline := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(offline.Close)
	for _, key := range []string{"OLLAMA_BASE_URL", "LM_STUDIO_BASE_URL", "MLX_LM_BASE_URL"} {
		t.Setenv(key, offline.URL)
	}
	return newDailyBriefTestServer(t)
}
