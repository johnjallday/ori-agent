package mcphttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

func TestNpmPackageFromConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  mcp.ServerConfig
		want string
	}{
		{
			name: "npx scoped with -y flag",
			cfg:  mcp.ServerConfig{Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"}},
			want: "@modelcontextprotocol/server-memory",
		},
		{
			name: "npx skips filesystem path arg",
			cfg:  mcp.ServerConfig{Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/Users/me/dir"}},
			want: "@modelcontextprotocol/server-filesystem",
		},
		{
			name: "pnpm dlx subcommand",
			cfg:  mcp.ServerConfig{Command: "pnpm", Args: []string{"dlx", "some-mcp-server"}},
			want: "some-mcp-server",
		},
		{
			name: "strips version from unscoped",
			cfg:  mcp.ServerConfig{Command: "npx", Args: []string{"cool-pkg@1.2.3"}},
			want: "cool-pkg",
		},
		{
			name: "strips version from scoped",
			cfg:  mcp.ServerConfig{Command: "npx", Args: []string{"@scope/pkg@2.0.0"}},
			want: "@scope/pkg",
		},
		{
			name: "non-npm runner yields empty",
			cfg:  mcp.ServerConfig{Command: "python", Args: []string{"-m", "server"}},
			want: "",
		},
		{
			name: "full path npx still recognized",
			cfg:  mcp.ServerConfig{Command: "/usr/local/bin/npx", Args: []string{"-y", "pkg"}},
			want: "pkg",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := npmPackageFromConfig(tc.cfg); got != tc.want {
				t.Fatalf("npmPackageFromConfig() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseGitHubRepo(t *testing.T) {
	cases := []struct {
		in        string
		wantOwner string
		wantRepo  string
	}{
		{"https://github.com/modelcontextprotocol/servers", "modelcontextprotocol", "servers"},
		{"https://github.com/owner/repo.git", "owner", "repo"},
		{"https://github.com/owner/repo/tree/main/sub", "owner", "repo"},
		{"https://www.github.com/owner/repo", "owner", "repo"},
		{"https://gitlab.com/owner/repo", "", ""},
		{"not a url at all", "", ""},
		{"https://github.com/owner", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			owner, repo := parseGitHubRepo(tc.in)
			if owner != tc.wantOwner || repo != tc.wantRepo {
				t.Fatalf("parseGitHubRepo(%q) = (%q, %q), want (%q, %q)", tc.in, owner, repo, tc.wantOwner, tc.wantRepo)
			}
		})
	}
}

func TestEnvKeysOnlyReturnsNamesSorted(t *testing.T) {
	got := envKeys(map[string]string{"ZULU": "secret-value", "ALPHA": "another-secret"})
	want := []string{"ALPHA", "ZULU"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envKeys() = %v, want %v", got, want)
	}
	if envKeys(nil) != nil {
		t.Fatalf("envKeys(nil) should be nil")
	}
}

func TestCapString(t *testing.T) {
	if got := capString("short", 100); got != "short" {
		t.Fatalf("capString should not truncate short input, got %q", got)
	}
	long := strings.Repeat("a", 50)
	got := capString(long, 10)
	if len(got) <= 10 || !strings.HasPrefix(got, strings.Repeat("a", 10)) {
		t.Fatalf("capString did not truncate as expected: %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("capString should append a truncation notice, got %q", got)
	}
}

// With ?start=true, GetServerDetailsHandler attempts a lazy start and returns a
// populated start_error when the command can't be launched, while still serving
// the config summary — and it must never leak environment variable values.
func TestGetServerDetailsHandler_StartErrorAndEnvRedaction(t *testing.T) {
	configManager := mcp.NewConfigManager(t.TempDir())
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, configManager)

	if err := registry.AddServer(mcp.ServerConfig{
		Name:      "broken",
		Command:   "definitely-not-a-real-command-for-mcp-test",
		Args:      []string{"--flag"},
		Env:       map[string]string{"SECRET_TOKEN": "super-secret"},
		Transport: "stdio",
		Enabled:   false,
	}); err != nil {
		t.Fatalf("failed to add server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/servers/broken/details?start=true", nil)
	req.SetPathValue("name", "broken")
	rr := httptest.NewRecorder()

	handler.GetServerDetailsHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	if strings.Contains(rr.Body.String(), "super-secret") {
		t.Fatalf("env value leaked into details response: %s", rr.Body.String())
	}

	var payload serverDetailsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response: %v body=%s", err, rr.Body.String())
	}
	if payload.Server != "broken" {
		t.Fatalf("expected server name 'broken', got %q", payload.Server)
	}
	if payload.StartError == "" {
		t.Fatalf("expected start_error to be populated for an unstartable server")
	}
	if want := []string{"SECRET_TOKEN"}; !reflect.DeepEqual(payload.EnvKeys, want) {
		t.Fatalf("expected env_keys %v, got %v", want, payload.EnvKeys)
	}
}

// Without ?start=true, opening details must not spawn the server: status stays
// stopped and no start error is produced, while config still serves.
func TestGetServerDetailsHandler_DoesNotStartByDefault(t *testing.T) {
	registry := mcp.NewRegistry()
	handler := NewHandler(registry, mcp.NewConfigManager(t.TempDir()))

	if err := registry.AddServer(mcp.ServerConfig{
		Name:      "idle",
		Command:   "definitely-not-a-real-command-for-mcp-test",
		Transport: "stdio",
		Enabled:   false,
	}); err != nil {
		t.Fatalf("failed to add server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/servers/idle/details", nil)
	req.SetPathValue("name", "idle")
	rr := httptest.NewRecorder()

	handler.GetServerDetailsHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var payload serverDetailsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response: %v body=%s", err, rr.Body.String())
	}
	if payload.Status != mcp.StatusStopped {
		t.Fatalf("expected status to remain %q, got %q", mcp.StatusStopped, payload.Status)
	}
	if payload.StartError != "" {
		t.Fatalf("expected no start_error when not starting, got %q", payload.StartError)
	}
}

func TestGetServerDetailsHandler_UnknownServerReturnsNotFound(t *testing.T) {
	handler := NewHandler(mcp.NewRegistry(), mcp.NewConfigManager(t.TempDir()))

	req := httptest.NewRequest(http.MethodGet, "/api/mcp/servers/ghost/details", nil)
	req.SetPathValue("name", "ghost")
	rr := httptest.NewRecorder()

	handler.GetServerDetailsHandler(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rr.Code)
	}
}
