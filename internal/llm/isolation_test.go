package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An isolated request may carry nothing that would widen it. The check is the
// first thing a provider does with one.
func TestCheckIsolatedRequestRefusesAnythingThatWidensTheCall(t *testing.T) {
	plain := ChatRequest{Model: "m", Messages: []Message{NewUserMessage("hi")}}
	if err := CheckIsolatedRequest(plain); err != nil {
		t.Fatalf("an ordinary request: %v", err)
	}
	isolated := plain
	isolated.Isolated = true
	if err := CheckIsolatedRequest(isolated); err != nil {
		t.Fatalf("a bare isolated request: %v", err)
	}
	for name, widen := range map[string]func(*ChatRequest){
		"tools":           func(r *ChatRequest) { r.Tools = []Tool{{Name: "read_file"}} },
		"mcp servers":     func(r *ChatRequest) { r.MCPServers = []MCPServerSpec{{Name: "files"}} },
		"workspace id":    func(r *ChatRequest) { r.WorkspaceID = "ws-1" },
		"workspace dir":   func(r *ChatRequest) { r.WorkspaceDir = "/somewhere" },
		"execution scope": func(r *ChatRequest) { r.ExecutionScope = &CLIExecutionScope{} },
	} {
		req := isolated
		widen(&req)
		if err := CheckIsolatedRequest(req); !errors.Is(err, ErrIsolationUnsupported) {
			t.Errorf("an isolated request with %s => %v, want ErrIsolationUnsupported", name, err)
		}
		// The same field on an ordinary request is none of this check's business.
		req.Isolated = false
		if err := CheckIsolatedRequest(req); err != nil {
			t.Errorf("an ordinary request with %s => %v", name, err)
		}
	}
}

// Which providers may be given an isolated call is stated per provider. A CLI
// agent with no verified tool-free posture is not one of them, and says so
// instead of running the call as plain text.
func TestIsolatedChatSupportIsExplicitPerProvider(t *testing.T) {
	for name, caps := range map[string]ProviderCapabilities{
		"ollama":           (&OllamaProvider{}).Capabilities(),
		"local compatible": (&OpenAICompatibleLocalProvider{}).Capabilities(),
		"claude api":       (&ClaudeProvider{}).Capabilities(),
		"gemini":           (&GeminiProvider{}).Capabilities(),
		"openai":           (&OpenAIProvider{}).Capabilities(),
	} {
		if !caps.SupportsIsolatedChat {
			t.Errorf("%s does not support an isolated call", name)
		}
	}
	// The shared helpers do not grant it: a new provider gets it only by saying so.
	if CloudProviderCapabilities(1000).SupportsIsolatedChat || LocalProviderCapabilities(1000).SupportsIsolatedChat {
		t.Fatal("isolated-call support is granted by default")
	}

	claudeCode := &ClaudeCodeProvider{cliPath: fakeCLI(t, "claude", "echo should-never-run; exit 0\n")}
	if claudeCode.Capabilities().SupportsIsolatedChat {
		t.Fatal("the Claude Code CLI claims an isolated posture")
	}
	_, err := claudeCode.Chat(context.Background(), ChatRequest{Isolated: true, Messages: []Message{NewUserMessage("hi")}})
	if !errors.Is(err, ErrIsolationUnsupported) {
		t.Fatalf("an isolated call to the Claude Code CLI => %v", err)
	}
}

const codexFeatureTable = `apps                                     stable             true
browser_use                              stable             true
browser_use_external                     stable             true
code_mode_host                           stable             true
computer_use                             stable             true
goals                                    stable             true
hooks                                    stable             true
image_generation                         stable             true
in_app_browser                           stable             true
js_repl                                  removed            false
memories                                 stable             true
multi_agent                              stable             true
multi_agent_v2                           under development  false
plugins                                  stable             true
shell_tool                               stable             true
skill_search                             stable             true
sleep_tool                               stable             true
tool_suggest                             stable             true
unified_exec                             stable             true
unified_exec_tty                         stable             true
`

func TestParseCodexFeaturesReadsNamesOnly(t *testing.T) {
	known := parseCodexFeatures(codexFeatureTable + "\nWARNING: something odd happened\n   \nNot-A-Feature x y\n")
	for _, name := range []string{"apps", "js_repl", "multi_agent_v2", "unified_exec_tty"} {
		if !known[name] {
			t.Errorf("feature %q was not read", name)
		}
	}
	for _, name := range []string{"WARNING:", "Not-A-Feature", "stable", "true"} {
		if known[name] {
			t.Errorf("%q was read as a feature", name)
		}
	}
}

// The isolated posture is used only when the installed CLI names every feature
// it relies on switching off. Anything else is refused, never run looser.
func TestCodexIsolatedPostureNeedsEveryFeatureItSwitchesOff(t *testing.T) {
	ctx := context.Background()
	lists := 0
	provider := &CodexProvider{cliPath: "codex", listFeatures: func(context.Context, string) (map[string]bool, error) {
		lists++
		return parseCodexFeatures(codexFeatureTable), nil
	}}
	disable, err := provider.isolatedFeatures(ctx)
	if err != nil {
		t.Fatalf("a CLI with every feature: %v", err)
	}
	joined := "," + strings.Join(disable, ",") + ","
	for _, feature := range codexIsolatedRequired {
		if !strings.Contains(joined, ","+feature+",") {
			t.Errorf("%s is not switched off", feature)
		}
	}
	// Narrower features are switched off when the CLI has them, and not asked
	// for when it does not.
	if !strings.Contains(joined, ",unified_exec_tty,") || !strings.Contains(joined, ",browser_use_external,") || strings.Contains(joined, ",remote_plugin,") {
		t.Errorf("optional features: %v", disable)
	}
	if !provider.Capabilities().SupportsIsolatedChat || !provider.IsolatedChatAvailable(ctx) {
		t.Fatal("a CLI that can take the posture is reported as unable")
	}
	if lists != 1 {
		t.Fatalf("the CLI's features were read %d times", lists)
	}

	for _, missing := range []string{"shell_tool", "multi_agent", "memories"} {
		table := strings.Replace(codexFeatureTable, missing+" ", "renamed_"+missing+" ", 1)
		older := &CodexProvider{cliPath: "codex", listFeatures: func(context.Context, string) (map[string]bool, error) {
			return parseCodexFeatures(table), nil
		}}
		if _, err := older.isolatedFeatures(ctx); !errors.Is(err, ErrIsolationUnsupported) || !strings.Contains(err.Error(), missing) {
			t.Errorf("a CLI without %s => %v", missing, err)
		}
		if older.Capabilities().SupportsIsolatedChat {
			t.Errorf("a CLI without %s still claims the posture", missing)
		}
		// The call itself is refused: nothing is run.
		if _, err := older.Chat(ctx, ChatRequest{Isolated: true, Messages: []Message{NewUserMessage("hi")}}); !errors.Is(err, ErrIsolationUnsupported) {
			t.Errorf("an isolated call on a CLI without %s => %v", missing, err)
		}
	}
	unreadable := &CodexProvider{cliPath: "codex", listFeatures: func(context.Context, string) (map[string]bool, error) {
		return nil, errors.New("exit status 2")
	}}
	if _, err := unreadable.isolatedFeatures(ctx); !errors.Is(err, ErrIsolationUnsupported) {
		t.Fatalf("an unreadable feature list => %v", err)
	}
}

// One isolated call, end to end against a stand-in CLI: the arguments are the
// isolated posture and nothing of any elevated one, the working folder is new
// and empty, and it is gone afterwards.
func TestCodexIsolatedChatRunsInAnEmptyFolderWithTheToolFreePosture(t *testing.T) {
	record := t.TempDir()
	script := `record="` + record + `"
if [ "$1" = "features" ]; then
cat <<'TABLE'
` + codexFeatureTable + `TABLE
exit 0
fi
printf '%s\n' "$@" > "$record/args"
pwd > "$record/cwd"
ls -A | wc -l | tr -d ' ' > "$record/entries"
cat > "$record/stdin"
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then out="$2"; fi
  shift
done
printf 'An assessment.\n' > "$out"
`
	provider := &CodexProvider{cliPath: fakeCLI(t, "codex", script)}
	response, err := provider.Chat(context.Background(), ChatRequest{
		Model: "gpt-test", Isolated: true,
		Messages: []Message{NewSystemMessage("Ori's rules."), NewUserMessage("The evidence.")},
	})
	if err != nil {
		t.Fatalf("isolated chat: %v", err)
	}
	if response.Content != "An assessment." || len(response.ToolCalls) != 0 || response.Provider != "codex" {
		t.Fatalf("response = %+v", response)
	}

	raw, err := os.ReadFile(filepath.Join(record, "args"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	joined := "\n" + strings.Join(args, "\n") + "\n"
	for _, want := range []string{
		"\nexec\n", "\n--sandbox\nread-only\n", "\n--ignore-user-config\n", "\n--ignore-rules\n", "\n--ephemeral\n",
		"\n-c\nweb_search=\"disabled\"\n", "\n--skip-git-repo-check\n", "\n--model\ngpt-test\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the isolated call lacks %q", strings.TrimSpace(want))
		}
	}
	for _, feature := range codexIsolatedRequired {
		if !strings.Contains(joined, "\n--disable\n"+feature+"\n") {
			t.Errorf("the isolated call does not disable %s", feature)
		}
	}
	for _, never := range []string{"workspace-write", "--profile", "--add-dir", "approval_policy", "network_access", "--output-schema", "--enable", "danger"} {
		if strings.Contains(joined, never) {
			t.Errorf("the isolated call carries %q", never)
		}
	}
	if args[len(args)-1] != "-" {
		t.Errorf("the prompt is not read from stdin: last argument %q", args[len(args)-1])
	}

	cwd, _ := os.ReadFile(filepath.Join(record, "cwd"))
	entries, _ := os.ReadFile(filepath.Join(record, "entries"))
	dir := strings.TrimSpace(string(cwd))
	wd, _ := os.Getwd()
	if dir == "" || dir == wd || strings.TrimSpace(string(entries)) != "0" {
		t.Fatalf("the call ran in %q with %s entries; want a new empty folder", dir, strings.TrimSpace(string(entries)))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the isolated folder was left behind: %v", err)
	}
	stdin, _ := os.ReadFile(filepath.Join(record, "stdin"))
	if !strings.Contains(string(stdin), "Ori's rules.") || !strings.Contains(string(stdin), "The evidence.") {
		t.Fatalf("the prompt did not reach the CLI: %q", stdin)
	}

	// The same provider refuses an isolated request that carries a tool.
	if _, err := provider.Chat(context.Background(), ChatRequest{Isolated: true, Tools: []Tool{{Name: "x"}}}); !errors.Is(err, ErrIsolationUnsupported) {
		t.Fatalf("an isolated call with a tool => %v", err)
	}
}

// A plain Codex call with no schema passes no schema file. A nil schema map
// used to be written out as the JSON text "null" and handed to the CLI.
func TestCodexPlainChatWithoutASchemaPassesNoSchemaFile(t *testing.T) {
	record := t.TempDir()
	script := `record="` + record + `"
printf '%s\n' "$@" > "$record/args"
cat > /dev/null
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then out="$2"; fi
  shift
done
printf 'ok\n' > "$out"
`
	provider := &CodexProvider{cliPath: fakeCLI(t, "codex", script)}
	if _, err := provider.Chat(context.Background(), ChatRequest{Model: "gpt-test", Messages: []Message{NewUserMessage("hi")}}); err != nil {
		t.Fatalf("plain chat: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(record, "args"))
	if strings.Contains(string(raw), "--output-schema") {
		t.Fatalf("a call with no schema was given a schema file:\n%s", raw)
	}
	// A real schema is still passed.
	if _, err := provider.Chat(context.Background(), ChatRequest{
		Model: "gpt-test", Messages: []Message{NewUserMessage("hi")}, ResponseSchema: map[string]any{"type": "object"},
	}); err != nil {
		t.Fatalf("chat with a schema: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(record, "args"))
	if !strings.Contains(string(raw), "--output-schema") {
		t.Fatalf("a call with a schema was given none:\n%s", raw)
	}
}
