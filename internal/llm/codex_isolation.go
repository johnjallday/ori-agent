package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// The Codex CLI is an agent: left to itself it can run commands, patch files,
// browse, view files, use plugins and memories, and hand work to sub-agents.
// An isolated call (ChatRequest.Isolated) switches all of that off for one
// run, takes nothing from the user's own Codex configuration, and runs in an
// empty folder under the read-only sandbox, so the model can only answer.
//
// The CLI gains features with every release, so the posture does not depend on
// knowing them by name. It reads what the installed CLI lists and switches off
// every feature that is on, except a short list of features that only concern
// how the request travels, how the user is signed in, or how the CLI reviews
// its own actions. A feature this file has never heard of is therefore off.
//
// The posture was characterized on Codex CLI 0.162.1.

// codexIsolatedAnchors are the CLI's command tools. The posture is trusted
// only on a CLI that still lists them as features that can be switched off: one
// that has dropped or renamed them offers commands some other way, which this
// file cannot see, and the call is refused instead of run.
var codexIsolatedAnchors = []string{"shell_tool", "unified_exec"}

// codexIsolatedKeep are the features left as the CLI has them. None gives the
// model a tool, a file, or anything from the user's machine.
var codexIsolatedKeep = map[string]bool{
	// How the request travels and which model answers.
	"api_key_model_discovery": true, "enable_request_compression": true, "system_proxy_fallback": true,
	"unbounded_connection_retries": true, "content_item_kinds": true, "compaction_image_budget": true,
	"fast_mode": true, "ultrafast_mode": true, "instant_interrupt": true,
	// How the user is signed in.
	"auth_elicitation": true,
	// The CLI's own review of what it is about to do. Switching a guard off
	// could only widen a call.
	"guardian_approval": true, "guardian_reuse_parent_compaction": true, "write_stdin_approval": true,
}

// codexIsolatedAlwaysOff are tool-bearing features switched off whether or not
// the CLI reports them as on, so a default that changes does not turn one on.
var codexIsolatedAlwaysOff = []string{
	"shell_tool", "unified_exec", "unified_exec_tty", "code_mode_host", // commands and code execution
	"multi_agent", "multi_agent_v2", // sub-agents
	"apps", "plugins", "remote_plugin", // connectors and plugin tools
	"browser_use", "browser_use_external", "browser_use_full_cdp_access", "computer_use", "in_app_browser",
	"memories", "hooks", // state and scripts from the user's machine
	"image_generation", "view_image", "goals", "sleep_tool", "tool_suggest", "skill_search", "worktrees",
}

const (
	// codexFeatureCacheTTL is how long one reading of the CLI's features is
	// used. The CLI can be upgraded while the server runs.
	codexFeatureCacheTTL = 5 * time.Minute
	// codexFeatureErrorTTL is how long a failed reading is remembered: long
	// enough that a broken CLI is not run on every page load, short enough
	// that a repaired one is noticed.
	codexFeatureErrorTTL = time.Minute
	codexFeatureRemoved  = "removed"
)

// codexFeature is one row of `codex features list`.
type codexFeature struct {
	// Stage is the CLI's word for the feature's maturity: stable,
	// experimental, under development, or removed.
	Stage   string
	Enabled bool
}

// isolatedFeatures returns the features to switch off for an isolated call, or
// ErrIsolationUnsupported when the installed CLI cannot take the posture.
func (p *CodexProvider) isolatedFeatures() ([]string, error) {
	listed, err := p.knownFeatures()
	if err != nil {
		return nil, fmt.Errorf("%w: the Codex CLI's features could not be read", ErrIsolationUnsupported)
	}
	for _, anchor := range codexIsolatedAnchors {
		if feature, ok := listed[anchor]; !ok || feature.Stage == codexFeatureRemoved {
			return nil, fmt.Errorf("%w: this Codex CLI no longer lists the feature %q, so its command tools cannot be switched off by name", ErrIsolationUnsupported, anchor)
		}
	}
	off := map[string]bool{}
	for _, name := range codexIsolatedAlwaysOff {
		if feature, ok := listed[name]; ok && feature.Stage != codexFeatureRemoved {
			off[name] = true
		}
	}
	for name, feature := range listed {
		// A removed feature has nothing left to switch off.
		if feature.Enabled && feature.Stage != codexFeatureRemoved && !codexIsolatedKeep[name] {
			off[name] = true
		}
	}
	disable := make([]string, 0, len(off))
	for name := range off {
		disable = append(disable, name)
	}
	sort.Strings(disable)
	return disable, nil
}

// knownFeatures reads the features the installed CLI lists, cached for a few
// minutes. It runs `codex features list`, which makes no model call. The
// reading has its own deadline rather than a caller's: one caller that gives
// up must not leave every other caller with its failure.
func (p *CodexProvider) knownFeatures() (map[string]codexFeature, error) {
	p.featuresMu.Lock()
	defer p.featuresMu.Unlock()
	ttl := codexFeatureCacheTTL
	if p.featuresErr != nil {
		ttl = codexFeatureErrorTTL
	}
	if !p.featuresAt.IsZero() && time.Since(p.featuresAt) < ttl {
		return p.features, p.featuresErr
	}
	list := p.listFeatures
	if list == nil {
		list = runCodexFeaturesList
	}
	p.features, p.featuresErr = list(context.Background(), p.cliPath)
	p.featuresAt = time.Now()
	return p.features, p.featuresErr
}

// runCodexFeaturesList runs `codex features list` and returns its rows.
func runCodexFeaturesList(ctx context.Context, cliPath string) (map[string]codexFeature, error) {
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
	listed := parseCodexFeatures(stdout.String())
	if len(listed) == 0 {
		return nil, fmt.Errorf("codex features list returned no features")
	}
	return listed, nil
}

// parseCodexFeatures reads `codex features list` output: one feature a line,
// its name first, then its stage, then whether it is on. A line that does not
// start with a plain feature name and end in true or false is not a feature.
func parseCodexFeatures(output string) map[string]codexFeature {
	listed := map[string]codexFeature{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name, state := fields[0], fields[len(fields)-1]
		if state != "true" && state != "false" {
			continue
		}
		plain := true
		for _, r := range name {
			lower, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
			if !lower && !digit && r != '_' && r != '.' {
				plain = false
				break
			}
		}
		if plain {
			listed[name] = codexFeature{Stage: strings.Join(fields[1:len(fields)-1], " "), Enabled: state == "true"}
		}
	}
	return listed
}

// IsolatedChatAvailable reports whether an isolated call could be run now.
func (p *CodexProvider) IsolatedChatAvailable() bool {
	_, err := p.isolatedFeatures()
	return err == nil
}

// buildCodexIsolatedArgs assembles the codex exec args for an isolated call:
// read-only sandbox, nothing from the user's configuration, rules or
// instruction files, no stored session, web search off, and every feature that
// is not on the keep list disabled.
func buildCodexIsolatedArgs(model, reasoningEffort, schemaPath, outPath string, disable []string) []string {
	args := []string{
		"exec",
		"-c", `model_reasoning_effort="` + normalizeCodexReasoningEffort(reasoningEffort) + `"`,
		"--color", "never",
		"--sandbox", "read-only",
		"--ignore-user-config", "--ignore-rules", "--ephemeral",
		"-c", `web_search="disabled"`,
		// No AGENTS.md is read, from the working folder or anywhere above it.
		"-c", "project_doc_max_bytes=0",
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
	disable, err := p.isolatedFeatures()
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
