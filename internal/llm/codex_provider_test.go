package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexProviderDefaultModels_UsesCachedModels(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cache := `{
  "models": [
    {"slug": "gpt-5.3-codex", "visibility": "list"},
    {"slug": "gpt-5.2", "visibility": "list"},
    {"slug": "gpt-5.3-codex", "visibility": "list"},
    {"slug": "gpt-5.2-codex", "visibility": "list"},
    {"slug": "hidden-codex", "visibility": "hidden"},
    {"slug": "gpt-5.1-codex", "visibility": "hide"}
  ]
}`
	cachePath := filepath.Join(codexHome, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(cache), 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if !containsModel(models, "gpt-5.3-codex") {
		t.Fatalf("expected gpt-5.3-codex in models, got %v", models)
	}
	if !containsModel(models, "gpt-5.2-codex") {
		t.Fatalf("expected gpt-5.2-codex in models, got %v", models)
	}
	if !containsModel(models, "gpt-5.2") {
		t.Fatalf("expected visible non-codex model in models, got %v", models)
	}
	if countModel(models, "gpt-5.3-codex") != 1 {
		t.Fatalf("expected duplicate cached model to be de-duplicated, got %v", models)
	}
	if containsModel(models, "hidden-codex") {
		t.Fatalf("hidden model should not be included, got %v", models)
	}
	if containsModel(models, "gpt-5.1-codex") {
		t.Fatalf("hide visibility model should not be included, got %v", models)
	}
}

func TestCodexProviderDefaultModels_FallsBackToCurated(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if len(models) == 0 {
		t.Fatal("expected fallback codex models, got none")
	}

	containsCodexFamily := false
	for _, model := range models {
		if strings.Contains(strings.ToLower(model), "codex") {
			containsCodexFamily = true
			break
		}
	}
	if !containsCodexFamily {
		t.Fatalf("expected at least one codex-family model in fallback list, got %v", models)
	}
}

func TestCodexProviderDefaultModels_PrioritizesGPT53(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cache := `{
  "models": [
    {"slug": "gpt-5.2-codex", "visibility": "list"},
    {"slug": "gpt-5-codex", "visibility": "list"},
    {"slug": "gpt-5.3-codex", "visibility": "list"},
    {"slug": "gpt-5.1-codex", "visibility": "list"}
  ]
}`
	cachePath := filepath.Join(codexHome, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(cache), 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if len(models) == 0 {
		t.Fatal("expected codex models, got none")
	}
	if models[0] != "gpt-5.3-codex" {
		t.Fatalf("expected gpt-5.3-codex to be first, got %q (full list: %v)", models[0], models)
	}
}

func TestCodexProviderDefaultModels_IncludesVisibleGPT54FromCache(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	cache := `{
  "models": [
    {"slug": "gpt-5.4", "visibility": "list"},
    {"slug": "gpt-5.3-codex", "visibility": "list"}
  ]
}`
	cachePath := filepath.Join(codexHome, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(cache), 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	provider := &CodexProvider{cliPath: "codex"}
	models := provider.DefaultModels()

	if !containsModel(models, "gpt-5.4") {
		t.Fatalf("expected gpt-5.4 in models, got %v", models)
	}
}

func containsModel(models []string, target string) bool {
	for _, model := range models {
		if model == target {
			return true
		}
	}
	return false
}

func countModel(models []string, target string) int {
	count := 0
	for _, model := range models {
		if model == target {
			count++
		}
	}
	return count
}

func TestNormalizeCodexReasoningEffort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "low", want: "low"},
		{input: "medium", want: "medium"},
		{input: "high", want: "high"},
		{input: "xhigh", want: "xhigh"},
		{input: " HIGH ", want: "high"},
		{input: "invalid", want: "medium"},
		{input: "", want: "medium"},
	}

	for _, tt := range tests {
		if got := normalizeCodexReasoningEffort(tt.input); got != tt.want {
			t.Fatalf("normalizeCodexReasoningEffort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRunCodexExecDeadlineWrapsContextError(t *testing.T) {
	dir := t.TempDir()
	cliPath := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf 'fake codex run\\n' >&2\nexec sleep 5\n"
	if err := os.WriteFile(cliPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake codex cli: %v", err)
	}

	provider := &CodexProvider{cliPath: cliPath}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := provider.runCodexExec(ctx, "gpt-test", "return json", "medium", map[string]any{
		"type": "object",
	})
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected error to wrap context deadline exceeded, got %v", err)
	}
}
