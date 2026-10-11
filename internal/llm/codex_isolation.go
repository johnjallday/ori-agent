package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The Codex CLI is an agent: left to itself it can run commands, patch files,
// browse, use plugins and memories, and hand work to sub-agents. An isolated
// call (ChatRequest.Isolated) switches all of that off for one run, takes
// nothing from the user's own Codex configuration, and runs in an empty folder
// under the read-only sandbox, so the model can only answer.
//
// The posture was characterized on Codex CLI 0.162.1. It is used only when the
// installed CLI still names every feature the posture relies on switching off:
// a CLI that has renamed or dropped one may have replaced it with something
// this list does not know, and then the call is refused instead of run.

// codexIsolatedRequired are the features an isolated call must be able to
// switch off. Every one must be known to the installed CLI.
var codexIsolatedRequired = []string{
	"shell_tool", "unified_exec", "code_mode_host", // commands and code execution
	"multi_agent",     // sub-agents
	"apps", "plugins", // connectors and plugin tools
	"browser_use", "computer_use", "in_app_browser", // browsing and desktop control
	"memories", "hooks", // state and scripts from the user's machine
	"image_generation", "goals", "sleep_tool", "tool_suggest", "skill_search",
}

// codexIsolatedOptional are narrower features of the same kinds. They are
// switched off too when the installed CLI has them.
var codexIsolatedOptional = []string{
	"unified_exec_tty", "unified_exec_zsh_fork", "remote_plugin",
	"browser_use_external", "browser_use_full_cdp_access",
}

// codexFeatureCacheTTL is how long one reading of the CLI's features is used.
// The CLI can be upgraded while the server runs.
const codexFeatureCacheTTL = 5 * time.Minute

// isolatedFeatures returns the features to switch off for an isolated call, or
// ErrIsolationUnsupported when the installed CLI cannot take the posture.
func (p *CodexProvider) isolatedFeatures(ctx context.Context) ([]string, error) {
	known, err := p.knownFeatures(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: the Codex CLI's features could not be read", ErrIsolationUnsupported)
	}
	disable := make([]string, 0, len(codexIsolatedRequired)+len(codexIsolatedOptional))
	for _, feature := range codexIsolatedRequired {
		if !known[feature] {
			return nil, fmt.Errorf("%w: this Codex CLI does not list the feature %q", ErrIsolationUnsupported, feature)
		}
		disable = append(disable, feature)
	}
	for _, feature := range codexIsolatedOptional {
		if known[feature] {
			disable = append(disable, feature)
		}
	}
	return disable, nil
}

// knownFeatures reads the feature names the installed CLI lists, cached for a
// few minutes. It runs `codex features list`, which makes no model call.
func (p *CodexProvider) knownFeatures(ctx context.Context) (map[string]bool, error) {
	p.featuresMu.Lock()
	defer p.featuresMu.Unlock()
	if !p.featuresAt.IsZero() && time.Since(p.featuresAt) < codexFeatureCacheTTL {
		return p.features, p.featuresErr
	}
	list := p.listFeatures
	if list == nil {
		list = runCodexFeaturesList
	}
	p.features, p.featuresErr = list(ctx, p.cliPath)
	p.featuresAt = time.Now()
	return p.features, p.featuresErr
}

// runCodexFeaturesList runs `codex features list` and returns the names.
func runCodexFeaturesList(ctx context.Context, cliPath string) (map[string]bool, error) {
	if strings.TrimSpace(cliPath) == "" {
		return nil, fmt.Errorf("codex CLI not available")
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// #nosec G204 -- cliPath comes from exec.LookPath("codex"); the arguments are fixed.
	cmd := exec.CommandContext(bounded, cliPath, "features", "list")
	cmd.Dir = os.TempDir()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codex features list: %w", err)
	}
	known := parseCodexFeatures(stdout.String())
	if len(known) == 0 {
		return nil, fmt.Errorf("codex features list returned no features")
	}
	return known, nil
}

// parseCodexFeatures reads `codex features list` output: one feature a line,
// its name first. A line that does not start with a plain feature name is not
// a feature.
func parseCodexFeatures(output string) map[string]bool {
	known := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		plain := true
		for _, r := range name {
			lower, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
			if !lower && !digit && r != '_' && r != '.' {
				plain = false
				break
			}
		}
		if plain {
			known[name] = true
		}
	}
	return known
}

// IsolatedChatAvailable reports whether an isolated call could be run now.
func (p *CodexProvider) IsolatedChatAvailable(ctx context.Context) bool {
	_, err := p.isolatedFeatures(ctx)
	return err == nil
}

// buildCodexIsolatedArgs assembles the codex exec args for an isolated call:
// read-only sandbox, nothing from the user's configuration or rules, no
// stored session, web search off, and every tool-bearing feature disabled.
func buildCodexIsolatedArgs(model, reasoningEffort, schemaPath, outPath string, disable []string) []string {
	args := []string{
		"exec",
		"-c", `model_reasoning_effort="` + normalizeCodexReasoningEffort(reasoningEffort) + `"`,
		"--color", "never",
		"--sandbox", "read-only",
		"--ignore-user-config", "--ignore-rules", "--ephemeral",
		"-c", `web_search="disabled"`,
	}
	for _, feature := range disable {
		args = append(args, "--disable", feature)
	}
	args = append(args, "--skip-git-repo-check", "--output-last-message", outPath)
	if model != "" {
		args = append(args, "--model", model)
	}
	if schemaPath != "" {
		args = append(args, "--output-schema", schemaPath)
	}
	return append(args, "-")
}

// runCodexIsolated runs one isolated call in a new, empty folder that is
// removed afterwards, so the CLI finds no project, no instructions file and
// nothing to read around it.
func (p *CodexProvider) runCodexIsolated(ctx context.Context, model, prompt, reasoningEffort string) (string, error) {
	disable, err := p.isolatedFeatures(ctx)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "codex-isolated-*")
	if err != nil {
		return "", fmt.Errorf("codex isolated folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	return p.execCodex(ctx, prompt, dir, func(outPath string) []string {
		return buildCodexIsolatedArgs(model, reasoningEffort, "", outPath, disable)
	})
}
