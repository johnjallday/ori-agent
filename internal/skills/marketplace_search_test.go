package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMarketplaceQueryRejectsOptionsControlsAndOversizeWithoutExecution(t *testing.T) {
	runs := 0
	s := &MarketplaceSearcher{resolve: func() (marketplaceRuntime, string) { runs++; return marketplaceRuntime{}, "node_required" }}
	for _, query := range []string{"", "  ", "-y", "telegram --install", "telegram\ncommunity", "a\x00b", "a\u202eb", string([]byte{0xff}), strings.Repeat("界", 257)} {
		if result := s.Search(context.Background(), query, 8); result.State != "invalid_query" {
			t.Fatalf("query %q: %+v", query, result)
		}
	}
	if runs != 0 {
		t.Fatalf("invalid queries resolved/executed runtime %d times", runs)
	}
	for _, query := range []string{"Telegram communities", "what's useful?", strings.Repeat("界", 256), "  exact spaces  "} {
		if err := ValidateMarketplaceQuery(query); err != nil {
			t.Fatalf("valid query %q: %v", query, err)
		}
	}
}

func TestMarketplaceParserDistinguishesListingMalformedPartialAndUnverifiedEmpty(t *testing.T) {
	listing := "\x1b[36malice/community@telegram\x1b[0m 1.2K installs\n└ https://skills.sh/forged/path\n"
	result := ParseMarketplaceSearchOutput(listing, 8)
	if result.State != "available" || len(result.Results) != 1 || result.Results[0].URL != "https://skills.sh/alice/community/telegram" || result.Results[0].Installs != "1.2K installs" {
		t.Fatalf("listing: %+v", result)
	}
	for _, output := range []string{"No skills found for telegram", "No skills found; network unavailable"} {
		if got := ParseMarketplaceSearchOutput(output, 8); got.State != "unavailable" || got.Reason != "unverified_empty" {
			t.Fatalf("empty: %+v", got)
		}
	}
	for _, output := range []string{"", "npm failure", "alice/community@telegram/evil", "alice/community@telegram;install", "<owner/repo@skill>", string([]byte{0xff})} {
		if got := ParseMarketplaceSearchOutput(output, 8); got.State != "malformed_output" || len(got.Results) != 0 {
			t.Fatalf("malformed %q: %+v", output, got)
		}
	}
	if got := ParseMarketplaceSearchOutput(listing+"bad/package@\n", 8); got.State != "partial" {
		t.Fatalf("partial: %+v", got)
	}
	output := listing
	for i := 0; i < 12; i++ {
		output += fmt.Sprintf("alice/community@skill%d\n", i)
	}
	if got := ParseMarketplaceSearchOutput(output, 999); len(got.Results) != 8 || !got.Truncated || got.State != "partial" {
		t.Fatalf("cap: %+v", got)
	}
	if got := ParseMarketplaceSearchOutput(listing+listing, 8); len(got.Results) != 1 {
		t.Fatalf("dedupe: %+v", got)
	}
}

func TestMarketplaceSearcherHonestRuntimeAndFailureStates(t *testing.T) {
	for _, reason := range []string{"node_required", "skills_runtime_required"} {
		s := &MarketplaceSearcher{resolve: func() (marketplaceRuntime, string) { return marketplaceRuntime{}, reason }}
		if got := s.Search(context.Background(), "telegram", 8); got.State != "missing_runtime" || got.Reason != reason || !got.ObservedAt.IsZero() {
			t.Fatalf("runtime: %+v", got)
		}
	}
	for _, failure := range []struct {
		err    error
		reason string
	}{
		{errors.New("SECRET_PROVIDER_SENTINEL /private/file"), "search_failed"},
		{errMarketplaceOutputLimit, "output_limit"},
	} {
		s := &MarketplaceSearcher{resolve: func() (marketplaceRuntime, string) { return marketplaceRuntime{version: marketplaceRuntimeVersion}, "" }, run: func(context.Context, marketplaceRuntime, string) (string, error) {
			return "alice/community@telegram\nSECRET_PROVIDER_SENTINEL", failure.err
		}}
		got := s.Search(context.Background(), "telegram", 8)
		if got.Reason != failure.reason || got.State != "unavailable" || len(got.Results) > 0 || !got.ObservedAt.IsZero() {
			t.Fatalf("failed output became evidence: %+v", got)
		}
	}
	exact := "  Telegram communities  "
	s := &MarketplaceSearcher{resolve: func() (marketplaceRuntime, string) { return marketplaceRuntime{version: marketplaceRuntimeVersion}, "" }, run: func(_ context.Context, _ marketplaceRuntime, query string) (string, error) {
		if query != exact {
			t.Fatalf("query changed: %q", query)
		}
		return "alice/community@telegram\n", nil
	}}
	if got := s.Search(context.Background(), exact, 8); got.State != "available" || got.ObservedAt.IsZero() || got.RuntimeVersion != marketplaceRuntimeVersion {
		t.Fatalf("success: %+v", got)
	}
}

// TestMarketplaceChild is a controlled executable, not a real Node runtime or
// network source. Production argv/environment/cancellation are exercised by
// executing this test binary directly; there is no shell or npm bootstrap.
func TestMarketplaceChild(t *testing.T) {
	if len(os.Args) != 4 || os.Args[2] != "find" {
		return
	}
	for _, key := range []string{"SECRET_PROVIDER_SENTINEL", "NODE_OPTIONS", "NPM_CONFIG_REGISTRY", "HTTP_PROXY", "OPENAI_API_KEY"} {
		if os.Getenv(key) != "" {
			os.Exit(9)
		}
	}
	cwd, _ := os.Getwd()
	if os.Getenv("HOME") != cwd || os.Getenv("SKILLS_API_URL") != MarketplaceAPI || os.Getenv("DISABLE_TELEMETRY") != "1" || os.Getenv("DO_NOT_TRACK") != "1" {
		os.Exit(10)
	}
	if err := os.WriteFile(filepath.Join(cwd, "temporary-output"), []byte("fixture"), 0o600); err != nil {
		os.Exit(11)
	}
	switch os.Args[3] {
	case "fixture-success":
		_, _ = fmt.Fprintln(os.Stdout, "alice/community@telegram 1K installs")
	case "fixture-error":
		_, _ = fmt.Fprintln(os.Stderr, "SECRET_PROVIDER_SENTINEL /private/runtime/path")
		os.Exit(1)
	case "fixture-large":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", MarketplaceOutputBytes+1))
	case "fixture-timeout":
		time.Sleep(2 * time.Second)
	default:
		os.Exit(12)
	}
	os.Exit(0)
}

func childMarketplaceRuntime(t *testing.T) marketplaceRuntime {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return marketplaceRuntime{node: executable, entry: "-test.run=^TestMarketplaceChild$", version: marketplaceRuntimeVersion}
}

func TestMarketplaceRunnerSanitizesEnvironmentAndCapsOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"SECRET_PROVIDER_SENTINEL", "NODE_OPTIONS", "NPM_CONFIG_REGISTRY", "HTTP_PROXY", "OPENAI_API_KEY"} {
		t.Setenv(key, "private-value")
	}
	runtime := childMarketplaceRuntime(t)
	output, err := runMarketplaceSearch(context.Background(), runtime, "fixture-success")
	if err != nil || !strings.Contains(output, "alice/community@telegram") {
		t.Fatalf("success: %q %v", output, err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) > 0 {
		t.Fatalf("real HOME changed: %v %v", entries, err)
	}
	if output, err := runMarketplaceSearch(context.Background(), runtime, "fixture-large"); !errors.Is(err, errMarketplaceOutputLimit) || output != "" {
		t.Fatalf("output cap: len=%d %v", len(output), err)
	}
	if output, err := runMarketplaceSearch(context.Background(), runtime, "fixture-error"); err == nil || output != "" {
		t.Fatalf("failure output leaked: %q %v", output, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := runMarketplaceSearch(ctx, runtime, "fixture-timeout"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
}

func writeRuntimeFixture(t *testing.T, root, version string) {
	t.Helper()
	for _, leaf := range []string{"bin", "dist"} {
		if err := os.MkdirAll(filepath.Join(root, leaf), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, leaf, "cli.mjs"), []byte("// inert fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := fmt.Sprintf(`{"name":"skills","version":%q,"bin":{"skills":"./bin/cli.mjs"}}`, version)
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMarketplaceRuntimeResolutionIsReadOnlyPinnedAndOffline(t *testing.T) {
	home := t.TempDir()
	lookedUp := []string{}
	lookPath := func(name string) (string, error) {
		lookedUp = append(lookedUp, name)
		if name == "node" {
			return "/trusted/node", nil
		}
		return "", exec.ErrNotFound
	}
	root := filepath.Join(home, ".npm", "_npx", "fixture-cache", "node_modules", "skills")
	writeRuntimeFixture(t, root, "9.0.0")
	if _, reason := findMarketplaceRuntime(lookPath, home); reason != "skills_runtime_required" {
		t.Fatalf("unsupported runtime: %s", reason)
	}
	writeRuntimeFixture(t, root, marketplaceRuntimeVersion)
	runtime, reason := findMarketplaceRuntime(lookPath, home)
	if reason != "" || runtime.entry != filepath.Join(root, "bin", "cli.mjs") || runtime.node != "/trusted/node" {
		t.Fatalf("pinned runtime: %+v %s", runtime, reason)
	}
	if !reflect.DeepEqual(lookedUp, []string{"node", "skills", "node", "skills"}) {
		t.Fatalf("resolution invoked a bootstrap/other executable: %v", lookedUp)
	}
	if _, reason := findMarketplaceRuntime(func(string) (string, error) { return "", exec.ErrNotFound }, home); reason != "node_required" {
		t.Fatalf("node failure: %s", reason)
	}
	if err := os.Remove(filepath.Join(root, "dist", "cli.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, reason := findMarketplaceRuntime(lookPath, home); reason != "skills_runtime_required" {
		t.Fatalf("incomplete runtime accepted: %s", reason)
	}
}
